import { browser } from '$app/environment';
import { writable, derived, type Readable } from 'svelte/store';
import {
  cancelTask as apiCancel,
  clearServerTasks as apiClearServerTasks,
  listTasks,
  submitCopy,
  submitDelete,
  submitMove,
  submitCompress,
  submitDecompress,
  submitTruncate,
  type Task,
  type CopyMeta,
  type DeleteMeta,
  type MoveMeta,
  type CompressMeta,
  type DecompressMeta,
  type UploadMeta,
  type SearchMeta,
  type SearchResultItem,
  type TruncateResponse
} from '$lib/fs/api';
import { archiveStem } from '$lib/fs/archive';
import { uploadStoreKey, uploadManager, type IngestState, globalChunkSpan } from '$lib/fs/upload';
import { reloadWindowsForPath } from '$lib/store/windows';
import { showTaskToast, taskNotification } from '$lib/store/notifications';
import { appendSearchResult, completeSearch } from '$lib/store/search';

// The task engine. `tasksMap` is the source of truth (id → task), kept current
// by an initial snapshot from /api/tasks and then by deltas over the SSE stream
// at /api/events. The derived stores expose the data the UI needs: the full list
// (oldest first) and the running / queued counts used by the taskbar badge.
const tasksMap = writable<Record<string, Task>>({});

// Ids removed by "Clear completed tasks". They are ignored if a delta or a late
// snapshot arrives before the server-side clear lands, so a task the user just
// cleared cannot flicker back into the panel during the same session. They are
// also persisted, so an immediate refresh (before the delete request completes)
// still hides them. The server deletes the terminal records too, which keeps
// them gone for every client after a reload.
const CLEARED_TASKS_KEY = 'filetoaster-cleared-task-ids';
const clearedTaskIDs = new Set<string>();

function loadClearedTaskIDs(): void {
  if (!browser) return;
  try {
    const raw = localStorage.getItem(CLEARED_TASKS_KEY);
    if (!raw) return;
    const ids = JSON.parse(raw);
    if (Array.isArray(ids)) {
      for (const id of ids) if (typeof id === 'string') clearedTaskIDs.add(id);
    }
  } catch {
    // ignore
  }
}

function persistClearedTaskIDs(): void {
  if (!browser) return;
  try {
    localStorage.setItem(CLEARED_TASKS_KEY, JSON.stringify([...clearedTaskIDs]));
  } catch {
    // ignore
  }
}

// Export for testing
export { tasksMap, taskNotification, showTaskToast };

export type { Task } from '$lib/fs/api';

function toList(map: Record<string, Task>): Task[] {
  return Object.values(map).sort((a, b) => Date.parse(a.created) - Date.parse(b.created));
}

export const taskList: Readable<Task[]> = derived(tasksMap, ($map, set) => set(toList($map)));

export const runningCount: Readable<number> = derived(taskList, ($list, set) =>
  set($list.filter((t) => t.status === 'running').length)
);
export const queuedCount: Readable<number> = derived(taskList, ($list, set) =>
  set($list.filter((t) => t.status === 'queued').length)
);
export const activeCount: Readable<number> = derived(taskList, ($list, set) =>
  set($list.filter((t) => t.status === 'running' || t.status === 'queued').length)
);
export const finishedCount: Readable<number> = derived(taskList, ($list, set) =>
  set($list.filter((t) => t.status === 'finished').length)
);
export const failedCount: Readable<number> = derived(taskList, ($list, set) =>
  set($list.filter((t) => t.status === 'failed').length)
);

// Convert an upload staging session to Task-like objects for display in the
// taskbar. A finalizing session produces no local task: the tracked server-side
// finalize task replaces the staging entry once staging completes.
export function ingestStateToTasks(state: IngestState): Task[] {
  const totalChunks = state.present.length;
  const doneChunks = state.present.filter((p) => p).length;
  const progress = totalChunks > 0 ? Math.round((doneChunks / totalChunks) * 100) : 0;
  
  // Calculate total bytes and done bytes
  const totalBytes = state.files.reduce((sum, f) => sum + f.size, 0);
  const doneBytes = state.files.reduce((sum, f, idx) => {
    const span = globalChunkSpan(state.files, idx);
    const fileDoneChunks = state.present.slice(span.start, span.end).filter((p) => p).length;
    const fileTotalChunks = span.end - span.start;
    return sum + Math.round((fileDoneChunks / fileTotalChunks) * f.size);
  }, 0);
  
  // Calculate files done/total (a file is "done" when all its chunks are present)
  const filesTotal = state.files.length;
  const filesDone = state.files.filter((_, idx) => {
    const span = globalChunkSpan(state.files, idx);
    return state.present.slice(span.start, span.end).every((p) => p);
  }).length;
  
  // Calculate upload speed (bytes per second)
  const elapsedSec = (Date.now() - state.startTime) / 1000;
  const ratePerSec = elapsedSec > 0 ? Math.round(state.bytesSent / elapsedSec) : 0;
  
  const tasks: Task[] = [];
  const baseCreated = new Date(state.startTime).toISOString();
  const now = new Date().toISOString();
  
  if (state.phase === 'staging') {
    // Staging task - shows upload progress with speed
    tasks.push({
      id: `staging-${state.taskID}`,
      type: 'upload',
      status: 'running',
      progress,
      created: baseCreated,
      updated: now,
      error: state.error ?? undefined,
      bytes: {
        total: totalBytes,
        done: doneBytes,
        filesTotal,
        filesDone,
        ratePerSec
      },
      meta: {
        staging: state.taskID,
        dest: state.dest,
        phase: 'staging',
        files: state.files.length
      } as UploadMeta
    });
  } else if (state.phase === 'cancelled' || state.phase === 'failed') {
    // A terminal-but-resumable session stays in the taskbar so the user can
    // resume it (the RunningTaskIcon shows a Resume button for these).
    tasks.push({
      id: `staging-${state.taskID}`,
      type: 'upload',
      status: state.phase === 'cancelled' ? 'cancelled' : 'failed',
      progress,
      created: baseCreated,
      updated: now,
      error: state.error ?? undefined,
      bytes: {
        total: totalBytes,
        done: doneBytes,
        filesTotal,
        filesDone,
        ratePerSec: 0
      },
      meta: {
        staging: state.taskID,
        dest: state.dest,
        phase: state.phase,
        files: state.files.length
      } as UploadMeta
    });
  }
  // A finalizing session has finished staging: it is intentionally not shown as
  // a second, local task. The server-side finalize task replaces it via SSE (and
  // reports its own failure/finish), so no stale "running" staging task is left
  // behind when the finalize copy fails.
  return tasks;
}

// uploadSessionVisible reports whether a session should be shown in the taskbar.
// Terminal-but-resumable sessions (cancelled/failed) stay visible so they can be
// resumed; a session that finished or was cleared is gone from the map entirely.
// A finalizing session is hidden because the tracked finalize task owns the
// display from that point on.
export function uploadSessionVisible(state: IngestState): boolean {
  return state.phase === 'staging' || state.phase === 'cancelled' || state.phase === 'failed';
}

// Reactive store for upload staging sessions (converted to Task-like objects)
function createUploadStagingTasksStore(): Readable<Task[]> {
  const { subscribe, set } = writable<Task[]>([]);
  
  const unsubscribe = uploadManager.subscribe(() => {
    const allTasks: Task[] = [];
    uploadManager.sessions.forEach((state) => {
      if (uploadSessionVisible(state)) {
        allTasks.push(...ingestStateToTasks(state));
      }
    });
    set(allTasks);
  });
  
  // Initial population
  const allTasks: Task[] = [];
  uploadManager.sessions.forEach((state) => {
    if (uploadSessionVisible(state)) {
      allTasks.push(...ingestStateToTasks(state));
    }
  });
  set(allTasks);
  
  return {
    subscribe,
    // Cleanup on last unsubscribe (though in practice this lives for the app lifetime)
  };
}

export const uploadStagingTasks: Readable<Task[]> = createUploadStagingTasksStore();

// Auto-open taskbar panel when new tasks start
export const taskPanelOpen = writable(false);

// Combined task list: SSE tasks + upload staging tasks
export const combinedTaskList: Readable<Task[]> = derived(
  [taskList, uploadStagingTasks],
  ([$taskList, $uploadStagingTasks]) => {
    // Merge, with upload staging tasks appearing first (they're more immediate)
    const merged = [...$uploadStagingTasks, ...$taskList];
    // Sort by creation time (oldest first)
    return merged.sort((a, b) => Date.parse(a.created) - Date.parse(b.created));
  }
);

// Re-export combined counts
export const combinedRunningCount: Readable<number> = derived(combinedTaskList, ($list, set) =>
  set($list.filter((t) => t.status === 'running').length)
);
export const combinedQueuedCount: Readable<number> = derived(combinedTaskList, ($list, set) =>
  set($list.filter((t) => t.status === 'queued').length)
);
export const combinedActiveCount: Readable<number> = derived(combinedTaskList, ($list, set) =>
  set($list.filter((t) => t.status === 'running' || t.status === 'queued').length)
);

// `clipboard` holds the paths the user has copied so a paste can reference them.
export const clipboard = writable<string[]>([]);
export function setClipboard(paths: string[]) {
  // A fresh copy resets any prior cut state, so the next paste is a copy.
  clipboard.set(paths);
  cutMode.set(false);
}
export function clearClipboard() {
  clipboard.set([]);
  cutMode.set(false);
  cutEntries.set([]);
}

// `cutMode` + `cutEntries` track a cut (move) selection: cut stores the entries
// in the clipboard too, but paste branches to a move when cutMode is set.
export const cutMode = writable<boolean>(false);
export const cutEntries = writable<Array<{ path: string; name: string; type: 'file' | 'dir' }>>([]);
export function setCut(entries: Array<{ path: string; name: string; type: 'file' | 'dir' }>) {
  clipboard.set(entries.map((e) => e.path));
  cutEntries.set(entries);
  cutMode.set(true);
}

// submitCopyTask enqueues a copy of `paths` into `dest`. Destination collisions
// are resolved server-side (the target is renamed, e.g. " (copy)", " (copy) 1",
// …), so no overwrite/cancel decision is passed.
export function submitCopyTask(paths: string[], dest: string) {
  return submitCopy({ paths, dest, conflict: '' });
}

// submitDeleteTask enqueues a delete task for `paths`. Delete is destructive and
// confirmed by the UI before this runs, so nothing is asked to be overwritten.
export function submitDeleteTask(paths: string[]) {
  return submitDelete(paths);
}

// submitMoveTask enqueues a move of `paths` into `dest`. Move overwrites any
// same-named item at the destination, so no overwrite/cancel decision is passed.
export function submitMoveTask(paths: string[], dest: string) {
  return submitMove({ paths, dest, conflict: '' });
}

  // submitCompressTask enqueues a compress task for `paths` into `format`
  // (zip, targz, tar). The archive is written in place and a colliding target is
  // renamed server-side, so no overwrite decision is passed.
  export function submitCompressTask(paths: string[], format: string, compressionLevel?: number) {
    return submitCompress({ paths, format, compressionLevel });
  }

// submitDecompressTask enqueues a decompress task for the single archive at
  // `paths` into `into` ("", extract here, or an archive stem, extract into a
  // sibling folder). An existing destination folder is renamed server-side.
  // windowPath is the current window's path for reload after extraction.
  export function submitDecompressTask(paths: string[], into: string, windowPath: string) {
    return submitDecompress({ paths, into, windowPath });
  }

  // submitTruncateTask enqueues a truncate task for `paths` to the target `size` in bytes.
  // Truncate only works on files (not directories).
  export function submitTruncateTask(paths: string[], size: number) {
    return submitTruncate({ paths, size });
  }

  // cancelTaskAction asks the server to stop a queued or running task.
  export function cancelTaskAction(id: string) {
  return apiCancel(id);
}

// clearCompletedTasks removes every task that is not actively running or
// queued: finished, failed and cancelled tasks (including upload finalize tasks)
// and any resumable/paused upload session, together with its persisted local
// state. Only running and queued work survives a clear.
export function clearCompletedTasks() {
  loadClearedTaskIDs();
  // Clear SSE tasks (everything not still running/queued) and remember their ids
  // so a late SSE delta cannot put them back.
  const removedIDs: string[] = [];
  tasksMap.update((map) => {
    const newMap: Record<string, Task> = {};
    for (const [id, task] of Object.entries(map)) {
      if (task.status === 'running' || task.status === 'queued') {
        newMap[id] = task;
      } else {
        removedIDs.push(id);
        if (task.type === 'upload') {
          // Match the terminal upload task back to its local staging session so
          // its localStorage bookmark is cleared too (clearByFinalizeID also
          // discards the session and its persisted file bytes).
          clearTerminalUploadSession(task);
        }
      }
    }
    return newMap;
  });
  for (const id of removedIDs) clearedTaskIDs.add(id);
  persistClearedTaskIDs();

  // Clear the local upload sessions too. Everything except an actively staging
  // upload is terminal from the taskbar's point of view: a finalizing session's
  // staging already finished (the server finalize task, if still running, is in
  // tasksMap and is preserved above), and cancelled/failed sessions are paused.
  for (const [taskID, state] of [...uploadManager.sessions]) {
    if (state.phase === 'staging') continue;
    try {
      localStorage.removeItem(uploadStoreKey(taskID));
    } catch {
      // ignore
    }
    uploadManager.clear(taskID);
  }

// Delete the terminal tasks server-side as well. The task list is rebuilt from
	// the server snapshot on every page load, so without this the cleared tasks
	// would reappear after a refresh. Running/queued tasks are left alone and any
	// that finish later surface normally.
	void apiClearServerTasks()
		.then((ids) => {
			// Server has confirmed deletion of these tasks. They are permanently
			// gone and won't reappear in future snapshots/SSE. Remove them from
			// localStorage to prevent unbounded growth. Keep a grace period
			// in case any late SSE deltas are still in flight.
			setTimeout(() => {
				for (const id of ids) clearedTaskIDs.delete(id);
				persistClearedTaskIDs();
			}, 5000);
		})
		.catch(() => {
			// Offline/transient: the local panel is still cleared; a later refresh may
			// restore server tasks until the clear succeeds.
		});
}

// taskLabel derives a one-line description for a task record, reading its meta
// when the operation carries one (currently copy).
export function taskLabel(task: Task): string {
  switch (task.type) {
    case 'copy': {
      const meta = (task.meta ?? null) as CopyMeta | null;
      const n = Array.isArray(meta?.items) ? meta.items.length : 0;
      const dest = meta?.items[meta.items.length - 1]?.dst ?? '';
      const name = dest ? dest.split('/').filter(Boolean).pop() ?? dest : '';
      if (n === 1) return `Copy ${name || 'file'}`;
      return n > 1 ? `Copy ${n} items` : 'Copy';
    }
    case 'delete': {
      const meta = (task.meta ?? null) as DeleteMeta | null;
      const n = meta?.paths?.length ?? 1;
      if (n === 1) return 'Delete';
      return `Delete ${n} items`;
    }
    case 'move': {
      const meta = (task.meta ?? null) as MoveMeta | null;
      const n = meta?.items?.length ?? 1;
      if (n === 1) return 'Move';
      return `Move ${n} items`;
    }
    case 'compress': {
      const meta = (task.meta ?? null) as CompressMeta | null;
      const n = meta?.source ?? 1;
      return n === 1 ? 'Compress' : `Compress ${n} items`;
    }
case 'decompress': {
      const meta = (task.meta ?? null) as DecompressMeta | null;
      // The label shows the archive's stem ("myfiles.zip" -> "myfiles").
      const raw = meta?.archive ?? '';
      const stem = archiveStem(raw.split('/').filter(Boolean).pop() ?? raw);
      return `Extract ${stem || 'archive'}`;
    }
    case 'truncate': {
      const meta = (task.meta ?? null) as TruncateMeta | null;
      const n = meta?.items?.length ?? 1;
      return n === 1 ? 'Truncate' : `Truncate ${n} files`;
    }
    case 'search': {
      const meta = (task.meta ?? null) as SearchMeta | null;
      const query = meta?.query?.trim();
      return query ? `Search "${query}"` : 'Search';
    }
    case 'upload': {
      // The finalize op's meta carries the phase flag ("finalizing", set when
      // the tracked copy begins) and the item count. During staging there is
      // no task yet, so an "upload" task always means copy is in progress; a
      // missing phase still falls back to a generic upload label.
      const meta = (task.meta ?? null) as UploadMeta | null;
      const n = meta?.files ?? 1;
      if (meta?.phase === 'finalizing') {
        return n === 1 ? 'Finalizing upload' : `Finalizing upload of ${n} items`;
      }
      return n === 1 ? 'Upload' : `Upload ${n} items`;
    }
    default:
      return task.type;
  }
}

// --- SSE connection ----------------------------------------------------------
// A single long-lived stream delivers every task delta. It is started once the
// UI is authenticated and torn down on logout. The reader is manual (fetch
// streaming) rather than EventSource so cookies are sent by default and reconnect
// timing is fully controlled.
let started = false;
let controller: AbortController | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelay = 2000;

export function initTasks() {
  if (started || !browser) return;
  started = true;
  loadClearedTaskIDs();
  refresh();
  connect();
}

function refresh() {
  listTasks()
    .then((list) => {
      const map: Record<string, Task> = {};
      for (const t of list) {
        // Ignore a terminal record the user cleared locally while the
        // server-side delete was in flight.
        if (clearedTaskIDs.has(t.id)) continue;
        map[t.id] = t;
        // A snapshot (e.g. after a reconnect) can already contain a terminal
        // finalize task whose SSE delta was missed; clear the matching local
        // staging session so it cannot linger as a hidden/resumable upload.
        clearTerminalUploadSession(t);
      }
      tasksMap.set(map);
    })
    .catch(() => {
      // Transient: the reconnect loop will retry the snapshot on next refresh.
    });
}

// clearTerminalUploadSession drops the local upload session that belongs to a
// terminal finalize task. Staging is finished by the time that task exists and
// the server has discarded the upload session, so a failed/cancelled finalize
// must not leave the local staging task stuck in the task panel; keeping a
// stale localStorage handle would also make it look resumable after a refresh.
function clearTerminalUploadSession(task: Task): void {
  if (task.type !== 'upload') return;
  if (task.status !== 'finished' && task.status !== 'failed' && task.status !== 'cancelled') return;

  // Local upload tasks are displayed as "staging-<sessionID>" /
  // "finalizing-<sessionID>", so strip the display prefix to reach the session.
  const localSessionID = task.id.replace(/^(staging|finalizing)-/, '');
  if (localSessionID !== task.id) {
    try {
      localStorage.removeItem(uploadStoreKey(localSessionID));
    } catch {
      // ignore
    }
    uploadManager.clear(localSessionID);
  }

  // Clear the staging/finalizing local task by the finalize task ID. The
  // finalize id is what the upload manager recorded when the server handed the
  // staging dir over to the tracked copy op.
  uploadManager.clearByFinalizeID(task.id);

  // A server finalize task carries the staging directory path in meta.staging.
  // Its basename is "filetoaster-<sessionID>"; this also lets a task that
  // finished after a refresh (where the paused session was never loaded) clear
  // any legacy bookmark and IndexedDB bytes left under the session id.
  const meta = (task.meta ?? null) as UploadMeta | null;
  const staging = typeof meta?.staging === 'string' ? meta.staging.split('/').filter(Boolean).pop() : '';
  if (staging?.startsWith('filetoaster-')) {
    uploadManager.clear(staging.slice('filetoaster-'.length));
  }
}

function connect() {
  const ctrl = new AbortController();
  controller = ctrl;

  fetch('/api/events', { credentials: 'include', signal: ctrl.signal })
    .then(async (res) => {
      if (!res.ok || !res.body) {
        throw new Error('stream unavailable');
      }
      const reader = res.body.getReader();
      const decoder = new TextDecoder();
      let buffer = '';
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let sep: number;
        while ((sep = buffer.indexOf('\n\n')) !== -1) {
          handleMessage(buffer.slice(0, sep));
          buffer = buffer.slice(sep + 2);
        }
      }
      if (started) scheduleReconnect();
    })
    .catch((err) => {
      if (err?.name === 'AbortError') return;
      if (started) scheduleReconnect();
    });
}

function handleMessage(message: string) {
  const ev = parseSSEEvent(message);
  if (!ev) return;
  // Search results arrive as their own event type, scoped to a search task id by
  // an envelope; route them to the per-search store and stop.
  if (ev.event === 'search-result') {
    routeSearchResult(ev.data);
    return;
  }
  if (ev.event === 'search-complete') {
    routeSearchComplete(ev.data);
    return;
  }
  if (ev.event !== 'task') return;
  const task = parseTask(ev.data);
  if (!task) return;
  // A cleared terminal task must not be resurrected by a delta that was already
  // in flight when the user pressed Clear.
  if (clearedTaskIDs.has(task.id)) return;

  // Mark a search session terminal when its task reaches a terminal status. The
  // task delta is the reliable completion signal (it also covers cancellation
  // from the task panel), complementing the search-complete event.
  if (
    task.type === 'search' &&
    (task.status === 'finished' || task.status === 'failed' || task.status === 'cancelled')
  ) {
    completeSearch(task.id, task.status, task.error);
  }
  
  // Check if this is a new task (not already in the map)
  let isNewTask = false;
  let prevStatus: Task['status'] | undefined;
  tasksMap.update((map) => {
    prevStatus = map[task.id]?.status;
    isNewTask = !map[task.id];
    return { ...map, [task.id]: task };
  });
  
  // Show toast for new active tasks (instead of auto-opening panel)
  if (isNewTask && (task.status === 'running' || task.status === 'queued')) {
    const label = taskLabel(task);
    taskNotification.set({ taskId: task.id, label: `New task: ${label}` });
    // Auto-clear after 3 seconds
    setTimeout(() => {
      taskNotification.update((n) => (n?.taskId === task.id ? null : n));
    }, 3000);
  }
  
  // Show toast for finished tasks
  if (!isNewTask && prevStatus && prevStatus !== 'finished' && prevStatus !== 'failed' && prevStatus !== 'cancelled' &&
      (task.status === 'finished' || task.status === 'failed' || task.status === 'cancelled')) {
    const label = taskLabel(task);
    const statusText = task.status === 'finished' ? 'completed' : task.status;
    taskNotification.set({ taskId: `finished-${task.id}`, label: `${label} ${statusText}` });
    // Auto-clear after 4 seconds
    setTimeout(() => {
      taskNotification.update((n) => (n?.taskId === `finished-${task.id}` ? null : n));
    }, 4000);
  }
  
  // An upload that reached a terminal status no longer has a resumable server
  // session; drop its remembered local handle so a stale localStorage entry
  // never triggers a restart prompt later. This includes failed and cancelled
  // finalize tasks: staging finished before the finalize copy began, and the
  // server drops the upload session when it submits that op, so a failed
  // finalize must remove the local staging/finalizing task too instead of
  // leaving it stuck as "running" in the task panel.
  clearTerminalUploadSession(task);
  // When a task reaches a terminal state, reload any open windows showing
  // the affected directories so the UI reflects the changes.
  if (task.status === 'finished' || task.status === 'failed' || task.status === 'cancelled') {
    const affectedPaths = getAffectedPaths(task);
    for (const path of affectedPaths) {
      reloadWindowsForPath(path);
    }
  }
}

// Extract directory paths that were affected by a completed task.
// Returns the paths of directories whose listings should be refreshed.
function getAffectedPaths(task: Task): string[] {
  const meta = task.meta ?? null;
  const paths = new Set<string>();
  function dirname(p: string): string {
    const i = p.lastIndexOf('/');
    return i <= 0 ? '/' : p.slice(0, i);
  }
  function addDir(p: string) {
    if (p) paths.add(p);
  }
  switch (task.type) {
    case 'copy': {
      const m = meta as CopyMeta | null;
      if (m?.items) {
        for (const item of m.items) {
          if (item.dst) addDir(dirname(item.dst));
        }
      }
      break;
    }
    case 'move': {
      const m = meta as MoveMeta | null;
      if (m?.items) {
        for (const item of m.items) {
          if (item.src) addDir(dirname(item.src));
          if (item.dst) addDir(dirname(item.dst));
        }
      }
      break;
    }
    case 'delete': {
      const m = meta as DeleteMeta | null;
      if (m?.paths) {
        for (const p of m.paths) addDir(dirname(p));
      }
      break;
    }
    case 'compress': {
      const m = meta as CompressMeta | null;
      if (m?.dest) addDir(dirname(m.dest));
      break;
    }
    case 'decompress': {
      const m = meta as DecompressMeta | null;
      // Use windowPath (the directory the user was viewing when they started extraction)
      // so the window refreshes to show the extracted files in context.
      // Fall back to extractDir/into for backward compatibility with old tasks.
      if (m?.windowPath) addDir(m.windowPath);
      else if (m?.extractDir) addDir(m.extractDir);
      else if (m?.into) addDir(m.into);
      break;
    }
    case 'upload': {
      const m = meta as UploadMeta | null;
      if (m?.dest) addDir(m.dest);
      break;
    }
    case 'truncate': {
      const m = meta as TruncateMeta | null;
      if (m?.items) {
        for (const item of m.items) {
          if (item.src) addDir(dirname(item.src));
        }
      }
      break;
    }
  }
  return Array.from(paths);
}

// TruncateMeta is the truncate operation's plan, carried in a task's Meta field.
interface TruncateMeta {
  items: Array<{ src: string; size: number }>;
}

// Export getAffectedPaths so it can be unit-tested independently.
export { getAffectedPaths };

// parseSSEEvent decodes one raw SSE message ("event: <name>\ndata: <payload>")
// into its event name and data payload. It never fails: an unknown framing
// yields the default "message" event with whatever data was present.
export function parseSSEEvent(message: string): { event: string; data: string } {
  let event = 'message';
  let data = '';
  for (const line of message.split('\n')) {
    if (line.startsWith('event:')) {
      event = line.slice(6).trim();
    } else if (line.startsWith('data:')) {
      data = line.slice(5).trimStart();
    }
  }
  return { event, data };
}

// parseTask decodes a task event's JSON payload, returning null on any failure.
function parseTask(data: string): Task | null {
  if (!data) return null;
  try {
    return JSON.parse(data) as Task;
  } catch {
    return null;
  }
}

// routeSearchResult routes a "search-result" event to its search session. The
// event data is an envelope ({ taskID, data }) so the client knows which search
// the match belongs to.
function routeSearchResult(data: string): void {
  if (!data) return;
  try {
    const env = JSON.parse(data) as { taskID?: string; data?: SearchResultItem };
    if (env?.taskID && env.data) appendSearchResult(env.taskID, env.data);
  } catch {
    // ignore a malformed event
  }
}

// routeSearchComplete marks a search session finished from the search-complete
// event, refreshing its scanned/matches totals.
function routeSearchComplete(data: string): void {
  if (!data) return;
  try {
    const env = JSON.parse(data) as {
      taskID?: string;
      data?: { scanned?: number; matches?: number };
    };
    if (env?.taskID) completeSearch(env.taskID, 'finished', undefined, env.data);
  } catch {
    // ignore a malformed event
  }
}

// parseSSEMessage decodes one raw SSE message ("event: task\ndata: {...}") into
// a Task, or returns null for anything that is not a task event with valid JSON.
export function parseSSEMessage(message: string): Task | null {
  const { event, data } = parseSSEEvent(message);
  if (event !== 'task') return null;
  return parseTask(data);
}

function scheduleReconnect() {
  if (reconnectTimer !== null || !started) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    reconnectDelay = Math.min(30000, reconnectDelay * 1.5);
    refresh();
    connect();
  }, reconnectDelay);
}

export function shutdownTasks() {
  started = false;
  if (controller) {
    controller.abort();
    controller = null;
  }
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  reconnectDelay = 2000;
  tasksMap.set({});
}
