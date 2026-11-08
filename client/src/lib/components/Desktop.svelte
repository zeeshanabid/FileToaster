<script lang="ts">
	import { ROOTS, type Root } from '$lib/roots';
	import { openWindow } from '$lib/store/windows';
	import FolderIcon from './FolderIcon.svelte';
</script>

<div class="desktop">
	{#each ROOTS as root (root.id)}
		<FolderIcon root={root} onopen={openWindow} />
	{/each}
</div>

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
</style>
