#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod web;

use tauri::{menu::{Menu, MenuItem}, tray::TrayIconBuilder, Manager};
use tauri_plugin_dialog::DialogExt;
use tauri_plugin_opener::OpenerExt;
use tauri_plugin_notification::NotificationExt;

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
fn notify_permission(app: tauri::AppHandle, body: String) -> Result<(), String> {
    let window = app.get_webview_window("main").ok_or("Main window unavailable")?;
    if window.is_visible().map_err(|error| error.to_string())?
        && !window.is_minimized().map_err(|error| error.to_string())?
        && window.is_focused().map_err(|error| error.to_string())?
    {
        return Ok(());
    }
    app.notification().builder()
        .title("Permission required")
        .body(body.chars().take(300).collect::<String>())
        .sound("Default")
        .show().map_err(|error| error.to_string())
}

fn main() {
    let app = tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _, _| open_window(app)))
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_notification::init())
        .invoke_handler(tauri::generate_handler![open_focus, notify_permission])
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
