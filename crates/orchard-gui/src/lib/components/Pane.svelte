<!--
  One conversation pane. Wraps the right header (worktree vs channel), routes
  between chat and terminal views, exposes its own close + focus-mode buttons.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import ConvHeader from "./ConvHeader.svelte";
	import ChannelHeader from "./ChannelHeader.svelte";
	import ChatView from "./ChatView.svelte";
	import TerminalView from "./TerminalView.svelte";
	import type {
		Agent,
		ChannelItem,
		Conversation,
		ConvView,
		ForkPreview,
		Message,
		TerminalLine,
		WorktreeItem,
	} from "$lib/data/types";

	type Props = {
		item: WorktreeItem | ChannelItem;
		active: boolean;
		isLast: boolean;
		flex: number;
		paneCount: number;
		view: ConvView;
		fullscreen: boolean | null;
		conversation: Conversation;
		terminalLines: TerminalLine[];
		agents: Agent[];
		now: number;
		surface: "desktop" | "mobile";
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		composeText: string;
		setComposeText: (s: string) => void;
		sending: { tempId: string; status: string } | null;
		forkPreview: ForkPreview | null;
		onActivate: () => void;
		onClose: () => void;
		onView: (v: ConvView) => void;
		onSend: () => void;
		onFork: () => void;
		onStartFork: (idx: number, m: Message) => void;
		onCommitFork: () => void;
		onCancelFork: () => void;
		onJumpToAgent: (id: string) => void;
		onOpenContract: (id: string) => void;
		onToggleFullscreen?: () => void;
	};
	let {
		item,
		active,
		isLast,
		flex,
		paneCount,
		view,
		fullscreen,
		conversation,
		terminalLines,
		agents,
		now,
		surface,
		statusVariant = "ticks",
		composeText,
		setComposeText,
		sending,
		forkPreview,
		onActivate,
		onClose,
		onView,
		onSend,
		onFork,
		onStartFork,
		onCommitFork,
		onCancelFork,
		onJumpToAgent,
		onOpenContract,
		onToggleFullscreen,
	}: Props = $props();
</script>

<div
	class="pane"
	class:active
	style:flex="{flex} 1 0"
	style:min-width="0"
	onmousedown={onActivate}
	role="region"
>
	{#if paneCount > 1}
		<div class="pane-header-bar">
			<button
				class="pane-close iconbtn"
				onclick={(e) => {
					e.stopPropagation();
					onClose();
				}}
				title="Close pane"
				aria-label="Close pane"
			>
				<Icon name="close" size={11} />
			</button>
			{#if isLast && onToggleFullscreen}
				<button
					class="pane-fs iconbtn"
					onclick={(e) => {
						e.stopPropagation();
						onToggleFullscreen();
					}}
					title={fullscreen ? "Exit focus mode (⌘⇧F)" : "Focus mode (⌘⇧F)"}
				>
					<Icon name={fullscreen ? "minimize" : "maximize"} size={12} />
				</button>
			{/if}
		</div>
	{/if}

	<div class="conv">
		{#if item.kind === "channel"}
			<ChannelHeader
				{item}
				{view}
				{surface}
				{agents}
				onView={onView}
				onFork={onFork}
				onJumpToAgent={onJumpToAgent}
			/>
		{:else}
			<ConvHeader
				{item}
				{view}
				{surface}
				sessionLive={!!item.session?.live}
				onView={onView}
				onFork={onFork}
				onOpenContract={onOpenContract}
			/>
		{/if}

		{#if view === "chat"}
			<ChatView
				{item}
				{conversation}
				{agents}
				{surface}
				{now}
				{statusVariant}
				{composeText}
				{setComposeText}
				{onSend}
				{sending}
				forkPreview={active ? forkPreview : null}
				{onStartFork}
				{onCommitFork}
				{onCancelFork}
			/>
		{:else}
			<TerminalView lines={terminalLines} {item} />
		{/if}
	</div>
</div>
