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
