import { describe, it, expect, vi } from 'vitest';
import { FileTree, chainTo } from '$lib/fs/tree';
import type { Entry, Listing } from '$lib/fs/api';

// --- in-memory fixture: a small directory tree -------------------------------
const FS: Record<string, Entry[]> = {
	'/root': [
		{ path: '/root/documents', name: 'documents', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/photos', name: 'photos', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/notes.txt', name: 'notes.txt', type: 'file', size: 3, created: '', modified: '' }
	],
	'/root/documents': [
		{ path: '/root/documents/work', name: 'work', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/documents/personal', name: 'personal', type: 'dir', size: 0, created: '', modified: '' }
	],
	'/root/documents/work': [
		{ path: '/root/documents/work/report.txt', name: 'report.txt', type: 'file', size: 10, created: '', modified: '' }
	],
	'/root/documents/personal': [
		{ path: '/root/documents/personal/notes.txt', name: 'notes.txt', type: 'file', size: 5, created: '', modified: '' }
	],
	'/root/photos': [
		{ path: '/root/photos/2023', name: '2023', type: 'dir', size: 0, created: '', modified: '' }
	],
	'/root/photos/2023': [
		{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file', size: 20, created: '', modified: '' }
	]
};

// loader(paths?) returns a fake fetch that serves the fixture, optionally
// restricting the served paths (any other path rejects). It records the number
// of times each path was requested so caching / dedupe can be asserted.
function makeLoader(served?: string[]) {
	const calls = new Map<string, number>();
	const fn = vi.fn(async (path: string): Promise<Listing> => {
		calls.set(path, (calls.get(path) ?? 0) + 1);
		if (served && !served.includes(path)) {
			throw new Error(`could not open ${path}`);
		}
		return { path, entries: FS[path] ?? [] };
	});
	return { fn, calls };
}

const ROOT = '/root';
const W = 'w1';

function expandedSet(f: FileTree): Set<string> | undefined {
	return (f as unknown as { expanded?: Map<string, Set<string>> }).expanded?.get(W);
}

describe('chainTo', () => {
	it('returns [root] when path is the root', () => {
		expect(chainTo(ROOT, ROOT)).toEqual([ROOT]);
	});
	it('builds the full chain root..path', () => {
		expect(chainTo('/root/documents/work', ROOT)).toEqual([
			'/root',
			'/root/documents',
			'/root/documents/work'
		]);
	});
	it('stays inside the root for an unrooted path', () => {
		expect(chainTo('/other/work', ROOT)).toEqual([ROOT, '/root/work']);
	});
});

describe('FileTree', () => {
	it('expands a node and lazily loads its children once', async () => {
		const f = new FileTree(makeLoader().fn);
		expect(f.isExpanded(W, ROOT)).toBe(false);
		await f.toggleExpand(W, ROOT);
		expect(f.isExpanded(W, ROOT)).toBe(true);
		expect(f.childEntries(ROOT).map((e) => e.name)).toEqual(['documents', 'photos']);
	});

	it('collapses a node without reloading', async () => {
		const f = new FileTree(makeLoader().fn);
		await f.toggleExpand(W, ROOT);
		await f.toggleExpand(W, ROOT);
		expect(f.isExpanded(W, ROOT)).toBe(false);
	});

	it('caches children per path and does not re-fetch', async () => {
		const { fn, calls } = makeLoader();
		const f = new FileTree(fn);
		await f.toggleExpand(W, ROOT);
		await f.toggleExpand(W, ROOT);
		await f.toggleExpand(W, ROOT);
		expect(calls.get(ROOT)).toBe(1);
		expect(f.childEntries(ROOT).length).toBe(2);
	});

	it('navigate expands root..path and returns the target listing', async () => {
		const { fn, calls } = makeLoader();
		const f = new FileTree(fn);
		const listing = await f.navigate(W, '/root/documents/work', ROOT);
		expect(listing.path).toBe('/root/documents/work');
		expect(f.isExpanded(W, ROOT)).toBe(true);
		expect(f.isExpanded(W, '/root/documents')).toBe(true);
		expect(f.isExpanded(W, '/root/documents/work')).toBe(true);
		expect(f.childEntries('/root/documents/work').length).toBe(0);
		// Every node in the branch was loaded exactly once (the final list(target)
		// serves the main view from the same cache the expansion already filled).
		expect(calls.get('/root')).toBe(1);
		expect(calls.get('/root/documents')).toBe(1);
		expect(calls.get('/root/documents/work')).toBe(1);
	});

	it('only expands nodes inside the root when navigating deep', async () => {
		const f = new FileTree(makeLoader().fn);
		await f.navigate(W, '/root/photos/2023', ROOT);
		expect([...expandedSet(f)!].sort()).toEqual([
			'/root',
			'/root/photos',
			'/root/photos/2023'
		]);
	});

	it('records a per-directory error (non-fatal) when a folder cannot be listed', async () => {
		const { fn } = makeLoader(['/root', '/root/documents', '/root/documents/work']);
		const f = new FileTree(fn);
		await f.navigate(W, '/root/documents/work', ROOT);
		// Expanding a leaf-like node that the server cannot list records the error
		// without throwing past the caller.
		await f.toggleExpand(W, '/root/documents/missing').catch(() => {});
		expect(f.getError('/root/documents/missing')).toBe('could not open /root/documents/missing');
	});

	it('clears the error once a path is loaded successfully', async () => {
		const f = new FileTree(makeLoader().fn);
		await f.list('/root/documents/work');
		expect(f.getError('/root/documents/work')).toBeNull();
	});

	it('reports loading while a load is in flight and clears it after', async () => {
		let resolve!: (v: Listing) => void;
		const pending = new Promise<Listing>((r) => {
			resolve = r;
		});
		const f = new FileTree(async () => pending);
		const p = f.list('/root/documents');
		expect(f.isLoading('/root/documents')).toBe(true);
		resolve({ path: '/root/documents', entries: FS['/root/documents']! });
		await p;
		expect(f.isLoading('/root/documents')).toBe(false);
	});

	it('dedupes concurrent loads of the same path', async () => {
		const { fn, calls } = makeLoader();
		const f = new FileTree(fn);
		await Promise.all([
			f.navigate(W, '/root/documents/work', ROOT),
			f.navigate(W, '/root/documents/work', ROOT)
		]);
		// The shared path /root/documents is requested only once per concurrent batch.
		expect(calls.get('/root/documents')).toBe(1);
	});

	it('resetWindow clears only that window\'s expansion', async () => {
		const f = new FileTree(makeLoader().fn);
		await f.navigate(W, '/root/documents', ROOT);
		f.resetWindow(W);
		expect(f.isExpanded(W, ROOT)).toBe(false);
	});
});
