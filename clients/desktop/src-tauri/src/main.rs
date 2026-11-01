#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod web;
mod client_auth;
mod draft_storage;

use tauri::{menu::{Menu, MenuItem}, tray::TrayIconBuilder, Manager};
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_opener::OpenerExt;

fn open_window(app: &tauri::AppHandle) {
    if let Some(window) = app.get_webview_window("main") {
        let _ = window.unminimize();
        let _ = window.show();
        let _ = window.set_focus();
    }
}

#[tauri::command]
fn open_focus(app: tauri::AppHandle, server: tauri::State<'_, web::WebServer>) -> Result<(), String> {
    server.ready()?;
    // Fixed URL only: never open a caller-supplied command/path, or another service
    // after a port collision. The web client has no native IPC capability.
    app.opener().open_url(web::FOCUS_URL, None::<&str>).map_err(|error| error.to_string())
}

#[tauri::command]
fn open_content_download(app: tauri::AppHandle, url: String) -> Result<(), String> {
    let parsed = url::Url::parse(&url).map_err(|_| "Invalid download URL.")?;
    let token = parsed.path().rsplit_once("/api/content-downloads/").map(|(_, token)| token).unwrap_or("");
    let loopback = matches!(parsed.host_str(), Some("localhost" | "127.0.0.1" | "[::1]"));
    if !(parsed.scheme() == "https" || parsed.scheme() == "http" && loopback)
        || !parsed.username().is_empty() || parsed.password().is_some()
        || parsed.fragment().is_some() || parsed.query() != Some("contract=3")
        || token.len() != 43 || !token.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'_' || c == b'-') {
        return Err("Invalid download URL.".into());
    }
    // Delegate the short-lived attachment capability to the platform browser's
    // download handler. No credential or response body crosses native IPC.
    app.opener().open_url(parsed.as_str(), None::<&str>).map_err(|_| "Could not open download.".into())
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _, _| open_window(app)))
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .invoke_handler(tauri::generate_handler![open_focus, open_content_download, client_auth::client_credential_get, client_auth::client_credential_set, client_auth::client_credential_delete, draft_storage::draft_read, draft_storage::draft_write, draft_storage::draft_delete])
        .setup(|app| {
            let open = MenuItem::with_id(app, "open", "Open", true, None::<&str>)?;
            let exit = MenuItem::with_id(app, "exit", "Exit", true, None::<&str>)?;
            let menu = Menu::with_items(app, &[&open, &exit])?;
            TrayIconBuilder::new()
                .icon(app.default_window_icon().expect("packaged KLM icon").clone())
                .tooltip("KLM")
                .menu(&menu)
                .show_menu_on_left_click(true)
                .on_menu_event(|app, event| match event.id.as_ref() {
                    "open" => open_window(app),
                    "exit" => app.exit(0),
                    _ => {}
                })
                .build(app)?;
            let server = web::WebServer::start();
            let error = server.error();
            app.manage(server);
            open_window(app.handle());
            if let Some(error) = error {
                app.dialog().message(error).title("Focus unavailable").show(|_| {});
            }
            Ok(())
        })
        .on_window_event(|window, event| {
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = window.hide();
            }
        })
        .build(tauri::generate_context!())
        .expect("Cannot start KLM Desktop");
    app.run(|app, event| {
        if let tauri::RunEvent::Exit = event {
            app.state::<web::WebServer>().stop();
        }
    });
}
