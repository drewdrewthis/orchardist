/**
 * Mock data — plausible fleet/conversation/channel content for visual development.
 *
 * Ported from design-prototype/project/data.jsx. Will be replaced by `graphql.ts`
 * once the daemon wiring lands. Keep this file consistent with `types.ts`.
 */

import type {
	Account,
	Agent,
	ChannelItem,
	Conversation,
	Host,
	Item,
	PaletteEntry,
	TerminalLine,
	WorktreeItem,
} from "./types";

const NOW = Date.now();
export const m = (n: number) => NOW - n * 60_000;
export const h = (n: number) => NOW - n * 3_600_000;
export const d = (n: number) => NOW - n * 86_400_000;

export const NOW_MS = NOW;

export const hosts: Host[] = [
	{ id: "h.drew-mac", hostname: "drew-mac", os: "macOS 15.4", kernel: "Darwin 24.4.0", reachable: true, load: { cpu: 22, mem: 41, disk: 64 }, lastSeenAt: m(0), tag: "primary" },
	{ id: "h.drew-linux", hostname: "drew-linux", os: "Ubuntu 24.04", kernel: "Linux 6.8.0-31", reachable: true, load: { cpu: 71, mem: 58, disk: 32 }, lastSeenAt: m(0), tag: "workhorse" },
	{ id: "h.drew-cloud", hostname: "drew-cloud", os: "NixOS 24.05", kernel: "Linux 6.6.32", reachable: true, load: { cpu: 8, mem: 22, disk: 18 }, lastSeenAt: m(1), tag: "remote" },
	{ id: "h.drew-air", hostname: "drew-air", os: "macOS 15.4", kernel: "Darwin 24.4.0", reachable: false, load: { cpu: 0, mem: 0, disk: 0 }, lastSeenAt: m(38), tag: "travel" },
];

export const account: Account = {
	email: "drew@orchard.dev",
	quotaUsed: 142,
	quotaCap: 200,
	quotaResetsAt: h(4),
};

export const worktreeItems: WorktreeItem[] = [
	{
		id: "w.orchard.api-pagination",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "fix/api-pagination",
		path: "~/code/orchard/wt/api-pagination",
		host: "drew-mac",
		title: "Fix cursor pagination on /sessions endpoint",
		status: "attn",
		attentionReason: "Agent paused — needs review of new test fixtures",
		lastActivity: m(2),
		unread: 3,
		session: { uuid: "a8f2-3c91", live: true, instance: "pane:0.1", model: "claude-sonnet-4-5" },
		tmux: { server: "orchard", session: "orchard", window: { idx: 0, name: "code" }, pane: 1 },
		pr: { number: 482, state: "open", ci: "passing", reviews: "changes-requested" },
		issue: { number: 471, state: "open" },
		contract: { id: "c.482", status: "open", openQuestions: 1 },
		sparkline: [3, 2, 4, 2, 5, 3, 6, 4, 3, 5, 4, 7, 3, 2, 1, 4, 3, 5, 6, 8],
	},
	{
		id: "w.orchard.federation-stale",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "feat/federation-stale-states",
		path: "~/code/orchard/wt/federation",
		host: "drew-linux",
		title: "Stale-state propagation across peers",
		status: "attn",
		attentionReason: "CI failing on integration suite",
		lastActivity: m(7),
		unread: 1,
		session: { uuid: "4d10-bc77", live: true, instance: "pane:0.0", model: "claude-sonnet-4-5" },
		tmux: { server: "orchard", session: "workhorse", window: { idx: 0, name: "federation" }, pane: 0 },
		pr: { number: 488, state: "open", ci: "failing", reviews: "pending" },
		issue: null,
		contract: null,
		sparkline: [1, 2, 1, 3, 4, 2, 3, 5, 4, 2, 1, 2, 3, 2, 4, 5, 3, 2, 3, 4],
	},
	{
		id: "w.orchard.gui2-fleet",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "gui2/fleet-view",
		path: "~/code/orchard/wt/gui2-fleet",
		host: "drew-mac",
		title: "Fleet view — first hi-fi pass",
		status: "ok",
		attentionReason: null,
		lastActivity: m(0),
		unread: 0,
		session: { uuid: "b1ee-2a04", live: true, instance: "pane:0.0", model: "claude-sonnet-4-5" },
		tmux: { server: "orchard", session: "orchard", window: { idx: 0, name: "code" }, pane: 0 },
		pr: null,
		issue: { number: 503, state: "open" },
		contract: { id: "c.gui2", status: "open", openQuestions: 0 },
		sparkline: [4, 5, 3, 6, 5, 7, 8, 6, 5, 7, 8, 9, 7, 6, 8, 7, 9, 8, 7, 8],
	},
	{
		id: "w.orchard.daemon-perf",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "perf/daemon-bootstrap",
		path: "~/code/orchard/wt/daemon-perf",
		host: "drew-linux",
		title: "Daemon cold-start under 200ms",
		status: "ok",
		attentionReason: null,
		lastActivity: m(14),
		unread: 0,
		session: { uuid: "7a3c-9e2f", live: true, instance: "pane:1.0", model: "claude-haiku-4-5" },
		tmux: { server: "orchard", session: "workhorse", window: { idx: 1, name: "perf" }, pane: 0 },
		pr: { number: 491, state: "open", ci: "passing", reviews: "approved" },
		issue: null,
		contract: { id: "c.perf", status: "open", openQuestions: 0 },
		sparkline: [2, 1, 3, 2, 4, 3, 2, 3, 4, 5, 3, 4, 2, 3, 4, 3, 2, 3, 4, 3],
	},
	{
		id: "w.langwatch.metrics-dashboard",
		kind: "worktree",
		repo: "langwatch/langwatch",
		branch: "feat/metrics-dashboard",
		path: "~/code/langwatch/wt/metrics",
		host: "drew-cloud",
		title: "Metrics dashboard — usage cohorts",
		status: "idle",
		attentionReason: null,
		lastActivity: h(3),
		unread: 0,
		session: { uuid: "e2d1-4f88", live: false, instance: null, model: "claude-sonnet-4-5" },
		pr: null,
		issue: { number: 1204, state: "open" },
		contract: null,
		sparkline: [1, 2, 1, 2, 3, 2, 1, 2, 3, 2, 1, 1, 2, 3, 2, 1, 1, 2, 1, 2],
	},
	{
		id: "w.orchard.contracts-spec",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "spec/contracts-v2",
		path: "~/code/orchard/wt/contracts-spec",
		host: "drew-mac",
		title: "Contracts spec — proposal for v2",
		status: "attn",
		attentionReason: "Open question from agent",
		lastActivity: m(22),
		unread: 2,
		session: { uuid: "cc01-1aaa", live: true, instance: "pane:1.0", model: "claude-opus-4-1" },
		tmux: { server: "orchard", session: "orchard", window: { idx: 1, name: "contracts" }, pane: 0 },
		pr: null,
		issue: null,
		contract: { id: "c.contracts-v2", status: "open", openQuestions: 2 },
		sparkline: [2, 3, 1, 2, 4, 3, 2, 3, 5, 4, 2, 3, 4, 2, 1, 2, 3, 4, 3, 2],
	},
	{
		id: "w.orchard.tmux-binding",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "fix/tmux-pane-bindings",
		path: "~/code/orchard/wt/tmux",
		host: "drew-mac",
		title: "tmux pane binding edge cases",
		status: "idle",
		attentionReason: null,
		lastActivity: h(7),
		unread: 0,
		session: { uuid: "0fa1-77b2", live: false, instance: null, model: "claude-sonnet-4-5" },
		pr: { number: 478, state: "merged", ci: "passing", reviews: "approved" },
		issue: null,
		contract: null,
		sparkline: [1, 1, 2, 1, 1, 2, 1, 2, 1, 1, 2, 1, 1, 1, 2, 1, 1, 1, 1, 1],
	},
	{
		id: "w.docs.adr-009",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "docs/adr-009-anchors",
		path: "~/code/orchard/wt/adr-009",
		host: "drew-cloud",
		title: "ADR-009 — anchor nodes",
		status: "ok",
		attentionReason: null,
		lastActivity: h(1),
		unread: 0,
		session: { uuid: "9c11-aa22", live: false, instance: null, model: "claude-sonnet-4-5" },
		pr: { number: 495, state: "draft", ci: "passing", reviews: "pending" },
		issue: null,
		contract: null,
		sparkline: [2, 1, 2, 1, 2, 2, 1, 2, 1, 2, 1, 2, 1, 1, 2, 1, 2, 1, 2, 1],
	},
	{
		id: "w.orchard.travel-bug",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "wip/travel-mode",
		path: "~/code/orchard/wt/travel",
		host: "drew-air",
		title: "Travel mode — local-only fallback",
		status: "stale",
		attentionReason: null,
		lastActivity: m(38),
		unread: 0,
		session: { uuid: "12bc-fa31", live: false, instance: null, model: "claude-haiku-4-5" },
		pr: null,
		issue: null,
		contract: null,
		sparkline: [1, 1, 2, 1, 1, 1, 2, 1, 1, 1, 1, 1, 2, 1, 1, 1, 1, 1, 1, 1],
	},
	{
		id: "w.orchard.bare-checklist",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "chore/release-checklist",
		path: "~/code/orchard/wt/release",
		host: "drew-mac",
		title: "Release checklist (bare)",
		status: "idle",
		bare: true,
		attentionReason: null,
		lastActivity: d(2),
		unread: 0,
		session: null,
		pr: null,
		issue: null,
		contract: null,
		sparkline: [],
	},
	{
		id: "w.orchard.search-perf",
		kind: "worktree",
		repo: "langwatch/orchard",
		branch: "perf/search-index",
		path: "~/code/orchard/wt/search",
		host: "drew-linux",
		title: "Faster fuzzy search across hosts",
		status: "ok",
		attentionReason: null,
		lastActivity: m(31),
		unread: 0,
		session: { uuid: "5e88-1234", live: true, instance: "pane:1.1", model: "claude-sonnet-4-5" },
		tmux: { server: "orchard", session: "workhorse", window: { idx: 1, name: "perf" }, pane: 1 },
		pr: { number: 499, state: "open", ci: "pending", reviews: "pending" },
		issue: null,
		contract: null,
		sparkline: [2, 3, 2, 3, 4, 3, 4, 3, 2, 3, 4, 5, 4, 3, 4, 3, 4, 3, 4, 5],
	},
	{
		id: "w.langwatch.instrumentation",
		kind: "worktree",
		repo: "langwatch/langwatch",
		branch: "feat/instrumentation-otel",
		path: "~/code/langwatch/wt/otel",
		host: "drew-cloud",
		title: "OTEL instrumentation across SDKs",
		status: "attn",
		attentionReason: "PR review: 4 comments",
		lastActivity: m(45),
		unread: 4,
		session: { uuid: "aa11-77f0", live: false, instance: null, model: "claude-sonnet-4-5" },
		pr: { number: 1198, state: "open", ci: "passing", reviews: "commented" },
		issue: null,
		contract: null,
		sparkline: [3, 4, 3, 5, 4, 5, 3, 4, 5, 4, 3, 4, 5, 3, 4, 5, 4, 3, 4, 5],
	},
];

export const conversation: Conversation = {
	itemId: "w.orchard.api-pagination",
	recap:
		"Working on cursor-based pagination for /sessions. Added forward & reverse cursors, decided to keep limit ≤ 200, drafted property-based tests against the existing fixture.",
	messages: [
		{ id: "m1", role: "user", status: "read", ts: m(38), text: "let's tackle the cursor pagination on /sessions. take a look at how list_runs does it and propose a shape" },
		{ id: "m2", role: "agent", status: "delivered", ts: m(37), text: "Reading `list_runs.rs:42-118` and `routes/sessions.rs`. The runs endpoint uses an opaque base64 cursor encoding `(created_at, id)`. I propose mirroring that shape on `/sessions` so the cursor stays opaque and tie-breaks on `id`.", tools: ["read", "grep"] },
		{ id: "m3", role: "user", status: "read", ts: m(34), text: "sounds right. one constraint: limit caps at 200, default 50. forward + reverse." },
		{ id: "m4", role: "agent", status: "delivered", ts: m(33), text: "Got it. Sketching the handler now.", tools: [] },
		{ id: "m5", role: "agent", status: "delivered", ts: m(31), text: "Drafted `cursor.rs` with `Cursor::encode` / `Cursor::decode`. Adding the query params (`after`, `before`, `limit`).", tools: ["edit"] },
		{ id: "m6", role: "agent", status: "delivered", ts: m(28), text: "Test fixture for the existing /sessions returns 14 records. I'll add a property-based test (proptest crate) that checks `forward(after=cursor(N)) ++ reverse(before=cursor(N))` reconstructs the full list.", tools: ["edit", "bash"] },
		{ id: "m7", role: "user", status: "read", ts: m(25), text: "good. show me the diff before you run cargo test." },
		{ id: "m8", role: "agent", status: "delivered", ts: m(24), text: "Diff against `routes/sessions.rs` and the new `cursor.rs`:", tools: [], diff: { plus: 142, minus: 18, files: 3 } },
		{ id: "m9", role: "user", status: "read", ts: m(15), text: "ship it. ⏎" },
		{ id: "m10", role: "agent", status: "delivered", ts: m(13), text: "`cargo test -p orchard-api` ran 218 tests, 0 failed. Pushing to `fix/api-pagination` and opening PR #482.", tools: ["bash", "git"] },
		{ id: "m11", role: "agent", status: "delivered", ts: m(11), text: "PR #482 opened against `main`. CI is green on lint + unit; integration suite running.", tools: ["gh"] },
		{ id: "m12", role: "agent", status: "delivered", ts: m(8), text: "__contract__: I have a blocking question. The new `cursor` query param shadows an existing client-side filter that one of the SDKs ships under the same name. Want me to namespace as `cursor_v2`, or break v1 silently?", isQuestion: true },
		{ id: "m13", role: "user", status: "read", ts: m(5), text: "let me think. give me 2 minutes." },
		{ id: "m14", role: "agent", status: "delivered", ts: m(4), text: "Standing by — paused before `git push`. The branch has 2 unpushed commits.", isPaused: true },
	],
};

export const terminalLines: TerminalLine[] = [
	{ c: "dim", t: "[orchard·tmux:fix/api-pagination] drew-mac · pane 0.1 · attached" },
	{ c: "dim", t: "claude-code 0.7.4 · sonnet-4-5 · session a8f2-3c91" },
	{ c: "", t: "" },
	{ c: "p", t: "$ rg -n \"encode_cursor\" crates/orchard-api/src" },
	{ c: "dim", t: "crates/orchard-api/src/list_runs.rs:53:    let cursor = encode_cursor(&row);" },
	{ c: "dim", t: "crates/orchard-api/src/list_runs.rs:91:    let cursor = encode_cursor(&row);" },
	{ c: "", t: "" },
	{ c: "p", t: "$ cargo test -p orchard-api" },
	{ c: "dim", t: "   Compiling orchard-api v0.7.4 (crates/orchard-api)" },
	{ c: "dim", t: "    Finished test [unoptimized + debuginfo] target(s) in 8.41s" },
	{ c: "dim", t: "     Running unittests src/lib.rs (target/debug/deps/orchard_api-9c1f)" },
	{ c: "ok", t: "test result: ok. 218 passed; 0 failed; 0 ignored; 0 measured; finished in 4.92s" },
	{ c: "", t: "" },
	{ c: "p", t: "$ git add . && git commit -m \"fix: cursor pagination on /sessions\"" },
	{ c: "dim", t: "[fix/api-pagination 4f3c92] fix: cursor pagination on /sessions" },
	{ c: "dim", t: " 3 files changed, 142 insertions(+), 18 deletions(-)" },
	{ c: "", t: "" },
	{ c: "p", t: "$ gh pr create --title \"Fix cursor pagination on /sessions\"" },
	{ c: "dim", t: "Creating pull request for fix/api-pagination into main in langwatch/orchard" },
	{ c: "ok", t: "https://github.com/langwatch/orchard/pull/482" },
	{ c: "", t: "" },
	{ c: "attn", t: "⌁ paused — open question logged to contract c.482" },
	{ c: "", t: "" },
	{ c: "live", t: "⏵ awaiting input" },
];

export const agents: Agent[] = [
	{ id: "a.architect", name: "Architect", hue: 215, model: "opus-4-1", role: "Planner", host: "drew-mac", avatar: "A" },
	{ id: "a.fixer", name: "Fixer", hue: 25, model: "sonnet-4-5", role: "Patcher", host: "drew-mac", avatar: "F" },
	{ id: "a.reviewer", name: "Reviewer", hue: 155, model: "sonnet-4-5", role: "Reviewer", host: "drew-linux", avatar: "R" },
	{ id: "a.scout", name: "Scout", hue: 260, model: "haiku-4-5", role: "Researcher", host: "drew-cloud", avatar: "S" },
	{ id: "a.qa", name: "QA", hue: 70, model: "sonnet-4-5", role: "Tester", host: "drew-linux", avatar: "Q" },
	{ id: "a.docs", name: "Docs", hue: 295, model: "haiku-4-5", role: "Writer", host: "drew-cloud", avatar: "D" },
];

export const channels: ChannelItem[] = [
	{
		id: "ch.api-pagination-review",
		kind: "channel",
		title: "api-pagination · review",
		topic: "Coordinating review + ship for PR #482",
		participants: ["a.architect", "a.fixer", "a.reviewer"],
		host: "multi",
		repo: "langwatch/orchard",
		status: "attn",
		attentionReason: "Reviewer flagged 2 spots",
		lastActivity: m(3),
		unread: 4,
		pinned: true,
		sparkline: [2, 3, 2, 4, 3, 5, 4, 3, 6, 5, 4, 3, 5, 6, 4, 5, 3, 4, 5, 6],
	},
	{
		id: "ch.federation-debugging",
		kind: "channel",
		title: "federation · stale-states",
		topic: "Debugging the integration suite — running jobs in parallel",
		participants: ["a.architect", "a.fixer", "a.qa"],
		host: "multi",
		repo: "langwatch/orchard",
		status: "ok",
		attentionReason: null,
		lastActivity: m(11),
		unread: 0,
		sparkline: [1, 2, 3, 2, 3, 4, 3, 2, 3, 4, 3, 2, 3, 4, 2, 3, 4, 3, 2, 3],
	},
	{
		id: "ch.adr-009-discussion",
		kind: "channel",
		title: "adr-009 · anchor model",
		topic: "Sketching what's first-class, what's derived",
		participants: ["a.architect", "a.scout", "a.docs"],
		host: "multi",
		repo: "langwatch/orchard",
		status: "idle",
		attentionReason: null,
		lastActivity: h(2),
		unread: 0,
		sparkline: [1, 1, 2, 1, 2, 1, 1, 2, 1, 2, 1, 1, 2, 1, 1, 2, 1, 1, 1, 1],
	},
];

export const channelConversations: Record<string, Conversation> = {
	"ch.api-pagination-review": {
		itemId: "ch.api-pagination-review",
		recap:
			"Architect proposed cursor mirroring runs-shape; Fixer drafted impl + tests; Reviewer caught two issues (cursor ordering near tie-breaks; missing reverse limit guard).",
		isChannel: true,
		messages: [
			{ id: "cm1", role: "agent", agentId: "a.architect", status: "read", ts: m(46), text: "Picking this up. I think the right move is to mirror `list_runs.rs`'s cursor encoding — opaque base64 of `(created_at, id)`. Same shape across endpoints means the SDK doesn't have to learn two." },
			{ id: "cm2", role: "agent", agentId: "a.fixer", status: "read", ts: m(42), text: "Drafted `cursor.rs` mirroring that. Added forward + reverse, limit defaults to 50, hard cap at 200. PR up at #482.", tools: ["edit", "gh"] },
			{ id: "cm3", role: "agent", agentId: "a.reviewer", status: "read", ts: m(28), text: "Two notes on the diff:\n• `decode_cursor` doesn't tie-break on `id` when `created_at` ties — that breaks ordering at high write rate.\n• reverse path is missing the limit guard." },
			{ id: "cm4", role: "user", status: "read", ts: m(24), text: "good catches. fixer — pick those up. architect — sanity check?" },
			{ id: "cm5", role: "agent", agentId: "a.architect", status: "read", ts: m(22), text: "Both are real. Tie-break should match `list_runs` exactly. Reverse limit guard belongs in the same place as forward — let's not duplicate." },
			{ id: "cm6", role: "agent", agentId: "a.fixer", status: "delivered", ts: m(8), text: "On it. Pulled out a `paginate::guard_limit` helper; both paths call it. Re-encoded cursor includes `id` as the secondary sort. Tests added.", tools: ["edit", "bash"], diff: { plus: 38, minus: 12, files: 2 } },
			{ id: "cm7", role: "agent", agentId: "a.reviewer", status: "delivered", ts: m(3), text: "Looks right. One nit: the helper name is fine but the error type should be `PaginateError::OverLimit`, not `BadRequest`. Approving once that's renamed." },
		],
	},
	"ch.federation-debugging": {
		itemId: "ch.federation-debugging",
		recap: "CI was flaking on the federation integration suite. QA reproed locally; Fixer is bisecting; Architect is coordinating.",
		isChannel: true,
		messages: [
			{ id: "cf1", role: "agent", agentId: "a.qa", status: "read", ts: m(58), text: "Reproed locally — `peer_stale_propagation` test fails ~1 in 4 runs. Smells like an ordering race." },
			{ id: "cf2", role: "agent", agentId: "a.architect", status: "read", ts: m(52), text: "If it's a race, it's probably in the `Meta` envelope merge path — that's the only place we order by `lastSeenAt` and it's not tie-broken." },
			{ id: "cf3", role: "agent", agentId: "a.fixer", status: "read", ts: m(40), text: "Bisecting. I'll narrow the commit window and report back.", tools: ["bash", "git"] },
			{ id: "cf4", role: "agent", agentId: "a.fixer", status: "read", ts: m(18), text: "Got it: 4f3c92 introduced the race. Same `lastSeenAt` ties between two peers cause undefined order. Fix is the same tie-break trick — secondary sort on `host_id`. Cooking it now." },
			{ id: "cf5", role: "agent", agentId: "a.architect", status: "delivered", ts: m(11), text: "Nice find. Once you push, kick off CI 5x — we want statistical confidence on a flake." },
		],
	},
	"ch.adr-009-discussion": {
		itemId: "ch.adr-009-discussion",
		recap: "Drafting ADR-009 — what's an anchor, what's derived. Three perspectives, sorting it out.",
		isChannel: true,
		messages: [
			{ id: "ca1", role: "agent", agentId: "a.architect", status: "read", ts: h(3), text: "I'd argue anchors are exactly: Worktree, ClaudeSession, GitHub-by-number. Everything else is a join." },
			{ id: "ca2", role: "agent", agentId: "a.scout", status: "read", ts: h(3), text: "Looked at how Linear models this — they have a similar split: durable anchors and view-time projections. We're not crazy." },
			{ id: "ca3", role: "agent", agentId: "a.docs", status: "read", ts: h(2), text: "If we go with that, the doc structure writes itself: 'Anchors' chapter, 'Joins' chapter, 'Views' chapter. Want me to scaffold?" },
		],
	},
};

export const allItems: Item[] = [...channels, ...worktreeItems];

export const paletteEntries: PaletteEntry[] = (() => {
	const entries: PaletteEntry[] = [];
	for (const it of worktreeItems) {
		entries.push({
			kind: "worktree",
			label: it.title,
			sub: `${it.repo} · ${it.branch}`,
			host: it.host,
			anchor: it.id,
			itemId: it.id,
			group: "Worktrees",
			keywords: [it.repo, it.branch, it.path, it.title].join(" ").toLowerCase(),
		});
		if (it.session) {
			entries.push({
				kind: "session",
				label: `${it.title} · session`,
				sub: `${it.session.uuid} · ${it.session.live ? "live" : "detached"}`,
				host: it.host,
				anchor: `${it.id}/session`,
				itemId: it.id,
				group: "Sessions",
				keywords: [it.session.uuid, it.title, it.repo].join(" ").toLowerCase(),
			});
		}
		if (it.pr) {
			entries.push({
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
			entries.push({
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
		if (it.contract) {
			entries.push({
				kind: "contract",
				label: `${it.title} · contract`,
				sub: it.contract.openQuestions
					? `${it.contract.openQuestions} open question${it.contract.openQuestions > 1 ? "s" : ""}`
					: "on track",
				host: it.host,
				anchor: `${it.id}/contract`,
				itemId: it.id,
				group: "Contracts",
				keywords: [it.contract.id, it.title, "contract"].join(" ").toLowerCase(),
			});
		}
	}
	for (const host of hosts) {
		entries.push({
			kind: "host",
			label: host.hostname,
			sub: `${host.os} · ${host.reachable ? "reachable" : "unreachable"}`,
			host: host.hostname,
			anchor: host.id,
			group: "Hosts",
			keywords: [host.hostname, host.os, host.kernel].join(" ").toLowerCase(),
		});
	}
	for (const ch of channels) {
		entries.push({
			kind: "channel",
			label: `#${ch.title}`,
			sub: ch.topic || `${ch.participants.length} agents`,
			host: ch.host,
			anchor: ch.id,
			itemId: ch.id,
			group: "Channels",
			keywords: [ch.title, ch.topic, ...ch.participants].filter(Boolean).join(" ").toLowerCase(),
		});
	}
	return entries;
})();

export const paletteActions: PaletteEntry[] = [
	{ kind: "action", label: "Launch new conversation", sub: "Pick a worktree + host", shortcut: ["⌘", "N"], action: "new-conversation", group: "Actions", keywords: "new conversation launch start agent" },
	{ kind: "action", label: "Switch lens · By attention", sub: "Default", action: "lens:attention", group: "Actions", keywords: "lens group attention" },
	{ kind: "action", label: "Switch lens · By host", sub: "Federation grouping", action: "lens:host", group: "Actions", keywords: "lens group host federation" },
	{ kind: "action", label: "Switch lens · By recent activity", sub: "Chronological", action: "lens:activity", group: "Actions", keywords: "lens group activity recent time" },
	{ kind: "action", label: "Switch lens · By repo", sub: "Project grouping", action: "lens:repo", group: "Actions", keywords: "lens group repo project" },
	{ kind: "action", label: "Switch lens · By tmux", sub: "Session/window/pane topology", action: "lens:tmux", group: "Actions", keywords: "lens group tmux topology session window pane" },
	{ kind: "action", label: "Toggle theme", sub: "Light / dark", action: "toggle-theme", group: "Actions", keywords: "theme dark light mode" },
	{ kind: "action", label: "Fork conversation", sub: "New thread from current", action: "fork", group: "Actions", keywords: "fork branch conversation new direction" },
	{ kind: "action", label: "Toggle terminal view", sub: "Chat ⇄ terminal", shortcut: ["⌘", "\\"], action: "toggle-view", group: "Actions", keywords: "view terminal chat switch toggle" },
];
