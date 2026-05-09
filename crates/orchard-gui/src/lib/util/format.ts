/** Relative-time formatter that breathes with the live tick. */
export function relTime(ms: number, now: number): string {
	const d = (now - ms) / 1000;
	if (d < 5) return "now";
	if (d < 60) return `${Math.floor(d)}s ago`;
	if (d < 3600) return `${Math.floor(d / 60)}m ago`;
	if (d < 86400) return `${Math.floor(d / 3600)}h ago`;
	const days = Math.floor(d / 86400);
	if (days < 7) return `${days}d ago`;
	return new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

export function shortTime(ms: number): string {
	return new Date(ms).toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" });
}

/** Tiny fuzzy match — returns score (higher better) + matched indices for highlighting. */
export interface FuzzyResult {
	score: number;
	idx: number[];
}

export function fuzzyMatch(query: string, text: string): FuzzyResult | null {
	if (!query) return { score: 1, idx: [] };
	const q = query.toLowerCase();
	const t = text.toLowerCase();
	const sub = t.indexOf(q);
	if (sub >= 0) {
		const idx: number[] = [];
		for (let i = 0; i < q.length; i++) idx.push(sub + i);
		const wordStart = sub === 0 || /\W/.test(t[sub - 1]) ? 50 : 0;
		return { score: 200 - sub + wordStart, idx };
	}
	let qi = 0;
	const idx: number[] = [];
	for (let i = 0; i < t.length && qi < q.length; i++) {
		if (t[i] === q[qi]) {
			idx.push(i);
			qi++;
		}
	}
	if (qi < q.length) return null;
	let span = 0;
	for (let i = 1; i < idx.length; i++) {
		if (idx[i] - idx[i - 1] === 1) span++;
	}
	return { score: 60 + span - (idx[0] || 0) * 0.1, idx };
}

/** Hash a string → consistent host hue. */
export function hostHue(host: string): number {
	let n = 0;
	for (const c of host) n = (n * 31 + c.charCodeAt(0)) | 0;
	return ((n % 360) + 360) % 360;
}

/** Two-letter initials from a hostname. */
export function hostInitials(host: string): string {
	return host.split(/[-_.]/).map((s) => s[0] || "").join("").slice(0, 2).toUpperCase();
}

export const STATUS_LABEL: Record<string, string> = {
	attn: "Needs attention",
	ok: "Healthy",
	bad: "Blocked",
	idle: "Idle",
	stale: "Stale",
};
