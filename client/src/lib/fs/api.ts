// Server helpers for the file-manager window: fetching the allowed-path roots
// and listing a single directory. Paths are sent in the request body (POST), so
// long or special-character paths are never URL-encoded into a query string.
export interface Entry {
	path: string;
	name: string;
	type: 'file' | 'dir';
	size: number;
	// ISO-8601 timestamps from the server.
	created: string;
	modified: string;
}

export interface Listing {
	path: string;
	entries: Entry[];
}

// FileInfo is the per-path payload returned by the information endpoint. Size is
// the on-disk size of the file, or the recursive on-disk total (every file in
// every sub-directory) for directories — the space actually allocated, like
// `du`, not the logical byte count. FileCount and DirCount are
// the number of files / sub-directories found within the path, walked to the
// deepest level. Perms is the classic ten-char Unix string (e.g. "drwxr-xr-x");
// PermsOct is the numeric mode (e.g. "0755").
export interface FileInfo {
	path: string;
	name: string;
	type: 'file' | 'dir';
	size: number;
	fileCount: number;
	dirCount: number;
	perms: string;
	permsOctal: string;
	created: string;
	modified: string;
}

export interface FileInfos {
	items: FileInfo[];
}

// TaskProgress is the byte/file accounting and throughput for a task. It maps to
// the server's task.ByteProgress: Total/Done are bytes processed (for copy, the
// bytes copied), FilesTotal/FilesDone are the files processed, and RatePerSec is
// the smoothed bytes-per-second throughput used to render KB/s or MB/s.
export interface TaskProgress {
	total: number;
	done: number;
	filesTotal: number;
	filesDone: number;
	ratePerSec: number;
}

// Task is a tracked async operation. It maps to the server's task.Task. Status is
// one of queued, running, finished, failed, cancelled. Bytes carries the live
// progress; Meta is operation-specific (opaque to the client) and, for copy,
// holds the resolved plan (items with their final, possibly renamed, dst).
export type TaskStatus = 'queued' | 'running' | 'finished' | 'failed' | 'cancelled';

export interface Task {
	id: string;
	type: string;
	status: TaskStatus;
	progress: number;
	created: string;
	updated: string;
	error?: string;
	bytes: TaskProgress;
	meta?: unknown;
}

// CopyMeta is the copy operation's plan, carried in a task's Meta field.
export interface CopyMeta {
	items: Array<{ src: string; dst: string }>;
	conflict: string;
}

// DeleteMeta is the delete operation's plan (the resolved source paths), carried
// in a task's Meta field so the label can count the selected items. It stays
// opaque to the client's task rendering; the engine only reads the paths.
export interface DeleteMeta {
	paths: string[];
}

// MoveMeta is the move operation's plan, carried in a task's Meta field so the
// label can count the selected items. It stays opaque to the client's task
// rendering; the engine only reads the items.
export interface MoveMeta {
	items: Array<{ src: string; dst: string }>;
}

// CompressMeta is the compress operation's plan, carried in a task's Meta field:
// the archive format, the resolved in-place destination, and the number of
// sources archived. It stays opaque to the client's task rendering.
export interface CompressMeta {
	format: string;
	dest: string;
	source: number;
}

// DecompressMeta is the decompress operation's plan, carried in a task's Meta
// field so the label can show the archive and its format. It stays opaque to the
// client's task rendering; the engine only reads the paths.
export interface DecompressMeta {
	format: string;
	archive: string;
	extractDir: string;
	into: string;
}

export interface TaskListResponse {
	tasks: Task[];
}

export interface CopyResponse {
	taskID?: string;
}

export interface DeleteResponse {
	taskID?: string;
}

export interface MoveResponse {
	taskID?: string;
}

export interface CompressResponse {
	taskID?: string;
}

export interface DecompressResponse {
	taskID?: string;
}

export interface CopyTaskRequest {
	paths: string[];
	dest: string;
	conflict: string;
}

export interface MoveTaskRequest {
	paths: string[];
	dest: string;
	conflict: string;
}

export interface CompressTaskRequest {
	paths: string[];
	format: string;
}

export interface DecompressTaskRequest {
	paths: string[];
	into: string;
}

// apiError carries a UI-facing message from the server's `{"error": ...}` body.
export class ApiError extends Error {
	override name = 'ApiError';
	constructor(message: string) {
		super(message);
	}
}

// postJSON sends a JSON request with the session cookie and decodes the JSON
// response. Both directory-listing endpoints are authenticated POSTs.
async function postJSON<T>(url: string, body: unknown): Promise<T> {
	const res = await fetch(url, {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(body),
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `request failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as T;
}

// fetchListing lists the directory at path (confined to the allowed roots
// server-side). The path is sent in the JSON request body.
export async function fetchListing(path: string): Promise<Listing> {
	return postJSON<Listing>('/api/fs', { path });
}

// mkdirDirectory creates a directory named `name` directly inside `parent`
// (confined to the allowed roots server-side). Returns the created absolute path.
export async function mkdirDirectory(parent: string, name: string): Promise<string> {
	const res = await postJSON<{ path: string }>('/api/fs/mkdir', { parent, name });
	return res.path;
}

// renameFile renames the given paths synchronously (confined to the allowed
// roots server-side). The first path is renamed to the exact `name` (each file
// keeping its own extension); subsequent paths get a " (N)" suffix, so a batch
// of three files becomes name, name (1), name (2). It rejects (never 409) the
// moment a target name already exists, leaving the selection untouched — the UI
// surfaces the message as an inline error, like a failed new-folder.
export async function renameFile(paths: string[], name: string): Promise<void> {
	await postJSON<{ ok: boolean }>('/api/fs/rename', { paths, name });
}

// fetchInfo computes information (recursive size, file/dir counts, permissions,
// times) for the given paths. Each path must lie within the allowed roots;
// unresolvable paths are skipped by the server.
export async function fetchInfo(paths: string[]): Promise<FileInfo[]> {
	const res = await postJSON<FileInfos>('/api/fs/info', { paths });
	return res.items;
}

// listTasks returns every tracked task, oldest first (matches GET /api/tasks).
export async function listTasks(): Promise<Task[]> {
	const res = await fetch('/api/tasks', { credentials: 'include' });
	if (!res.ok) {
		throw new ApiError(`could not list tasks (${res.status})`);
	}
	return (await res.json()).tasks;
}

// submitCopy enqueues a copy task. It resolves to the server's response
// ({ taskID } on success). Destination collisions are resolved server-side by
// renaming, so the request never needs an overwrite decision and a 409 is never
// produced. It rejects on any other error.
export async function submitCopy(req: CopyTaskRequest): Promise<CopyResponse> {
	const res = await fetch('/api/copy', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `copy failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as CopyResponse;
}

// submitDelete enqueues a delete task for the given paths. Delete is destructive
// and has no destination, so the request carries only paths; the client confirms
// before submitting. It resolves to the server's response ({ taskID } on success)
// and rejects on any other error.
export async function submitDelete(paths: string[]): Promise<DeleteResponse> {
	const res = await fetch('/api/delete', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify({ paths })
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `delete failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as DeleteResponse;
}

// submitMove enqueues a move task for `paths` into `dest`. A move overwrites any
// same-named item at the destination (no conflict handling, no 409), so the
// request carries a placeholder `conflict` that the server ignores. It resolves
// to the server's response ({ taskID } on success) and rejects on any other error.
export async function submitMove(req: MoveTaskRequest): Promise<MoveResponse> {
	const res = await fetch('/api/move', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `move failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as MoveResponse;
}

// submitCompress enqueues a compress task for `paths` into the chosen `format`
// (zip, targz, tar). The archive is written in place by the server, renaming a
// colliding target instead of prompting, so the request carries no overwrite
// decision. It resolves to the server's response ({ taskID } on success) and
// rejects on any other error.
export async function submitCompress(req: CompressTaskRequest): Promise<CompressResponse> {
	const res = await fetch('/api/compress', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `compress failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as CompressResponse;
}

// submitDecompress enqueues a decompress task for a single archive `paths` into
// the requested destination `into` ("", extract here, or an archive stem, extract
// into a sibling folder). The archive is opened read-only and its contents are
// extracted into a folder next to it; the request carries no overwrite decision
// because an existing destination folder is renamed server-side. It resolves to
// the server's response ({ taskID } on success) and rejects on any other error.
export async function submitDecompress(req: DecompressTaskRequest): Promise<DecompressResponse> {
	const res = await fetch('/api/decompress', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `decompress failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as DecompressResponse;
}

// cancelTask asks the server to stop a queued or running task (POST
// /api/tasks/{id}/cancel).
export async function cancelTask(id: string): Promise<void> {
	await postJSON<{ cancelled: string }>(
		'/api/tasks/' + encodeURIComponent(id) + '/cancel',
		{}
	);
}

// --- Upload (chunked + resumable) -------------------------------------------
// Upload sends selected files and directories recursively into a destination
// folder, chunked per file into fixed-size blobs, in a resumable session. A
// session is created (POST /api/uploads), chunks are ingested with PUT
// /api/uploads/{id}/chunk, the session status (GET) reports which chunks are
// present for resume, and DELETE aborts ingestion (chunks kept). Completion
// auto-triggers a tracked finalize op of type "upload"; the session id returned
// by createUpload is reused as the finalize task id from then on.

// UploadFile is one file in an upload manifest: its path relative to the upload
// root, its size in bytes, and the chunk count the client sliced it into
// (informational; the server recomputes it from size and chunk size).
export interface UploadFile {
	rel: string;
	size: number;
	chunks: number;
	// blob is the actual file content, present when the manifest was built from
	// live files (the picker or a drop). Uploads slice real bytes from it; it is
	// absent for purely metadata manifests (e.g. the in-window tree cache, or a
	// resumed session whose bytes live in localStorage), where the manager falls
	// back to the assembled `received` buffer instead.
	blob?: Blob;
}

// UploadManifest describes what an upload carries: the files (in manifest order)
// and the relative directory paths (including empty ones) to recreate at the
// destination on finalize.
export interface UploadManifest {
	files: UploadFile[];
	dirs: string[];
}

// UploadStatus is the resumable snapshot the server returns per session: chunk
// counts and totals let a client re-send only the chunks it has not yet sent.
export interface UploadStatus {
	chunkSize: number;
	totalChunks: number;
	receivedChunks: number;
	totalBytes: number;
	receivedBytes: number;
}

// UploadMeta is the finalize op's meta, carried in the "upload" task's Meta
// field: the phase flag ("finalizing") drives the staged-vs-copy task label and
// files counts the uploaded items.
export interface UploadMeta {
	staging: string;
	dest: string;
	phase: string;
	files: number;
}

export interface UploadResponse {
	taskID?: string;
}

// UploadChunkResponse is what a chunk ingest returns: normally the resumable
// status (so the client can resume), but once the final chunk completes the
// server enqueues the tracked finalize op and returns its taskID instead.
export type UploadChunkResponse = UploadStatus & { taskID?: string };

export interface CreateUploadRequest {
	dest: string;
	chunkSize: number;
	files: UploadFile[];
	dirs: string[];
}

// UploadConfig is the upload sizing the server advertises (GET /api/config):
// the fixed per-file chunk size the client must slice with, and the maximum
// number of chunks to upload at once across every file and session (a global
// limit that caps simultaneous upload requests in the browser). The client
// fetches this once at startup so the client and server agree on the chunk size
// (the server recomputes chunk counts from it) and stay within the concurrency
// limit that avoids resource-exhaustion upload failures.
export interface UploadConfig {
	uploadChunkSize: number;
	maxConcurrentChunkUploads: number;
}

// DEFAULT_CHUNK_SIZE is the per-file chunk size the client uploads with when the
// server does not override it (4 MiB, matching the server default).
export const DEFAULT_CHUNK_SIZE = 4 << 20;

// chunkCountForFile returns the number of fixed-size chunks a file needs at
// `chunkSize`. A non-empty chunk size always yields at least one chunk, so even
// a zero-byte file is uploaded as a single empty chunk.
export function chunkCountForFile(size: number, chunkSize: number): number {
	if (chunkSize <= 0) return 1;
	const n = Math.ceil(size / chunkSize);
	return Math.max(1, n);
}

// createUpload starts a resumable upload session into `dest`. It resolves to the
// server's response ({ taskID } on success) and rejects on any other error. The
// taskID is the session id, reused as the finalize task id once ingestion
// completes.
export async function createUpload(req: CreateUploadRequest): Promise<UploadResponse> {
	const res = await fetch('/api/uploads', {
		method: 'POST',
		credentials: 'include',
		headers: { 'Content-Type': 'application/json' },
		body: JSON.stringify(req)
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `upload failed (${res.status})`;
		throw new ApiError(message);
	}
	return (await res.json()) as UploadResponse;
}

// uploadChunk ingests one chunk for the session. `blob` is the sliced chunk
// (its byte length must equal chunkSize, or be the trailing partial chunk). It
// resolves to the server's resumable status after the write.
export async function uploadChunk(
	taskID: string,
	fileIdx: number,
	chunkIdx: number,
	blob: Blob
): Promise<UploadChunkResponse> {
	const res = await fetch(
		`/api/uploads/${encodeURIComponent(taskID)}/chunk?file=${fileIdx}&index=${chunkIdx}`,
		{
			method: 'PUT',
			credentials: 'include',
			body: blob
		}
	);
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `chunk upload failed (${res.status})`;
		throw new ApiError(message);
	}
	if (res.status === 200 || res.status === 202) {
		return (await res.json()) as UploadStatus;
	}
	return { chunkSize: 0, totalChunks: 0, receivedChunks: 0, totalBytes: 0, receivedBytes: 0 };
}

// getUploadStatus returns the resumable snapshot for a session so a client can
// determine which chunks remain to be sent.
export async function getUploadStatus(taskID: string): Promise<UploadStatus> {
	const res = await fetch(`/api/uploads/${encodeURIComponent(taskID)}`, {
		credentials: 'include'
	});
	if (!res.ok) {
		throw new ApiError(`upload status failed (${res.status})`);
	}
	return (await res.json()) as UploadStatus;
}

// cancelUpload aborts ingestion for a session. Chunks already received are kept
// (the upload stays resumable); the staging dir and session are reclaimed only
// after the server's age threshold.
export async function cancelUpload(taskID: string): Promise<void> {
	const res = await fetch(`/api/uploads/${encodeURIComponent(taskID)}`, {
		method: 'DELETE',
		credentials: 'include'
	});
	if (!res.ok) {
		const payload = (await res.json().catch(() => ({}))) as { error?: unknown };
		const message =
			typeof payload.error === 'string' && payload.error
				? payload.error
				: `upload cancel failed (${res.status})`;
		throw new ApiError(message);
	}
}

// fetchUploadConfig returns the upload sizing the server wants the client to use:
// the fixed per-file chunk size and the maximum number of chunks to upload at
// once. Values fall back to the client defaults (DEFAULT_CHUNK_SIZE, 1) when the
// server omits or clutters them, so a missing endpoint never breaks uploads.
export async function fetchUploadConfig(): Promise<UploadConfig> {
	const res = await fetch('/api/config', { credentials: 'include' });
	if (!res.ok) {
		throw new ApiError(`could not load upload config (${res.status})`);
	}
	const data = (await res.json()) as {
		uploadChunkSize?: number;
		maxConcurrentChunkUploads?: number;
	};
	const chunkSize =
		typeof data.uploadChunkSize === 'number' && data.uploadChunkSize > 0
			? data.uploadChunkSize
			: DEFAULT_CHUNK_SIZE;
	const maxConcurrent =
		typeof data.maxConcurrentChunkUploads === 'number' && data.maxConcurrentChunkUploads > 0
			? data.maxConcurrentChunkUploads
			: 1;
	return { uploadChunkSize: chunkSize, maxConcurrentChunkUploads: maxConcurrent };
}
