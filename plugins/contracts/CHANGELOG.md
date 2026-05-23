# Changelog

## 0.8.0

- Inlined into git-orchard-rs at `plugins/contracts/` as part of the contracts domain refactor (#630).
- Added `orchardclaude.targets` to `.claude-plugin/plugin.json` declaring the `contracts` domain interface at `api_version ^0.1.0`.
- TODO: Rewire MCP server to consume canonical `scripts/contracts/{add,update,list,get}.sh` instead of reading/writing JSONL directly. Blocked on resolving the daemon-backed read path (list.sh/get.sh use GraphQL) vs. the local fold path used by the current server. See inline TODO in `server/index.ts`.

## 0.7.0

- Initial release in orchard-codex. Append-only JSONL store, three v1 tools (`add_contract`, `update_contract`, `list_contracts`) plus `get_contract`. Stop-hook reminder. ADR-011 spec.
