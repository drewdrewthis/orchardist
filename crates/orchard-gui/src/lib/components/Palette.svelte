<!--
  ⌘K palette. Searches anchors (worktrees, sessions, hosts, channels) +
  actions. Filter syntax: host: repo:

  Entries come from the Houdini-driven palette builder; each one carries
  the row identity (paneId, sessionUuid, roomId) needed to open it.
-->
<script lang="ts">
	import Icon from "$lib/icons/Icon.svelte";
	import HostGlyph from "$lib/icons/HostGlyph.svelte";
	import { fuzzyMatch } from "$lib/util/format";
	import type { PaletteEntry } from "$lib/data/types";

	type Props = {
		open: boolean;
		surface: "desktop" | "mobile";
		entries: PaletteEntry[];
		actions: PaletteEntry[];
		onClose: () => void;
		onPick: (e: PaletteEntry) => void;
	};
	let { open, surface, entries, actions, onClose, onPick }: Props = $props();

	let query = $state("");
	let active = $state(0);
	let inputEl: HTMLInputElement | undefined = $state();

	$effect(() => {
		if (open) {
			query = "";
			active = 0;
			setTimeout(() => inputEl?.focus(), 30);
		}
	});

	function parseFilters(q: string) {
		const filters = { host: [] as string[], repo: [] as string[] };
		const rest = q
			.replace(/(host|repo):([^\s]+)/gi, (_m, k: string, v: string) => {
				filters[k.toLowerCase() as keyof typeof filters].push(v.toLowerCase());
				return "";
			})
			.trim();
		return { rest, filters };
	}

	type Hit = PaletteEntry & { score: number; idx: number[] };

	const results = $derived.by((): Hit[] => {
		const { rest, filters } = parseFilters(query);
		const out: Hit[] = [];
		for (const a of actions) {
			if (!rest && Object.values(filters).every((v) => v.length === 0)) {
				out.push({ ...a, score: 50, idx: [] });
				continue;
			}
			const m = fuzzyMatch(rest, a.label) || fuzzyMatch(rest, a.keywords);
			if (m) out.push({ ...a, score: m.score - 5, idx: m.idx });
		}
		for (const e of entries) {
			if (filters.host.length && !filters.host.some((h) => (e.host || "").toLowerCase().includes(h)))
				continue;
			if (
				filters.repo.length &&
				!filters.repo.some(
					(r) => (e.sub || "").toLowerCase().includes(r) || e.label.toLowerCase().includes(r),
				)
			)
				continue;
			if (!rest) {
				if (Object.values(filters).some((v) => v.length > 0)) {
					out.push({ ...e, score: 30, idx: [] });
				}
				continue;
			}
			const m = fuzzyMatch(rest, e.label) || fuzzyMatch(rest, e.keywords);
			if (m) out.push({ ...e, score: m.score, idx: m.idx });
		}
		return out.sort((a, b) => b.score - a.score).slice(0, 50);
	});

	const grouped = $derived.by(() => {
		const order = ["Actions", "Worktrees", "Hosts", "Channels"];
		const map = new Map<string, { r: Hit; i: number }[]>();
		results.forEach((r, i) => {
			const g = r.kind === "action" ? "Actions" : r.group;
			if (!map.has(g)) map.set(g, []);
			map.get(g)!.push({ r, i });
		});
		return order.filter((o) => map.has(o)).map((o) => ({ name: o, rows: map.get(o)! }));
	});

	$effect(() => {
		const _ = query;
		void _;
		active = 0;
	});

	function onKey(e: KeyboardEvent) {
		if (!open) return;
		if (e.key === "Escape") {
			e.preventDefault();
			onClose();
		} else if (e.key === "ArrowDown") {
			e.preventDefault();
			active = Math.min(results.length - 1, active + 1);
		} else if (e.key === "ArrowUp") {
			e.preventDefault();
			active = Math.max(0, active - 1);
		} else if (e.key === "Enter") {
			e.preventDefault();
			const r = results[active];
			if (r) onPick(r);
		}
	}

	function highlight(text: string, idx: number[]): { ch: string; on: boolean }[] {
		const out: { ch: string; on: boolean }[] = [];
		const set = new Set(idx);
		for (let i = 0; i < text.length; i++) out.push({ ch: text[i], on: set.has(i) });
		return out;
	}

	function iconForKind(kind: string): string {
		const map: Record<string, string> = {
			session: "git-branch",
			host: "host",
			channel: "message",
			action: "bolt",
			lens: "filter",
		};
		return map[kind] || "dot";
	}
</script>

<svelte:window onkeydown={onKey} />

{#if open}
	<div class="palette-scrim fadeIn" class:mobile={surface === "mobile"} onclick={onClose} role="presentation">
		<div
			class="palette glass-strong scaleIn"
			class:mobile={surface === "mobile"}
			onclick={(e) => e.stopPropagation()}
			role="dialog"
			aria-modal="true"
		>
			<div class="palette-input-row">
				<Icon name="search" size={16} />
				<input
					bind:this={inputEl}
					class="palette-input"
					placeholder="Search anchors or run a command…"
					bind:value={query}
				/>
				<span class="kbd" style:font-size="10.5px">esc</span>
			</div>
			<div class="palette-results">
				{#each grouped as g (g.name)}
					<div class="palette-group">
						<div class="palette-group-name">{g.name}</div>
						{#each g.rows as { r, i } (`${r.kind}:${r.label}:${r.sub ?? ''}`)}
							<div
								class="palette-row"
								class:active={i === active}
								onmouseenter={() => (active = i)}
								onclick={() => onPick(r)}
								role="option"
								aria-selected={i === active}
								tabindex="-1"
							>
								<div class="palette-row-icon">
									<Icon name={iconForKind(r.kind)} size={14} />
								</div>
								<div class="palette-row-body">
									<div class="palette-row-label">
										{#each highlight(r.label, r.idx) as c}
											{#if c.on}
												<mark style="background: transparent; color: var(--accent); font-weight: 600;"
													>{c.ch}</mark
												>
											{:else}
												<span>{c.ch}</span>
											{/if}
										{/each}
									</div>
									{#if r.sub}
										<div class="palette-row-sub mono">{r.sub}</div>
									{/if}
								</div>
								{#if r.host && r.kind !== "action"}
									<HostGlyph host={r.host} size={12} />
								{/if}
								{#if r.shortcut}
									<div class="palette-row-shortcut">
										{#each r.shortcut as s}
											<span class="kbd">{s}</span>
										{/each}
									</div>
								{/if}
							</div>
						{/each}
					</div>
				{/each}
				{#if results.length === 0}
					<div class="palette-empty">
						<span class="dimer">No matches.</span>
						<span class="dimest" style:font-size="12px">
							Try <span class="mono">host:drew-mac</span> or
							<span class="mono">repo:orchard</span>
						</span>
					</div>
				{/if}
			</div>
			<div class="palette-foot mono">
				<span><span class="kbd">↑</span><span class="kbd">↓</span> navigate</span>
				<span><span class="kbd">↵</span> open</span>
				<span style:margin-left="auto">
					{results.length} {results.length === 1 ? "result" : "results"}
				</span>
			</div>
		</div>
	</div>
{/if}
