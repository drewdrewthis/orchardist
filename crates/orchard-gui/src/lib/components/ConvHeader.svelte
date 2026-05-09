<!--
  Header for an active worktree conversation. Status pip + title + actionable
  chips (host / branch / PR / issue / tmux-attach copy / session-uuid copy) +
  contracts badge / fork / more / view switcher.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import ViewSwitcher from "./ViewSwitcher.svelte";
	import { getStore } from "$lib/store.svelte";
	import { relTime } from "$lib/util/format";
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

	const store = getStore();
	const conv = $derived(store.conversationFor(item.path));

	// Pane is the unit. Window/session = breadcrumb context. Attach copy
	// preselects the pane so paste-attach lands the user where the work is.
	const panes = $derived(store.tmuxPanesFor(item));
	const attachCmdFor = (paneId: string, sessionName: string) =>
		`tmux select-pane -t ${paneId} \\; attach -t ${sessionName}`;

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
				{#each panes as pane (pane.paneId)}
					{@const live = pane.window.active && pane.session.activeAttached}
					{@const cmd = attachCmdFor(pane.paneId, pane.session.name)}
					<button
						class="conv-chip"
						class:live
						title="{pane.session.name} → {pane.window.name} · {pane.paneId} ({pane.command}). Click to copy: {cmd}"
						onclick={() => copy(`pane-${pane.paneId}`, cmd)}
					>
						<Icon name="terminal" size={10} />
						<span class="mono">{pane.paneId}</span>
						<span class="dimer">·</span>
						<span class="mono">{pane.window.name}</span>
						{#if live}
							<span class="pip live" title="Active pane in an attached session"></span>
						{/if}
						<Icon name={copied === `pane-${pane.paneId}` ? "check" : "copy"} size={10} />
					</button>
				{/each}
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
				{#if conv}
					<span class="conv-chip" title="{conv.messageCount} messages in transcript">
						<Icon name="message" size={10} />
						<span class="tnum">{conv.messageCount}</span>
					</span>
					{#if conv.lastSeenAt > 0}
						<span class="conv-chip" title="Last activity: {new Date(conv.lastSeenAt).toLocaleString()}">
							<Icon name="clock" size={10} />
							<span>{relTime(conv.lastSeenAt, store.now)}</span>
						</span>
					{/if}
					{#if conv.open}
						<span class="conv-chip" title="Session is currently open">
							<span class="pip live"></span>
							<span>open</span>
						</span>
					{/if}
				{/if}
			</div>
			{#if conv?.recap}
				<div class="conv-recap mono dimer" title={conv.recap}>{conv.recap}</div>
			{/if}
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

<style>
	.conv-recap {
		margin-top: 4px;
		font-size: 11.5px;
		line-height: 1.4;
		max-height: 2.8em;
		overflow: hidden;
		text-overflow: ellipsis;
		display: -webkit-box;
		-webkit-line-clamp: 2;
		-webkit-box-orient: vertical;
	}
</style>
