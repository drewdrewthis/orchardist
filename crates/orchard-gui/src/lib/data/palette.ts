/**
 * Build palette entries from live store data. Pure function — no mock.
 *
 * The palette is a derived view of `items` + `hosts` + chat rooms; it must
 * not contain any data the user can't navigate to via the rest of the UI.
 */

import type { Host, Item, PaletteEntry } from "./types";

export function buildPaletteEntries(
	items: Item[],
	hosts: Host[],
	chatRooms: { id: string }[],
): PaletteEntry[] {
	const out: PaletteEntry[] = [];

	for (const it of items) {
		if (it.kind !== "worktree") continue;
		out.push({
			kind: "worktree",
			label: it.title,
			sub: `${it.repo} · ${it.branch}`,
			host: it.host,
			anchor: it.id,
			itemId: it.id,
			group: "Worktrees",
			keywords: [it.repo, it.branch, it.path, it.title].join(" ").toLowerCase(),
		});
		if (it.pr) {
			out.push({
				kind: "pr",
				label: `${it.repo} · PR #${it.pr.number}`,
				sub: it.title,
				host: it.host,
				anchor: `${it.id}/pr`,
				itemId: it.id,
				group: "Pull requests",
				keywords: [`#${it.pr.number}`, it.title, it.repo, it.branch].join(" ").toLowerCase(),
			});
		}
		if (it.issue) {
			out.push({
				kind: "issue",
				label: `${it.repo} · #${it.issue.number}`,
				sub: it.title,
				host: it.host,
				anchor: `${it.id}/issue`,
				itemId: it.id,
				group: "Issues",
				keywords: [`#${it.issue.number}`, it.title, it.repo].join(" ").toLowerCase(),
			});
		}
	}

	for (const host of hosts) {
		out.push({
			kind: "host",
			label: host.hostname,
			sub: host.reachable ? "reachable" : "unreachable",
			host: host.hostname,
			anchor: host.id,
			group: "Hosts",
			keywords: [host.hostname, host.os].join(" ").toLowerCase(),
		});
	}

	for (const r of chatRooms) {
		const id = r.id;
		out.push({
			kind: "channel",
			label: id.startsWith("@") ? id : `#${id}`,
			sub: "chat room",
			anchor: id,
			itemId: id,
			group: "Channels",
			keywords: id.toLowerCase(),
		});
	}

	return out;
}

export const PALETTE_ACTIONS: PaletteEntry[] = [
	{ kind: "action", label: "Launch new conversation", sub: "Pick a worktree + host", shortcut: ["⌘", "N"], action: "new-conversation", group: "Actions", keywords: "new conversation launch start" },
	{ kind: "action", label: "Switch lens · By attention", action: "lens:attention", group: "Actions", keywords: "lens attention" },
	{ kind: "action", label: "Switch lens · By host", action: "lens:host", group: "Actions", keywords: "lens host" },
	{ kind: "action", label: "Switch lens · By recent activity", action: "lens:activity", group: "Actions", keywords: "lens activity recent" },
	{ kind: "action", label: "Switch lens · By repo", action: "lens:repo", group: "Actions", keywords: "lens repo" },
	{ kind: "action", label: "Switch lens · By tmux", action: "lens:tmux", group: "Actions", keywords: "lens tmux" },
	{ kind: "action", label: "Toggle theme", action: "toggle-theme", group: "Actions", keywords: "theme dark light" },
	{ kind: "action", label: "Toggle terminal view", shortcut: ["⌘", "\\"], action: "toggle-view", group: "Actions", keywords: "view terminal chat toggle" },
];
