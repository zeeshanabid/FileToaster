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

export interface TaskListResponse {
	tasks: Task[];
}

export interface CopyResponse {
	taskID?: string;
}

export interface DeleteResponse {
	taskID?: string;
}

export interface CopyTaskRequest {
	paths: string[];
	dest: string;
	conflict: string;
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

// cancelTask asks the server to stop a queued or running task (POST
// /api/tasks/{id}/cancel).
export async function cancelTask(id: string): Promise<void> {
	await postJSON<{ cancelled: string }>(
		'/api/tasks/' + encodeURIComponent(id) + '/cancel',
		{}
	);
}
