<script lang="ts">
  import { runningCount, queuedCount, activeCount, taskList, taskLabel, cancelTaskAction, combinedTaskList, combinedRunningCount, combinedQueuedCount, combinedActiveCount, taskPanelOpen, clearCompletedTasks } from '$lib/store/tasks';
  import { formatRate, formatSize } from '$lib/fs/format';
  import type { Task } from '$lib/fs/api';
  import { uploadManager } from '$lib/fs/upload';

  // Local open state, synced with the global taskPanelOpen store
  let open = $state(false);

  // Sync with global store
  $effect(() => {
    open = $taskPanelOpen;
  });

  // Auto-open panel when there are active tasks
  $effect(() => {
    if ($combinedActiveCount > 0 && !open) {
      taskPanelOpen.set(true);
    }
  });

  function togglePanel() {
    const newOpen = !open;
    open = newOpen;
    taskPanelOpen.set(newOpen);
  }

  async function on_cancel(task: Task) {
    if (task.status !== 'running' && task.status !== 'queued') return;
    // For upload staging tasks, use the upload manager's abort with the original task ID from meta
    if (task.type === 'upload' && task.meta && (task.meta as any).staging) {
      const stagingTaskID = (task.meta as any).staging;
      try {
        await uploadManager.abort(stagingTaskID);
      } catch {
        // Ignore transport errors
      }
      return;
    }
    try {
      await cancelTaskAction(task.id);
    } catch {
      // The pool reports cancellation itself; ignore transport errors.
    }
  }

  async function on_resume(task: Task) {
    if (task.type !== 'upload') return;
    if (task.status !== 'cancelled' && task.status !== 'failed') return;
    const stagingTaskID = (task.meta as any)?.staging;
    if (!stagingTaskID) return;
    try {
      await uploadManager.resumeSession(stagingTaskID);
    } catch {
      // Ignore transport errors
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
    onclick={togglePanel}
  >
    <span class="tasks-icon" aria-hidden="true">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
        <path d="M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2" />
        <path d="M9 5a2 2 0 0 1 2-2h2a2 2 0 0 1 2 2" />
        <path d="M9 14l2 2 4-4" />
      </svg>
    </span>
    {#if $combinedActiveCount > 0}
      <span class="badge" title={$combinedActiveCount + " task" + ($combinedActiveCount === 1 ? '' : 's') + " active"}>{$combinedActiveCount}</span>
    {/if}
  </button>

  {#if open}
    <div class="popover" role="dialog" aria-label="Tasks">
      <div class="popover-head">
        Tasks
        {#if $combinedRunningCount > 0 || $combinedQueuedCount > 0}
          <span class="counts">
            {$combinedRunningCount} running{#if $combinedQueuedCount > 0}, {$combinedQueuedCount} queued{/if}
          </span>
        {/if}
        {#if $combinedTaskList.some(t => t.status === 'finished' || t.status === 'cancelled')}
          <button
            class="clear-btn"
            type="button"
            aria-label="Clear completed tasks"
            title="Clear completed tasks"
            onclick={clearCompletedTasks}
          >
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
              <path d="M3 6h18" />
              <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
              <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
            </svg>
          </button>
        {/if}
      </div>
      {#if $combinedTaskList.length === 0}
        <div class="empty">No tasks yet.</div>
      {:else}
        {#each $combinedTaskList as task (task.id)}
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
              {:else if task.type === 'upload' && (task.status === 'cancelled' || task.status === 'failed')}
                <button
                  class="resume"
                  type="button"
                  aria-label="Resume upload"
                  title="Resume upload"
                  onclick={() => on_resume(task)}
                >
                  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
                    <path d="M5 12h14" />
                    <path d="M12 5l7 7-7 7" />
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
                {#if task.status === 'running' && task.bytes.ratePerSec > 0}
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
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px;
    color: var(--taskbar-text);
    cursor: pointer;
    transition: all 150ms ease;
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
  }
  .tasks-button:hover,
  .tasks-button.open {
    background: rgba(255, 255, 255, 0.14);
    border-color: rgba(255, 255, 255, 0.18);
    box-shadow:
      0 2px 10px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.08);
  }
  .tasks-button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .tasks-icon {
    display: grid;
    place-items: center;
    width: 18px;
    height: 18px;
    color: currentColor;
    opacity: 0.9;
  }
  .tasks-icon svg {
    width: 17px;
    height: 17px;
  }
  .badge {
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    border-radius: 9px;
    background: linear-gradient(135deg, var(--alert), var(--alert-strong));
    color: #fff;
    font-size: 11px;
    font-weight: 700;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    box-shadow: 0 2px 8px rgba(251, 113, 133, 0.35);
  }
  .popover {
    position: absolute;
    bottom: 54px;
    right: 0;
    width: 360px;
    max-height: 500px;
    overflow: auto;
    background: var(--surface);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 14px;
    box-shadow: var(--shadow-popover);
    padding: 14px;
    z-index: 50;
    animation: popover-in 140ms ease-out;
  }
  @keyframes popover-in {
    from {
      opacity: 0;
      transform: translateY(8px);
    }
    to {
      opacity: 1;
      transform: translateY(0);
    }
  }
  .popover-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    font-size: 11px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--text-faint);
    margin-bottom: 12px;
    padding-bottom: 8px;
    border-bottom: 1px solid var(--border);
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
    padding: 16px 0;
    text-align: center;
  }
  .task {
    padding: 10px 4px;
    border-top: 1px solid var(--border);
  }
  .task:first-child {
    border-top: none;
  }
  .task.terminal {
    opacity: 0.7;
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
    color: var(--ok-strong);
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
    transition: all 120ms ease;
  }
  .cancel:hover {
    background: var(--alert);
    color: #fff;
  }
  .cancel svg {
    width: 12px;
    height: 12px;
  }
  .resume {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    flex: none;
    background: var(--ok-strong);
    border: none;
    border-radius: 8px;
    color: #fff;
    cursor: pointer;
    transition: all 120ms ease;
    box-shadow: 0 2px 8px rgba(34, 197, 94, 0.4);
  }
  .resume:hover {
    background: var(--ok);
    transform: scale(1.05);
    box-shadow: 0 4px 12px rgba(34, 197, 94, 0.5);
  }
  .resume:active {
    transform: scale(0.98);
  }
  .resume svg {
    width: 16px;
    height: 16px;
  }
  .clear-btn {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    flex: none;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    transition: all 120ms ease;
  }
  .clear-btn:hover {
    background: var(--alert);
    color: #fff;
  }
  .clear-btn svg {
    width: 14px;
    height: 14px;
  }
  .bar {
    height: 7px;
    border-radius: 999px;
    background: var(--surface-inset);
    overflow: hidden;
    margin-top: 8px;
  }
  .fill {
    height: 100%;
    background: linear-gradient(90deg, var(--accent), var(--accent-2));
    border-radius: inherit;
    transition: width 180ms ease-out;
    box-shadow: 0 0 12px rgba(129, 140, 248, 0.4);
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