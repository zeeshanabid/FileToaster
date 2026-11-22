// UploadManager implements the client half of the chunked + resumable upload
// flow. A session is created on the server (POST /api/uploads); each selected
// file is sliced into fixed-size chunks and PUT per chunk; the server reports,
// per session, which chunks are present so a client can resume by re-sending
// only the missing ones; when every chunk of every file has arrived the server
// auto-enqueues a tracked finalize op (type "upload"). Ingestion does not hold a
// worker slot; only the finalize copy does.
//
// Sessions survive a page refresh via localStorage (task id, manifest, and the
// per-file present-chunk bitmap) plus IndexedDB (the live file bytes, so the
// chunks the server is still missing can be re-sliced after the browser drops
// its File objects); they do NOT survive a server restart (the in-memory server
// session is gone → the client is told to restart). Manifest building and the
// chunk math are pure functions at the top of this file so they can be
// unit-tested without a browser.

import type {
  CreateUploadRequest,
  UploadFile,
  UploadManifest,
  UploadStatus,
  UploadChunkResponse
} from '$lib/fs/api';
import {
  createUpload as apiCreateUpload,
  uploadChunk as apiUploadChunk,
  getUploadStatus as apiGetUploadStatus,
  cancelUpload as apiCancelUpload,
  resumeUpload as apiResumeUpload,
  chunkCountForFile,
  DEFAULT_CHUNK_SIZE
} from '$lib/fs/api';
import { showTaskToast } from '$lib/store/notifications';
import { basename, relativeUnder } from '$lib/fs/paths';

// UploadApi abstracts the network calls so the manager can be exercised in tests
// with an in-memory fake that never touches the server.
export interface UploadApi {
  createUpload(req: CreateUploadRequest): Promise<{ taskID?: string }>;
  uploadChunk(taskID: string, fileIdx: number, chunkIdx: number, blob: Blob): Promise<UploadChunkResponse>;
  getUploadStatus(taskID: string): Promise<UploadStatus>;
  cancelUpload(taskID: string): Promise<void>;
  resumeUpload(taskID: string): Promise<UploadStatus>;
}

// ManifestEntry is what the window passes for a selected row: a path with its
// name, type and size.
export interface ManifestEntry {
  path: string;
  name: string;
  type: 'file' | 'dir';
  size: number;
}

// ChildLoader returns the cached entries (files and directories) of a directory,
// or undefined when the directory's listing is not available yet.
export type ChildLoader = (path: string) => ReadonlyArray<ManifestEntry> | undefined;

// DnDItem is one entry from a DataTransfer/file list. webkitRelativePath is
// present for items inside a dropped folder; isFile distinguishes a file from a
// folder entry (folders are enumerated by their members in the browser). blob
// carries the live file content when the item comes from a real File.
export interface DnDItem {
  name: string;
  size: number;
  webRelativePath: string;
  isFile: boolean;
  blob?: Blob;
}

// UPLOAD_STORAGE_PREFIX namespaces upload handles in localStorage.
export const UPLOAD_STORAGE_PREFIX = 'filetoaster-upload:';

// uploadStoreKey returns the localStorage key for a session (keyed by task id).
export function uploadStoreKey(taskID: string): string {
  return UPLOAD_STORAGE_PREFIX + taskID;
}

// UploadBlobStore persists the live file bytes of an upload session. localStorage
// can only hold the resumable manifest (paths, sizes, present chunk bitmap), not
// the file contents, and a browser refresh drops the selected File/Blob objects.
// Without the original bytes a client cannot rebuild the chunks the server is
// still missing, which surfaced as "chunk N bytes are missing". Keeping the
// blobs in IndexedDB (which structured-clones Blob/File to disk, not memory) lets
// resume re-slice exactly the missing chunks — including the chunk that was in
// flight when the page was refreshed — and skip the ones already present.
export interface UploadBlobStore {
  save(taskID: string, files: ReadonlyArray<UploadFile>): Promise<void>;
  load(taskID: string): Promise<UploadFile[] | null>;
  remove(taskID: string): Promise<void>;
}

const BLOB_DB_NAME = 'filetoaster-upload-blobs';
const BLOB_STORE_NAME = 'sessions';
const BLOB_DB_VERSION = 1;

interface BlobRecord {
  rel: string;
  size: number;
  chunks: number;
  blob: Blob;
}

// IndexedDbUploadBlobStore stores each session's files in one object-store
// record keyed by the session id. Every operation is best-effort: environments
// without IndexedDB (SSR, tests) or with a full quota simply skip persistence, so
// uploads never fail because the resumable bookmark could not be written.
export class IndexedDbUploadBlobStore implements UploadBlobStore {
  private dbPromise: Promise<IDBDatabase | null> | null = null;

  private open(): Promise<IDBDatabase | null> {
    if (typeof indexedDB === 'undefined') return Promise.resolve(null);
    if (this.dbPromise) return this.dbPromise;
    this.dbPromise = new Promise((resolve) => {
      let req: IDBOpenDBRequest;
      try {
        req = indexedDB.open(BLOB_DB_NAME, BLOB_DB_VERSION);
      } catch {
        resolve(null);
        return;
      }
      req.onupgradeneeded = () => {
        const db = req.result;
        if (!db.objectStoreNames.contains(BLOB_STORE_NAME)) {
          db.createObjectStore(BLOB_STORE_NAME);
        }
      };
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => resolve(null);
      req.onblocked = () => resolve(null);
    });
    return this.dbPromise;
  }

  private async run<T>(
    mode: IDBTransactionMode,
    op: (store: IDBObjectStore) => IDBRequest<T> | void
  ): Promise<void> {
    const db = await this.open();
    if (!db) return;
    await new Promise<void>((resolve) => {
      try {
        const tx = db.transaction(BLOB_STORE_NAME, mode);
        tx.oncomplete = () => resolve();
        tx.onerror = () => resolve();
        tx.onabort = () => resolve();
        op(tx.objectStore(BLOB_STORE_NAME));
      } catch {
        resolve();
      }
    });
  }

  async save(taskID: string, files: ReadonlyArray<UploadFile>): Promise<void> {
    const records: BlobRecord[] = [];
    for (const f of files) {
      if (!f.blob) continue;
      records.push({ rel: f.rel, size: f.size, chunks: f.chunks, blob: f.blob });
    }
    if (records.length === 0) return;
    await this.run('readwrite', (store) => store.put(records, taskID));
  }

  async load(taskID: string): Promise<UploadFile[] | null> {
    const db = await this.open();
    if (!db) return null;
    return new Promise((resolve) => {
      try {
        const tx = db.transaction(BLOB_STORE_NAME, 'readonly');
        const req = tx.objectStore(BLOB_STORE_NAME).get(taskID);
        req.onsuccess = () => {
          const records = req.result as BlobRecord[] | undefined;
          if (!records || records.length === 0) {
            resolve(null);
            return;
          }
          resolve(
            records.map((r) => ({ rel: r.rel, size: r.size, chunks: r.chunks, blob: r.blob }))
          );
        };
        req.onerror = () => resolve(null);
        tx.onabort = () => resolve(null);
      } catch {
        resolve(null);
      }
    });
  }

  async remove(taskID: string): Promise<void> {
    await this.run('readwrite', (store) => store.delete(taskID));
  }
}

// uploadBlobStore is the process-wide blob persistence used by the manager.
export const uploadBlobStore = new IndexedDbUploadBlobStore();

// relUnder returns `path` relative to `root` with forward slashes. Anything not
// actually rooted at `root` falls back to its basename so a stray path still
// yields a usable destination name.
export function relUnder(path: string, root: string): string {
  const rel = relativeUnder(path, root);
  if (rel !== null) return rel;
  return basename(path);
}

// manifestForSelection builds an UploadManifest from the selected entries of a
// window. Each selected directory is expanded recursively against `childrenOf`
// (the window's tree cache), collecting files (with sizes) and every directory
// path (including empty ones) so empty directories are recreated at the
// destination. Files and dirs are sorted for a deterministic manifest order.
export function manifestForSelection(
  selectedEntries: ReadonlyArray<ManifestEntry>,
  childrenOf: ChildLoader,
  rootPath: string
): UploadManifest {
  const files: UploadFile[] = [];
  const dirRel = new Set<string>();
  const queue: ManifestEntry[] = [];
  const seenDir = new Set<string>();
  const chunkSize = DEFAULT_CHUNK_SIZE;

  const enqueueDir = (entry: ManifestEntry) => {
    const rel = relUnder(entry.path, rootPath);
    if (!rel || seenDir.has(rel)) return;
    seenDir.add(rel);
    dirRel.add(rel);
    queue.push(entry);
  };

  for (const entry of selectedEntries) {
    if (entry.type === 'file') {
      const rel = relUnder(entry.path, rootPath);
      if (rel) files.push({ rel, size: entry.size, chunks: chunkCountForFile(entry.size, chunkSize) });
    } else {
      enqueueDir(entry);
    }
  }

  while (queue.length) {
    const dir = queue.shift() as ManifestEntry;
    const children = childrenOf(dir.path);
    if (!children) continue;
    for (const child of children) {
      if (child.type === 'file') {
        const rel = relUnder(child.path, rootPath);
        if (rel) files.push({ rel, size: child.size, chunks: chunkCountForFile(child.size, chunkSize) });
      } else {
        enqueueDir(child);
      }
    }
  }

  files.sort((a, b) => a.rel.localeCompare(b.rel));
  const dirs = [...dirRel].sort((a, b) => a.localeCompare(b));
  return { files, dirs };
}

// manifestForDataTransfer builds an UploadManifest from files dropped into the
// browser. `webkitRelativePath` gives each item's path under the dropped folder;
// file rels become manifest entries and every intermediate directory becomes a
// (recreated) dir. A folder entry is recorded by its own rel.
export function manifestForDataTransfer(items: ReadonlyArray<DnDItem>): UploadManifest {
  const files: UploadFile[] = [];
  const dirRel = new Set<string>();
  const chunkSize = DEFAULT_CHUNK_SIZE;

  for (const item of items) {
    const rel = (item.webRelativePath || item.name).replace(/^\/+/, '');
    if (item.isFile) {
      if (!rel) continue;
      files.push({ rel, size: item.size, chunks: chunkCountForFile(item.size, chunkSize), blob: item.blob });
      const parts = rel.split('/');
      for (let i = 1; i < parts.length; i++) dirRel.add(parts.slice(0, i).join('/'));
    } else {
      if (rel) dirRel.add(rel);
    }
  }

  files.sort((a, b) => a.rel.localeCompare(b.rel));
  const dirs = [...dirRel].sort((a, b) => a.localeCompare(b));
  return { files, dirs };
}

// ChunkSlice is one fixed-size slice of a file for upload at index `index`.
export interface ChunkSlice {
  index: number;
  start: number;
  end: number;
}

// chunkSlicesForFile returns the fixed-size slices of a file of `size` at
// `chunkSize` (a zero-byte file yields one empty slice at index 0).
export function chunkSlicesForFile(size: number, chunkSize: number): ChunkSlice[] {
  const cs = chunkSize <= 0 ? DEFAULT_CHUNK_SIZE : chunkSize;
  const count = Math.max(1, Math.ceil(size / cs));
  const slices: ChunkSlice[] = [];
  for (let i = 0; i < count; i++) {
    const start = i * cs;
    slices.push({ index: i, start, end: Math.min(start + cs, size) });
  }
  return slices;
}

// missingChunkIndices returns the global chunk indices in [0, expectedChunks)
// not present in `received`, i.e. the chunks a client must still send.
export function missingChunkIndices(received: ReadonlySet<number>, expectedChunks: number): number[] {
  const missing: number[] = [];
  for (let i = 0; i < expectedChunks; i++) {
    if (!received.has(i)) missing.push(i);
  }
  return missing;
}

// globalChunkFor locates the global chunk index of fileIdx/chunkIdx within a
// manifest (chunk indices accumulate across files in manifest order).
export function globalChunkFor(files: ReadonlyArray<UploadFile>, fileIdx: number, chunkIdx: number): number {
  let base = 0;
  for (let i = 0; i < fileIdx; i++) base += files[i].chunks;
  return base + chunkIdx;
}

// globalChunkSpan returns the [start, end) global chunk range for fileIdx.
export function globalChunkSpan(files: ReadonlyArray<UploadFile>, fileIdx: number): { start: number; end: number } {
  let start = 0;
  for (let i = 0; i < fileIdx; i++) start += files[i].chunks;
  return { start, end: start + files[fileIdx].chunks };
}

interface PersistedUpload {
  taskID: string;
  dest: string;
  chunkSize: number;
  files: UploadFile[];
  dirs: string[];
  present: boolean[];
  received: number[][];
  phase: IngestState['phase'];
}

// IngestState tracks one in-progress upload session on the client.
export interface IngestState {
  taskID: string;
  dest: string;
  chunkSize: number;
  files: UploadFile[];
  dirs: string[];
  // received is the assembled, received bytes per file (used to re-slice and
  // re-send missing chunks after a refresh).
  received: Uint8Array[];
  // present marks, per global chunk index, which chunks the client has sent.
  present: boolean[];
  // phase moves staging → finalizing → done/failed/cancelled.
  phase: 'staging' | 'finalizing' | 'done' | 'failed' | 'cancelled';
  error: string | null;
  finalizeID: string | null;
  // Speed tracking
  bytesSent: number;
  startTime: number;
  lastNotifyTime: number;
  lastBytesSent: number;
}

const sleep = (ms: number) => new Promise<void>((res) => setTimeout(res, ms));
 
// UploadManager owns the in-memory upload sessions and drives chunk upload,
  // resume, retry and abort. It is a plain class (no Svelte) so svelte-check type
  // checks it as ordinary TypeScript.
  export class UploadManager {
    sessions = new Map<string, IngestState>();
    // hydration tracks an in-flight blob restore per session so loadPersisted can
    // start restoring the live bytes in the background while a later resume()
    // joins the same operation instead of racing it.
    private hydration = new Map<string, Promise<void>>();
    // Subscribers for reactive updates when sessions change
    private subscribers = new Set<() => void>();
    // Global chunk-upload concurrency pool shared by every session. `inflight`
  // counts chunk PUTs currently on the network and `waiters` queues the chunks
  // waiting for a slot. Capping the pool here (rather than per file, as a
  // per-session pool did) is what bounds the number of simultaneous upload
  // requests the browser opens, regardless of how many files are uploaded: the
  // limit applies across all files and sessions, never per file. This is the
  // fix for net::ERR_INSUFFICIENT_RESOURCES when uploading large files.
  private inflight = 0;
  // Each waiter carries the session that queued it, so a release can skip the
  // waiters left behind by a cancelled session instead of waking them (which
  // would starve a later upload's chunks — see release()).
  private waiters: Array<{ resolve: () => void; state: IngestState }> = [];

  constructor(
    private api: UploadApi = {
      createUpload: apiCreateUpload,
      uploadChunk: apiUploadChunk,
      getUploadStatus: apiGetUploadStatus,
      cancelUpload: apiCancelUpload,
      resumeUpload: apiResumeUpload
    },
    private concurrency = 8,
    private maxRetries = 3,
    private retryDelayMs = 250,
    private defaultChunkSize: number = DEFAULT_CHUNK_SIZE,
    private blobStore: UploadBlobStore = uploadBlobStore
  ) {}

  // configure overrides the chunk size and the global chunk-upload concurrency
  // at runtime, from the server's GET /api/config. Both clamp to sane minima so
  // a missing or bogus value can never disable slicing or stall the upload.
  configure(opts: { chunkSize?: number; maxConcurrent?: number }): void {
    if (typeof opts.chunkSize === 'number' && opts.chunkSize > 0) {
      this.defaultChunkSize = opts.chunkSize;
    }
    if (typeof opts.maxConcurrent === 'number' && opts.maxConcurrent > 0) {
      this.concurrency = Math.floor(opts.maxConcurrent);
    }
  }

  // Subscribe to session changes for reactive UI updates
  subscribe(fn: () => void): () => void {
    this.subscribers.add(fn);
    return () => this.subscribers.delete(fn);
  }

  private notify(): void {
    for (const fn of this.subscribers) fn();
  }

  // acquire waits for a free slot in the global pool, then reserves it and
  // returns true. It returns false (without taking a slot) as soon as the
  // session is cancelled, so an aborted upload stops waking and slicing chunks
  // instead of piling up on the pool forever. A zero or negative concurrency
  // (defensively) still lets a single chunk through.
  private async acquire(state: IngestState): Promise<boolean> {
    const cap = this.concurrency < 1 ? 1 : this.concurrency;
    while (state.phase !== 'cancelled') {
      if (this.inflight < cap) {
        this.inflight++;
        return true;
      }
      await new Promise<void>((resolve) => this.waiters.push({ resolve, state }));
    }
    return false;
  }

  // release frees a slot and wakes the next waiter whose session is still
  // active. Waiters belonging to a cancelled session are dropped instead of
  // woken: waking them would consume the release (they return immediately) and
  // leave a later upload's chunks stuck behind them forever — the bug that made
  // a re-upload hang in the same tab until a refresh cleared the pool.
  private release(): void {
    this.inflight--;
    while (this.waiters.length) {
      const next = this.waiters.shift()!;
      if (next.state.phase !== 'cancelled') {
        next.resolve();
        return;
      }
    }
  }

  // dropWaitersFor removes every waiter belonging to `state` and wakes it, so the
  // suspended acquire() re-checks the phase and returns. Called when a session is
  // aborted or resumed so its pending chunks do not linger in (and block) the pool.
  private dropWaitersFor(state: IngestState): void {
    const keep: Array<{ resolve: () => void; state: IngestState }> = [];
    for (const w of this.waiters) {
      if (w.state === state) {
        w.resolve();
      } else {
        keep.push(w);
      }
    }
    this.waiters = keep;
  }

  // startUpload creates a server session for `dest`, persists the handle, and
  // enqueues the per-file chunk uploads, drained through the shared global pool
  // capped at `this.concurrency`. It resolves to the session task id, or null
  // when creation failed.
  async startUpload(dest: string, manifest: UploadManifest, chunkSize?: number): Promise<string | null> {
    const cs = chunkSize ?? this.defaultChunkSize;
    // Re-slice the manifest chunk counts from `cs` so the client's global
    // chunk indexing matches the server, which recomputes chunk counts from
    // file size and chunk size. A manifest built with a different chunk size
    // (the pure builders default to DEFAULT_CHUNK_SIZE) is therefore harmless.
    const files = manifest.files.map((f) => ({ ...f, chunks: chunkCountForFile(f.size, cs) }));
    let res: { taskID?: string };
    try {
      const req: CreateUploadRequest = { dest, chunkSize: cs, files, dirs: manifest.dirs };
      res = await this.api.createUpload(req);
    } catch (err) {
      throw err instanceof Error ? err : new Error('upload could not start');
    }
    const taskID = res.taskID;
    if (!taskID) throw new Error('upload session was not created');

    // The global chunk count is the sum of every file's chunks; `present` is a
    // per-chunk bitmap (indexed globally across all files) of what the client
    // has already sent. `received` only holds assembled bytes for files that
    // have no live blob to slice (a resumed session whose bytes live in
    // localStorage); for a live upload the content is read straight from the
    // blob, so a multi-gigabyte file is never materialised in memory.
    const total = files.reduce((n, f) => n + f.chunks, 0);
    const state: IngestState = {
      taskID,
      dest,
      chunkSize: cs,
      files,
      dirs: manifest.dirs,
      received: files.map((f) => (f.blob ? new Uint8Array(0) : new Uint8Array(f.size))),
      present: new Array(total).fill(false),
      phase: 'staging',
      error: null,
      finalizeID: null,
      bytesSent: 0,
      startTime: Date.now(),
      lastNotifyTime: Date.now(),
      lastBytesSent: 0
    };
    this.sessions.set(taskID, state);
    this.persist(state);
    this.notify();
    // Show toast for new upload
    const n = files.length;
    showTaskToast(`Upload started: ${n === 1 ? '1 file' : `${n} files`}`, `upload-${taskID}`);
    void this.persistBlobsThenUpload(state);
    return taskID;
  }

  // persistBlobsThenUpload bookmarks the live file bytes before the first chunk
  // is sent, then runs the upload. Storing the blobs first is what makes a
  // refresh at any point during staging resumable: when the page reloads, the
  // File objects are gone, but resume can reload them from IndexedDB and
  // re-slice the chunks the server still needs (including the one that was in
  // flight when the page refreshed). Persistence is best-effort, so a quota or
  // missing IndexedDB never blocks the upload itself.
  private async persistBlobsThenUpload(state: IngestState): Promise<void> {
    try {
      await this.blobStore.save(state.taskID, state.files);
    } catch {
      // Best-effort: the in-session resume path still works without the blobs.
    }
    await this.uploadAll(state);
  }

  // hydrateBlobs reloads the session's file bytes from the blob store so a
  // session restored after a refresh (or a manager that no longer holds the live
  // Blobs) can rebuild the chunks the server is missing instead of failing with
  // "chunk N bytes are missing". Files that already carry a live Blob are left
  // untouched; a stored entry is matched by its relative path.
  private async hydrateBlobs(state: IngestState): Promise<void> {
    if (state.files.every((f) => f.blob)) return;
    let stored: UploadFile[] | null = null;
    try {
      stored = await this.blobStore.load(state.taskID);
    } catch {
      stored = null;
    }
    if (!stored) return;
    const blobs = new Map<string, Blob>();
    for (const f of stored) {
      if (f.blob) blobs.set(f.rel, f.blob);
    }
    if (blobs.size === 0) return;
    state.files = state.files.map((f) =>
      f.blob ? f : blobs.has(f.rel) ? { ...f, blob: blobs.get(f.rel) } : f
    );
    // A hydrated live file no longer needs an assembled in-memory copy; slice
    // the restored Blob instead (same path a live upload takes).
    state.received = state.received.map((bytes, idx) =>
      state.files[idx]?.blob ? new Uint8Array(0) : bytes
    );
  }

  // ensureHydrated joins (or starts) the single blob-restore operation for a
  // session, so eager hydration at page load and a later resume cannot race.
  private ensureHydrated(state: IngestState): Promise<void> {
    const existing = this.hydration.get(state.taskID);
    if (existing) return existing;
    const p = this.hydrateBlobs(state).finally(() => {
      this.hydration.delete(state.taskID);
    });
    this.hydration.set(state.taskID, p);
    return p;
  }

  // needsFiles reports whether a paused session is missing the original bytes
  // for at least one chunk the server has not received yet. That happens when
  // the browser was refreshed before the blobs could be persisted (quota,
  // private mode, a very large file). Such a session can only be resumed after
  // the user re-selects the files; sending the chunks without them would fail
  // with "chunk N bytes are missing".
  needsFiles(taskID: string): boolean {
    const state = this.sessions.get(taskID);
    if (!state) return false;
    return state.files.some((f, idx) => {
      if (f.blob) return false;
      const restored = state.received[idx]?.length ?? 0;
      return restored < f.size;
    });
  }

  // needsFolderSelection reports whether the resumed manifest contains nested
  // paths, so the re-selection picker must return webkitRelativePath values.
  needsFolderSelection(taskID: string): boolean {
    const state = this.sessions.get(taskID);
    if (!state) return false;
    return state.files.some((f) => f.rel.includes('/'));
  }

  // resumeWithFiles attaches the files the user re-selected after a refresh to
  // a paused session (matching by relative path and size), then resumes it.
  // The already-staged chunks are kept; only the missing ones are sent. It
  // returns false when the selection does not match the interrupted manifest.
  async resumeWithFiles(
    taskID: string,
    picked: ReadonlyArray<{ rel: string; size: number; blob: Blob }>
  ): Promise<boolean> {
    const state = this.sessions.get(taskID);
    if (!state) return false;
    if (state.phase !== 'cancelled' && state.phase !== 'failed') return false;
    const byRel = new Map<string, { rel: string; size: number; blob: Blob }>();
    for (const p of picked) byRel.set(p.rel, p);
    state.files = state.files.map((f) => {
      if (f.blob) return f;
      const match = byRel.get(f.rel);
      return match && match.size === f.size ? { ...f, blob: match.blob } : f;
    });
    state.received = state.received.map((bytes, idx) =>
      state.files[idx]?.blob ? new Uint8Array(0) : bytes
    );
    // Any files still missing can also come from the blob store (e.g. the user
    // re-selected only part of a mixed selection).
    await this.ensureHydrated(state);
    if (this.needsFiles(taskID)) {
      state.phase = 'cancelled';
      state.error = 'Select the interrupted files to resume this upload.';
      this.persist(state);
      this.notify();
      return false;
    }
    // Refresh the resumable bookmark with the re-selected bytes so another
    // page reload does not have to ask again.
    void this.blobStore.save(taskID, state.files);
    state.error = null;
    return this.resumeSession(taskID);
  }

  // resume reloads a remembered session (after a refresh) from localStorage and
  // re-sends only the chunks the client had not already sent. It returns true
  // when a session was resumed, false when nothing was remembered.
  async resume(taskID: string): Promise<boolean> {
    const raw = localStorage.getItem(uploadStoreKey(taskID));
    if (!raw) return false;
    let persisted: PersistedUpload;
    try {
      persisted = JSON.parse(raw) as PersistedUpload;
    } catch {
      return false;
    }
    const state: IngestState = {
      taskID: persisted.taskID,
      dest: persisted.dest,
      chunkSize: persisted.chunkSize,
      files: persisted.files,
      dirs: persisted.dirs,
      received: persisted.received.map((bytes) => new Uint8Array(bytes)),
      present: persisted.present,
      phase: 'staging',
      error: null,
      finalizeID: null,
      bytesSent: persisted.present.filter((p) => p).length * persisted.chunkSize, // approximate
      startTime: Date.now(),
      lastNotifyTime: Date.now(),
      lastBytesSent: 0
    };
    this.sessions.set(taskID, state);
    this.notify();
    // Reload the original file bytes so the chunks still missing after the
    // refresh can be re-sliced (present chunks are skipped by uploadAll).
    await this.ensureHydrated(state);
    if (this.needsFiles(taskID)) {
      // The browser could not persist the file bytes; leave the session paused
      // (not failed) so the UI can ask the user to re-select the files.
      state.phase = 'cancelled';
      state.error = 'Select the interrupted files to resume this upload.';
      this.persist(state);
      this.notify();
      return true;
    }
    // Clear any server-side abort before re-sending the missing chunks so the
    // server accepts them again. A session gone server-side (restart/swept)
    // rejects here; the re-sent chunks then 404 and the session settles as failed.
    try {
      await this.api.resumeUpload(taskID);
    } catch {
      // fall through — uploadAll surfaces any real failure via the session state
    }
    void this.uploadAll(state);
    return true;
  }

  // loadPersisted restores every remembered session from localStorage into the
  // in-memory map as a paused (cancelled) session, so a page refresh surfaces the
  // upload in the taskbar with a Resume control instead of silently dropping it.
  // No chunks are re-sent until the user resumes. Sessions whose persisted phase
  // is finalizing/done are skipped: their work lives server-side and arrives via
  // the task stream.
  loadPersisted(): void {
    if (!browserAvailable()) return;
    let changed = false;
    for (let i = 0; i < localStorage.length; i++) {
      const key = localStorage.key(i);
      if (!key || !key.startsWith(UPLOAD_STORAGE_PREFIX)) continue;
      const taskID = key.slice(UPLOAD_STORAGE_PREFIX.length);
      if (this.sessions.has(taskID)) continue;
      const raw = localStorage.getItem(key);
      if (!raw) continue;
      let persisted: PersistedUpload;
      try {
        persisted = JSON.parse(raw) as PersistedUpload;
      } catch {
        continue;
      }
      if (persisted.phase === 'finalizing' || persisted.phase === 'done') continue;
      const state: IngestState = {
        taskID: persisted.taskID,
        dest: persisted.dest,
        chunkSize: persisted.chunkSize,
        files: persisted.files,
        dirs: persisted.dirs,
        received: persisted.received.map((bytes) => new Uint8Array(bytes)),
        present: persisted.present,
        phase: 'cancelled',
        error: null,
        finalizeID: null,
        bytesSent: persisted.present.filter((p) => p).length * persisted.chunkSize,
        startTime: Date.now(),
        lastNotifyTime: Date.now(),
        lastBytesSent: 0
      };
      this.sessions.set(taskID, state);
      // Restore the live file bytes in the background so a Refresh then Resume
      // (or Play) is seamless when IndexedDB did persist them.
      void this.ensureHydrated(state);
      changed = true;
    }
    if (changed) this.notify();
  }

  // abort marks the session aborted and asks the server to stop ingestion,
  // keeping the already-received chunks so the upload stays resumable.
  async abort(taskID: string): Promise<void> {
    const state = this.sessions.get(taskID);
    if (!state) return;
    state.phase = 'cancelled';
    // Drop this session's pending pool waiters so an aborted upload cannot
    // starve a later upload's chunks (see release()).
    this.dropWaitersFor(state);
    try {
      await this.api.cancelUpload(taskID);
    } catch {
      // A failed cancel leaves the chunks on the server; the age sweep still
      // reclaims the staging dir. Ignore so the UI can report the abort.
    }
    this.persist(state);
    this.notify();
  }

  // resumeSession resumes an aborted or failed session that is already in memory.
  // It keeps the already-received chunks and continues uploading the missing ones.
  async resumeSession(taskID: string): Promise<boolean> {
    const state = this.sessions.get(taskID);
    if (!state) return false;
    if (state.phase !== 'cancelled' && state.phase !== 'failed') return false;
    // Drop any waiters still queued from the previous attempt so the resumed
    // chunks do not duplicate them (and are not stuck behind them).
    this.dropWaitersFor(state);
    state.phase = 'staging';
    state.error = null;
    // Reset start time for accurate speed calculation on resume
    state.startTime = Date.now();
    state.lastNotifyTime = Date.now();
    state.lastBytesSent = state.bytesSent;
    // A session restored after a refresh has no live File objects; reload the
    // original bytes from IndexedDB so the missing chunks (including the one
    // that was in flight when the page reloaded) can be re-sent.
    await this.ensureHydrated(state);
    if (this.needsFiles(taskID)) {
      // Never start a transfer that would fail with "bytes are missing"; keep
      // the session paused and let the UI request the files again.
      state.phase = 'cancelled';
      state.error = 'Select the interrupted files to resume this upload.';
      this.persist(state);
      this.notify();
      return false;
    }
    // Clear the server-side abort so the missing chunks are accepted again. An
    // aborted session refuses chunks until this succeeds; if the session is gone
    // server-side the re-sent chunks 404 and the session settles as failed.
    try {
      await this.api.resumeUpload(taskID);
    } catch {
      // fall through — uploadAll surfaces any real failure via the session state
    }
    this.persist(state);
    this.notify();
    void this.uploadAll(state);
    return true;
  }

  // state returns a copy of the ingest state for a session (for the window to
  // render ingest progress and an abort control while staging).
  state(taskID: string): IngestState | undefined {
    return this.sessions.get(taskID);
  }

  // finished reports whether a session has reached a terminal phase.
  finished(taskID: string): boolean {
    const s = this.sessions.get(taskID);
    return !!s && (s.phase === 'done' || s.phase === 'failed' || s.phase === 'cancelled');
  }

  // clear forgets a session's local state (called once the finalize task is
  // terminal and its localStorage entry has been cleared).
  clear(taskID: string): void {
    const state = this.sessions.get(taskID);
    if (state) {
      this.discard(state);
    } else {
      // The session may already be gone from memory (e.g. a finalize task that
      // finished after a refresh); still drop any persisted bookmark and bytes.
      if (browserAvailable()) {
        try {
          localStorage.removeItem(uploadStoreKey(taskID));
        } catch {
          // ignore
        }
      }
      void this.blobStore.remove(taskID);
    }
    this.notify();
  }

  // clearByFinalizeID finds and clears a session by its finalize task ID.
  // This is used when the server's finalize task finishes (its ID differs from the staging session ID).
  clearByFinalizeID(finalizeID: string): void {
    for (const [_, state] of this.sessions) {
      if (state.finalizeID === finalizeID) {
        this.discard(state);
        this.notify();
        return;
      }
    }
  }

  private persist(state: IngestState): void {
    if (!browserAvailable()) return;
    const persisted: PersistedUpload = {
      taskID: state.taskID,
      dest: state.dest,
      chunkSize: state.chunkSize,
      files: state.files.map((f) => ({ rel: f.rel, size: f.size, chunks: f.chunks })),
      dirs: state.dirs,
      present: state.present,
      received: state.received.map((bytes) => Array.from(bytes)),
      phase: state.phase
    };
    try {
      localStorage.setItem(uploadStoreKey(state.taskID), JSON.stringify(persisted));
    } catch {
      // localStorage may be full or unavailable; ignore — the session still
      // works until a refresh.
    }
  }

  private discard(state: IngestState): void {
    if (browserAvailable()) {
      try {
        localStorage.removeItem(uploadStoreKey(state.taskID));
      } catch {
        // ignore
      }
    }
    // The session can no longer be resumed, so its persisted file bytes are
    // dead weight; drop them instead of leaving multi-gigabyte blobs behind.
    void this.blobStore.remove(state.taskID);
    this.sessions.delete(state.taskID);
  }

  // enterFinalizing marks a session as finalizing and drops everything that made
  // it resumable: the persisted file bytes and the localStorage bookmark. Once
  // the server owns the staging dir the client can no longer send chunks (the
  // server has dropped the upload session), so a refresh during finalize is
  // handled by the tracked finalize task rather than by resuming chunks. This
  // also guarantees a finished upload leaves no local state behind.
  private enterFinalizing(state: IngestState): void {
    state.phase = 'finalizing';
    void this.blobStore.remove(state.taskID);
    if (browserAvailable()) {
      try {
        localStorage.removeItem(uploadStoreKey(state.taskID));
      } catch {
        // ignore
      }
    }
    this.notify();
  }

  // uploadAll uploads every file, draining the per-file chunks through the
  // shared global pool (bounded at `this.concurrency`). It settles the session
  // as finalizing/done/failed.
  //
  // Every missing chunk is enqueued up front (a chunk already present on resume
  // is skipped); each chunk acquires the global slot only when one is free, so
  // the number of simultaneous upload requests is capped across every file and
  // session, never per file.
  private async uploadAll(state: IngestState): Promise<void> {
    const jobs: Array<Promise<void>> = [];
    for (let fileIdx = 0; fileIdx < state.files.length; fileIdx++) {
      const slices = chunkSlicesForFile(state.files[fileIdx].size, state.chunkSize);
      for (const slice of slices) {
        const globalIndex = globalChunkSpan(state.files, fileIdx).start + slice.index;
        if (state.present[globalIndex]) continue;
        jobs.push(this.sendWithRetry(state, fileIdx, slice));
      }
    }
    await Promise.all(jobs);
    this.checkCompleted(state);
  }

  private async sendWithRetry(state: IngestState, fileIdx: number, slice: ChunkSlice): Promise<void> {
    const file = state.files[fileIdx];
    const expected = slice.end - slice.start;
    const live = !!file.blob;
    let attempt = 0;
    while (true) {
      // Reserve a slot in the global pool *before* slicing the blob, so at
      // most `concurrency` chunk blobs (each up to a full chunk) are held in
      // memory at once and only that many upload requests are in flight.
      if (!(await this.acquire(state))) return;
      try {
        // A live upload slices the real file straight from its blob (no
        // whole-file copy in memory). A resumed session after a refresh has
        // no live file, so it re-slices the assembled bytes kept in
        // `received`; if those are missing the chunk cannot be rebuilt.
        let blob: Blob;
        if (file.blob) {
          blob = await file.blob.slice(slice.start, slice.end);
        } else {
          blob = new Blob([state.received[fileIdx].slice(slice.start, slice.end)]);
        }
        if (blob.size !== expected) {
          throw new Error(live
            ? `chunk ${slice.index} of ${file.rel} has an unexpected size`
            : `cannot resume ${file.rel}: chunk ${slice.index} bytes are missing`);
        }
        const globalIndex = globalChunkFor(state.files, fileIdx, slice.index);
        const resp = await this.api.uploadChunk(state.taskID, fileIdx, slice.index, blob);
        state.present[globalIndex] = true;
        // Track bytes sent for speed calculation
        state.bytesSent += blob.size;
        this.persist(state);
        this.notify();
        if (resp.taskID) {
          state.finalizeID = resp.taskID;
          this.enterFinalizing(state);
        }
        return;
      } catch (err) {
        if (state.phase === 'cancelled') return;
        if (attempt >= this.maxRetries) {
          state.phase = 'failed';
          state.error = err instanceof Error ? err.message : 'chunk upload failed';
          this.persist(state);
          this.notify();
          return;
        }
        await sleep(this.retryDelayMs * 2 ** attempt);
        attempt++;
      } finally {
        // Free the slot for the next waiting chunk before retrying, so a
        // chunk that is backing off does not hold a slot against others.
        this.release();
      }
    }
  }

  // checkCompleted transitions the session once every global chunk is present
  // (the server finalized it and handed over the finalize task id).
  private checkCompleted(state: IngestState): void {
    if (state.finalizeID) {
      this.enterFinalizing(state);
      return;
    }
    if (state.phase !== 'staging') return;
    const total = state.present.length;
    if (total === 0) return;
    if (state.present.every((p) => p)) {
      // No finalize id was returned synchronously; the server finalized and
      // dropped the session. The finalize task will appear via SSE; the label
      // (phase "finalizing") picks it up from the terminal task meta.
      this.enterFinalizing(state);
    }
  }
}

// browserAvailable reports whether localStorage is usable (browser context).
function browserAvailable(): boolean {
  return typeof localStorage !== 'undefined';
}

// uploadManager is the singleton the UI drives (Window.svelte, app init resume).
export const uploadManager = new UploadManager();
