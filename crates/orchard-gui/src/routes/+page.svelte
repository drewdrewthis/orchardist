<script lang="ts">
  import { invoke } from "@tauri-apps/api/core";
  import { onMount } from "svelte";

  type SessionMeta = {
    path: string;
    cwd: string | null;
    last_modified_unix: number;
    session_id: string;
  };

  type ChatEvent = {
    type?: string;
    role?: string;
    message?: any;
    timestamp?: string;
    [key: string]: any;
  };

  let sessions = $state<SessionMeta[]>([]);
  let selected = $state<SessionMeta | null>(null);
  let events = $state<ChatEvent[]>([]);
  let inputText = $state("");
  let target = $state("orchardist");
  let sendStatus = $state("");
  let loading = $state(false);

  async function loadSessions() {
    loading = true;
    try {
      sessions = await invoke<SessionMeta[]>("list_sessions");
    } catch (e) {
      console.error("list_sessions failed", e);
    } finally {
      loading = false;
    }
  }

  async function openSession(s: SessionMeta) {
    selected = s;
    events = [];
    try {
      events = await invoke<ChatEvent[]>("read_session", { path: s.path });
    } catch (e) {
      console.error("read_session failed", e);
    }
  }

  async function send(e: Event) {
    e.preventDefault();
    if (!inputText.trim()) return;
    sendStatus = "sending...";
    try {
      await invoke("send_to_tmux", { target, text: inputText });
      sendStatus = `sent → ${target}`;
      inputText = "";
    } catch (err) {
      sendStatus = `error: ${err}`;
    }
  }

  function extractText(ev: ChatEvent): string {
    if (typeof ev.message === "string") return ev.message;
    if (ev.message?.content) {
      if (typeof ev.message.content === "string") return ev.message.content;
      if (Array.isArray(ev.message.content)) {
        return ev.message.content
          .map((c: any) => (typeof c === "string" ? c : c?.text || ""))
          .join("");
      }
    }
    return JSON.stringify(ev).slice(0, 200);
  }

  function role(ev: ChatEvent): "user" | "assistant" | "system" {
    if (ev.message?.role) return ev.message.role as any;
    if (ev.role) return ev.role as any;
    return "system";
  }

  function shortId(s: string): string {
    return s.slice(0, 8);
  }

  function fmtTime(unix: number): string {
    return new Date(unix * 1000).toLocaleString();
  }

  onMount(loadSessions);
</script>

<div class="app">
  <aside class="sidebar">
    <header>
      <h1>Orchard</h1>
      <button onclick={loadSessions} disabled={loading} class="refresh">
        {loading ? "..." : "↻"}
      </button>
    </header>
    <div class="session-list">
      {#each sessions as s (s.path)}
        <button
          class="session-item"
          class:active={selected?.path === s.path}
          onclick={() => openSession(s)}
        >
          <div class="session-id">{shortId(s.session_id)}</div>
          <div class="session-cwd">{s.cwd ?? "(no cwd)"}</div>
          <div class="session-time">{fmtTime(s.last_modified_unix)}</div>
        </button>
      {:else}
        <div class="empty">{loading ? "loading..." : "no sessions found"}</div>
      {/each}
    </div>
  </aside>

  <main class="chat">
    {#if selected}
      <header class="chat-header">
        <span class="chat-title">{shortId(selected.session_id)}</span>
        <span class="chat-cwd">{selected.cwd ?? ""}</span>
      </header>
      <div class="messages">
        {#each events as ev, i (i)}
          <div class="bubble {role(ev)}">
            <div class="bubble-role">{role(ev)}</div>
            <div class="bubble-text">{extractText(ev)}</div>
          </div>
        {/each}
      </div>
    {:else}
      <div class="placeholder">Pick a session from the sidebar →</div>
    {/if}

    <form class="input-bar" onsubmit={send}>
      <input
        class="target-input"
        bind:value={target}
        placeholder="tmux session name"
        title="tmux target session"
      />
      <input
        class="msg-input"
        bind:value={inputText}
        placeholder="Type a message to send via tmux send-keys..."
      />
      <button type="submit">Send</button>
      {#if sendStatus}<span class="status">{sendStatus}</span>{/if}
    </form>
  </main>
</div>

<style>
  :global(html, body) {
    margin: 0;
    height: 100%;
    background: #1a1a1f;
    color: #e8e8ec;
    font-family: -apple-system, BlinkMacSystemFont, "Inter", "SF Pro Display",
      sans-serif;
    -webkit-font-smoothing: antialiased;
  }

  .app {
    display: grid;
    grid-template-columns: 260px 1fr;
    height: 100vh;
    overflow: hidden;
  }

  .sidebar {
    background: rgba(28, 28, 34, 0.85);
    backdrop-filter: blur(40px) saturate(160%);
    -webkit-backdrop-filter: blur(40px) saturate(160%);
    border-right: 1px solid rgba(255, 255, 255, 0.08);
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }

  .sidebar header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 14px 16px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  }

  .sidebar h1 {
    font-size: 14px;
    font-weight: 600;
    margin: 0;
    letter-spacing: 0.5px;
    text-transform: uppercase;
    color: rgba(255, 255, 255, 0.7);
  }

  .refresh {
    background: transparent;
    border: 1px solid rgba(255, 255, 255, 0.12);
    color: rgba(255, 255, 255, 0.7);
    width: 28px;
    height: 24px;
    border-radius: 6px;
    cursor: pointer;
  }

  .refresh:hover {
    background: rgba(255, 255, 255, 0.06);
  }

  .session-list {
    flex: 1;
    overflow-y: auto;
    padding: 6px;
  }

  .session-item {
    display: block;
    width: 100%;
    background: transparent;
    color: inherit;
    border: none;
    text-align: left;
    padding: 10px 12px;
    border-radius: 8px;
    cursor: pointer;
    margin-bottom: 2px;
    font-family: inherit;
  }

  .session-item:hover {
    background: rgba(255, 255, 255, 0.04);
  }

  .session-item.active {
    background: rgba(120, 130, 255, 0.18);
  }

  .session-id {
    font-family: "SF Mono", "Menlo", monospace;
    font-size: 11px;
    color: rgba(255, 255, 255, 0.85);
  }

  .session-cwd {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.6);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    margin-top: 2px;
  }

  .session-time {
    font-size: 10px;
    color: rgba(255, 255, 255, 0.4);
    margin-top: 2px;
  }

  .empty {
    padding: 20px;
    text-align: center;
    color: rgba(255, 255, 255, 0.4);
    font-size: 12px;
  }

  .chat {
    display: flex;
    flex-direction: column;
    height: 100vh;
    overflow: hidden;
  }

  .chat-header {
    padding: 12px 20px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.06);
    display: flex;
    align-items: baseline;
    gap: 12px;
  }

  .chat-title {
    font-family: "SF Mono", monospace;
    font-size: 12px;
    color: rgba(255, 255, 255, 0.85);
  }

  .chat-cwd {
    font-size: 12px;
    color: rgba(255, 255, 255, 0.5);
  }

  .messages {
    flex: 1;
    overflow-y: auto;
    padding: 16px 20px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }

  .bubble {
    max-width: 78%;
    padding: 10px 14px;
    border-radius: 14px;
    background: rgba(255, 255, 255, 0.05);
    font-size: 13px;
    line-height: 1.45;
    word-wrap: break-word;
  }

  .bubble.user {
    align-self: flex-end;
    background: rgba(120, 130, 255, 0.25);
  }

  .bubble.assistant {
    align-self: flex-start;
    background: rgba(255, 255, 255, 0.05);
  }

  .bubble.system {
    align-self: center;
    background: rgba(255, 255, 255, 0.03);
    color: rgba(255, 255, 255, 0.5);
    font-size: 11px;
    font-family: "SF Mono", monospace;
    max-width: 60%;
  }

  .bubble-role {
    font-size: 10px;
    color: rgba(255, 255, 255, 0.5);
    margin-bottom: 4px;
    text-transform: uppercase;
    letter-spacing: 0.5px;
  }

  .bubble-text {
    white-space: pre-wrap;
  }

  .placeholder {
    flex: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255, 255, 255, 0.3);
    font-size: 14px;
  }

  .input-bar {
    padding: 12px 16px;
    border-top: 1px solid rgba(255, 255, 255, 0.06);
    display: flex;
    gap: 8px;
    align-items: center;
  }

  .target-input {
    width: 140px;
  }

  .msg-input {
    flex: 1;
  }

  input {
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.08);
    color: #e8e8ec;
    padding: 8px 12px;
    border-radius: 8px;
    font-family: inherit;
    font-size: 13px;
    outline: none;
  }

  input:focus {
    border-color: rgba(120, 130, 255, 0.6);
  }

  button[type="submit"] {
    background: rgba(120, 130, 255, 0.35);
    border: 1px solid rgba(120, 130, 255, 0.5);
    color: #e8e8ec;
    padding: 8px 16px;
    border-radius: 8px;
    font-family: inherit;
    font-size: 13px;
    cursor: pointer;
  }

  button[type="submit"]:hover {
    background: rgba(120, 130, 255, 0.5);
  }

  .status {
    font-size: 11px;
    color: rgba(255, 255, 255, 0.6);
    font-family: "SF Mono", monospace;
  }
</style>
