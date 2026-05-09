//! Tauri shell entry. The GUI is a thin Svelte client over the local daemon
//! (GraphQL @ 127.0.0.1:7777) for reads and `worktree-core` (via the bridges
//! in `commands`) for stateless system ops.

pub mod chat;
pub mod commands;
pub mod pty;
pub mod transcript;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .manage(chat::ChatState::default())
        .manage(pty::PtyState::default())
        .setup(|app| {
            chat::spawn_watcher(app.handle().clone());
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            commands::list_worktrees,
            commands::create_worktree,
            commands::remove_worktree,
            commands::prune_worktrees,
            chat::chat_list_rooms,
            chat::chat_load_room,
            chat::chat_send,
            chat::chat_self_handle,
            pty::pty_spawn,
            pty::pty_write,
            pty::pty_resize,
            pty::pty_kill,
            transcript::read_transcript_jsonl,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
