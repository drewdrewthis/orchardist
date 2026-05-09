import type { Item, Lens } from "$lib/data/types";

export interface ItemGroup {
	key: string;
	label: string;
	sortKey: number;
	items: Item[];
	subgroups?: SubGroup[];
	kind?: "tmux-session" | "detached" | "none" | "channels";
	host?: string;
	sessionName?: string;
}

export interface SubGroup {
	key: string;
	label: string;
	idx: number;
	items: Item[];
}

const STATUS_ORDER: Record<string, number> = {
	attn: 0,
	bad: 1,
	ok: 2,
	idle: 3,
	stale: 4,
};

export function groupItems(items: Item[], lens: Lens, now: number): ItemGroup[] {
	const groups = new Map<string, ItemGroup>();
	const subgroupMaps = new Map<string, Map<string, SubGroup>>();
	const push = (key: string, label: string, item: Item, sortKey = 0) => {
		if (!groups.has(key)) {
			groups.set(key, { key, label, sortKey, items: [] });
		}
		groups.get(key)!.items.push(item);
	};

	const channels = items.filter((it) => it.kind === "channel");
	const rest = items.filter((it) => it.kind !== "channel");
	for (const ch of channels) push("channels", "Channels", ch, -1);
	const channelsGroup = groups.get("channels");
	if (channelsGroup) channelsGroup.kind = "channels";

	if (lens === "attention") {
		for (const it of rest) {
			if (it.kind !== "worktree") {
				push("other", "Other", it, 5);
				continue;
			}
			if (it.status === "attn") push("attn", "Attention", it, 0);
			else if (it.status === "bad") push("bad", "Blocked", it, 1);
			else if (it.session?.live) push("active", "Active", it, 2);
			else if (it.status === "idle" || !it.session) push("idle", "Idle", it, 3);
			else if (it.status === "stale") push("stale", "Stale", it, 4);
			else push("other", "Other", it, 5);
		}
	} else if (lens === "host") {
		for (const it of rest) {
			if (it.kind !== "worktree") continue;
			push("host:" + it.host, it.host, it, 0);
		}
	} else if (lens === "tmux") {
		for (const it of rest) {
			if (it.kind !== "worktree") continue;
			if (it.tmux) {
				const gKey = `tmux:${it.host}/${it.tmux.session}`;
				const gLabel = `${it.host} · ${it.tmux.session}`;
				if (!groups.has(gKey)) {
					groups.set(gKey, {
						key: gKey,
						label: gLabel,
						sortKey: 0,
						items: [],
						kind: "tmux-session",
						host: it.host,
						sessionName: it.tmux.session,
					});
					subgroupMaps.set(gKey, new Map());
				}
				const g = groups.get(gKey)!;
				const subs = subgroupMaps.get(gKey)!;
				const wKey = `w:${it.tmux.window.idx}`;
				if (!subs.has(wKey)) {
					subs.set(wKey, {
						key: wKey,
						label: `window ${it.tmux.window.idx} · ${it.tmux.window.name}`,
						idx: it.tmux.window.idx,
						items: [],
					});
				}
				subs.get(wKey)!.items.push(it);
				g.items.push(it);
			} else if (it.session && !it.session.live) {
				push("tmux:detached", "Detached sessions", it, 8);
				const g = groups.get("tmux:detached");
				if (g) g.kind = "detached";
			} else {
				push("tmux:none", "No tmux", it, 9);
				const g = groups.get("tmux:none");
				if (g) g.kind = "none";
			}
		}
		for (const [key, subs] of subgroupMaps) {
			const g = groups.get(key);
			if (g) g.subgroups = [...subs.values()].sort((a, b) => a.idx - b.idx);
		}
	} else if (lens === "repo") {
		for (const it of rest) {
			if (it.kind !== "worktree") continue;
			push("repo:" + it.repo, it.repo, it, 0);
		}
	} else if (lens === "issue") {
		for (const it of rest) {
			if (it.kind !== "worktree") continue;
			if (it.issue)
				push("issue:#" + it.issue.number, "#" + it.issue.number + " · " + it.repo, it, 0);
			else push("issue:none", "No linked issue", it, 99);
		}
	} else if (lens === "activity") {
		for (const it of rest) {
			if (it.kind !== "worktree") continue;
			const age = now - it.lastActivity;
			if (age < 10 * 60_000) push("a:now", "Last 10 minutes", it, 0);
			else if (age < 60 * 60_000) push("a:hr", "Last hour", it, 1);
			else if (age < 24 * 3_600_000) push("a:day", "Today", it, 2);
			else push("a:older", "Earlier", it, 3);
		}
	}

	for (const g of groups.values()) {
		g.items.sort((a, b) => {
			const oa = STATUS_ORDER[a.status] ?? 9;
			const ob = STATUS_ORDER[b.status] ?? 9;
			if (oa !== ob) return oa - ob;
			const da = b.lastActivity - a.lastActivity;
			if (da !== 0) return da;
			return a.id.localeCompare(b.id);
		});
	}

	return [...groups.values()].sort((a, b) => a.sortKey - b.sortKey);
}
