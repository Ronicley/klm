use tauri_plugin_dialog::DialogExt;

#[tauri::command]
pub async fn save_graph_file(app: tauri::AppHandle, name: String, content: String) -> Result<bool, String> {
    let extension = if name.ends_with(".json") { "json" } else if name.ends_with(".svg") { "svg" } else { return Err("Use a JSON or SVG filename.".into()); };
    if name.len() > 240 || name.contains(['/', '\\', ':']) || content.len() > 2 * 1024 * 1024 {
        return Err("Invalid filename or graph file exceeds 2 MiB.".into());
    }
    tauri::async_runtime::spawn_blocking(move || {
        let selected = app.dialog().file().set_file_name(&name).add_filter("Graph", &[extension]).blocking_save_file();
        let Some(selected) = selected else { return Ok(false); };
        let path = selected.into_path().map_err(|e| e.to_string())?;
        std::fs::write(path, content).map_err(|e| format!("Could not save graph file: {e}"))?;
        Ok(true)
    }).await.map_err(|e| e.to_string())?
}
