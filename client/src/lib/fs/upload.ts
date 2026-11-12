// UploadManager implements the client half of the chunked + resumable upload
// flow. A session is created on the server (POST /api/uploads); each selected
// file is sliced into fixed-size chunks and PUT per chunk; the server reports,
// per session, which chunks are present so a client can resume by re-sending
// only the missing ones; when every chunk of every file has arrived the server
// auto-enqueues a tracked finalize op (type "upload"). Ingestion does not hold a
// worker slot; only the finalize copy does.
//
// Sessions survive a page refresh via localStorage (task id, manifest, and the
// per-file received bytes so missing chunks can be re-sliced); they do NOT
// survive a server restart (the in-memory server session is gone → the client
// is told to restart). Manifest building and the chunk math are pure functions
// at the top of this file so they can be unit-tested without a browser.

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
	chunkCountForFile,
	DEFAULT_CHUNK_SIZE
} from '$lib/fs/api';

// UploadApi abstracts the network calls so the manager can be exercised in tests
// with an in-memory fake that never touches the server.
export interface UploadApi {
	createUpload(req: CreateUploadRequest): Promise<{ taskID?: string }>;
	uploadChunk(taskID: string, fileIdx: number, chunkIdx: number, blob: Blob): Promise<UploadChunkResponse>;
	getUploadStatus(taskID: string): Promise<UploadStatus>;
	cancelUpload(taskID: string): Promise<void>;
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

// relUnder returns `path` relative to `root` with forward slashes. Anything not
// actually rooted at `root` falls back to its basename so a stray path still
// yields a usable destination name.
export function relUnder(path: string, root: string): string {
	if (path === root) return '';
	if (path.startsWith(root + '/')) return path.slice(root.length + 1);
	const i = path.lastIndexOf('/');
	return i <= 0 ? path : path.slice(i + 1);
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
}

const sleep = (ms: number) => new Promise<void>((res) => setTimeout(res, ms));
 
// UploadManager owns the in-memory upload sessions and drives chunk upload,
// resume, retry and abort. It is a plain class (no Svelte) so svelte-check type
// checks it as ordinary TypeScript.
export class UploadManager {
	private sessions = new Map<string, IngestState>();
	// Global chunk-upload concurrency pool shared by every session. `inflight`
	// counts chunk PUTs currently on the network and `waiters` queues the chunks
	// waiting for a slot. Capping the pool here (rather than per file, as a
	// per-session pool did) is what bounds the number of simultaneous upload
	// requests the browser opens, regardless of how many files are uploaded: the
	// limit applies across all files and sessions, never per file. This is the
	// fix for net::ERR_INSUFFICIENT_RESOURCES when uploading large files.
	private inflight = 0;
	private waiters: Array<() => void> = [];

	constructor(
		private api: UploadApi = {
			createUpload: apiCreateUpload,
			uploadChunk: apiUploadChunk,
			getUploadStatus: apiGetUploadStatus,
			cancelUpload: apiCancelUpload
		},
		private concurrency = 8,
		private maxRetries = 3,
		private retryDelayMs = 250,
		private defaultChunkSize: number = DEFAULT_CHUNK_SIZE
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
			await new Promise<void>((resolve) => this.waiters.push(resolve));
		}
		return false;
	}

	// release frees a slot and wakes the next waiting chunk, if any.
	private release(): void {
		this.inflight--;
		const next = this.waiters.shift();
		if (next) next();
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
			finalizeID: null
		};
		this.sessions.set(taskID, state);
		this.persist(state);
		void this.uploadAll(state);
		return taskID;
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
			finalizeID: null
		};
		this.sessions.set(taskID, state);
		void this.uploadAll(state);
		return true;
	}

	// abort marks the session aborted and asks the server to stop ingestion,
	// keeping the already-received chunks so the upload stays resumable.
	async abort(taskID: string): Promise<void> {
		const state = this.sessions.get(taskID);
		if (!state) return;
		state.phase = 'cancelled';
		try {
			await this.api.cancelUpload(taskID);
		} catch {
			// A failed cancel leaves the chunks on the server; the age sweep still
			// reclaims the staging dir. Ignore so the UI can report the abort.
		}
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
		this.sessions.delete(taskID);
	}

	private persist(state: IngestState): void {
		if (!browserAvailable()) return;
		const persisted: PersistedUpload = {
			taskID: state.taskID,
			dest: state.dest,
			chunkSize: state.chunkSize,
			files: state.files,
			dirs: state.dirs,
			present: state.present,
			received: state.received.map((bytes) => Array.from(bytes))
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
		this.sessions.delete(state.taskID);
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
				this.persist(state);
				if (resp.taskID) {
					state.finalizeID = resp.taskID;
					state.phase = 'finalizing';
					this.persist(state);
				}
				return;
			} catch (err) {
				if (state.phase === 'cancelled') return;
				if (attempt >= this.maxRetries) {
					state.phase = 'failed';
					state.error = err instanceof Error ? err.message : 'chunk upload failed';
					this.persist(state);
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
			state.phase = 'finalizing';
			return;
		}
		if (state.phase !== 'staging') return;
		const total = state.present.length;
		if (total === 0) return;
		if (state.present.every((p) => p)) {
			// No finalize id was returned synchronously; the server finalized and
			// dropped the session. The finalize task will appear via SSE; the label
			// (phase "finalizing") picks it up from the terminal task meta.
			state.phase = 'finalizing';
		}
	}
}

// browserAvailable reports whether localStorage is usable (browser context).
function browserAvailable(): boolean {
	return typeof localStorage !== 'undefined';
}

// uploadManager is the singleton the UI drives (Window.svelte, app init resume).
export const uploadManager = new UploadManager();
