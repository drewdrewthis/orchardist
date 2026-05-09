<!--
  Compact icon-only signal strip on each fleet row. Each glyph is a single
  status (PR, CI, reviews, comments, issue, contracts). All carry a tooltip;
  none have visible labels by default.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import type { Item } from "$lib/data/types";

	type Props = { item: Item };
	let { item }: Props = $props();

	type Sig = { key: string; iconName: string; iconColor?: string; title: string; unread?: number };

	const sigs = $derived.by((): Sig[] => {
		if (item.kind === "channel") return [];
		const out: Sig[] = [];
		if (item.bare) out.push({ key: "bare", iconName: "git-branch", title: "Bare worktree (no session)" });
		if (item.session && !item.session.live)
			out.push({ key: "detached", iconName: "circle-dash", title: "Session detached (pickup-able)" });
		if (item.pr) {
			const pr = item.pr;
			let prCol = "var(--ok)";
			let icon = "pull-request";
			let title = `PR #${pr.number} — open`;
			if (pr.state === "merged") {
				prCol = "oklch(0.62 0.16 295)";
				title = `PR #${pr.number} — merged`;
			} else if (pr.state === "draft") {
				prCol = "var(--fg-3)";
				icon = "draft";
				title = `PR #${pr.number} — draft`;
			}
			out.push({ key: "pr", iconName: icon, iconColor: prCol, title });
			if (pr.ci === "failing")
				out.push({ key: "ci", iconName: "circle-x", iconColor: "var(--bad)", title: "CI failing" });
			else if (pr.ci === "pending")
				out.push({
					key: "ci",
					iconName: "circle-dash",
					iconColor: "var(--attn)",
					title: "CI pending",
				});
			if (pr.reviews === "changes-requested")
				out.push({
					key: "rev",
					iconName: "circle-x",
					iconColor: "var(--attn)",
					title: "Changes requested",
				});
			else if (pr.reviews === "approved")
				out.push({
					key: "rev",
					iconName: "circle-check",
					iconColor: "var(--ok)",
					title: "Approved",
				});
			else if (pr.reviews === "commented")
				out.push({
					key: "rev",
					iconName: "message",
					iconColor: "var(--fg-3)",
					title: "Reviewer commented",
				});
		}
		if (item.issue)
			out.push({
				key: "issue",
				iconName: "issue",
				iconColor: "var(--fg-3)",
				title: `Issue #${item.issue.number}`,
			});
		if (item.unread > 0)
			out.push({
				key: "unread",
				iconName: "message",
				iconColor: "var(--accent)",
				title: `${item.unread} unread`,
				unread: item.unread,
			});
		if (item.contract && item.contract.openQuestions > 0)
			out.push({
				key: "q",
				iconName: "question",
				iconColor: "var(--attn)",
				title: `${item.contract.openQuestions} open question${item.contract.openQuestions > 1 ? "s" : ""}`,
			});
		return out;
	});
</script>

{#if sigs.length > 0}
	<span class="signal-row">
		{#each sigs as s (s.key)}
			<span class="signal" title={s.title} style:color={s.iconColor || "var(--fg-3)"}>
				{#if s.unread}
					<span class="unread-glyph">
						<Icon name={s.iconName} size={11} />
						<span class="unread-count tnum">{s.unread}</span>
					</span>
				{:else}
					<Icon name={s.iconName} size={11} />
				{/if}
			</span>
		{/each}
	</span>
{/if}
