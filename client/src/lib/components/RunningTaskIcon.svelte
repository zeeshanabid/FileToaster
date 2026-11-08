<script lang="ts">
	import { runningTasks } from '$lib/store/tasks';

	let open = $state(false);
</script>

<div class="tasks">
	<button
		class="tasks-button"
		type="button"
		class:open={open}
		aria-haspopup="true"
		aria-expanded={open}
		onclick={() => (open = !open)}
	>
		<span class="gear" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
				<path d="m19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.27.62.86 1.05 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
			</svg>
		</span>
		{#if $runningTasks.length > 0}
			<span class="badge">{$runningTasks.length}</span>
		{/if}
	</button>

	{#if open}
		<div class="popover" role="dialog">
			<div class="popover-head">Running tasks</div>
			{#if $runningTasks.length === 0}
				<div class="empty">No tasks running</div>
			{/if}
			{#each $runningTasks as task (task.id)}
				<div class="task">
					<div class="task-name">{task.label}</div>
					<div class="bar" role="progressbar" aria-valuenow={task.progress} aria-valuemin="0" aria-valuemax="100">
						<div class="fill" style="width:{task.progress}%"></div>
					</div>
				</div>
			{/each}
		</div>
	{/if}
</div>

<style>
	.tasks {
		position: relative;
	}
	.tasks-button {
		display: inline-flex;
		align-items: center;
		gap: 7px;
		height: 38px;
		padding: 0 12px;
		background: rgba(255, 255, 255, 0.07);
		border: 1px solid rgba(255, 255, 255, 0.12);
		border-radius: 10px;
		color: var(--taskbar-text);
		cursor: pointer;
	}
	.tasks-button:hover,
	.tasks-button.open {
		background: rgba(255, 255, 255, 0.15);
	}
	.gear {
		display: grid;
		place-items: center;
		width: 18px;
		height: 18px;
	}
	.gear svg {
		width: 17px;
		height: 17px;
	}
	.badge {
		min-width: 18px;
		height: 18px;
		padding: 0 5px;
		border-radius: 9px;
		background: var(--alert);
		color: #fff;
		font-size: 11px;
		font-weight: 700;
		display: inline-flex;
		align-items: center;
		justify-content: center;
	}
	.popover {
		position: absolute;
		bottom: 54px;
		right: 0;
		width: 280px;
		background: var(--surface);
		color: var(--text);
		border: 1px solid var(--border);
		border-radius: 12px;
		box-shadow: var(--shadow-popover);
		padding: 12px;
		z-index: 50;
	}
	.popover-head {
		font-size: 11px;
		font-weight: 700;
		text-transform: uppercase;
		letter-spacing: 0.05em;
		color: var(--text-faint);
		margin-bottom: 10px;
	}
	.empty {
		font-size: 13px;
		color: var(--text-faint);
		padding: 8px 0;
	}
	.task {
		padding: 8px 2px;
		border-top: 1px solid var(--border);
	}
	.task:first-child {
		border-top: none;
	}
	.task-name {
		font-size: 13px;
		font-weight: 500;
		margin-bottom: 8px;
		color: var(--text);
	}
	.bar {
		height: 7px;
		border-radius: 999px;
		background: var(--surface-inset);
		overflow: hidden;
	}
	.fill {
		height: 100%;
		background: linear-gradient(90deg, var(--accent), var(--accent-2));
		border-radius: inherit;
	}
</style>
