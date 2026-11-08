<script lang="ts">
	import Desktop from '$lib/components/Desktop.svelte';
	import Taskbar from '$lib/components/Taskbar.svelte';
	import Window from '$lib/components/Window.svelte';
	import { windows } from '$lib/store/windows';
</script>

<svelte:window onkeydown={(e) => (e.key === 'Escape' && e.preventDefault())} />

<div class="shell">
	<Desktop />
	{#each $windows as win (win.id)}
		<Window window={win} />
	{/each}
	<Taskbar />
</div>

<style>
	.shell {
		position: fixed;
		inset: 0;
	}
</style>
