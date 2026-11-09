<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import { derived } from 'svelte/store';
	import {
		closeWindow,
		focusWindow,
		updateWindow,
		windows,
		type AppWindow
	} from '$lib/store/windows';
	import { type Entry, mkdirDirectory } from '$lib/fs/api';
	import { tree } from '$lib/fs/tree';
	import FileIcon from './FileIcon.svelte';

	let { window: winProp }: { window: AppWindow } = $props();

	// Subscribe to this window's slice of the store so the tree highlight, title
	// and main view all react to navigation performed anywhere (tree click,
	// double-click, up button). Both views observe the same store, so they cannot
	// diverge. `winProp` only supplies the values set once at window creation.
	const selection = derived(windows, ($all) => $all.find((w) => w.id === winProp.id) ?? null);
	let win = $state<AppWindow | null>(null);
	let stop: (() => void) | undefined;
	onMount(() => {
		tree.resetWindow(winProp.id);
		stop = selection.subscribe((w) => {
			win = w;
		});
		void navigate(winProp.path);
	});
	onDestroy(() => stop?.());

	// `tree` is an external cache; Svelte only re-renders when a $state changes.
	// Bump `frame` after every tree mutation so the fold/outlines and per-directory
	// loading/error nodes update even when the store path did not change (twisty).
	let frame = $state(0);

	// --- geometry: drag (header) and resize (bottom-right handle) ---
	let pos = $state<{ x: number; y: number } | null>(null);
	let dragging = $state(false);
	let resizeStart = $state({ x: 0, y: 0, w: 0, h: 0 });
	let resizing = $state(false);
	// svelte-ignore state_referenced_locally
	let size = $state({ width: winProp.width, height: winProp.height });

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
		if (pos === null) pos = { x: winProp.x, y: winProp.y };
		dragStart = { x: e.clientX, y: e.clientY };
		dragOrigin = pos ? { ...pos } : { x: winProp.x, y: winProp.y };
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

	// --- data: main listing view state (derived from the store's `path`) ---
	let entries = $state<Entry[]>([]);
	let mainLoading = $state(false);
	let mainError = $state<string | null>(null);

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

	// The tree is anchored at the window's root and never re-rooted on navigation.
	// The title bar shows the currently opened folder; the store's `name` stays as
	// the window's display name (the root label) and never changes after open.
	let title = $derived(
		win ? (win.path === win.rootPath ? win.name : basename(win.path)) : winProp.name
	);

	// Navigate to `path`: it becomes the active folder (store `path`), the tree
	// expands root..path (lazy child-load, cached), and the main listing reloads.
	async function navigate(path: string): Promise<void> {
		if (!win) return;
		const root = win.rootPath;
		if (path !== root && !path.startsWith(root + '/')) return;
		try {
			mainLoading = true;
			mainError = null;
			const listing = await tree.navigate(win.id, path, root);
			entries = listing.entries;
			updateWindow(win.id, { path });
		} catch (err) {
			mainError = err instanceof Error ? err.message : 'could not open this folder';
		} finally {
			mainLoading = false;
		}
	}

	// Move up one directory from the currently opened folder (the toolbar "up"
	// button). Never navigates above the window's root path.
	function goUp() {
		if (!win || !win.path || win.path === win.rootPath) return;
		const parent = dirname(win.path);
		if (parent !== win.rootPath && !parent.startsWith(win.rootPath + '/')) {
			void navigate(win.rootPath);
			return;
		}
		void navigate(parent);
	}

	// Reload the currently opened directory from the server, bypassing cache, so
	// changes made outside the UI become visible without navigating away.
	async function reload() {
		if (!win) return;
		try {
			mainLoading = true;
			mainError = null;
			const listing = await tree.refresh(win.path);
			entries = listing.entries;
			frame += 1;
		} catch (err) {
			mainError = err instanceof Error ? err.message : 'could not reload this folder';
		} finally {
			mainLoading = false;
		}
	}

	// In-place directory naming in the toolbar. `editingNew` toggles the input;
	// Enter creates and navigates into the folder, Escape/blur discards.
	let editingNew = $state(false);
	let newName = $state('');
	let inputEl: HTMLInputElement | null = $state(null);
	let isAtRoot = $derived(win ? win.path === win.rootPath : true);

	function startNewDir() {
		if (!win) return;
		newName = '';
		editingNew = true;
	}
	// Focus the in-place input once it is rendered (it does not exist while
	// `editingNew` is false, so a plain call in startNewDir would be a no-op).
	$effect(() => {
		if (editingNew) inputEl?.focus();
	});
	function cancelNewDir() {
		if (!editingNew) return;
		editingNew = false;
		newName = '';
	}
	async function confirmNewDir() {
		const name = newName.trim();
		if (!name || !win || !editingNew) return;
		editingNew = false;
		const parent = win.path;
		try {
			await mkdirDirectory(parent, name);
			void navigate(parent + '/' + name);
		} catch (err) {
			mainError = err instanceof Error ? err.message : 'could not create folder';
		}
		newName = '';
	}

	// Snapshot of the reactive shape the tree row renders from. Reading `frame`
	// (bumped on every tree mutation) makes this snapshot re-subscribe to the
	// shared `tree` cache, which is not itself reactive, so the row re-renders
	// when expansion, loading, or error state changes.
	function rowSnapshot(dir: string) {
		const version = frame;
		void version;
		const active = win?.path;
		void active;
		return {
			version,
			expanded: tree.isExpanded(winProp.id, dir),
			loading: tree.isLoading(dir),
			error: tree.getError(dir),
			kids: tree.isExpanded(winProp.id, dir) ? tree.childEntries(dir) : []
		};
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

	// Tree node label: the window's display name at the root, folder name below.
	function label(dir: string): string {
		if (!win) return basename(dir);
		return dir === win.rootPath ? win.name : basename(dir);
	}

	function arrow(dir: string): string {
		if (!win) return '·';
		if (tree.isLoading(dir)) return '▾';
		const kids = tree.childEntries(dir);
		if (kids.length === 0) return '·';
		return tree.isExpanded(win.id, dir) ? '▾' : '▸';
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
		return `${v.toFixed(v < 1 ? 1 : 0)} ${units[i]}`;
	}
	function formatDate(iso: string): string {
		const d = new Date(iso);
		if (isNaN(d.getTime())) return '';
		return d.toLocaleString(undefined, {
			year: 'numeric',
			month: 'short',
			day: 'numeric',
			hour: '2-digit',
			minute: '2-digit',
			second: '2-digit'
		});
	}

	// Keep the highlighted active row scrolled into view on navigation.
	$effect(() => {
		const active = win?.path;
		if (!active) return;
		requestAnimationFrame(() => {
			const el = document.querySelector<HTMLElement>(
				`[data-tree-active="${CSS.escape(active)}"]`
			);
			if (el && typeof el.scrollIntoView === 'function') {
				el.scrollIntoView({ block: 'nearest' });
			}
		});
	});
</script>

<div
	class="window"
	class:minimized={winProp.minimized}
	role="dialog"
	tabindex="0"
	aria-label={title}
	style="left:{pos ? pos.x : winProp.x}px; top:{pos ? pos.y : winProp.y}px; width:{size.width}px; height:{size.height}px; z-index:{winProp.zIndex};"
	onmousedown={() => focusWindow(winProp.id)}
>
	<header class="header" onpointerdown={onHeaderPointerDown} aria-roledescription="draggable" role="application">
		<span class="logo" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
				<path d="M9 12h6M9 15h4" />
			</svg>
		</span>
		<span class="title" title={`${title} — ${winProp.path}`}>{title}</span>
		<button class="close" type="button" onclick={() => closeWindow(winProp.id)} aria-label="Close window">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
				<path d="M7 7l10 10M17 7L7 17" />
			</svg>
		</button>
	</header>

	<div class="toolbar">
		<button class="tool" type="button" title="New folder" aria-label="New folder" onclick={() => startNewDir()} disabled={editingNew}>
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
				<path d="M12 11v6M9 14h6" />
			</svg>
		</button>
		<button class="tool" type="button" title="Reload" aria-label="Reload" onclick={() => reload()}>
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M21 12a9 9 0 1 1-3.2-6.9" />
				<path d="M21 4v5h-5" />
			</svg>
		</button>
		<button class="up" type="button" title="Up one directory" aria-label="Up one directory" disabled={isAtRoot} onclick={() => goUp()}>
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M12 19V5M5 12l7-7 7 7" />
			</svg>
		</button>
		{#if editingNew}
			<form class="new" onsubmit={() => confirmNewDir()}>
				<input class="new-input" bind:this={inputEl} bind:value={newName} autocomplete="off" spellcheck="false"
					placeholder="New folder"
					onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); confirmNewDir(); } else if (e.key === 'Escape') { e.preventDefault(); cancelNewDir(); } }}
					onblur={() => cancelNewDir()} />
			</form>
		{/if}
	</div>

	<main class="body">
		<aside class="tree" aria-label="Folder tree">
			<ul class="tree-root" data-frame={frame}>
				{#if win}
					{@render treeRow(win.rootPath, 0)}
				{/if}
				{#snippet treeRow(dir: string, depth: number)}
				{@const s = rowSnapshot(dir)}
				<li
					class="trow"
					class:active={win && win.path === dir}
					data-tree-active={win && win.path === dir ? dir : undefined}
					style="padding-left:{8 + depth * 14}px"
				>
					<button type="button" class="tnode" title={dir} onclick={() => navigate(dir)}>
						<span class="twisty" aria-hidden="true" onclick={(e) => { e.stopPropagation(); void tree.toggleExpand(winProp.id, dir).then(() => frame += 1).catch(() => frame += 1); frame += 1; }}>{arrow(dir)}</span>
						<span class="ticon" aria-hidden="true">
							<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
								<path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
							</svg>
						</span>
						<span class="tname" title={label(dir)}>{label(dir)}</span>
					</button>
					{#if s.expanded}
						{#if s.loading}
							<span class="tchild t-loading">Loading…</span>
						{:else if s.error}
							<span class="tchild t-error">{s.error}</span>
						{:else}
							{#each s.kids as child (child.path)}
								{@render treeRow(child.path, depth + 1)}
							{/each}
						{/if}
					{/if}
				</li>
			{/snippet}</ul>
		</aside>

		<div class="content">
			{#if mainLoading && entries.length === 0}
				<div class="empty">Loading…</div>
			{:else if mainError}
				<div class="error">{mainError}</div>
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
		<span class="spath-val" title={winProp.path}>{winProp.path}</span>
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
	.toolbar {
		display: flex;
		align-items: center;
		height: 40px;
		padding: 0 10px;
		background: var(--surface-soft);
		border-bottom: 1px solid var(--border);
		flex: none;
	}
	.up {
		display: grid;
		place-items: center;
		height: 30px;
		width: 30px;
		background: transparent;
		border: none;
		border-radius: 8px;
		color: var(--text-muted);
		cursor: pointer;
	}
	.up:hover {
		background: var(--surface-inset);
		color: var(--text);
	}
	.up svg {
		width: 16px;
		height: 16px;
	}
	.tool {
		display: grid;
		place-items: center;
		height: 30px;
		width: 30px;
		background: transparent;
		border: none;
		border-radius: 8px;
		color: var(--text-muted);
		cursor: pointer;
	}
	.tool:hover:not(:disabled) {
		background: var(--surface-inset);
		color: var(--text);
	}
	.tool:disabled {
		opacity: 0.4;
		cursor: default;
	}
	.tool svg {
		width: 16px;
		height: 16px;
	}
	.new {
		display: grid;
		place-items: center;
		margin-left: auto;
		padding-right: 4px;
	}
	.new-input {
		width: 160px;
		height: 28px;
		padding: 0 8px;
		font: inherit;
		font-size: 13px;
		color: var(--text);
		background: var(--surface);
		border: 1px solid var(--accent-strong);
		border-radius: 7px;
		outline: none;
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
	.trow.active > .tnode {
		color: var(--accent-strong);
		font-weight: 600;
		background: var(--surface-inset);
		border-radius: 6px;
	}
	.twisty {
		width: 12px;
		text-align: center;
		font-size: 11px;
		color: var(--text-faint);
		flex: none;
		cursor: pointer;
	}
	.twisty:hover {
		color: var(--text);
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
	.tchild {
		font-size: 12px;
		padding: 4px 10px 4px 20px;
		white-space: nowrap;
	}
	.t-loading {
		color: var(--text-muted);
	}
	.t-error {
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
	.size {
		width: 1%;
		white-space: nowrap;
		text-align: right;
		color: var(--text-muted);
		min-width: 76px;
	}
	.date {
		width: 1%;
		white-space: nowrap;
		text-align: right;
		color: var(--text-muted);
		min-width: 156px;
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
