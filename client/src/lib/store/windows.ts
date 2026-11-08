import { writable, type Writable } from 'svelte/store';
import type { Root } from '$lib/roots';

export interface AppWindow {
	id: string;
	title: string;
	rootPath: string;
	x: number;
	y: number;
	width: number;
	height: number;
	zIndex: number;
	minimized: boolean;
}

// Simple window manager backed by a writable store. Kept side-effect free so
// it can be wired to real data later without rewriting the UI.
export const windows: Writable<AppWindow[]> = writable([]);

let nextZ = 10;

function bringToFront(id: string) {
	nextZ += 1;
	windows.update((list) =>
		list.map((w) => (w.id === id ? { ...w, zIndex: nextZ, minimized: false } : w))
	);
}

export function openWindow(root: Root) {
	windows.update((list) => {
		const existing = list.find((w) => w.rootPath === root.path);
		if (existing) {
			bringToFront(existing.id);
			return list;
		}
		nextZ += 1;
		const offset = (list.length % 6) * 28;
		const opened: AppWindow = {
			id: `${root.id}-${Date.now()}`,
			title: root.name,
			rootPath: root.path,
			x: 90 + offset,
			y: 70 + offset,
			width: 560,
			height: 360,
			zIndex: nextZ,
			minimized: false
		};
		return [...list, opened];
	});
}

export function focusWindow(id: string) {
	bringToFront(id);
}

export function closeWindow(id: string) {
	windows.update((list) => list.filter((w) => w.id !== id));
}

export function toggleMinimize(id: string) {
	windows.update((list) =>
		list.map((w) => (w.id === id ? { ...w, minimized: !w.minimized } : w))
	);
}
