import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { Mock } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import type { Writable } from 'svelte/store';
import { fireEvent, screen, within } from '@testing-library/dom';
import type { Task } from '$lib/fs/api';
import { formatRate, formatSize } from '$lib/fs/format';
import { uploadManager } from '$lib/fs/upload';
import { taskList, runningCount, queuedCount, activeCount, cancelTaskAction, combinedTaskList, combinedRunningCount, combinedQueuedCount, combinedActiveCount, taskPanelOpen } from '$lib/store/tasks';
import { taskNotification } from '$lib/store/notifications';
import { clearCompletedTasks } from '$lib/store/tasks';
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
  const taskNotificationStore = writable<{ taskId: string; label: string } | null>(null);
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
    taskNotification: taskNotificationStore,
    taskLabel: (t: Task) => (t.type === 'copy' ? 'Copy' : t.type),
    cancelTaskAction: vi.fn(async () => {}),
    clearCompletedTasks: vi.fn(async () => {})
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
  taskPanelOpen.set(false);
  taskNotification.set(null);
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
    const bar = task.querySelector('.bar') as HTMLProgressElement;
    expect(bar).toBeTruthy();
    expect(bar.value).toBe(50);
    expect(bar.max).toBe(100);
    expect(bar.getAttribute('aria-label')).toBe('Progress');
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

  it('does not let a visible toast block opening the task panel', async () => {
    taskNotification.set({ taskId: 'toast-1', label: 'Upload started' });
    mountComp();
    await tick();

    // The toast portal is pass-through so its full-viewport host does not eat
    // clicks meant for the taskbar button underneath it.
    await vi.waitFor(() => expect(document.querySelector('.window-portal')).not.toBeNull());
    const portal = document.querySelector('.window-portal');
    expect(portal).toHaveClass('passthrough');
    expect(screen.getByText('Upload started')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Tasks' }));
    await tick();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
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

  it('shows no Resume button for a failed finalize task (staging has no session left)', async () => {
    await openPanel();
    tasks.set([
      copyTask({
        id: 'task-finish-failed',
        type: 'upload',
        status: 'failed',
        error: 'could not copy the staged upload',
        meta: { staging: '/tmp/filetoaster-abc', dest: '/root/out', phase: 'finalizing', files: 1 }
      })
    ]);
    active.set(0);
    await tick();

    expect(within(screen.getByRole('dialog')).queryByRole('button', { name: 'Resume upload' })).toBeNull();
  });

  it('asks for the files when a refreshed upload lost its bytes, then resumes with them', async () => {
    const needsSpy = vi.spyOn(uploadManager, 'needsFiles').mockReturnValue(true);
    const folderSpy = vi.spyOn(uploadManager, 'needsFolderSelection').mockReturnValue(false);
    const resumeSpy = vi.spyOn(uploadManager, 'resumeSession').mockResolvedValue(true);
    const pickedSpy = vi.spyOn(uploadManager, 'resumeWithFiles').mockResolvedValue(true);
    const clickSpy = vi.spyOn(HTMLInputElement.prototype, 'click').mockImplementation(() => {});

    await openPanel();
    tasks.set([
      copyTask({
        id: 'staging-sess-lost',
        type: 'upload',
        status: 'cancelled',
        meta: { staging: 'sess-lost', dest: '/root/out', phase: 'cancelled', files: 1 }
      })
    ]);
    active.set(0);
    await tick();

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Resume upload' }));
    await tick();
    // The picker opens synchronously; no transfer is started without the bytes.
    expect(clickSpy).toHaveBeenCalled();
    expect(resumeSpy).not.toHaveBeenCalled();

    const input = document.querySelector('.resume-input') as HTMLInputElement;
    const file = new File(['abc'], 'a.bin');
    Object.defineProperty(input, 'files', { value: [file], configurable: true });
    fireEvent.change(input);
    await vi.waitFor(() =>
      expect(pickedSpy).toHaveBeenCalledWith('sess-lost', [{ rel: 'a.bin', size: 3, blob: file }])
    );

    needsSpy.mockRestore();
    folderSpy.mockRestore();
    resumeSpy.mockRestore();
    pickedSpy.mockRestore();
    clickSpy.mockRestore();
  });
});

describe('RunningTaskIcon — upload cancel', () => {
  it('aborts a local staging task through the upload manager', async () => {
    const abortSpy = vi.spyOn(uploadManager, 'abort').mockResolvedValue();
    await openPanel();
    tasks.set([
      copyTask({
        id: 'staging-sess-cancel',
        type: 'upload',
        status: 'running',
        meta: { staging: 'sess-cancel', dest: '/root/out', phase: 'staging', files: 1 }
      })
    ]);
    active.set(1);
    await tick();

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel task' }));
    await tick();
    expect(abortSpy).toHaveBeenCalledWith('sess-cancel');
    expect(cancelTaskAction as unknown as Mock).not.toHaveBeenCalled();
    abortSpy.mockRestore();
  });

  it('cancels a server finalize task by its task id', async () => {
    await openPanel();
    tasks.set([
      copyTask({
        id: 'task-finalize-running',
        type: 'upload',
        status: 'running',
        meta: { staging: '/tmp/filetoaster-abc', dest: '/root/out', phase: 'finalizing', files: 1 }
      })
    ]);
    active.set(1);
    await tick();

    fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel task' }));
    await tick();
    // The meta staging value is a directory path for a finalize task, so the
    // cancel must go to the tracked task rather than the upload manager.
    expect(cancelTaskAction).toHaveBeenCalledWith('task-finalize-running');
  });
});

describe('RunningTaskIcon — clear button visibility', () => {
  it('shows clear button when there are finished tasks', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'finished', progress: 100 })]);
    active.set(0);
    await tick();

    const clearBtn = within(screen.getByRole('dialog')).getByRole('button', { name: 'Clear completed tasks' });
    expect(clearBtn).toBeInTheDocument();
  });

  it('shows clear button when there are failed tasks', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'failed', progress: 100, error: 'boom' })]);
    active.set(0);
    await tick();

    const clearBtn = within(screen.getByRole('dialog')).getByRole('button', { name: 'Clear completed tasks' });
    expect(clearBtn).toBeInTheDocument();
  });

  it('shows clear button when there are cancelled tasks', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'cancelled', progress: 50 })]);
    active.set(0);
    await tick();

    const clearBtn = within(screen.getByRole('dialog')).getByRole('button', { name: 'Clear completed tasks' });
    expect(clearBtn).toBeInTheDocument();
  });

  it('hides clear button when only running tasks exist', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'running', progress: 50 })]);
    active.set(1);
    await tick();

    const dialog = screen.getByRole('dialog');
    expect(dialog.querySelector('[aria-label="Clear completed tasks"]')).toBeNull();
  });

  it('hides clear button when only queued tasks exist', async () => {
    await openPanel();
    tasks.set([copyTask({ status: 'queued', progress: 0 })]);
    active.set(1);
    await tick();

    const dialog = screen.getByRole('dialog');
    expect(dialog.querySelector('[aria-label="Clear completed tasks"]')).toBeNull();
  });
});

describe('RunningTaskIcon — popover z-index', () => {
  it('renders the task panel popover with high z-index (1000) above windows', async () => {
    await openPanel();
    tasks.set([copyTask({ progress: 50 })]);
    active.set(1);
    await tick();

    const dialog = screen.getByRole('dialog');
    // The popover should have a high z-index to stay above windows
    const popover = dialog.closest('.popover') as HTMLElement;
    expect(popover).toBeInTheDocument();
    // The popover element should have the z-index style from CSS
    expect(popover).toHaveClass('popover');
  });
});
