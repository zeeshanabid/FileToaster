import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { Mock } from 'vitest';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import { taskList, initTasks, shutdownTasks } from '$lib/store/tasks';
import { listTasks, cancelTask, submitCopy } from '$lib/fs/api';
import { reloadWindowsForPath } from '$lib/store/windows';
import type { Task } from '$lib/fs/api';

// The live SSE path lives entirely inside tasks.ts and is exercised through the
// exported store + initTasks. We mock the api module (its `listTasks` is used by
// the initial snapshot) and stub the global `fetch` used by the SSE connector.
  vi.mock('$lib/fs/api', () => ({
    listTasks: vi.fn(),
    cancelTask: vi.fn(),
    submitCopy: vi.fn(),
    createUpload: vi.fn(),
    uploadChunk: vi.fn(),
    getUploadStatus: vi.fn(),
    cancelUpload: vi.fn(),
    chunkCountForFile: vi.fn(() => 1),
    DEFAULT_CHUNK_SIZE: 4 << 20
  }));

// The windows store is mocked so the SSE completion path can verify which
// directory paths are scheduled for reload without touching real windows.
vi.mock('$lib/store/windows', () => ({
  reloadWindowsForPath: vi.fn()
}));

const listTasksMock = listTasks as unknown as Mock;
const reloadSpy = reloadWindowsForPath as unknown as Mock;
const enc = new TextEncoder();
let ctrl: ReadableStreamDefaultController<Uint8Array> | null = null;

function iso(n = 0): string {
  return new Date(Date.now() - n * 1000).toISOString();
}

function copyTask(overrides: Partial<Task>): Task {
  return {
    id: 'task-1',
    type: 'copy',
    status: 'running',
    progress: 0,
    created: iso(),
    updated: iso(),
    bytes: { total: 0, done: 0, filesTotal: 0, filesDone: 0, ratePerSec: 0 },
    ...overrides
  } as Task;
}

function sseMessage(task: Task): string {
  return `event: task\ndata: ${JSON.stringify(task)}\n\n`;
}

let lastStream: ReadableStream<Uint8Array> | null;

function stubFetch() {
  listTasksMock.mockResolvedValue([]);
  vi.stubGlobal(
    'fetch',
    vi.fn(async (url: string) => {
      if (String(url).includes('/api/tasks')) {
        return new Response(JSON.stringify({ tasks: [] }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' }
        });
      }
      // /api/events: an event-stream whose bytes we push on demand.
      lastStream = new ReadableStream<Uint8Array>({
        start(c) {
          ctrl = c;
        }
      });
      return new Response(lastStream, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' }
      });
    })
  );
}

function push(task: Task) {
  if (!ctrl) throw new Error('sse stream not started');
  ctrl.enqueue(enc.encode(sseMessage(task)));
}

function delay(ms = 30) {
  return new Promise((r) => setTimeout(r, ms));
}

beforeEach(() => {
    stubFetch();
    shutdownTasks();
    initTasks();
    reloadSpy.mockClear();
  });

  afterEach(() => {
    shutdownTasks();
    ctrl?.close();
    vi.unstubAllGlobals();
  });

describe('live SSE task progress', () => {
  it('updates the store (and thus the progress bar) when the server streams deltas', async () => {
    await tick();

    // The server starts the copy: queued, then running with partial bytes.
    push(copyTask({ status: 'running', progress: 30, bytes: { total: 1000, done: 300, filesTotal: 4, filesDone: 1, ratePerSec: 2048 } }));
    await tick();
    await delay();

    const afterFirst = get(taskList).find((t) => t.id === 'task-1');
    expect(afterFirst).toBeDefined();
    expect(afterFirst!.status).toBe('running');
    expect(afterFirst!.progress).toBe(30);
    expect(afterFirst!.bytes.done).toBe(300);
    expect(afterFirst!.bytes.total).toBe(1000);

    // The bar must climb as more bytes stream through — the reported symptom
    // is that it never increases while the copy runs.
    push(copyTask({ status: 'running', progress: 75, bytes: { total: 1000, done: 750, filesTotal: 4, filesDone: 3, ratePerSec: 4096 } }));
    await tick();
    await delay();

    const afterSecond = get(taskList).find((t) => t.id === 'task-1');
    expect(afterSecond).toBeDefined();
    expect(afterSecond!.progress).toBe(75);
    expect(afterSecond!.bytes.done).toBe(750);
    expect(afterSecond!.bytes.filesDone).toBe(3);
  });

  it('rejects reload when the task is still running', async () => {
    await tick();

    push(copyTask({ status: 'running', progress: 50, bytes: { total: 1000, done: 500, filesTotal: 4, filesDone: 2, ratePerSec: 2048 } }));
    await tick();
    await delay();

    // A non-terminal task must not trigger any reload.
    expect(reloadSpy).not.toHaveBeenCalled();
  });

  it('triggers window reloads when a task reaches a terminal status', async () => {
    await tick();

    // Task is running, then finishes with the destination directory in meta.
    push(copyTask({
      status: 'running',
      progress: 50,
      bytes: { total: 1000, done: 500, filesTotal: 4, filesDone: 2, ratePerSec: 2048 }
    }));
    await tick();
    await delay();
    expect(reloadSpy).not.toHaveBeenCalled();

    // The server reports the copy finished; the affected destination directory
    // must be scheduled for reload.
    push(copyTask({
      status: 'finished',
      progress: 100,
      bytes: { total: 1000, done: 1000, filesTotal: 4, filesDone: 4, ratePerSec: 4096 },
      meta: { items: [{ src: '/source/a.txt', dst: '/dest/a.txt' }], conflict: '' }
    }));
    await tick();
    await delay();

    expect(reloadSpy).toHaveBeenCalledTimes(1);
    expect(reloadSpy).toHaveBeenCalledWith('/dest');
  });

  it('triggers reload for both source and destination when a move finishes', async () => {
    await tick();

    push(copyTask({
      id: 'task-move',
      type: 'move',
      status: 'finished',
      progress: 100,
      bytes: { total: 100, done: 100, filesTotal: 1, filesDone: 1, ratePerSec: 100 },
      meta: { items: [{ src: '/from/a.txt', dst: '/to/a.txt' }] }
    }));
    await tick();
    await delay();

    const calledPaths = reloadSpy.mock.calls.map((c) => c[0]).sort();
    expect(calledPaths).toEqual(['/from', '/to']);
  });

  it('triggers reload with the archive destination when compress finishes', async () => {
    await tick();

    push(copyTask({
      id: 'task-compress',
      type: 'compress',
      status: 'finished',
      progress: 100,
      bytes: { total: 100, done: 100, filesTotal: 2, filesDone: 2, ratePerSec: 100 },
      meta: { format: 'zip', dest: '/data/archive.zip', source: 2 }
    }));
    await tick();
    await delay();

    expect(reloadSpy).toHaveBeenCalledWith('/data');
  });

  it('triggers reload for a failed task terminal state', async () => {
    await tick();

    push(copyTask({
      status: 'failed',
      progress: 40,
      bytes: { total: 1000, done: 400, filesTotal: 4, filesDone: 1, ratePerSec: 2048 },
      meta: { items: [{ src: '/source/a.txt', dst: '/dest/a.txt' }], conflict: '' }
    }));
    await tick();
    await delay();

    // Failed tasks are terminal, so the directory state (which may be partial)
    // should still be refreshed in the UI.
    expect(reloadSpy).toHaveBeenCalledTimes(1);
    expect(reloadSpy).toHaveBeenCalledWith('/dest');
  });
});
