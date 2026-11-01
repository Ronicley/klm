use serde::Deserialize;
use sha2::{Digest, Sha256};
use std::{fs, io::{Read, Write}, path::PathBuf, sync::Mutex};
use tauri::Manager;

// IPC accepts an endpoint/identity, never a renderer-chosen filesystem path.
// None is reserved for pre-connection forms, not entity drafts.
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct DraftNamespace {
    endpoint: String,
    database_id: Option<String>,
}

static STORAGE: Mutex<()> = Mutex::new(());
const MAX_BYTES: usize = 32 * 1024 * 1024;

fn location(app: &tauri::AppHandle, namespace: &DraftNamespace) -> Result<(PathBuf, Vec<u8>), String> {
    let url = url::Url::parse(&namespace.endpoint).map_err(|_| "Invalid draft endpoint.")?;
    let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]" | "::1"));
    if namespace.endpoint.len() > 2048 || url.host_str().is_none()
        || !url.username().is_empty() || url.password().is_some() || url.query().is_some() || url.fragment().is_some()
        || !(url.scheme() == "https" || url.scheme() == "http" && local)
        || url.as_str().trim_end_matches('/') != namespace.endpoint
        || namespace.database_id.as_ref().is_some_and(|id| id.is_empty() || id.len() > 128 || !id.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')) {
        return Err("Invalid draft namespace.".into());
    }
    let mut hash = Sha256::new();
    hash.update(app.config().identifier.as_bytes());
    hash.update(b"\0");
    hash.update(namespace.endpoint.as_bytes());
    hash.update(b"\0");
    hash.update(namespace.database_id.as_deref().unwrap_or("connection").as_bytes());
    // Distinguish the reserved pre-connection scope from any real database ID.
    hash.update(if namespace.database_id.is_some() { b"\0db".as_slice() } else { b"\0pre".as_slice() });
    let entropy = hash.finalize().to_vec();
    let name = entropy.iter().map(|byte| format!("{byte:02x}")).collect::<String>();
    let path = app.path().app_data_dir().map_err(|_| "Cannot locate private draft storage.")?
        .join("drafts").join(format!("{name}.dpapi"));
    Ok((path, entropy))
}

#[cfg(windows)]
fn protect(bytes: &[u8], entropy: &[u8], decrypt: bool) -> Result<Vec<u8>, String> {
    use windows_sys::Win32::{Foundation::LocalFree, Security::Cryptography::{
        CryptProtectData, CryptUnprotectData, CRYPTPROTECT_UI_FORBIDDEN, CRYPT_INTEGER_BLOB,
    }};
    let input = CRYPT_INTEGER_BLOB { cbData: bytes.len() as u32, pbData: bytes.as_ptr() as *mut u8 };
    let salt = CRYPT_INTEGER_BLOB { cbData: entropy.len() as u32, pbData: entropy.as_ptr() as *mut u8 };
    let mut output = CRYPT_INTEGER_BLOB { cbData: 0, pbData: std::ptr::null_mut() };
    // No CRYPTPROTECT_LOCAL_MACHINE: DPAPI binds ciphertext to CurrentUser.
    let ok = unsafe {
        if decrypt {
            CryptUnprotectData(&input, std::ptr::null_mut(), &salt, std::ptr::null(), std::ptr::null(), CRYPTPROTECT_UI_FORBIDDEN, &mut output)
        } else {
            CryptProtectData(&input, std::ptr::null(), &salt, std::ptr::null(), std::ptr::null(), CRYPTPROTECT_UI_FORBIDDEN, &mut output)
        }
    };
    if ok == 0 { return Err(if decrypt { "Cannot decrypt saved drafts." } else { "Cannot protect drafts." }.into()); }
    if output.pbData.is_null() || output.cbData == 0 {
        if !output.pbData.is_null() { unsafe { LocalFree(output.pbData as _); } }
        return Err("Protected draft data is empty.".into());
    }
    let result = unsafe { std::slice::from_raw_parts(output.pbData, output.cbData as usize).to_vec() };
    unsafe {
        if decrypt { std::ptr::write_bytes(output.pbData, 0, output.cbData as usize); }
        LocalFree(output.pbData as _);
    }
    Ok(result)
}

#[cfg(not(windows))]
fn protect(_: &[u8], _: &[u8], _: bool) -> Result<Vec<u8>, String> {
    Err("Protected draft storage requires Windows.".into())
}

#[cfg(windows)]
fn replace(source: &std::path::Path, destination: &std::path::Path) -> Result<(), String> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Storage::FileSystem::{MoveFileExW, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH};
    let from: Vec<u16> = source.as_os_str().encode_wide().chain(Some(0)).collect();
    let to: Vec<u16> = destination.as_os_str().encode_wide().chain(Some(0)).collect();
    if unsafe { MoveFileExW(from.as_ptr(), to.as_ptr(), MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH) } == 0 {
        return Err("Cannot commit protected drafts.".into());
    }
    Ok(())
}

#[cfg(not(windows))]
fn replace(_: &std::path::Path, _: &std::path::Path) -> Result<(), String> {
    Err("Protected draft storage requires Windows.".into())
}

#[tauri::command]
pub fn draft_read(app: tauri::AppHandle, namespace: DraftNamespace) -> Result<Option<String>, String> {
    let _lock = STORAGE.lock().map_err(|_| "Draft storage is unavailable.")?;
    let (path, entropy) = location(&app, &namespace)?;
    let file = match fs::File::open(path) {
        Ok(file) => file,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err("Cannot read saved drafts.".into()),
    };
    let mut bytes = Vec::new();
    file.take((MAX_BYTES + 1024 * 1024 + 1) as u64).read_to_end(&mut bytes).map_err(|_| "Cannot read saved drafts.")?;
    if bytes.is_empty() || bytes.len() > MAX_BYTES + 1024 * 1024 { return Err("Saved drafts have an invalid size.".into()); }
    let plaintext = protect(&bytes, &entropy, true)?;
    if plaintext.len() > MAX_BYTES { return Err("Saved drafts exceed the storage limit.".into()); }
    String::from_utf8(plaintext).map(Some).map_err(|_| "Saved drafts are not valid UTF-8.".into())
}

#[tauri::command]
pub fn draft_write(app: tauri::AppHandle, namespace: DraftNamespace, document: String) -> Result<(), String> {
    let _lock = STORAGE.lock().map_err(|_| "Draft storage is unavailable.")?;
    let (path, entropy) = location(&app, &namespace)?;
    if document.len() > MAX_BYTES || serde_json::from_str::<serde_json::Value>(&document).is_err() {
        return Err("Draft data is invalid or exceeds the storage limit.".into());
    }
    let ciphertext = protect(document.as_bytes(), &entropy, false)?;
    let directory = path.parent().ok_or("Invalid draft storage location.")?;
    fs::create_dir_all(directory).map_err(|_| "Cannot create private draft storage.")?;
    // Only ciphertext enters the temporary file. Sync before atomic replacement;
    // errors leave the previously committed file intact and propagate to IPC.
    let mut temporary = tempfile::NamedTempFile::new_in(directory).map_err(|_| "Cannot stage protected drafts.")?;
    temporary.write_all(&ciphertext).map_err(|_| "Cannot write protected drafts.")?;
    temporary.as_file().sync_all().map_err(|_| "Cannot sync protected drafts.")?;
    let staged = temporary.into_temp_path();
    replace(&staged, &path)
}

#[tauri::command]
pub fn draft_delete(app: tauri::AppHandle, namespace: DraftNamespace) -> Result<(), String> {
    let _lock = STORAGE.lock().map_err(|_| "Draft storage is unavailable.")?;
    let (path, _) = location(&app, &namespace)?;
    match fs::remove_file(path) {
        Ok(()) => Ok(()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(_) => Err("Cannot remove saved drafts.".into()),
    }
}
