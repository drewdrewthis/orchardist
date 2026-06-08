#!/usr/bin/env bun
/**
 * orchard-chat channel plugin (MCP, stdio).
 *
 * Bridges the orchard-chat broker into a Claude Code session:
 *   - declares the claude/channel capability so Claude registers a listener
 *   - opens a WS to the broker, identifies via Bearer token from local config
 *   - on each broker message, emits notifications/claude/channel so the row lands
 *     in Claude's context as <channel source="orchard-chat" ...>
 *   - exposes a `chat:post` MCP tool: Claude calls it to send a message;
 *     we POST to the broker, the broker fans the row back so Claude sees its own
 *     send echoed (single source of truth for ordering).
 *   - on reconnect, calls /replay since the persisted last-seen timestamp
 *
 * Config (~/.config/orchard/local-orchardist.json):
 *   {
 *     "agent_name": "boxd_orchardist",
 *     "machine":    "boxd",
 *     "chat_token": "<32-byte hex>",
 *     "chat_listen": true,           // false -> post-only (no WS, no events)
 *     "chat_rooms":  ["general", "alerts"]   // optional; default ["general"]
 *   }
 *
 * Env (overridable, set via .mcp.json):
 *   ORCHARD_CHAT_CONFIG         path to config json
 *   ORCHARD_CHAT_BROKER_WS      ws://host:port/ws
 *   ORCHARD_CHAT_BROKER_HTTP    http://host:port
 *   ORCHARD_CHAT_LASTSEEN_DIR   per-agent lastseen state
 */

import { Server } from "@modelcontextprotocol/sdk/server/index.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import {
  CallToolRequestSchema,
  ListToolsRequestSchema,
} from "@modelcontextprotocol/sdk/types.js";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";

const CONFIG_PATH =
  process.env.ORCHARD_CHAT_CONFIG ??
  join(process.env.HOME ?? "/tmp", ".config", "orchard", "local-orchardist.json");
const BROKER_WS =
  process.env.ORCHARD_CHAT_BROKER_WS ?? "ws://orchard.boxd:8790/ws";
const BROKER_HTTP =
  process.env.ORCHARD_CHAT_BROKER_HTTP ?? "http://orchard.boxd:8790";
const LASTSEEN_DIR =
  process.env.ORCHARD_CHAT_LASTSEEN_DIR ??
  join(process.env.HOME ?? "/tmp", ".cache", "orchard-chat");

type Config = {
  agent_name: string;
  machine: string;
  chat_listen?: boolean;
  chat_rooms?: string[];
};

// Open-mode config: env vars take priority, then config file (legacy), then defaults.
// No token, no allowlist — broker accepts any self-asserted name.
function loadConfig(): Config {
  const fromFile: Partial<Config> = (() => {
    if (!existsSync(CONFIG_PATH)) return {};
    try { return JSON.parse(readFileSync(CONFIG_PATH, "utf8")); } catch { return {}; }
  })();
  const agent_name =
    process.env.ORCHARD_CHAT_AGENT ??
    process.env.AGENT_NAME ??
    fromFile.agent_name ??
    process.env.HOSTNAME ??
    "anonymous";
  const machine =
    process.env.ORCHARD_CHAT_MACHINE ??
    fromFile.machine ??
    process.env.HOSTNAME ??
    "unknown";
  const chat_listen = process.env.ORCHARD_CHAT_LISTEN
    ? process.env.ORCHARD_CHAT_LISTEN !== "false"
    : fromFile.chat_listen !== false;
  const chat_rooms =
    process.env.ORCHARD_CHAT_ROOMS?.split(",").map((s) => s.trim()).filter(Boolean) ??
    fromFile.chat_rooms ??
    ["general"];
  return { agent_name, machine, chat_listen, chat_rooms };
}

function lastSeenPath(agent: string): string {
  return join(LASTSEEN_DIR, `${agent}.lastseen`);
}
function readLastSeen(agent: string): string | null {
  const p = lastSeenPath(agent);
  if (!existsSync(p)) return null;
  return readFileSync(p, "utf8").trim();
}
function writeLastSeen(agent: string, ts: string): void {
  mkdirSync(LASTSEEN_DIR, { recursive: true });
  writeFileSync(lastSeenPath(agent), ts);
}

const cfg = loadConfig();
const canListen = cfg.chat_listen !== false; // default true if absent
const log = (...a: unknown[]) =>
  // MCP servers must keep stdout clean — log to stderr.
  console.error(`[orchard-chat]`, ...a);

const mcp = new Server(
  { name: "orchard-chat", version: "0.1.0" },
  {
    capabilities: {
      experimental: { "claude/channel": {} },
      tools: {},
    },
    instructions:
      `Cross-machine chat with other orchardists. Messages arrive as ` +
      `<channel source="orchard-chat" sender_agent="..." sender_machine="..." room="..." message_id="...">text</channel>. ` +
      `To send a message back, call the chat:post tool with {room, text}. Default room is "general". ` +
      `Keep replies terse — every orchardist on the network reads them. Don't reply to your own messages (sender_agent matches your agent name).`,
  },
);

// -- chat:post reply tool ----------------------------------------------------
mcp.setRequestHandler(ListToolsRequestSchema, async () => ({
  tools: [
    {
      name: "chat:post",
      description:
        "Post a message to the orchard chat (cross-machine). Visible to every orchardist subscribed to the room.",
      inputSchema: {
        type: "object",
        properties: {
          text: {
            type: "string",
            description: "The message body (plain text). Keep it terse.",
          },
          room: {
            type: "string",
            description:
              'Optional room. Defaults to "general". Common rooms: general, alerts, drew.',
          },
        },
        required: ["text"],
      },
    },
  ],
}));

mcp.setRequestHandler(CallToolRequestSchema, async (req) => {
  if (req.params.name !== "chat:post") {
    throw new Error(`unknown tool: ${req.params.name}`);
  }
  const { text, room } = req.params.arguments as { text: string; room?: string };
  const res = await fetch(`${BROKER_HTTP}/post`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      room: room ?? "general",
      text,
      sender_agent: cfg.agent_name,
      sender_machine: cfg.machine,
    }),
  });
  if (!res.ok) {
    const body = await res.text();
    return {
      content: [
        { type: "text", text: `chat:post failed: ${res.status} ${body}` },
      ],
      isError: true,
    };
  }
  const j = (await res.json()) as { id: string; ts: string };
  return {
    content: [{ type: "text", text: `posted id=${j.id} at ${j.ts}` }],
  };
});

// -- Connect MCP first so Claude Code knows we're alive ---------------------
await mcp.connect(new StdioServerTransport());
log(
  `connected as ${cfg.agent_name}@${cfg.machine}; listen=${canListen}; broker=${BROKER_HTTP}`,
);

// -- WS subscriber loop (only if can_listen) --------------------------------
type ChatRow = {
  id: string;
  ts: string;
  sender_agent: string;
  sender_machine: string;
  room: string;
  text: string;
};

async function deliver(row: ChatRow): Promise<void> {
  // Drop our own posts to avoid echo-spam in the orchardist's context.
  // (The post tool already returned 'posted'; landing it again is noise.)
  if (row.sender_agent === cfg.agent_name && row.sender_machine === cfg.machine) {
    writeLastSeen(cfg.agent_name, row.ts);
    return;
  }
  await mcp.notification({
    method: "notifications/claude/channel",
    params: {
      content: row.text,
      meta: {
        sender_agent: row.sender_agent,
        sender_machine: row.sender_machine,
        room: row.room,
        message_id: row.id,
      },
    },
  });
  writeLastSeen(cfg.agent_name, row.ts);
}

async function replay(since: string): Promise<void> {
  try {
    const url = `${BROKER_HTTP}/replay?since=${encodeURIComponent(since)}`;
    const res = await fetch(url);
    if (!res.ok) {
      log(`replay failed: ${res.status}`);
      return;
    }
    const j = (await res.json()) as { rows: ChatRow[] };
    for (const row of j.rows) await deliver(row);
    log(`replay delivered ${j.rows.length} rows since ${since}`);
  } catch (e) {
    log(`replay error: ${(e as Error).message}`);
  }
}

let backoff = 500;
const MAX_BACKOFF = 30_000;

function connect(): void {
  if (!canListen) return;
  const params = new URLSearchParams({
    agent: cfg.agent_name,
    machine: cfg.machine,
  });
  if (cfg.chat_rooms && cfg.chat_rooms.length > 0) {
    params.set("rooms", cfg.chat_rooms.join(","));
  }
  const url = `${BROKER_WS}?${params.toString()}`;
  const ws = new WebSocket(url);

  ws.addEventListener("open", async () => {
    log(`ws open`);
    backoff = 500;
    const since =
      readLastSeen(cfg.agent_name) ??
      new Date(Date.now() - 60 * 60 * 1000).toISOString();
    await replay(since);
  });

  ws.addEventListener("message", async (ev: MessageEvent) => {
    let msg: any;
    try {
      msg = JSON.parse(typeof ev.data === "string" ? ev.data : ev.data.toString());
    } catch {
      return;
    }
    if (msg && typeof msg === "object" && msg._control) {
      // hello/ping/pong/replay_complete — never user-facing
      return;
    }
    const row = msg as ChatRow;
    if (!row.id || !row.ts || !row.text) return;
    await deliver(row);
  });

  ws.addEventListener("close", () => {
    log(`ws closed; reconnect in ${backoff}ms`);
    setTimeout(connect, backoff);
    backoff = Math.min(MAX_BACKOFF, backoff * 2);
  });

  ws.addEventListener("error", (e: Event) => {
    log(`ws error`, (e as any)?.message ?? "(no message)");
    // close handler will reconnect
    try {
      ws.close();
    } catch {}
  });
}

connect();
