<script lang="ts">
  import { runningCount, queuedCount, activeCount, taskList, taskLabel, cancelTaskAction } from '$lib/store/tasks';
  import { formatRate, formatSize } from '$lib/fs/format';
  import type { Task } from '$lib/fs/api';

  let open = $state(false);

  async function on_cancel(task: Task) {
    if (task.status !== 'running' && task.status !== 'queued') return;
    try {
      await cancelTaskAction(task.id);
    } catch {
      // The pool reports cancellation itself; ignore transport errors.
    }
  }

  function statusLabel(status: Task['status']): string {
    switch (status) {
      case 'queued':
        return 'Queued';
      case 'running':
        return 'Running';
      case 'finished':
        return 'Done';
      case 'failed':
        return 'Failed';
      case 'cancelled':
        return 'Cancelled';
    }
  }
</script>

<div class="tasks">
  <button
    class="tasks-button"
    type="button"
    class:open={open}
    aria-haspopup="true"
    aria-expanded={open}
    aria-label="Tasks"
    title="Show tasks"
    onclick={() => (open = !open)}
  >
    <span class="gear" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
        <path d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6Z" />
        <path d="m19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9c.27.62.86 1.05 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z" />
      </svg>
    </span>
    {#if $activeCount > 0}
      <span class="badge" title={$activeCount + " task" + ($activeCount === 1 ? '' : 's') + " active"}>{$activeCount}</span>
    {/if}
  </button>

  {#if open}
    <div class="popover" role="dialog" aria-label="Tasks">
      <div class="popover-head">
        Tasks
        {#if $runningCount > 0 || $queuedCount > 0}
          <span class="counts">
            {$runningCount} running{#if $queuedCount > 0}, {$queuedCount} queued{/if}
          </span>
        {/if}
      </div>
      {#if $taskList.length === 0}
        <div class="empty">No tasks yet.</div>
      {:else}
        {#each $taskList as task (task.id)}
          <div class="task" class:terminal={task.status === 'finished' || task.status === 'failed' || task.status === 'cancelled'}>
            <div class="task-row">
              <span class="task-name" title={taskLabel(task)}>{taskLabel(task)}</span>
              <span class="status status-{task.status}">{statusLabel(task.status)}</span>
              {#if task.status === 'running' || task.status === 'queued'}
                <button
                  class="cancel"
                  type="button"
                  aria-label="Cancel task"
                  title="Cancel task"
                  onclick={() => on_cancel(task)}
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" width="100%" height="100%">
                    <path d="M7 7l10 10M17 7L7 17" />
                  </svg>
                </button>
              {/if}
            </div>
            {#if task.status === 'running' || task.status === 'queued'}
              <div class="bar" role="progressbar" aria-valuenow={Math.round(task.progress)} aria-valuemin="0" aria-valuemax="100" aria-label="Progress">
                <div class="fill" style="width:{task.progress}%"></div>
              </div>
              <div class="meta">
                {#if task.bytes.filesTotal > 0}
                  <span class="files">{task.bytes.filesDone} / {task.bytes.filesTotal} files</span>
                {/if}
                {#if task.bytes.total > 0}
                  <span class="bytes">{formatSize(task.bytes.done)} / {formatSize(task.bytes.total)}</span>
                {/if}
                {#if task.status === 'running'}
                  <span class="rate">{formatRate(task.bytes.ratePerSec)}</span>
                {/if}
              </div>
            {:else}
              <div class="meta {task.status}">
                {#if task.status === 'failed' && task.error}
                  <span class="err">{task.error}</span>
                {:else}
                  <span>{statusLabel(task.status)}</span>
                {/if}
              </div>
            {/if}
          </div>
        {/each}
      {/if}
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
    width: 340px;
    max-height: 480px;
    overflow: auto;
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
  .counts {
    text-transform: none;
    letter-spacing: 0;
    font-weight: 600;
    color: var(--accent-strong);
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
  .task.terminal {
    opacity: 0.75;
  }
  .task-row {
    display: flex;
    align-items: center;
    gap: 8px;
  }
  .task-name {
    font-size: 13px;
    font-weight: 500;
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--text);
  }
  .status {
    font-size: 10px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.03em;
    flex: none;
  }
  .status-running {
    color: var(--accent-strong);
  }
  .status-queued {
    color: var(--text-muted);
  }
  .status-finished {
    color: var(--success, #4ade80);
  }
  .status-failed,
  .status-cancelled {
    color: var(--alert);
  }
  .cancel {
    display: grid;
    place-items: center;
    width: 22px;
    height: 22px;
    flex: none;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .cancel:hover {
    background: var(--alert);
    color: #fff;
  }
  .cancel svg {
    width: 12px;
    height: 12px;
  }
  .bar {
    height: 7px;
    border-radius: 999px;
    background: var(--surface-inset);
    overflow: hidden;
    margin-top: 7px;
  }
  .fill {
    height: 100%;
    background: linear-gradient(90deg, var(--accent), var(--accent-2));
    border-radius: inherit;
    transition: width 120ms ease-out;
  }
  .meta {
    display: flex;
    align-items: center;
    gap: 10px;
    flex-wrap: wrap;
    margin-top: 6px;
    font-size: 11px;
    color: var(--text-muted);
  }
  .meta .err {
    color: var(--alert);
  }
  .files,
  .bytes,
  .rate {
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }
</style>
