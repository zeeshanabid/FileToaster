<script lang="ts">
	import { focusWindow, toggleMinimize, type AppWindow } from '$lib/store/windows';

	let { window: win, active }: { window: AppWindow; active: boolean } = $props();
</script>

<button
	class="window-button"
	class:active={active}
	class:minimized={win.minimized}
	title={win.minimized ? `${win.name} (minimized)` : win.name}
	onclick={() => (win.minimized ? toggleMinimize(win.id) : focusWindow(win.id))}
>
	<span class="indicator" aria-hidden="true"></span>
	<span class="name">{win.name}</span>
</button>

<style>
	.window-button {
		display: inline-flex;
		align-items: center;
		gap: 9px;
		max-width: 170px;
		height: 38px;
		padding: 0 13px;
		background: rgba(255, 255, 255, 0.07);
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 10px;
		color: var(--taskbar-text);
		font-size: 13px;
		font-weight: 500;
		cursor: pointer;
	}
	.window-button:hover {
		background: rgba(255, 255, 255, 0.15);
	}
	.window-button.active {
		background: var(--taskbar-active);
		border-color: rgba(129, 140, 248, 0.5);
		color: #fff;
	}
	.window-button.minimized {
		opacity: 0.6;
	}
	.indicator {
		width: 5px;
		height: 5px;
		border-radius: 50%;
		background: var(--ok);
		flex: none;
		opacity: 0.85;
	}
	.window-button.active .indicator {
		background: var(--accent-2);
		opacity: 1;
	}
	.name {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
</style>
