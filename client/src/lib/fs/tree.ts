// Left-pane folder tree state: per-window expansion, a lazy per-path child
// cache, and per-directory loading/error flags. Kept in its own module so the
// navigation logic can be unit-tested without rendering Svelte. The singleton
// at the bottom is shared by every window's tree view; its loader defaults to
// the real /api/fs fetch but can be replaced (e.g. in tests).

import { fetchListing, type Entry, type Listing } from '$lib/fs/api';

function basename(p: string): string {
	const i = p.lastIndexOf('/');
	return i <= 0 ? p : p.slice(i + 1);
}

function byName(a: Entry, b: Entry): number {
	return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
}

// chainTo returns the path chain from `root` down to `path`, inclusive of both
// ends. Anything not actually rooted at `root` is treated as relative to the
// root (basename fallback) so the tree never renders a node outside the window.
export function chainTo(path: string, root: string): string[] {
	if (path === root) return [root];
	const rel = path.startsWith(root + '/') ? path.slice(root.length + 1) : basename(path);
	const parts = rel.split('/').filter(Boolean);
	const chain = [root];
	let acc = root;
	for (const part of parts) {
		acc = acc + '/' + part;
		chain.push(acc);
	}
	return chain;
}

export class FileTree {
	private loader: (path: string) => Promise<Listing>;
	private cache = new Map<string, Listing>();
	private inflight = new Map<string, Promise<Listing>>();
	private expanded = new Map<string, Set<string>>();
	private loading = new Set<string>();
	private error = new Map<string, string>();

	constructor(loader = fetchListing) {
		this.loader = loader;
	}

	has(path: string): boolean {
		return this.cache.has(path);
	}

	// childEntries returns the sorted sub-directories of a directory from cache.
	childEntries(path: string): Entry[] {
		const listing = this.cache.get(path);
		if (!listing) return [];
		return listing.entries.filter((e) => e.type === 'dir').sort(byName);
	}

	isExpanded(windowId: string, path: string): boolean {
		return this.expanded.get(windowId)?.has(path) ?? false;
	}

	isLoading(path: string): boolean {
		return this.loading.has(path);
	}

	getError(path: string): string | null {
		return this.error.get(path) ?? null;
	}

	// list(path) resolves to the cached listing or loads it exactly once. A
	// rejected load is remembered so the tree can show a per-directory error
	// node without blocking the rest of the tree.
	list(path: string): Promise<Listing> {
		const cached = this.cache.get(path);
		if (cached) {
			this.error.delete(path);
			return Promise.resolve(cached);
		}
		const pending = this.inflight.get(path);
		if (pending) return pending;
		this.loading.add(path);
		const promise = this.loader(path)
			.then((listing) => {
				this.cache.set(path, listing);
				this.error.delete(path);
				return listing;
			})
			.catch((err) => {
				const message = err instanceof Error ? err.message : 'could not open this folder';
				this.error.set(path, message);
				throw new Error(message);
			})
			.finally(() => {
				this.inflight.delete(path);
				this.loading.delete(path);
			});
		this.inflight.set(path, promise);
		return promise;
	}

	private setExpanded(windowId: string, path: string, value: boolean): void {
		let set = this.expanded.get(windowId);
		if (!set) {
			set = new Set();
			this.expanded.set(windowId, set);
		}
		if (value) set.add(path);
		else set.delete(path);
	}

	// toggleExpand flips a single node; expanding triggers a lazy child load.
	async toggleExpand(windowId: string, path: string): Promise<void> {
		const willExpand = !this.isExpanded(windowId, path);
		this.setExpanded(windowId, path, willExpand);
		if (willExpand) await this.list(path);
	}

	// expandChain expands root..path (inclusive) and loads children for each node
	// so the full branch is visible after navigation. A load that fails for one
	// node is recorded as that node's error (the tree renders an error child) but
	// does not abort the rest of the branch.
	async expandChain(windowId: string, path: string, root: string): Promise<void> {
		const chain = chainTo(path, root);
		for (const dir of chain) {
			this.setExpanded(windowId, dir, true);
			await this.list(dir).catch(() => {});
		}
	}

	// navigate expands the root..path branch and resolves to the target listing
	// (used to render the main view) in one round trip.
	async navigate(windowId: string, path: string, root: string): Promise<Listing> {
		await this.expandChain(windowId, path, root);
		return this.list(path);
	}

	// refresh reloads a single path from the server, bypassing cache (used by the
	// toolbar "reload" button so changes made outside the UI become visible).
	// Expansion of other nodes is left untouched.
	refresh(path: string): Promise<Listing> {
		this.cache.delete(path);
		this.error.delete(path);
		this.loading.delete(path);
		return this.list(path);
	}

	resetWindow(windowId: string): void {
		this.expanded.delete(windowId);
	}

	resetAll(): void {
		this.cache.clear();
		this.inflight.clear();
		this.expanded.clear();
		this.loading.clear();
		this.error.clear();
	}
}

export const tree = new FileTree();
