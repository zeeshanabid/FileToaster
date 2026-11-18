import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { Mock } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import type { Writable } from 'svelte/store';
import { fireEvent, screen, within } from '@testing-library/dom';
import type { Task } from '$lib/fs/api';
import { formatRate, formatSize } from '$lib/fs/format';
import { uploadManager } from '$lib/fs/upload';
import { taskList, runningCount, queuedCount, activeCount, cancelTaskAction, combinedTaskList, combinedRunningCount, combinedQueuedCount, combinedActiveCount, taskPanelOpen } from '$lib/store/tasks';
import RunningTaskIcon from './RunningTaskIcon.svelte';

// The component reads its live data from $lib/store/tasks. These stores are
// created inside the hoisted mock factory (vi.mock runs before top-level
// declarations) and re-imported here so the test can drive the very same
// instances the component subscribes to. The real module types them as
// Readable; the mock instances are writable, so cast for driving them.
vi.mock('$lib/store/tasks', async () => {
  const { writable, derived } = await import('svelte/store');
  const taskListStore = writable<Task[]>([]);
  const runningCountStore = writable<number>(0);
  const queuedCountStore = writable<number>(0);
  const activeCountStore = writable<number>(0);
  const taskPanelOpenStore = writable<boolean>(false);
  return {
    taskList: taskListStore,
    runningCount: runningCountStore,
    queuedCount: queuedCountStore,
    activeCount: activeCountStore,
    combinedTaskList: derived(taskListStore, ($t) => $t),
    combinedRunningCount: derived(runningCountStore, ($r) => $r),
    combinedQueuedCount: derived(queuedCountStore, ($q) => $q),
    combinedActiveCount: derived(activeCountStore, ($a) => $a),
    taskPanelOpen: taskPanelOpenStore,
    taskLabel: (t: Task) => (t.type === 'copy' ? 'Copy' : t.type),
    cancelTaskAction: vi.fn(async () => {})
  };
});

const tasks = taskList as unknown as Writable<Task[]>;
const running = runningCount as unknown as Writable<number>;
const queued = queuedCount as unknown as Writable<number>;
const active = activeCount as unknown as Writable<number>;

const instances: unknown[] = [];
function mountComp(): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instances.push(mount(RunningTaskIcon as never, { target } as never));
  return target;
}
function unmountAll() {
  while (instances.length) unmount(instances.pop() as never);
  document.querySelectorAll('body > div').forEach((el) => el.remove());
}

function copyTask(overrides: Partial<Task>): Task {
  const now = new Date().toISOString();
  return {
    id: 'task-1',
    type: 'copy',
    status: 'running',
    progress: 0,
    created: now,
    updated: now,
    bytes: { total: 0, done: 0, filesTotal: 0, filesDone: 0, ratePerSec: 0 },
    meta: { items: [{ src: '/a/photo.jpg', dst: '/b/photo.jpg' }], conflict: '' },
    ...overrides
  } as Task;
}

function openTasks(): void {
  fireEvent.click(screen.getByRole('button', { name: 'Tasks' }));
}

// The progress panel only renders once the popover is open.
async function openPanel(): Promise<void> {
  mountComp();
  openTasks();
  await tick();
  const button = screen.getByRole('button', { name: 'Tasks' });
  expect(button).toHaveAttribute('aria-expanded', 'true');
}

beforeEach(() => {
  tasks.set([]);
  running.set(0);
  queued.set(0);
  active.set(0);
  (cancelTaskAction as unknown as Mock).mockClear();
});
afterEach(() => {
  unmountAll();
});

describe('RunningTaskIcon — progress bar and stats', () => {
  it('renders a per-task progress bar sized to the task progress', async () => {
    await openPanel();
    tasks.set([copyTask({ progress: 50 })]);
    active.set(1);
    await tick();

    const task = within(screen.getByRole('dialog')).getByTitle('Copy').closest('.task') as HTMLElement;
    const bar = task.querySelector('.bar') as HTMLElement;
    const fill = bar.querySelector('.fill') as HTMLElement;
    expect(bar).toBeTruthy();
    expect(fill).toBeTruthy();
    expect(fill.getAttribute('style')).toContain('50%');
    expect(bar.getAttribute('role')).toBe('progressbar');
  });

  it('shows files done/total, human byte counts, and transfer rate while running', async () => {
    await openPanel();
    tasks.set([
      copyTask({
        progress: 50,
        bytes: { total: 1024, done: 512, filesTotal: 3, filesDone: 2, ratePerSec: 3072 }
      })
    ]);
    active.set(1);
    await tick();

    const meta = within(screen.getByRole('dialog')).getByTitle('Copy').closest('.task')
      ?.querySelector('.meta') as HTMLElement;
    expect(meta).not.toBeNull();
    expect(within(meta).getByText(`2 / 3 files`)).toBeInTheDocument();
    expect(within(meta).getByText(`${formatSize(512)} / ${formatSize(1024)}`)).toBeInTheDocument();
    expect(within(meta).getByText(formatRate(3072))).toBeInTheDocument();
  });

  it('hides the progress bar and stats once the task finishes', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'finished', progress: 100 })]);
    active.set(0);
    await tick();

    const tasksPanel = screen.getByRole('dialog');
    // The status label is shown once in the row header ...
    expect(tasksPanel.querySelector('.status-finished')?.textContent?.trim()).toBe('Done');
    // ... but no in-progress bar or file/byte/rate metadata renders for a terminal task.
    expect(tasksPanel.querySelector('.bar')).toBeNull();
    expect(tasksPanel.querySelector('.files, .bytes, .rate')).toBeNull();
  });

  it('shows queued tasks without a speed (rate) value', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'queued', progress: 0 })]);
    active.set(1);
    await tick();

    const tasksPanel = screen.getByRole('dialog');
    expect(tasksPanel.querySelector('.status-queued')?.textContent?.trim()).toBe('Queued');
    expect(tasksPanel.querySelector('.bar')).toBeTruthy();
    // The rate cell only renders while running.
    expect(tasksPanel.querySelector('.rate')).toBeNull();
  });
});

describe('RunningTaskIcon — upload resume', () => {
  it('shows a Resume button for a cancelled upload and resumes it by its staging id', async () => {
    const resumeSpy = vi.spyOn(uploadManager, 'resumeSession').mockResolvedValue(true);
    await openPanel();
    tasks.set([
      copyTask({
        id: 'staging-sess-1',
        type: 'upload',
        status: 'cancelled',
        meta: { staging: 'sess-1', dest: '/root/out', phase: 'cancelled', files: 1 }
      })
    ]);
    active.set(0);
    await tick();

    const resume = within(screen.getByRole('dialog')).getByRole('button', { name: 'Resume upload' });
    fireEvent.click(resume);
    await tick();
    expect(resumeSpy).toHaveBeenCalledWith('sess-1');
    resumeSpy.mockRestore();
  });

  it('shows a Resume button for a failed upload', async () => {
    await openPanel();
    tasks.set([
      copyTask({
        id: 'staging-sess-2',
        type: 'upload',
        status: 'failed',
        error: 'boom',
        meta: { staging: 'sess-2', dest: '/root/out', phase: 'failed', files: 1 }
      })
    ]);
    active.set(0);
    await tick();

    expect(within(screen.getByRole('dialog')).getByRole('button', { name: 'Resume upload' })).toBeInTheDocument();
  });
});
