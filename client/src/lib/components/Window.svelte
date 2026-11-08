<script lang="ts">
	import { closeWindow, focusWindow, type AppWindow } from '$lib/store/windows';
	import FileIcon from './FileIcon.svelte';

	let { window: win }: { window: AppWindow } = $props();

	let pos = $state<{ x: number; y: number } | null>(null);
	let dragging = $state(false);
	let dragOffset = $state({ x: 0, y: 0 });

	function onHeaderPointerDown(e: PointerEvent) {
		if (!pos) {
			pos = { x: win.x, y: win.y };
		}
		dragOffset.x = e.clientX - pos.x;
		dragOffset.y = e.clientY - pos.y;
		dragging = true;
		(e.currentTarget as Element).setPointerCapture?.(e.pointerId);
	}

	function onPointerMove(e: PointerEvent) {
		if (!dragging || !pos) return;
		pos.x = Math.max(0, e.clientX - dragOffset.x);
		pos.y = Math.max(0, e.clientY - dragOffset.y);
	}

	function onPointerUp() {
		dragging = false;
	}

	function onClose() {
		closeWindow(win.id);
	}

	const files = [
		{ name: 'Quarterly Report.pdf', type: 'pdf', color: 'var(--alert)', size: '2.4 MB', modified: 'Nov 3' },
		{ name: 'Team Photo.jpg', type: 'image', color: 'var(--warn)', size: '1.8 MB', modified: 'Oct 29' },
		{ name: 'Budget 2026.xlsx', type: 'sheet', color: 'var(--ok-strong)', size: '86 KB', modified: 'Oct 25' },
		{ name: 'Design Mockups', type: 'folder', color: 'var(--accent)', size: '', modified: 'Nov 5' },
		{ name: 'Notes.md', type: 'doc', color: 'var(--accent-2)', size: '12 KB', modified: 'Nov 1' }
	];
</script>

<div
	class="window"
	class:minimized={win.minimized}
	role="dialog"
	tabindex="0"
	aria-label="{win.title}"
	style="left:{pos ? pos.x : win.x}px; top:{pos ? pos.y : win.y}px; width:{win.width}px; height:{win.height}px; z-index:{win.zIndex};"
	onpointermove={onPointerMove}
	onpointerup={onPointerUp}
	onpointerleave={onPointerUp}
	onmousedown={() => focusWindow(win.id)}
>
	<header class="header" onpointerdown={onHeaderPointerDown} aria-roledescription="draggable" role="application">
		<span class="logo" aria-hidden="true">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
				<path d="M9 12h6M9 15h4" />
			</svg>
		</span>
		<span class="title">{win.title}</span>
		<button class="close" type="button" onclick={onClose} aria-label="Close window">
			<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
				<path d="M7 7l10 10M17 7L7 17" />
			</svg>
		</button>
	</header>

	<div class="pathbar">
		<span class="path" aria-hidden="true">
			<svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
				<path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
			</svg>
		</span>
		<span class="path-val">{win.rootPath}</span>
		<span class="chip">5 items · 7.2 MB</span>
	</div>

	<main class="body">
		<ul class="list">
			{#each files as f}
				<li class="row">
					<span class="fg" aria-hidden="true"><FileIcon type={f.type} color={f.color} /></span>
					<span class="name">{f.name}</span>
					{#if f.size}<span class="size">{f.size}</span>{/if}
					<span class="mod">{f.modified}</span>
				</li>
			{/each}
		</ul>
	</main>
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
	.pathbar {
		display: flex;
		align-items: center;
		gap: 8px;
		height: 36px;
		padding: 0 14px;
		background: var(--surface-inset);
		border-bottom: 1px solid var(--border);
	}
	.path {
		display: grid;
		place-items: center;
		color: var(--accent-strong);
	}
	.path svg {
		width: 15px;
		height: 15px;
	}
	.path-val {
		font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
		font-size: 12px;
		color: var(--text-muted);
	}
	.chip {
		margin-left: auto;
		font-size: 11px;
		color: var(--text-faint);
		background: var(--surface);
		border: 1px solid var(--border);
		border-radius: 999px;
		padding: 2px 9px;
	}
	.body {
		flex: 1;
		overflow: auto;
		background: var(--surface);
	}
	.list {
		list-style: none;
		margin: 0;
		padding: 6px 0;
	}
	.row {
		display: flex;
		align-items: center;
		gap: 12px;
		padding: 9px 14px;
		color: var(--text);
	}
	.row:hover {
		background: var(--surface-soft);
	}
	.fg {
		flex: none;
	}
	.name {
		flex: 1;
		min-width: 0;
		font-size: 13.5px;
		font-weight: 500;
		overflow: hidden;
		text-overflow: ellipsis;
		white-space: nowrap;
	}
	.size {
		flex: none;
		width: 66px;
		text-align: right;
		font-size: 12px;
		color: var(--text-faint);
	}
	.mod {
		flex: none;
		width: 52px;
		text-align: right;
		font-size: 12px;
		color: var(--text-faint);
	}
</style>
