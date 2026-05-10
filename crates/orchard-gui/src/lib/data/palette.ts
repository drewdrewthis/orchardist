/**
 * Build palette entries from live Houdini data + chat rooms. Pure
 * function — no AppStore intermediate. Each entry carries the row
 * identity (paneId, sessionUuid, roomId) the panel needs to open it.
 *
 * The palette is a derived view of `worktreesStore`/`hostsStore` +
 * the chat-room list; it must not contain any data the user can't
 * navigate to via the rest of the UI.
 */

import type { PaletteEntry } from "./types";
import type { HostRow, WorktreePickerRow } from "./daemon-stores";

export function buildPaletteEntries(
	worktrees: WorktreePickerRow[],
	hosts: HostRow[],
	chatRooms: { id: string }[],
): PaletteEntry[] {
	const out: PaletteEntry[] = [];

	for (const w of worktrees) {
		out.push({
			kind: "session",
			label: w.branch,
			sub: `${w.repo} · ${w.host}`,
			host: w.host,
			group: "Worktrees",
			keywords: [w.repo, w.branch, w.path].join(" ").toLowerCase(),
		});
	}

	for (const h of hosts) {
		out.push({
			kind: "host",
			label: h.hostname,
			sub: h.reachable ? "reachable" : "unreachable",
			host: h.hostname,
			group: "Hosts",
			keywords: [h.hostname, h.os, h.kernel || ""].join(" ").toLowerCase(),
		});
	}

	for (const r of chatRooms) {
		const id = r.id;
		out.push({
			kind: "channel",
			label: id.startsWith("@") ? id : `#${id}`,
			sub: "chat room",
			roomId: id,
			group: "Channels",
			keywords: id.toLowerCase(),
		});
	}

	return out;
}

export const PALETTE_ACTIONS: PaletteEntry[] = [
	{ kind: "action", label: "Launch new conversation", sub: "Pick a worktree + host", shortcut: ["⌘", "N"], action: "new-conversation", group: "Actions", keywords: "new conversation launch start" },
	{ kind: "action", label: "Switch lens · Attention", action: "lens:attention", group: "Actions", keywords: "lens attention" },
	{ kind: "action", label: "Switch lens · Recent", action: "lens:recent", group: "Actions", keywords: "lens recent activity" },
	{ kind: "action", label: "Switch lens · Tmux", action: "lens:tmux", group: "Actions", keywords: "lens tmux" },
	{ kind: "action", label: "Switch lens · Issue", action: "lens:issue", group: "Actions", keywords: "lens issue work" },
	{ kind: "action", label: "Toggle theme", action: "toggle-theme", group: "Actions", keywords: "theme dark light" },
	{ kind: "action", label: "Toggle terminal view", shortcut: ["⌘", "\\"], action: "toggle-view", group: "Actions", keywords: "view terminal chat toggle" },
];
