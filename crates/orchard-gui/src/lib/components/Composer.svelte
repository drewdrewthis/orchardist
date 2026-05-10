<!-- Multi-line composer with auto-grow + send icon. Enter sends, shift+enter newline. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import type { Surface } from "$lib/data/types";

	type Props = {
		value: string;
		onChange: (s: string) => void;
		onSend: () => void;
		roomId: string;
		sending: unknown;
		surface: Surface;
	};
	let { value, onChange, onSend, roomId, sending }: Props = $props();

	let ta: HTMLTextAreaElement | undefined = $state();

	function onKey(e: KeyboardEvent) {
		if (e.key === "Enter" && !e.shiftKey) {
			e.preventDefault();
			if (value.trim()) onSend();
		}
	}

	$effect(() => {
		const _ = value;
		void _;
		if (ta) {
			ta.style.height = "auto";
			ta.style.height = Math.min(140, Math.max(36, ta.scrollHeight)) + "px";
		}
	});

	const placeholder = $derived(
		`Message ${roomId.startsWith("@") ? roomId : `#${roomId}`}`,
	);
</script>

<div class="composer">
	<div class="composer-inner">
		<button class="iconbtn" title="Attach" aria-label="Attach"><Icon name="attach" size={15} /></button>
		<textarea
			bind:this={ta}
			class="composer-ta"
			{value}
			oninput={(e) => onChange((e.target as HTMLTextAreaElement).value)}
			onkeydown={onKey}
			{placeholder}
			rows="1"
		></textarea>
		<div class="composer-actions">
			<button
				class="iconbtn-primary"
				disabled={!value.trim() || !!sending}
				onclick={onSend}
				aria-label="Send"
				title="Send · ↵"
			>
				<Icon name="send" size={14} />
			</button>
		</div>
	</div>
</div>
