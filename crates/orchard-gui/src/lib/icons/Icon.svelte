<!--
  Single SVG icon by name. All icons share viewBox 0 0 24 24, currentColor strokes,
  stroke-width 1.5. Listed inline so adding/changing is one-line edits.
-->
<script lang="ts">
	type Props = {
		name: string;
		size?: number;
		strokeWidth?: number;
	};
	let { name, size = 16, strokeWidth = 1.5 }: Props = $props();
</script>

<svg
	width={size}
	height={size}
	viewBox="0 0 24 24"
	fill="none"
	stroke="currentColor"
	stroke-width={strokeWidth}
	stroke-linecap="round"
	stroke-linejoin="round"
	style="flex: none; display: block;"
	aria-hidden="true"
>
	{#if name === "search"}
		<circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" />
	{:else if name === "close"}
		<path d="M6 6l12 12M18 6 6 18" />
	{:else if name === "chevron-right"}
		<path d="m9 6 6 6-6 6" />
	{:else if name === "chevron-down"}
		<path d="m6 9 6 6 6-6" />
	{:else if name === "arrow-up"}
		<path d="M12 19V5M5 12l7-7 7 7" />
	{:else if name === "arrow-right"}
		<path d="M5 12h14M13 5l7 7-7 7" />
	{:else if name === "arrow-left"}
		<path d="M19 12H5M11 5l-7 7 7 7" />
	{:else if name === "plus"}
		<path d="M12 5v14M5 12h14" />
	{:else if name === "check"}
		<path d="m4 12 5 5L20 6" />
	{:else if name === "check-double"}
		<path d="m4 12 4 4 8-10M11 16l1 1L22 6" />
	{:else if name === "clock"}
		<circle cx="12" cy="12" r="9" /><path d="M12 7v5l3 2" />
	{:else if name === "dot"}
		<circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />
	{:else if name === "bolt"}
		<path d="M13 3 4 14h7l-1 7 9-11h-7l1-7Z" />
	{:else if name === "terminal"}
		<path d="M5 9l3 3-3 3M11 15h7" /><rect x="2" y="4" width="20" height="16" rx="2" />
	{:else if name === "chat"}
		<path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5V14a2.5 2.5 0 0 1-2.5 2.5H10l-4 3.5v-3.5h-.5A2.5 2.5 0 0 1 3 14V8" />
	{:else if name === "git-branch"}
		<circle cx="6" cy="5" r="2" /><circle cx="6" cy="19" r="2" /><circle cx="18" cy="9" r="2" /><path d="M6 7v10M6 13a6 6 0 0 0 6-6h4" />
	{:else if name === "folder"}
		<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z" />
	{:else if name === "worktree"}
		<path d="M12 22V8" /><circle cx="12" cy="5" r="2" /><circle cx="6" cy="14" r="2" /><circle cx="18" cy="14" r="2" /><path d="M12 8 6 12M12 8l6 4" />
	{:else if name === "git-fork"}
		<circle cx="6" cy="5" r="2" /><circle cx="18" cy="5" r="2" /><circle cx="12" cy="19" r="2" /><path d="M6 7v2a3 3 0 0 0 3 3h6a3 3 0 0 0 3-3V7M12 12v5" />
	{:else if name === "host"}
		<rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 20h8M12 16v4" />
	{:else if name === "cpu"}
		<rect x="6" y="6" width="12" height="12" rx="1.5" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M1 9h3M1 15h3M20 9h3M20 15h3" />
	{:else if name === "bell"}
		<path d="M6 9a6 6 0 0 1 12 0c0 5 2 6 2 6H4s2-1 2-6Z" /><path d="M10 20a2 2 0 0 0 4 0" />
	{:else if name === "filter"}
		<path d="M3 5h18l-7 8v6l-4-2v-4Z" />
	{:else if name === "layers"}
		<path d="m12 3 9 5-9 5-9-5 9-5Z" /><path d="m3 13 9 5 9-5M3 18l9 5 9-5" />
	{:else if name === "sidebar"}
		<rect x="3" y="4" width="18" height="16" rx="2" /><path d="M9 4v16" />
	{:else if name === "command"}
		<path d="M9 9h6v6H9zM9 9V6a3 3 0 1 0-3 3h3Zm6 0V6a3 3 0 1 1 3 3h-3ZM9 15v3a3 3 0 1 1-3-3h3Zm6 0v3a3 3 0 1 0 3-3h-3Z" />
	{:else if name === "sparkle"}
		<path d="M12 3v3M12 18v3M3 12h3M18 12h3M5.5 5.5l2 2M16.5 16.5l2 2M5.5 18.5l2-2M16.5 7.5l2-2" />
	{:else if name === "question"}
		<circle cx="12" cy="12" r="9" /><path d="M9.5 9a2.5 2.5 0 0 1 5 0c0 1.5-2.5 2-2.5 4M12 17h.01" />
	{:else if name === "alert"}
		<path d="M10.3 3.5 2 18a2 2 0 0 0 1.7 3h16.6a2 2 0 0 0 1.7-3L13.7 3.5a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4M12 17h.01" />
	{:else if name === "refresh"}
		<path d="M3 12a9 9 0 0 1 15.5-6.3L21 8M21 4v4h-4M21 12a9 9 0 0 1-15.5 6.3L3 16M3 20v-4h4" />
	{:else if name === "copy"}
		<rect x="9" y="9" width="11" height="11" rx="2" /><path d="M5 15V6a2 2 0 0 1 2-2h9" />
	{:else if name === "more"}
		<circle cx="6" cy="12" r="1.2" fill="currentColor" stroke="none" /><circle cx="12" cy="12" r="1.2" fill="currentColor" stroke="none" /><circle cx="18" cy="12" r="1.2" fill="currentColor" stroke="none" />
	{:else if name === "send"}
		<path d="M5 12h14M14 6l6 6-6 6" />
	{:else if name === "attach"}
		<path d="M21 11.5 12.5 20a5 5 0 0 1-7-7L13.5 5a3.5 3.5 0 0 1 5 5L11 17.5a2 2 0 1 1-3-3l6.5-6.5" />
	{:else if name === "pull-request"}
		<circle cx="6" cy="6" r="2" /><circle cx="6" cy="18" r="2" /><circle cx="18" cy="18" r="2" /><path d="M6 8v8M11 6h3a4 4 0 0 1 4 4v6M14 3l-3 3 3 3" />
	{:else if name === "issue"}
		<circle cx="12" cy="12" r="9" /><circle cx="12" cy="12" r="3" fill="currentColor" stroke="none" />
	{:else if name === "sun"}
		<circle cx="12" cy="12" r="4" /><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.5 5.5l1.5 1.5M17 17l1.5 1.5M5.5 18.5 7 17M17 7l1.5-1.5" />
	{:else if name === "moon"}
		<path d="M21 12.8A9 9 0 1 1 11.2 3a7 7 0 0 0 9.8 9.8Z" />
	{:else if name === "wifi"}
		<path d="M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M12 19.5h.01" />
	{:else if name === "wifi-off"}
		<path d="M2 8.8A14 14 0 0 1 22 8.8M5 12.5a10 10 0 0 1 4-2.7M19 12.5a10 10 0 0 0-4-2.7M8.5 16a5 5 0 0 1 7 0M12 19.5h.01M3 3l18 18" />
	{:else if name === "external"}
		<path d="M14 4h6v6M10 14 20 4M19 13v5a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V7a2 2 0 0 1 2-2h5" />
	{:else if name === "docs"}
		<path d="M5 4h10l4 4v12a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Z" /><path d="M14 4v5h5M8 13h8M8 17h5" />
	{:else if name === "key"}
		<circle cx="8" cy="15" r="4" /><path d="m11 13 9-9 2 2-2 2 2 2-2 2-2-2-3 3" />
	{:else if name === "circle-x"}
		<circle cx="12" cy="12" r="9" /><path d="m9 9 6 6M15 9l-6 6" />
	{:else if name === "circle-check"}
		<circle cx="12" cy="12" r="9" /><path d="m8 12 3 3 5-6" />
	{:else if name === "circle-dash"}
		<circle cx="12" cy="12" r="9" stroke-dasharray="3 3" />
	{:else if name === "message"}
		<path d="M4 6.5A2.5 2.5 0 0 1 6.5 4h11A2.5 2.5 0 0 1 20 6.5V14a2.5 2.5 0 0 1-2.5 2.5H10l-4 3.5v-3.5h-.5A2.5 2.5 0 0 1 3 14V8" />
	{:else if name === "pause"}
		<rect x="7" y="5" width="3" height="14" rx="0.5" /><rect x="14" y="5" width="3" height="14" rx="0.5" />
	{:else if name === "draft"}
		<circle cx="6" cy="6" r="2" /><circle cx="6" cy="18" r="2" stroke-dasharray="2 2" /><path d="M6 8v8" />
	{:else if name === "maximize"}
		<path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" />
	{:else if name === "minimize"}
		<path d="M9 4v5H4M15 4v5h5M9 20v-5H4M15 20v-5h5" />
	{:else if name === "arrows-lr"}
		<path d="M3 8h14m0 0-3-3m3 3-3 3M21 16H7m0 0 3 3m-3-3 3-3" />
	{:else if name === "sort-asc"}
		<path d="M3 6h12M3 12h8M3 18h4M17 4v16m0 0 4-4m-4 4-4-4" />
	{:else if name === "sort-desc"}
		<path d="M3 6h4M3 12h8M3 18h12M17 20V4m0 0 4 4m-4-4-4 4" />
	{:else if name === "orchard"}
		<path d="M12 22V11" stroke-width="2" stroke-linecap="round" />
		<path d="M12 14 7 9M12 14l5-5M12 9 9 6M12 9l3-3" stroke-width="1.6" stroke-linecap="round" />
		<circle cx="7" cy="9" r="1.6" fill="currentColor" />
		<circle cx="17" cy="9" r="1.6" fill="currentColor" />
		<circle cx="9" cy="6" r="1.2" fill="currentColor" />
		<circle cx="15" cy="6" r="1.2" fill="currentColor" />
		<circle cx="12" cy="3" r="1.4" fill="currentColor" />
	{/if}
</svg>
