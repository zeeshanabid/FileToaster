<script lang="ts">
	import { windows } from '$lib/store/windows';
	import WindowButton from './WindowButton.svelte';
	import RunningTaskIcon from './RunningTaskIcon.svelte';
</script>

<div class="taskbar">
	<button class="launcher" type="button" title="FileToaster">
		<span class="launcher-mark" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
				<path d="M9 12h6M9 15h4" />
			</svg>
		</span>
		<span>FileToaster</span>
	</button>

	<div class="windows">
		{#each $windows as win (win.id)}
			<WindowButton window={win} active={!win.minimized} />
		{/each}
	</div>

	<div class="spacer"></div>
	<RunningTaskIcon />
</div>

<style>
	.taskbar {
		position: absolute;
		left: 0;
		right: 0;
		bottom: 12px;
		height: 54px;
		display: flex;
		align-items: center;
		gap: 8px;
		padding: 0 14px;
		background: var(--taskbar-bg);
		backdrop-filter: blur(16px) saturate(160%);
		-webkit-backdrop-filter: blur(16px) saturate(160%);
		border: 1px solid var(--taskbar-border);
		border-top: 1px solid var(--taskbar-border);
		border-radius: 16px;
		z-index: 40;
		box-shadow: 0 12px 30px rgba(0, 0, 0, 0.35);
	}
	.launcher {
		display: inline-flex;
		align-items: center;
		gap: 9px;
		padding: 7px 13px 7px 11px;
		background: linear-gradient(135deg, var(--accent), var(--accent-2));
		border: none;
		border-radius: 11px;
		color: #fff;
		font-weight: 700;
		font-size: 14px;
		cursor: pointer;
	}
	.launcher-mark {
		display: inline-flex;
		align-items: center;
		justify-content: center;
		width: 22px;
		height: 22px;
		background: rgba(255, 255, 255, 0.18);
		border-radius: 7px;
	}
	.launcher-mark svg {
		width: 14px;
		height: 14px;
		color: #fff;
	}
	.windows {
		display: flex;
		align-items: center;
		gap: 6px;
		flex: 1;
		min-width: 0;
		height: 100%;
		overflow: hidden;
	}
	.spacer {
		flex: 0 0 auto;
	}
</style>
