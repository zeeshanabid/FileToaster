<script lang="ts">
	let { type = 'doc', color = 'var(--accent)' }: { type?: string; color?: string } = $props();
</script>

<span class="fg" style="color:{color}" aria-hidden="true">
	{#if type === 'folder'}
		<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
			<path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
		</svg>
	{:else if type === 'image'}
		<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
			<rect x="3" y="4" width="14" height="13" rx="1.5" />
			<circle cx="7.5" cy="8.5" r="1.3" />
			<path d="m6 16 3.5-3.5 2 2L13 12l2 3" />
		</svg>
	{:else if type === 'sheet'}
		<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
			<path d="M5 2h6l4 4v12H5a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Z" />
			<path d="M7 10h6M7 13h6M9 7v6" />
		</svg>
	{:else}
		<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
			<path d="M5 2h6l4 4v12H5a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1Z" />
			<path d="M7 9h6M7 12h6M7 15h4" />
		</svg>
	{/if}
</span>

<style>
	.fg {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border-radius: 8px;
		background: var(--surface-inset);
	}
	.fg svg {
		width: 16px;
		height: 16px;
	}
</style>
