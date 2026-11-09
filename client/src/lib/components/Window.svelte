<script lang="ts">
	import { onMount } from 'svelte';
	import { closeWindow, focusWindow, type AppWindow } from '$lib/store/windows';
	import { fetchListing, type Entry } from '$lib/fs/api';
	import FileIcon from './FileIcon.svelte';

	let { window: win }: { window: AppWindow } = $props();

	// --- geometry: drag (header) and resize (bottom-right handle) ---
	let pos = $state<{ x: number; y: number } | null>(null);
	let dragging = $state(false);
	let resizeStart = $state({ x: 0, y: 0, w: 0, h: 0 });
	let resizing = $state(false);
	// svelte-ignore state_referenced_locally
	let size = $state({ width: win.width, height: win.height });

	const MIN_W = 480;
	const MIN_H = 300;

	function addMoveListeners() {
		window.addEventListener('pointermove', onPointerMove);
		window.addEventListener('pointerup', onPointerUp);
	}
	function removeMoveListeners() {
		window.removeEventListener('pointermove', onPointerMove);
		window.removeEventListener('pointerup', onPointerUp);
	}
	function onHeaderPointerDown(e: PointerEvent) {
		if (pos === null) pos = { x: win.x, y: win.y };
		dragStart = { x: e.clientX, y: e.clientY };
		dragOrigin = pos ? { ...pos } : { x: win.x, y: win.y };
		dragging = true;
		addMoveListeners();
	}
	let dragStart = $state({ x: 0, y: 0 });
	let dragOrigin = $state<{ x: number; y: number } | null>(null);
	function onResizePointerDown(e: PointerEvent) {
		e.stopPropagation();
		resizeStart = { x: e.clientX, y: e.clientY, w: size.width, h: size.height };
		resizing = true;
		addMoveListeners();
	}
	function onPointerMove(e: PointerEvent) {
		if (resizing) {
			size.width = Math.max(MIN_W, resizeStart.w + (e.clientX - resizeStart.x));
			size.height = Math.max(MIN_H, resizeStart.h + (e.clientY - resizeStart.y));
		} else if (dragOrigin) {
			pos = {
				x: Math.max(0, dragOrigin.x + (e.clientX - dragStart.x)),
				y: Math.max(0, dragOrigin.y + (e.clientY - dragStart.y))
			};
		}
	}
	function onPointerUp() {
		dragging = false;
		resizing = false;
		removeMoveListeners();
	}

	// --- path helpers ---
	function dirname(p: string): string {
		const i = p.lastIndexOf('/');
		return i <= 0 ? '/' : p.slice(0, i);
	}
	function basename(p: string): string {
		const i = p.lastIndexOf('/');
		return i <= 0 ? p : p.slice(i + 1);
	}

	// --- data: current listing, per-path cache, tree expansion, sort ---
	let entries = $state<Entry[]>([]);
	let listingCache = $state<Map<string, { entries: Entry[] }>>(new Map());
	let expanded = $state<Set<string>>(new Set());
	let currentPath = $state('');
	let title = $state('');
	let loading = $state(false);
	let error = $state<string | null>(null);

	type SortKey = 'name' | 'size' | 'created' | 'updated';
	const SORT_KEYS: SortKey[] = ['name', 'size', 'created', 'updated'];
	let sortKey = $state<SortKey>('name');
	let sortDir = $state<'asc' | 'desc'>('asc');

	function toggleSort(key: SortKey) {
		if (sortKey === key) {
			sortDir = sortDir === 'asc' ? 'desc' : 'asc';
		} else {
			sortKey = key;
			sortDir = 'asc';
		}
	}

	// Load a directory. When asMain is true the listing replaces the current view
	// (and status totals); otherwise it only populates the cache for the tree.
	async function load(path: string, asMain: boolean) {
		try {
			loading = true;
			error = null;
			let cached = listingCache.get(path);
			if (!cached) {
				const listing = await fetchListing(path);
				cached = { entries: listing.entries };
				listingCache.set(path, cached);
			}
			if (asMain) {
				entries = cached.entries;
			}
			expanded.add(path);
		} catch (err) {
			error = err instanceof Error ? err.message : 'could not open this folder';
		} finally {
			loading = false;
		}
	}

	// The path chain from the window's root down to `path`, inclusive.
	function chainTo(path: string): string[] {
		const root = win.rootPath;
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

	function currentTitle(path: string): string {
		return path === win.rootPath ? win.name : basename(path);
	}

	// Navigate (double-click) into a directory: expand the branch in the tree and
	// refresh the main view.
	async function navigate(dir: string) {
		const chain = chainTo(dir);
		for (const d of chain) expanded.add(d);
		await Promise.all(chain.map((d) => load(d, false)));
		await load(dir, true);
		currentPath = dir;
		title = currentTitle(dir);
	}

	// Single-click a tree node: expand or collapse it (loading children on expand).
	function toggleExpand(dir: string) {
		if (expanded.has(dir)) {
			expanded.delete(dir);
		} else {
			expanded.add(dir);
			void load(dir, false);
		}
	}

	// Subdirectories of a directory, from the cache.
	function childDirs(dir: string): Entry[] {
		const cached = listingCache.get(dir);
		if (!cached) return [];
		return cached.entries.filter((e) => e.type === 'dir');
	}

	// Sorted view of the current entries. For name, folders sort first; the other
	// keys sort purely by value.
	const rows = $derived(
		[...entries].sort((a, b) => {
			const mul = sortDir === 'asc' ? 1 : -1;
			if (sortKey === 'name') {
				if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
				return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }) * mul;
			}
			let cmp = 0;
			if (sortKey === 'size') cmp = a.size - b.size;
			else if (sortKey === 'created') cmp = Date.parse(a.created) - Date.parse(b.created);
			else cmp = Date.parse(b.modified) - Date.parse(a.modified);
			return cmp * mul;
		})
	);

	function headerLabel(key: SortKey): string {
		switch (key) {
			case 'name':
				return 'Name';
			case 'size':
				return 'Size';
			case 'created':
				return 'Created';
			case 'updated':
				return 'Updated';
		}
	}

	function arrow(dir: string): string {
		const kids = childDirs(dir);
		if (kids.length === 0) return '·';
		return expanded.has(dir) ? '▾' : '▸';
	}

	// --- formatting helpers ---
	function formatSize(n: number): string {
		if (n < 1024) return `${n} B`;
		const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
		let v = n / 1024;
		let i = 0;
		while (v >= 1024 && i < units.length - 1) {
			v /= 1024;
			i++;
		}
		return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
	}
	function formatDate(iso: string): string {
		const d = new Date(iso);
		if (isNaN(d.getTime())) return '';
		return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
	}

	onMount(() => {
		currentPath = win.rootPath;
		title = win.name;
		void navigate(win.rootPath);
	});
</script>

<div
	class="window"
	class:minimized={win.minimized}
	role="dialog"
	tabindex="0"
	aria-label="{title}"
	style="left:{pos ? pos.x : win.x}px; top:{pos ? pos.y : win.y}px; width:{size.width}px; height:{size.height}px; z-index:{win.zIndex};"
	onmousedown={() => focusWindow(win.id)}
>
	<header class="header" onpointerdown={onHeaderPointerDown} aria-roledescription="draggable" role="application">
		<span class="logo" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
				<path d="M9 12h6M9 15h4" />
			</svg>
		</span>
		<span class="title" title={`${title} — ${currentPath}`}>{title}</span>
		<button class="close" type="button" onclick={() => closeWindow(win.id)} aria-label="Close window">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
				<path d="M7 7l10 10M17 7L7 17" />
			</svg>
		</button>
	</header>

	<main class="body">
		<aside class="tree" aria-label="Folder tree">
			{#if error && entries.length === 0}
				<div class="tree-error">{error}</div>
			{:else}
				<ul class="tree-root">{#snippet treeRow(dir: string, depth: number)}
					<li class="trow" style="padding-left:{8 + depth * 14}px">
						<button type="button" class="tnode" onclick={() => toggleExpand(dir)} ondblclick={() => navigate(dir)}>
							<span class="twisty" aria-hidden="true">{arrow(dir)}</span>
							<span class="ticon" aria-hidden="true">
								<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
									<path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
								</svg>
							</span>
							<span class="tname" title={dir}>{basename(dir)}</span>
						</button>
						{#if expanded.has(dir)}
							{#each childDirs(dir).sort((a, b) => a.name.localeCompare(b.name)) as child (child.path)}
								{treeRow(child.path, depth + 1)}
							{/each}
						{/if}
					</li>
				{/snippet}</ul>
			{/if}
		</aside>

		<div class="content">
			{#if loading && entries.length === 0}
				<div class="empty">Loading…</div>
			{:else if error}
				<div class="error">{error}</div>
			{:else if rows.length === 0}
				<div class="empty">This folder is empty.</div>
			{:else}
				<table class="list">
					<thead>
						<tr>
							{#each SORT_KEYS as key (key)}
								<th class:active={sortKey === key} onclick={() => toggleSort(key)} role="button" tabindex="0"
									onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') ? toggleSort(key) : undefined}>
									<span class="thc">{headerLabel(key)}<span class="sort-arrow" aria-hidden="true">{sortKey === key ? (sortDir === 'asc' ? ' ▲' : ' ▼') : ''}</span></span>
								</th>
							{/each}
						</tr>
					</thead>
					<tbody>
						{#each rows as row (row.path)}
							<tr class="row" ondblclick={() => (row.type === 'dir' && navigate(row.path))}>
								<td class="name">
									<span class="fg" aria-hidden="true">
										<FileIcon type={row.type === 'dir' ? 'folder' : 'doc'} />
									</span>
									<span class="rname">{row.name}</span>
								</td>
								<td class="size">{formatSize(row.size)}</td>
								<td class="date">{formatDate(row.created)}</td>
								<td class="date">{formatDate(row.modified)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			{/if}
		</div>
	</main>

	<footer class="status">
		<span class="spath" aria-hidden="true">
			<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
			</svg>
		</span>
		<span class="spath-val" title={currentPath}>{currentPath}</span>
		<span class="count">{rows.length} item{rows.length === 1 ? '' : 's'}</span>
	</footer>

	<!-- svelte-ignore a11y_no_static_element_interactions -->
	<div class="resize" onpointerdown={onResizePointerDown} aria-label="Resize window"></div>
</div>

<style>
	.window {
		position: absolute;
		display: flex;
		flex-direction: column;
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 14px;
		box-shadow: var(--shadow-window);
		overflow: hidden;
		min-width: 360px;
		min-height: 260px;
	}
	.window.minimized {
		display: none;
	}
	.header {
		display: flex;
		align-items: center;
		gap: 10px;
		height: 48px;
		padding: 0 10px 0 12px;
		background: var(--surface-soft);
		border-bottom: 1px solid var(--border);
		color: var(--text);
		cursor: grab;
		flex: none;
	}
	.header:active {
		cursor: grabbing;
	}
	.logo {
		display: grid;
		place-items: center;
		width: 26px;
		height: 26px;
		border-radius: 8px;
		background: linear-gradient(135deg, var(--accent), var(--accent-2));
		color: #fff;
	}
	.logo svg {
		width: 15px;
		height: 15px;
	}
	.title {
		font-size: 14px;
		font-weight: 600;
	}
	.close {
		display: grid;
		place-items: center;
		margin-left: auto;
		width: 30px;
		height: 30px;
		background: transparent;
		border: none;
		border-radius: 8px;
		color: var(--text-muted);
		cursor: pointer;
	}
	.close:hover {
		background: var(--alert);
		color: #fff;
	}
	.close svg {
		width: 14px;
		height: 14px;
	}
	.body {
		flex: 1;
		display: flex;
		min-height: 0;
	}
	.tree {
		flex: none;
		width: 220px;
		border-right: 1px solid var(--border);
		background: var(--surface-soft);
		overflow: auto;
		padding: 8px 0;
	}
	.tree-root {
		list-style: none;
		margin: 0;
		padding: 0;
	}
	.trow {
		margin: 0;
	}
	.tnode {
		display: flex;
		align-items: center;
		gap: 7px;
		width: 100%;
		background: transparent;
		border: none;
		color: var(--text);
		font: inherit;
		font-size: 13px;
		padding: 6px 10px;
		cursor: pointer;
		text-align: left;
	}
	.tnode:hover {
		background: var(--surface-inset);
	}
	.twisty {
		width: 12px;
		text-align: center;
		font-size: 11px;
		color: var(--text-faint);
		flex: none;
	}
	.ticon {
		flex: none;
		color: var(--accent-strong);
	}
	.ticon svg {
		width: 15px;
		height: 15px;
	}
	.tname {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.tree-error {
		padding: 10px 12px;
		font-size: 12px;
		color: var(--alert);
	}
	.content {
		flex: 1;
		min-width: 0;
		overflow: auto;
		position: relative;
	}
	.empty,
	.error {
		position: absolute;
		inset: 0;
		display: grid;
		place-items: center;
		color: var(--text-faint);
		font-size: 13px;
	}
	.error {
		color: var(--alert);
		padding: 14px;
		text-align: center;
	}
	.list {
		border-collapse: collapse;
		width: 100%;
		font-size: 13px;
	}
	thead th {
		position: sticky;
		top: 0;
		background: var(--surface-soft);
		border-bottom: 1px solid var(--border);
		padding: 0 8px;
		height: 30px;
		text-align: left;
		font-weight: 600;
		color: var(--text-muted);
		user-select: none;
		cursor: pointer;
		white-space: nowrap;
	}
	thead th.active {
		color: var(--accent-strong);
	}
	.sort-arrow {
		margin-left: 4px;
		font-size: 10px;
		color: var(--accent-strong);
	}
	.row {
		cursor: default;
	}
	.row td {
		padding: 7px 8px;
		border-bottom: 1px solid var(--surface-inset);
	}
	.row:hover td {
		background: var(--surface-soft);
	}
	.name {
		display: flex;
		align-items: center;
		gap: 10px;
		min-width: 0;
	}
	.fg {
		flex: none;
	}
	.rname {
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.size,
	.date {
		width: 1%;
		white-space: nowrap;
		text-align: right;
		color: var(--text-muted);
	}
	.status {
		flex: none;
		display: flex;
		align-items: center;
		gap: 8px;
		height: 30px;
		padding: 0 12px;
		background: var(--surface-inset);
		border-top: 1px solid var(--border);
		color: var(--text-muted);
		font-size: 12px;
	}
	.spath {
		display: grid;
		place-items: center;
		color: var(--accent-strong);
	}
	.spath svg {
		width: 14px;
		height: 14px;
	}
	.spath-val {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 12px;
		min-width: 0;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
		flex: 1;
	}
	.count {
		margin-left: auto;
		white-space: nowrap;
	}
	.resize {
		position: absolute;
		right: 0;
		bottom: 0;
		width: 16px;
		height: 16px;
		cursor: nwse-resize;
	}
	.resize::after {
		content: '';
		position: absolute;
		right: 2px;
		bottom: 2px;
		width: 8px;
		height: 8px;
		border-right: 2px solid var(--text-faint);
		border-bottom: 2px solid var(--text-faint);
		border-radius: 0 0 3px 0;
	}
</style>
