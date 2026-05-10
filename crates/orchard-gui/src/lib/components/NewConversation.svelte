<!--
  Launch new conversation modal. Picks a worktree + host + model + first task,
  emits onLaunch with the spec. Anchored to "(host, cwd)" semantics from ADR-009.

  Reads worktrees and hosts directly from Houdini stores — no AppStore
  intermediary, no fake fields.
-->
<script lang="ts">
	import { onMount } from "svelte";
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import { fuzzyMatch } from "$lib/util/format";
	import {
		hostsStore,
		worktreesStore,
		buildHosts,
		buildWorktreePickerRows,
	} from "$lib/data/daemon-stores";

	type Props = {
		open: boolean;
		surface: "desktop" | "mobile";
		onClose: () => void;
		onLaunch: (spec: { worktreeId: string; host: string; model: string; task: string }) => void;
	};
	let { open, surface, onClose, onLaunch }: Props = $props();

	onMount(() => {
		worktreesStore.fetch();
		hostsStore.fetch();
	});

	const worktrees = $derived(buildWorktreePickerRows($worktreesStore.data));
	const hosts = $derived(buildHosts($hostsStore.data));

	let worktreeId = $state("");
	let host = $state("");
	let model = $state("claude-sonnet-4-5");
	let task = $state("");
	let q = $state("");
	let listOpen = $state(false);

	const cur = $derived(worktrees.find((i) => i.id === worktreeId) || null);
	const cwd = $derived(cur?.path || "~/code");

	const matches = $derived.by(() => {
		if (q) {
			return worktrees
				.map((it) => ({
					it,
					m: fuzzyMatch(q, `${it.repo} ${it.branch}`),
				}))
				.filter((x) => x.m)
				.sort((a, b) => (b.m?.score || 0) - (a.m?.score || 0))
				.slice(0, 8);
		}
		return worktrees.slice(0, 8).map((it) => ({ it, m: { score: 0, idx: [] } }));
	});

	$effect(() => {
		if (open) {
			worktreeId = worktrees[0]?.id || "";
			host = worktrees[0]?.host || hosts[0]?.hostname || "";
			model = "claude-sonnet-4-5";
			task = "";
			q = "";
		}
	});

	function pickWorktree(id: string) {
		worktreeId = id;
		const it = worktrees.find((x) => x.id === id);
		if (it) host = it.host;
		listOpen = false;
		q = "";
	}

	const MODELS = [
		{ v: "claude-haiku-4-5", l: "Haiku" },
		{ v: "claude-sonnet-4-5", l: "Sonnet" },
		{ v: "claude-opus-4-1", l: "Opus" },
	];
</script>

{#if open}
	<div
		class="nc-scrim fadeIn"
		class:mobile={surface === "mobile"}
		onclick={onClose}
		role="presentation"
	>
		<div
			class="nc-sheet glass-strong scaleIn"
			onclick={(e) => e.stopPropagation()}
			role="dialog"
			aria-modal="true"
		>
			<div class="nc-head">
				<div>
					<b style:font-size="15px">Launch new conversation</b>
					<div class="dimer" style:font-size="12px">
						This composes a new <span class="mono">ClaudeSession</span> anchor on the picked host.
					</div>
				</div>
				<button class="iconbtn" onclick={onClose} aria-label="Close"><Icon name="close" size={14} /></button>
			</div>

			<div class="nc-body">
				<div class="nc-row">
					<div class="nc-row-label">Worktree</div>
					<div class="nc-row-control" style:position="relative">
						<div
							style="display: flex; align-items: center; gap: 8px; padding: 0 10px; background: var(--surface-2); border: 0.5px solid var(--line); border-radius: 8px; height: 34px;"
						>
							<Icon name="search" size={13} />
							<input
								class="input"
								style="height: 32px; background: transparent; border: 0; padding: 0;"
								placeholder={cur ? `${cur.repo} · ${cur.branch}` : "Pick a worktree…"}
								bind:value={q}
								onfocus={() => (listOpen = true)}
								onblur={() => setTimeout(() => (listOpen = false), 150)}
							/>
						</div>
						{#if listOpen}
							<div
								style="position: absolute; top: calc(100% + 6px); left: 0; right: 0; z-index: 10; max-height: 200px; background: var(--surface); border: 0.5px solid var(--line); border-radius: 10px; box-shadow: var(--shadow-1); padding: 4px; overflow: auto;"
							>
								{#each matches as { it } (it.id)}
									<div
										style="display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-radius: 6px; cursor: default; font-size: 12px;"
										class:on={it.id === worktreeId}
										onmousedown={() => pickWorktree(it.id)}
										role="option"
										aria-selected={it.id === worktreeId}
										tabindex="-1"
									>
										<HostGlyph host={it.host} size={12} />
										<span class="mono dimer" style:font-size="11px" style:width="110px">
											{it.repo.split("/")[1] ?? it.repo}
										</span>
										<span class="mono" style:font-size="12px">{it.branch}</span>
									</div>
								{/each}
								{#if matches.length === 0}
									<div class="dimer" style:padding="12px" style:font-size="12px">No matches</div>
								{/if}
							</div>
						{/if}
					</div>
				</div>

				<div class="nc-row">
					<div class="nc-row-label">Host</div>
					<div class="nc-row-control">
						<div class="nc-host-grid">
							{#each hosts as h (h.id)}
								<button
									class="nc-host"
									class:on={host === h.hostname}
									class:down={!h.reachable}
									disabled={!h.reachable}
									onclick={() => (host = h.hostname)}
								>
									<div style="display: flex; align-items: center; gap: 8px;">
										<HostGlyph host={h.hostname} size={16} />
										<b class="mono" style="font-size: 12.5px;">{h.hostname}</b>
										{#if !h.reachable}
											<span class="chip bad" style="height: 16px; font-size: 10px; padding: 0 6px;">
												down
											</span>
										{/if}
									</div>
									<div class="dimest mono" style="font-size: 10.5px; margin-top: 6px;">
										{h.os.split(" ")[0]}
									</div>
									{#if h.reachable && h.resourceLoad}
										<div style="display: flex; align-items: center; gap: 6px; margin-top: 8px;">
											<span class="dimer mono" style:font-size="10px">cpu</span>
											<span class="mono dimer tnum" style:font-size="10px">
												{h.resourceLoad.cpuPercent.toFixed(0)}%
											</span>
										</div>
									{/if}
								</button>
							{/each}
						</div>
					</div>
				</div>

				<div class="nc-row">
					<div class="nc-row-label">Working dir</div>
					<div class="nc-row-control">
						<div class="nc-cwd mono">{cwd}</div>
					</div>
				</div>

				<div class="nc-row">
					<div class="nc-row-label">Model</div>
					<div class="nc-row-control">
						<div class="seg" style:width="fit-content">
							<div
								class="seg-thumb"
								style:left="calc(2px + {MODELS.findIndex((m) => m.v === model)} * (100% - 4px) / 3)"
								style:width="calc((100% - 4px) / 3)"
							></div>
							{#each MODELS as o (o.v)}
								<button data-on={model === o.v} onclick={() => (model = o.v)}>{o.l}</button>
							{/each}
						</div>
					</div>
				</div>

				<div class="nc-row" style:align-items="flex-start">
					<div class="nc-row-label">First task</div>
					<div class="nc-row-control">
						<textarea
							class="input"
							rows="3"
							style="height: auto; min-height: 64px; padding: 10px; resize: vertical;"
							placeholder="What should the agent do? Optional."
							bind:value={task}
						></textarea>
					</div>
				</div>
			</div>

			<div class="nc-foot">
				<span class="dimer mono" style:font-size="11px">
					anchor:
					<span style:color="var(--accent)">
						(host:{host}, cwd:{cur?.path?.split("/").slice(-2).join("/") || ""})
					</span>
				</span>
				<div style="display: flex; gap: 8px;">
					<button class="btn-ghost" onclick={onClose}>Cancel</button>
					<button
						class="btn-primary"
						onclick={() => onLaunch({ worktreeId, host, model, task })}
						disabled={!host}
					>
						<Icon name="sparkle" size={13} /> Launch
					</button>
				</div>
			</div>
		</div>
	</div>
{/if}
