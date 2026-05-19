# `orchard` marketplace

The orchard marketplace. Today this exists primarily to **pin the `claude-contracts` plugin at a specific commit** so that fresh hosts can install the contracts MCP server without first cloning `drewdrewthis/orchard-codex`.

## Install

From a Claude Code session on a fresh host:

```
/plugin marketplace add drewdrewthis/git-orchard-rs
/plugin install claude-contracts@orchard
```

After install, the contracts MCP server (`add_contract`, `update_contract`, `list_contracts`, `get_contract` tools) is available in every Claude session on that host.

## What is pinned

See [`marketplace.json`](./.claude-plugin/marketplace.json) for the current pin set. The pin is a `(repo URL, path, ref, sha)` tuple:

- `url`: the upstream repo (today: `drewdrewthis/orchard-codex.git`).
- `path`: the subdirectory containing the plugin source (today: `plugin-sources/claude-contracts`).
- `ref`: the branch name for human-readable updates (today: `main`).
- `sha`: the **exact commit** the plugin is installed from. This is the canonical pin.

When the upstream plugin moves, the `sha` is bumped via a single-file PR against this repo. Consumers re-install via `/plugin upgrade` to pick up the new pin.

## Spec ownership

The `claude-contracts` plugin's spec lives in the plugin source itself at [`plugin-sources/claude-contracts/spec/`](https://github.com/drewdrewthis/orchard-codex/tree/main/plugin-sources/claude-contracts/spec) in the orchard-codex repo. Per [ADR-023](../../../docs/adr/023-contracts-spec-ownership.md), spec ownership belongs to the plugin + marketplace; downstream consumers (this repo's daemon, the Stop-hook, the GUI) adopt the plugin's spec, they do not define it.

## Why a marketplace here

The contracts plugin was historically installed by cloning the codex directly. That works for the codex's owner but not for:

- Fresh boxd VMs that don't have the codex cloned.
- New contributors who don't need the rest of the codex.
- CI hosts that should install the minimum set.

The marketplace pin solves these: the contracts plugin is fetched directly from its upstream subdirectory, at a known sha, without bringing the rest of the codex along.

## Version bump flow

1. Spec or implementation change committed to `drewdrewthis/orchard-codex` (direct-to-main per [orchard-codex ADR-014](https://github.com/drewdrewthis/orchard-codex/blob/main/adrs/014-contracts-plugin-lifecycle.md)).
2. Plugin's `plugin.json` `version` bumped in the same codex commit.
3. PR against this repo updating `marketplace.json` to:
   - Bump the plugin's `version` field.
   - Bump the `sha` to the codex commit that landed the change.
4. Consumers install with `/plugin upgrade claude-contracts@orchard`.

## Daemon coordination

When the spec change requires daemon behaviour changes (as the 2026-05-19 status enum collapse did), the daemon update lands in this repo in the **same PR** that bumps the marketplace pin. That guarantees a fresh `git pull && cargo install` on this repo brings a coordinated plugin + daemon together.

A future enhancement is a `compatible-daemon-version` field on the plugin pin to surface incompatibilities; until then, the PR coupling is the enforcement.
