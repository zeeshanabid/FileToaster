import { describe, it, expect } from 'vitest';
import { parseSSEMessage, taskLabel, getAffectedPaths, ingestStateToTasks, uploadSessionVisible } from '$lib/store/tasks';
import type { Task } from '$lib/fs/api';
import type { IngestState } from '$lib/fs/upload';

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
});
