<script lang="ts">
	import { onMount } from 'svelte';
	import { ROOTS, fetchRoots, type Root } from '$lib/roots';
	import { openWindow } from '$lib/store/windows';
	import FolderIcon from './FolderIcon.svelte';

	let roots = $state<Root[]>(ROOTS);
	let error = $state<string | null>(null);

	onMount(async () => {
		try {
			roots = await fetchRoots();
			error = null;
		} catch {
			// Keep the demo folders so the desktop is not empty if the server is
			// unreachable.
		}
	});
</script>

<div class="desktop">
	{#each roots as root (root.id)}
		<FolderIcon root={root} onopen={openWindow} />
	{/each}
</div>

{#if error}
	<div class="hint">Could not load folders from the server; showing default folders.</div>
{/if}

<style>
	.desktop {
		position: absolute;
		inset: 0;
		padding: 44px 48px 72px;
		display: flex;
		flex-wrap: wrap;
		align-content: flex-start;
		gap: 14px;
		align-items: flex-start;
	}
	.hint {
		position: absolute;
		bottom: 84px;
		left: 48px;
		font-size: 12px;
		color: rgba(255, 255, 255, 0.6);
	}
</style>
