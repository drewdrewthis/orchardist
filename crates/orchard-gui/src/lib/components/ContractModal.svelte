<!-- Surfaces an item's contract: id/status, statement, host, open questions. -->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import { shortTime } from "$lib/util/format";
	import type { Item, Message, WorktreeItem } from "$lib/data/types";

	type Props = {
		item: Item | null;
		messages: Message[];
		onClose: () => void;
	};
	let { item, messages, onClose }: Props = $props();

	const wt = $derived(item && item.kind === "worktree" ? (item as WorktreeItem) : null);
	const c = $derived(wt?.contract || null);
	const questions = $derived(messages.filter((m) => m.text && /__contract__:/.test(m.text)));
</script>

{#if item && c}
	<div class="overlay-backdrop fadeIn" onclick={onClose} role="presentation">
		<div
			class="contract-modal glass-strong scaleIn"
			onclick={(e) => e.stopPropagation()}
			role="dialog"
			aria-modal="true"
		>
			<div class="contract-modal-header">
				<div style="display: flex; align-items: center; gap: 8px;">
					<Icon name="docs" size={15} />
					<b style:font-size="14px">{c.id}</b>
					<span class="chip" style="height: 18px; font-size: 10.5px;">{c.status}</span>
				</div>
				<button class="iconbtn" onclick={onClose} aria-label="Close">
					<Icon name="close" size={14} />
				</button>
			</div>
			<div class="contract-modal-body">
				<div class="contract-line">
					<span class="dimer mono" style="font-size: 11px; width: 88px;">statement</span>
					<span>{item.title}</span>
				</div>
				{#if wt}
					<div class="contract-line">
						<span class="dimer mono" style="font-size: 11px; width: 88px;">owner</span>
						<span class="mono" style:font-size="12px">{wt.session?.uuid ?? "—"}</span>
					</div>
					<div class="contract-line">
						<span class="dimer mono" style="font-size: 11px; width: 88px;">host</span>
						<span class="mono" style:font-size="12px">{wt.host}</span>
					</div>
				{/if}
				<div style="border-top: 0.5px solid var(--line); margin: 12px 0;"></div>
				<div class="dimer mono" style="font-size: 11px; margin-bottom: 6px;">
					{c.openQuestions || 0} open question{c.openQuestions === 1 ? "" : "s"}
				</div>
				{#if questions.length === 0}
					<div class="dimer" style:font-size="12px">No active questions on this contract.</div>
				{:else}
					<ul class="contract-questions">
						{#each questions as q (q.id)}
							<li class="contract-question">
								<Icon name="question" size={12} />
								<div>
									<div style="font-size: 13px; line-height: 1.5;">
										{q.text.replace(/__contract__:\s*/, "")}
									</div>
									<div class="dimest mono" style="font-size: 10.5px; margin-top: 4px;">
										{shortTime(q.ts)}
									</div>
								</div>
							</li>
						{/each}
					</ul>
				{/if}
			</div>
		</div>
	</div>
{/if}
