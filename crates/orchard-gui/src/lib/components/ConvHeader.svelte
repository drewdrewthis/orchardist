<!--
  Header for an active worktree conversation. Status pip + title + actionable
  chips (host / branch / PR / issue / tmux-attach copy / session-uuid copy) +
  contracts badge / fork / more / view switcher.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import ViewSwitcher from "./ViewSwitcher.svelte";
	import type { ConvView, Surface, WorktreeItem } from "$lib/data/types";

	type Props = {
		item: WorktreeItem;
		view: ConvView;
		switcherVariant?: "segmented" | "icon-toggle";
		surface: Surface;
		sessionLive: boolean;
		onView: (v: ConvView) => void;
		onFork: () => void;
		onClose?: () => void;
		onOpenContract: (id: string) => void;
	};
	let {
		item,
		view,
		switcherVariant = "segmented",
		surface,
		sessionLive,
		onView,
		onFork,
		onClose,
		onOpenContract,
	}: Props = $props();

	let copied = $state<string | null>(null);

	const tmuxName = $derived(
		item.session?.instance ||
			(item.session?.uuid ? `claude-${item.session.uuid.slice(0, 4)}` : null),
	);
	const attachCmd = $derived(tmuxName ? `tmux -L orchard attach -t ${tmuxName}` : null);

	async function copy(kind: string, text: string) {
		try {
			await navigator.clipboard.writeText(text);
		} catch {
			/* ignore */
		}
		copied = kind;
		setTimeout(() => {
			if (copied === kind) copied = null;
		}, 1200);
	}
</script>

<div class="conv-header">
	<div class="conv-header-row">
		{#if surface === "mobile" && onClose}
			<button class="iconbtn" onclick={onClose} aria-label="Back" style="margin-left: -6px;">
				<Icon name="arrow-left" size={16} />
			</button>
		{/if}

		<div class="conv-title-block">
			<div class="conv-title-row">
				<span class="pip {item.status}"></span>
				<span class="conv-title">{item.title}</span>
				{#if sessionLive}
					<span class="pip live" title="live"></span>
				{/if}
				{#if item.attentionReason}
					<span class="conv-attn-inline" title={item.attentionReason}>
						<Icon name="alert" size={11} />
						<span>{item.attentionReason}</span>
					</span>
				{/if}
			</div>
			<div class="conv-sub mono dimer">
				<span class="conv-chip" title="Host · {item.host}">
					<HostGlyph host={item.host} size={11} />
					<span>{item.host}</span>
				</span>
				<span class="conv-chip" title="Branch · {item.branch}">
					<Icon name="git-branch" size={10} />
					<span>{item.branch}</span>
				</span>
				{#if item.pr}
					<a
						class="conv-chip"
						href="https://github.com/{item.repo}/pull/{item.pr.number}"
						target="_blank"
						rel="noreferrer"
						title="PR #{item.pr.number} · {item.pr.state}"
					>
						<Icon name="pull-request" size={10} />
						<span>#{item.pr.number}</span>
					</a>
				{/if}
				{#if item.issue}
					<a
						class="conv-chip"
						href="https://github.com/{item.repo}/issues/{item.issue.number}"
						target="_blank"
						rel="noreferrer"
						title="Issue #{item.issue.number}"
					>
						<Icon name="issue" size={10} />
						<span>#{item.issue.number}</span>
					</a>
				{/if}
				{#if tmuxName && attachCmd}
					<button
						class="conv-chip"
						title="Click to copy: {attachCmd}"
						onclick={() => copy("tmux", attachCmd)}
					>
						<Icon name="terminal" size={10} />
						<span class="mono">{tmuxName}</span>
						<Icon name={copied === "tmux" ? "check" : "copy"} size={10} />
					</button>
				{/if}
				{#if item.session?.uuid}
					<button
						class="conv-chip"
						title="Click to copy session UUID"
						onclick={() => copy("uuid", item.session!.uuid)}
					>
						<span style:opacity="0.7">id</span>
						<span>{item.session.uuid.slice(0, 6)}…</span>
						<Icon name={copied === "uuid" ? "check" : "copy"} size={10} />
					</button>
				{/if}
			</div>
		</div>

		<div class="conv-header-actions">
			{#if item.contract}
				<button
					class="iconbtn contract-badge"
					onclick={() => onOpenContract(item.id)}
					title="Contract {item.contract.id}{item.contract.openQuestions
						? ` · ${item.contract.openQuestions} open`
						: ''}"
				>
					<Icon name="docs" size={14} />
					{#if item.contract.openQuestions > 0}
						<span class="contract-count tnum">{item.contract.openQuestions}</span>
					{/if}
				</button>
			{/if}
			<button class="iconbtn" onclick={onFork} title="Fork conversation">
				<Icon name="git-fork" size={15} />
			</button>
			<button class="iconbtn" title="More">
				<Icon name="more" size={15} />
			</button>
			{#if surface === "desktop"}
				<span class="conv-header-divider" aria-hidden="true"></span>
				<ViewSwitcher value={view} onChange={onView} variant={switcherVariant} />
			{/if}
		</div>
	</div>
</div>
