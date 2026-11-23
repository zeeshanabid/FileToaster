import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { get } from 'svelte/store';
import { parseSSEMessage, taskLabel, getAffectedPaths, ingestStateToTasks, uploadSessionVisible, clearCompletedTasks, tasksMap } from '$lib/store/tasks';
import type { Task } from '$lib/fs/api';
import type { IngestState } from '$lib/fs/upload';
import { uploadManager } from '$lib/fs/upload';

function task(overrides: Partial<Task> & Pick<Task, 'id' | 'type' | 'status'>): Task {
  const now = new Date().toISOString();
  const base: Partial<Task> = {
    progress: 0,
    created: now,
    updated: now,
    bytes: { total: 0, done: 0, filesTotal: 0, filesDone: 0, ratePerSec: 0 },
    meta: undefined
  };
  return { ...base, ...overrides } as Task;
}

describe('parseSSEMessage', () => {
  it('decodes a task event with JSON data', () => {
    const raw = 'event: task\ndata: {"id":"task-1","type":"copy","status":"running","progress":50,"bytes":{"total":100,"done":50,"filesTotal":2,"filesDone":1,"ratePerSec":2048},"created":"2023-01-01T00:00:00Z","updated":"2023-01-01T00:00:01Z"}';
    const parsed = parseSSEMessage(raw);
    expect(parsed).not.toBeNull();
    expect(parsed!.id).toBe('task-1');
    expect(parsed!.status).toBe('running');
    expect(parsed!.bytes.ratePerSec).toBe(2048);
  });

  it('returns null for a non-task event', () => {
    expect(parseSSEMessage('event: other\ndata: {}')).toBeNull();
  });

  it('returns null when the data is not JSON', () => {
    expect(parseSSEMessage('event: task\ndata: {not json}')).toBeNull();
  });

  it('ignores leading keepalive comments', () => {
    const raw = ': keepalive\n\nevent: task\ndata: {"id":"t","type":"copy","status":"finished","progress":100,"bytes":{},"created":"2023-01-01T00:00:00Z","updated":"2023-01-01T00:00:00Z"}';
    const parsed = parseSSEMessage(raw);
    expect(parsed?.id).toBe('t');
  });
});

describe('taskLabel', () => {
  it('describes a single-file copy by its destination basename', () => {
    const t = task({
      id: '1',
      type: 'copy',
      status: 'running',
      meta: { items: [{ src: '/a/b/photo.jpg', dst: '/c/photo.jpg' }], conflict: '' }
    });
    expect(taskLabel(t)).toBe('Copy photo.jpg');
  });

  it('describes a multi-item copy', () => {
    const t = task({
      id: '2',
      type: 'copy',
      status: 'running',
      meta: {
        items: [
          { src: '/a/1.txt', dst: '/c/1.txt' },
          { src: '/a/2.txt', dst: '/c/2.txt' }
        ],
        conflict: ''
      }
    });
    expect(taskLabel(t)).toBe('Copy 2 items');
  });

  it('describes a single delete', () => {
    const t = task({
      id: '4',
      type: 'delete',
      status: 'running',
      meta: { paths: ['/root/notes.txt'] }
    });
    expect(taskLabel(t)).toBe('Delete');
  });

  it('describes a multi-item delete', () => {
    const t = task({
      id: '5',
      type: 'delete',
      status: 'running',
      meta: { paths: ['/root/a.txt', '/root/b.txt', '/root/documents'] }
    });
    expect(taskLabel(t)).toBe('Delete 3 items');
  });

  it('falls back to the bare operation type for unknown operations', () => {
    expect(taskLabel(task({ id: '3', type: 'foobar', status: 'running' }))).toBe('foobar');
  });

  it('describes a single-item compress by count', () => {
    expect(taskLabel(task({ id: '6', type: 'compress', status: 'running', meta: { format: 'zip', dest: '/a/b.zip', source: 1 } }))).toBe('Compress');
  });

  it('describes a multi-item compress by count', () => {
    expect(taskLabel(task({ id: '7', type: 'compress', status: 'running', meta: { format: 'zip', dest: '/a/b.zip', source: 4 } }))).toBe('Compress 4 items');
  });

  it('describes a decompress by the archive stem', () => {
    expect(taskLabel(task({ id: '8', type: 'decompress', status: 'running', meta: { format: 'zip', archive: '/root/myfiles.zip', extractDir: '/root/myfiles', into: 'myfiles' } }))).toBe('Extract myfiles');
  });
});

describe('getAffectedPaths', () => {
  it('returns destination directory for copy tasks', () => {
    const t = task({
      id: '1',
      type: 'copy',
      status: 'finished',
      meta: {
        items: [
          { src: '/root/a.txt', dst: '/dest/a.txt' },
          { src: '/root/b.txt', dst: '/dest/b.txt' }
        ],
        conflict: ''
      }
    });
    expect(getAffectedPaths(t)).toEqual(['/dest']);
  });

  it('returns source and destination directories for move tasks', () => {
    const t = task({
      id: '2',
      type: 'move',
      status: 'finished',
      meta: {
        items: [
          { src: '/root/a.txt', dst: '/dest/a.txt' },
          { src: '/root/b.txt', dst: '/dest/b.txt' }
        ]
      }
    });
    const paths = getAffectedPaths(t).sort();
    expect(paths).toEqual(['/dest', '/root']);
  });

  it('returns parent directory for delete tasks', () => {
    const t = task({
      id: '3',
      type: 'delete',
      status: 'finished',
      meta: { paths: ['/root/a.txt', '/root/b.txt'] }
    });
    expect(getAffectedPaths(t)).toEqual(['/root']);
  });

  it('returns parent directory for compress tasks', () => {
    const t = task({
      id: '4',
      type: 'compress',
      status: 'finished',
      meta: { format: 'zip', dest: '/root/archive.zip', source: 2 }
    });
    expect(getAffectedPaths(t)).toEqual(['/root']);
  });

  it('returns extract directory for decompress tasks', () => {
    const t = task({
      id: '5',
      type: 'decompress',
      status: 'finished',
      meta: { format: 'zip', archive: '/root/archive.zip', extractDir: '/root/extracted', into: 'extracted' }
    });
    expect(getAffectedPaths(t)).toEqual(['/root/extracted']);
  });

  it('returns destination directory for upload tasks', () => {
    const t = task({
      id: '6',
      type: 'upload',
      status: 'finished',
      meta: { staging: 'staging', dest: '/root/uploads', phase: 'finalizing', files: 3 }
    });
    expect(getAffectedPaths(t)).toEqual(['/root/uploads']);
  });

  it('returns empty array when no meta is present', () => {
    const t = task({ id: '7', type: 'copy', status: 'finished' });
    expect(getAffectedPaths(t)).toEqual([]);
  });
});

describe('upload session → taskbar task', () => {
  function ingest(overrides: Partial<IngestState> = {}): IngestState {
    return {
      taskID: 'sess-1',
      dest: '/root/out',
      chunkSize: 1,
      files: [{ rel: 'a', size: 4, chunks: 4 }],
      dirs: [],
      received: [new Uint8Array(4)],
      present: [true, true, false, false],
      phase: 'staging',
      error: null,
      finalizeID: null,
      bytesSent: 2,
      startTime: Date.now() - 1000,
      lastNotifyTime: Date.now(),
      lastBytesSent: 0,
      ...overrides
    };
  }

  it('shows a staging session as a running upload with progress', () => {
    const tasks = ingestStateToTasks(ingest({ phase: 'staging' }));
    expect(tasks).toHaveLength(1);
    expect(tasks[0].type).toBe('upload');
    expect(tasks[0].status).toBe('running');
    expect(tasks[0].progress).toBe(50);
    // The task carries the staging session id so cancel/resume target it.
    expect((tasks[0].meta as { staging: string }).staging).toBe('sess-1');
  });

  it('keeps a cancelled session visible so it can be resumed', () => {
    expect(uploadSessionVisible(ingest({ phase: 'cancelled' }))).toBe(true);
    const tasks = ingestStateToTasks(ingest({ phase: 'cancelled' }));
    expect(tasks).toHaveLength(1);
    expect(tasks[0].status).toBe('cancelled');
    // Resume targets the session by the staging id carried in meta.
    expect((tasks[0].meta as { staging: string }).staging).toBe('sess-1');
  });

  it('keeps a failed session visible so it can be resumed', () => {
    expect(uploadSessionVisible(ingest({ phase: 'failed' }))).toBe(true);
    const tasks = ingestStateToTasks(ingest({ phase: 'failed', error: 'boom' }));
    expect(tasks).toHaveLength(1);
    expect(tasks[0].status).toBe('failed');
    expect(tasks[0].error).toBe('boom');
  });

  it('does not show a finished session', () => {
    expect(uploadSessionVisible(ingest({ phase: 'done' }))).toBe(false);
    expect(ingestStateToTasks(ingest({ phase: 'done' }))).toHaveLength(0);
  });

  it('hands off from staging to the finalize task and shows no local finalizing task', () => {
    // Staging is done: the tracked finalize task in the SSE stream owns the
    // taskbar entry from here on. Keeping a local "finalizing" task would leave
    // a duplicate/stale running task behind if the finalize copy failed.
    expect(uploadSessionVisible(ingest({ phase: 'finalizing' }))).toBe(false);
    expect(ingestStateToTasks(ingest({ phase: 'finalizing' }))).toHaveLength(0);
  });
});

describe('clearCompletedTasks', () => {
  const mockLocalStorage = {
    store: {} as Record<string, string>,
    getItem(key: string) {
      return this.store[key] ?? null;
    },
    setItem(key: string, value: string) {
      this.store[key] = value;
    },
    removeItem(key: string) {
      delete this.store[key];
    },
    clear() {
      this.store = {};
    },
    get length() {
      return Object.keys(this.store).length;
    },
    key(i: number) {
      return Object.keys(this.store)[i] ?? null;
    }
  };

  beforeEach(() => {
    vi.stubGlobal('localStorage', mockLocalStorage);
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ({ ok: true, json: async () => ({ cleared: [] }) }))
    );
    mockLocalStorage.clear();
    uploadManager.sessions.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('removes finished and cancelled tasks, keeps running and queued', async () => {
    const now = new Date().toISOString();
    const tasks: Record<string, Task> = {
      'task-1': { id: 'task-1', type: 'copy', status: 'running', progress: 50, created: now, updated: now, bytes: { total: 100, done: 50, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
      'task-2': { id: 'task-2', type: 'delete', status: 'finished', progress: 100, created: now, updated: now, bytes: { total: 100, done: 100, filesTotal: 1, filesDone: 1, ratePerSec: 0 } },
      'task-3': { id: 'task-3', type: 'move', status: 'cancelled', progress: 0, created: now, updated: now, bytes: { total: 100, done: 0, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
      'task-4': { id: 'task-4', type: 'compress', status: 'queued', progress: 0, created: now, updated: now, bytes: { total: 100, done: 0, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
      'task-5': { id: 'task-5', type: 'decompress', status: 'failed', progress: 0, created: now, updated: now, bytes: { total: 100, done: 0, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
    };

    tasksMap.set(tasks);

    clearCompletedTasks();

    const result = get(tasksMap);
    expect(Object.keys(result)).toEqual(['task-1', 'task-4']);
    expect(result['task-1'].status).toBe('running');
    expect(result['task-4'].status).toBe('queued');
  });

  it('deletes cleared tasks on the server so a refresh does not restore them', async () => {
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ cleared: ['task-2'] })
    } as Response);
    const now = new Date().toISOString();
    tasksMap.set({
      'task-2': {
        id: 'task-2',
        type: 'upload',
        status: 'finished',
        progress: 100,
        created: now,
        updated: now,
        bytes: { total: 100, done: 100, filesTotal: 1, filesDone: 1, ratePerSec: 0 }
      }
    });

    clearCompletedTasks();

    await vi.waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        '/api/tasks/clear',
        expect.objectContaining({ method: 'POST' })
      )
    );
    expect(Object.keys(get(tasksMap))).toEqual([]);
    // The cleared id is remembered locally too, so an immediate refresh (before
    // the server delete finishes) cannot show it again.
    expect(
      JSON.parse(mockLocalStorage.getItem('filetoaster-cleared-task-ids') ?? '[]')
    ).toContain('task-2');
  });

  it('clears localStorage for finished upload tasks', async () => {
    const now = new Date().toISOString();
    const tasks: Record<string, Task> = {
      'staging-upload-1': { id: 'staging-upload-1', type: 'upload', status: 'finished', progress: 100, created: now, updated: now, bytes: { total: 100, done: 100, filesTotal: 1, filesDone: 1, ratePerSec: 0 }, meta: { staging: 'upload-1', dest: '/dest', phase: 'finalizing', files: 1 } },
    };

    mockLocalStorage.setItem('filetoaster-upload:upload-1', '{"data": "test"}');

    tasksMap.set(tasks);

    clearCompletedTasks();

    expect(mockLocalStorage.getItem('filetoaster-upload:upload-1')).toBeNull();
  });

  it('clears localStorage for cancelled upload tasks', async () => {
    const now = new Date().toISOString();
    const tasks: Record<string, Task> = {
      'staging-upload-2': { id: 'staging-upload-2', type: 'upload', status: 'cancelled', progress: 50, created: now, updated: now, bytes: { total: 100, done: 50, filesTotal: 1, filesDone: 0, ratePerSec: 0 }, meta: { staging: 'upload-2', dest: '/dest', phase: 'staging', files: 1 } },
    };

    mockLocalStorage.setItem('filetoaster-upload:upload-2', '{"data": "test"}');

    tasksMap.set(tasks);

    clearCompletedTasks();

    expect(mockLocalStorage.getItem('filetoaster-upload:upload-2')).toBeNull();
  });

  it('clears uploadManager sessions in done/failed/cancelled phase', async () => {
    const state: IngestState = {
      taskID: 'upload-3',
      dest: '/dest',
      chunkSize: 1,
      files: [{ rel: 'a', size: 4, chunks: 4 }],
      dirs: [],
      received: [new Uint8Array(4)],
      present: [true, true, false, false],
      phase: 'cancelled',
      error: null,
      finalizeID: null,
      bytesSent: 2,
      startTime: Date.now() - 1000,
      lastNotifyTime: Date.now(),
      lastBytesSent: 0,
    };
    uploadManager.sessions.set('upload-3', state);
    mockLocalStorage.setItem('filetoaster-upload:upload-3', '{"data": "test"}');

    clearCompletedTasks();

    expect(uploadManager.sessions.has('upload-3')).toBe(false);
    expect(mockLocalStorage.getItem('filetoaster-upload:upload-3')).toBeNull();
  });

  it('keeps only staging uploadManager sessions and clears finalizing ones', async () => {
    const base: IngestState = {
      taskID: 'upload-4',
      dest: '/dest',
      chunkSize: 1,
      files: [{ rel: 'a', size: 4, chunks: 4 }],
      dirs: [],
      received: [new Uint8Array(4)],
      present: [true, true, false, false],
      phase: 'staging',
      error: null,
      finalizeID: null,
      bytesSent: 2,
      startTime: Date.now() - 1000,
      lastNotifyTime: Date.now(),
      lastBytesSent: 0,
    };
    uploadManager.sessions.set('upload-4', base);
    const finalizing: IngestState = {
      ...base,
      taskID: 'upload-5',
      phase: 'finalizing',
      finalizeID: 'task-finalize-5',
      present: [true, true, true, true]
    };
    uploadManager.sessions.set('upload-5', finalizing);
    mockLocalStorage.setItem('filetoaster-upload:upload-5', '{"data": "test"}');

    clearCompletedTasks();

    // An actively staging upload is running and survives; a finalizing session
    // has handed its work to the tracked finalize task and is not resumable.
    expect(uploadManager.sessions.has('upload-4')).toBe(true);
    expect(uploadManager.sessions.has('upload-5')).toBe(false);
    expect(mockLocalStorage.getItem('filetoaster-upload:upload-5')).toBeNull();
  });

  it('clears failed upload finalize tasks and their persisted state', async () => {
    const now = new Date().toISOString();
    const state: IngestState = {
      taskID: 'upload-6',
      dest: '/dest',
      chunkSize: 1,
      files: [{ rel: 'a', size: 4, chunks: 4 }],
      dirs: [],
      received: [new Uint8Array(4)],
      present: [true, true, true, true],
      phase: 'finalizing',
      error: null,
      finalizeID: 'task-finalize-6',
      bytesSent: 4,
      startTime: Date.now() - 1000,
      lastNotifyTime: Date.now(),
      lastBytesSent: 0,
    };
    uploadManager.sessions.set('upload-6', state);
    mockLocalStorage.setItem('filetoaster-upload:upload-6', '{"data": "test"}');
    tasksMap.set({
      'task-finalize-6': {
        id: 'task-finalize-6',
        type: 'upload',
        status: 'failed',
        progress: 40,
        created: now,
        updated: now,
        bytes: { total: 100, done: 40, filesTotal: 1, filesDone: 0, ratePerSec: 0 },
        meta: { staging: '/tmp/filetoaster-upload-6', dest: '/dest', phase: 'finalizing', files: 1 }
      }
    });

    clearCompletedTasks();

    expect(Object.keys(get(tasksMap))).toEqual([]);
    expect(uploadManager.sessions.has('upload-6')).toBe(false);
    expect(mockLocalStorage.getItem('filetoaster-upload:upload-6')).toBeNull();
  });

  it('removes cleared task IDs from localStorage after server confirms deletion (5s grace)', async () => {
    vi.useFakeTimers();
    
    const fetchMock = vi.mocked(fetch);
    fetchMock.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ cleared: ['task-A', 'task-B'] })
    } as Response);
    const now = new Date().toISOString();
    tasksMap.set({
      'task-1': { id: 'task-1', type: 'copy', status: 'running', progress: 50, created: now, updated: now, bytes: { total: 100, done: 50, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
      'task-A': { id: 'task-A', type: 'delete', status: 'finished', progress: 100, created: now, updated: now, bytes: { total: 100, done: 100, filesTotal: 1, filesDone: 1, ratePerSec: 0 } },
      'task-B': { id: 'task-B', type: 'move', status: 'cancelled', progress: 0, created: now, updated: now, bytes: { total: 100, done: 0, filesTotal: 1, filesDone: 0, ratePerSec: 0 } },
    });

    clearCompletedTasks();

    // Wait for the fetch promise to resolve
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());

    // IDs are immediately added to localStorage (local clear)
    expect(
      JSON.parse(mockLocalStorage.getItem('filetoaster-cleared-task-ids') ?? '[]')
    ).toEqual(expect.arrayContaining(['task-A', 'task-B']));

    // Before grace period: IDs still in localStorage
    vi.advanceTimersByTime(4000);
    expect(
      JSON.parse(mockLocalStorage.getItem('filetoaster-cleared-task-ids') ?? '[]')
    ).toEqual(expect.arrayContaining(['task-A', 'task-B']));

    // After grace period (5s): only the server-returned IDs are removed
    vi.advanceTimersByTime(2000); // total 6s
    // Flush any pending microtasks
    await vi.runAllTimersAsync();
    const clearedIds = JSON.parse(mockLocalStorage.getItem('filetoaster-cleared-task-ids') ?? '[]');
    // The specific IDs from server response should be gone
    expect(clearedIds).not.toContain('task-A');
    expect(clearedIds).not.toContain('task-B');

    vi.useRealTimers();
  });
});
