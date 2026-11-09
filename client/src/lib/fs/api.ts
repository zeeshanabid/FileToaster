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
