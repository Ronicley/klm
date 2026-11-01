use keyring::{Entry, Error};

// Native credentials never enter web storage. Credential Manager owns encryption
// and user isolation; separate app identifiers also separate dev/release clients.
fn entry(app: &tauri::AppHandle, engine: &str) -> Result<Entry, String> {
    let url = url::Url::parse(engine).map_err(|_| "Invalid engine URL.")?;
    let local = matches!(url.host_str(), Some("localhost" | "127.0.0.1" | "[::1]" | "::1"));
    if engine.len() > 2048 || !url.username().is_empty() || url.password().is_some()
        || url.query().is_some() || url.fragment().is_some()
        || !(url.scheme() == "https" || (url.scheme() == "http" && local)) {
        return Err("Use HTTPS or a direct loopback engine URL.".into());
    }
    Entry::new(&format!("{}.client-auth", app.config().identifier), engine)
        .map_err(|_| "Cannot access native credential storage.".into())
}

#[tauri::command]
pub fn client_credential_get(app: tauri::AppHandle, engine: String) -> Result<Option<String>, String> {
    match entry(&app, &engine)?.get_password() {
        Ok(value) => Ok(Some(value)),
        Err(Error::NoEntry) => Ok(None),
        Err(_) => Err("Cannot read native credential storage.".into()),
    }
}

#[tauri::command]
pub fn client_credential_set(app: tauri::AppHandle, engine: String, credential: String) -> Result<(), String> {
    if credential.len() != 43 || !credential.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_') {
        return Err("Invalid client credential.".into());
    }
    entry(&app, &engine)?.set_password(&credential).map_err(|_| "Cannot save native credential.".into())
}

#[tauri::command]
pub fn client_credential_delete(app: tauri::AppHandle, engine: String) -> Result<(), String> {
    match entry(&app, &engine)?.delete_credential() {
        Ok(()) | Err(Error::NoEntry) => Ok(()),
        Err(_) => Err("Cannot remove native credential.".into()),
    }
}
