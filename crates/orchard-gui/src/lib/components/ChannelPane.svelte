<!--
  Channel pane — renders a chat-room conversation (chat-core jsonl).
  Counterpart to SessionPane; both implement the discriminated Tab union.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import ChannelHeader from "./ChannelHeader.svelte";
	import ChatView from "./ChatView.svelte";
	import { getStore } from "$lib/store.svelte";
	import type {
		Conversation,
		ForkPreview,
		Message,
	} from "$lib/data/types";

	type Props = {
		roomId: string;
		active: boolean;
		paneCount: number;
		isLast: boolean;
		fullscreen: boolean | null;
		conversation: Conversation;
		now: number;
		surface?: "desktop" | "mobile";
		statusVariant?: "ticks" | "dots" | "minimal" | "text";
		composeText: string;
		setComposeText: (s: string) => void;
		sending: { tempId: string; status: string } | null;
		forkPreview: ForkPreview | null;
		onSend: () => void;
		onStartFork: (idx: number, m: Message) => void;
		onCommitFork: () => void;
		onCancelFork: () => void;
		onActivate: () => void;
		onClose: () => void;
		onToggleFullscreen?: () => void;
	};
	let {
		roomId,
		active,
		paneCount,
		isLast,
		fullscreen,
		conversation,
		now,
		surface = "desktop",
		statusVariant = "ticks",
		composeText,
		setComposeText,
		sending,
		forkPreview,
		onSend,
		onStartFork,
		onCommitFork,
		onCancelFork,
		onActivate,
		onClose,
		onToggleFullscreen,
	}: Props = $props();

	const store = getStore();
	const room = $derived(store.chatRooms.find((r) => r.id === roomId));
	const memberCount = $derived(room?.memberCount ?? 0);
</script>

<div
	class="pane"
	class:active
	style:flex="1 1 0"
	style:min-width="0"
	onmousedown={onActivate}
	role="region"
>
	{#if paneCount > 1 || surface === "mobile"}
		<div class="pane-header-bar">
			<button
				class="pane-close iconbtn"
				onclick={(e) => { e.stopPropagation(); onClose(); }}
				aria-label={surface === "mobile" ? "Back to sidebar" : "Close pane"}
			>
				<Icon name={surface === "mobile" ? "arrow-left" : "close"} size={surface === "mobile" ? 14 : 11} />
			</button>
			{#if isLast && onToggleFullscreen}
				<button
					class="pane-fs iconbtn"
					onclick={(e) => { e.stopPropagation(); onToggleFullscreen(); }}
					title={fullscreen ? "Exit focus mode" : "Focus mode"}
				>
					<Icon name={fullscreen ? "minimize" : "maximize"} size={12} />
				</button>
			{/if}
		</div>
	{/if}
	<div class="conv">
		<ChannelHeader
			{roomId}
			{memberCount}
			view="chat"
			surface="desktop"
			onView={() => {}}
			onFork={() => {}}
		/>
		<ChatView
			{roomId}
			{conversation}
			surface="desktop"
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
	</div>
</div>
