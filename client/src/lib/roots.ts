// A root is a top-level folder shown on the desktop. In the full app these come
// from the server config's `allowed_paths`; this module fetches them from
// `POST /api/fs/roots`. The POC hardcoded a demo list — that fallback is kept so
// the desktop still renders if the server is unreachable.
export interface Root {
  id: string;
  name: string;
  path: string;
}

interface ServerRoot {
  name: string;
  path: string;
}

export const ROOTS: Root[] = [
  { id: 'documents', name: 'Documents', path: '/data/documents' },
  { id: 'photos', name: 'Photos', path: '/data/photos' },
  { id: 'downloads', name: 'Downloads', path: '/data/downloads' },
  { id: 'share', name: 'Shared', path: '/data/share' },
  { id: 'media', name: 'Media', path: '/data/media' }
];

// fetchRoots returns the configured allowed paths. It maps each entry to a Root
// keyed by its path so multiple icons never collide. Sent as an authenticated
// POST; on network failure it rejects; callers decide whether to fall back to
// demo data.
export async function fetchRoots(): Promise<Root[]> {
  const res = await fetch('/api/fs/roots', {
    method: 'POST',
    credentials: 'include',
  });
  if (!res.ok) {
    throw new Error(`failed to load folders (${res.status})`);
  }
  const body = (await res.json().catch(() => ({}))) as { allowed_paths?: ServerRoot[] };
  const list = Array.isArray(body.allowed_paths) ? body.allowed_paths : [];
  return list.map((r) => ({ id: r.path, name: r.name, path: r.path }));
}
