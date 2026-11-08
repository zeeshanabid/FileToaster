// A root is a top-level folder shown on the desktop.
// In the full app these come from the server config's `allowed_paths`.
// This POC hardcodes a demo list; no backend calls are made.
export interface Root {
	id: string;
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
