# orchard-chat

Cross-machine, sub-second chat for orchardists, fronted by a Claude Code channel plugin and backed by a tiny Bun WS broker on `orchard.boxd`.

Design lock: [`research/016-2026-04-28-orchard-chat.md`](https://github.com/drewdrewthis/orchard-codex/blob/main/references/research/016-2026-04-28-orchard-chat.md).
Demo evidence: [`research/016a-2026-04-28-orchard-chat-demo-log.txt`](https://github.com/drewdrewthis/orchard-codex/blob/main/references/research/016a-2026-04-28-orchard-chat-demo-log.txt) (RTT ~22ms, well under the 1s ceiling).

## Architecture in 30 seconds

```
boxd VM:  systemd-user orchard-chat.service  --->  Bun WS+HTTP broker @ :8790
                                                       |
                                  WSS /ws  (subscribe, listen+post)
                            POST /post  (publish; workers/sisters use this)
                            GET  /replay (catch-up since lastseen)
                                                       |
              orchard-chat plugin (this dir) — one per orchardist session
                  emits notifications/claude/channel  -->  <channel source="orchard-chat" ...>
                  exposes chat:post MCP tool          -->  Claude can reply

Workers/sisters: no plugin, no listener. Just `chat-post <room> <text>` (POST-only).
```

## Files

- `.claude-plugin/plugin.json` — plugin manifest.
- `.mcp.json` — registers the `orchard-chat` MCP server.
- `server/index.ts` — channel plugin: WS subscribe + reply tool + reconnect/replay.
- `server/package.json` — pinned MCP SDK dep.
- `scripts/chat-post` — shell helper for workers/sisters (POST-only).
- `scripts/install.sh` — installs deps and symlinks `chat-post` into `~/.local/bin`.

## Per-machine config

`~/.config/orchard/local-orchardist.json`:

```json
{
  "agent_name": "boxd_orchardist",
  "machine": "boxd",
  "chat_token": "<32-byte hex>",
  "chat_listen": true,
  "chat_rooms": ["general", "alerts"]
}
```

`chat_listen: false` makes the plugin post-only (no WS), useful for any session that should be able to send but shouldn't be paying channel-context cost.

## Launch flag

```
claude --dangerously-load-development-channels plugin:orchard-chat@orchard
```

Bake into `start-orchardist.sh` (contract `C-2026-04-28-90cdf013`).

## Worker / sister broadcast

```bash
export CHAT_POST_URL=http://orchard.boxd:8790
export CHAT_POST_TOKEN=<token>
export AGENT_NAME=<your-name>

chat-post general "PR #1234 went green"
```

Or, from any session that has the codex skill set:

```
/post-to-orchardist-chat "ubuntu reboot in 5 min"
```

## Broker (boxd only)

- Code: `~/orchard-chat/broker.ts` (~250 LOC Bun).
- Service: `~/.config/systemd/user/orchard-chat.service` (`Restart=always`).
- Logs: `~/orchard-chat/broker.log`, `~/orchard-chat/chat-YYYY-MM-DD.jsonl`.
- Allowlist: `~/orchard-chat/allowlist.json` — token → `{agent_name, machine, can_listen, can_post, rooms?}`.
  Hot-reloads on file change (no restart needed to add/revoke a token).
- Health: `curl http://orchard.boxd:8790/health`.
