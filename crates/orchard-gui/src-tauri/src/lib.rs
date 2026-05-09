//! Tauri shell entry. The GUI is a thin Svelte client over the local daemon
//! (GraphQL @ 127.0.0.1:7777) for reads and `worktree-core` (via the bridges
//! in `commands`) for stateless system ops.

pub mod commands;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .invoke_handler(tauri::generate_handler![
            commands::list_worktrees,
            commands::create_worktree,
            commands::remove_worktree,
            commands::prune_worktrees,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
