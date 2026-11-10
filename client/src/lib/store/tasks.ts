import { browser } from '$app/environment';
import { writable, derived, type Readable } from 'svelte/store';
import {
	cancelTask as apiCancel,
	listTasks,
	submitCopy,
	submitDelete,
	submitMove,
	type Task,
	type CopyMeta,
	type DeleteMeta,
	type MoveMeta
} from '$lib/fs/api';

// The task engine. `tasksMap` is the source of truth (id → task), kept current
// by an initial snapshot from /api/tasks and then by deltas over the SSE stream
// at /api/events. The derived stores expose the data the UI needs: the full list
// (oldest first) and the running / queued counts used by the taskbar badge.
const tasksMap = writable<Record<string, Task>>({});

export type { Task } from '$lib/fs/api';

function toList(map: Record<string, Task>): Task[] {
	return Object.values(map).sort((a, b) => Date.parse(a.created) - Date.parse(b.created));
}

export const taskList: Readable<Task[]> = derived(tasksMap, ($map, set) => set(toList($map)));

export const runningCount: Readable<number> = derived(taskList, ($list, set) =>
	set($list.filter((t) => t.status === 'running').length)
);
export const queuedCount: Readable<number> = derived(taskList, ($list, set) =>
	set($list.filter((t) => t.status === 'queued').length)
);
export const activeCount: Readable<number> = derived(taskList, ($list, set) =>
	set($list.filter((t) => t.status === 'running' || t.status === 'queued').length)
);
export const finishedCount: Readable<number> = derived(taskList, ($list, set) =>
	set($list.filter((t) => t.status === 'finished').length)
);
export const failedCount: Readable<number> = derived(taskList, ($list, set) =>
	set($list.filter((t) => t.status === 'failed').length)
);

// `clipboard` holds the paths the user has copied so a paste can reference them.
export const clipboard = writable<string[]>([]);
export function setClipboard(paths: string[]) {
	// A fresh copy resets any prior cut state, so the next paste is a copy.
	clipboard.set(paths);
	cutMode.set(false);
}
export function clearClipboard() {
	clipboard.set([]);
	cutMode.set(false);
	cutEntries.set([]);
}

// `cutMode` + `cutEntries` track a cut (move) selection: cut stores the entries
// in the clipboard too, but paste branches to a move when cutMode is set.
export const cutMode = writable<boolean>(false);
export const cutEntries = writable<Array<{ path: string; name: string; type: 'file' | 'dir' }>>([]);
export function setCut(entries: Array<{ path: string; name: string; type: 'file' | 'dir' }>) {
	clipboard.set(entries.map((e) => e.path));
	cutEntries.set(entries);
	cutMode.set(true);
}

// submitCopyTask enqueues a copy of `paths` into `dest`. Destination collisions
// are resolved server-side (the target is renamed, e.g. " (copy)", " (copy) 1",
// …), so no overwrite/cancel decision is passed.
export function submitCopyTask(paths: string[], dest: string) {
	return submitCopy({ paths, dest, conflict: '' });
}

// submitDeleteTask enqueues a delete task for `paths`. Delete is destructive and
// confirmed by the UI before this runs, so nothing is asked to be overwritten.
export function submitDeleteTask(paths: string[]) {
	return submitDelete(paths);
}

// submitMoveTask enqueues a move of `paths` into `dest`. Move overwrites any
// same-named item at the destination, so no overwrite/cancel decision is passed.
export function submitMoveTask(paths: string[], dest: string) {
	return submitMove({ paths, dest, conflict: '' });
}

// cancelTaskAction asks the server to stop a queued or running task.
export function cancelTaskAction(id: string) {
	return apiCancel(id);
}

// taskLabel derives a one-line description for a task record, reading its meta
// when the operation carries one (currently copy).
export function taskLabel(task: Task): string {
	switch (task.type) {
		case 'copy': {
			const meta = (task.meta ?? null) as CopyMeta | null;
			const n = Array.isArray(meta?.items) ? meta.items.length : 0;
			const dest = meta?.items[meta.items.length - 1]?.dst ?? '';
			const name = dest ? dest.split('/').filter(Boolean).pop() ?? dest : '';
			if (n === 1) return `Copy ${name || 'file'}`;
			return n > 1 ? `Copy ${n} items` : 'Copy';
		}
		case 'delete': {
			const meta = (task.meta ?? null) as DeleteMeta | null;
			const n = meta?.paths?.length ?? 1;
			if (n === 1) return 'Delete';
			return `Delete ${n} items`;
		}
		case 'move': {
			const meta = (task.meta ?? null) as MoveMeta | null;
			const n = meta?.items?.length ?? 1;
			if (n === 1) return 'Move';
			return `Move ${n} items`;
		}
		default:
			return task.type;
	}
}

// --- SSE connection ----------------------------------------------------------
// A single long-lived stream delivers every task delta. It is started once the
// UI is authenticated and torn down on logout. The reader is manual (fetch
// streaming) rather than EventSource so cookies are sent by default and reconnect
// timing is fully controlled.
let started = false;
let controller: AbortController | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelay = 2000;

export function initTasks() {
	if (started || !browser) return;
	started = true;
	refresh();
	connect();
}

function refresh() {
	listTasks()
		.then((list) => {
			const map: Record<string, Task> = {};
			for (const t of list) map[t.id] = t;
			tasksMap.set(map);
		})
		.catch(() => {
			// Transient: the reconnect loop will retry the snapshot on next refresh.
		});
}

function connect() {
	const ctrl = new AbortController();
	controller = ctrl;

	fetch('/api/events', { credentials: 'include', signal: ctrl.signal })
		.then(async (res) => {
			if (!res.ok || !res.body) {
				throw new Error('stream unavailable');
			}
			const reader = res.body.getReader();
			const decoder = new TextDecoder();
			let buffer = '';
			for (;;) {
				const { value, done } = await reader.read();
				if (done) break;
				buffer += decoder.decode(value, { stream: true });
				let sep: number;
				while ((sep = buffer.indexOf('\n\n')) !== -1) {
					handleMessage(buffer.slice(0, sep));
					buffer = buffer.slice(sep + 2);
				}
			}
			if (started) scheduleReconnect();
		})
		.catch((err) => {
			if (err?.name === 'AbortError') return;
			if (started) scheduleReconnect();
		});
}

function handleMessage(message: string) {
	const task = parseSSEMessage(message);
	if (!task) return;
	tasksMap.update((map) => ({ ...map, [task.id]: task }));
}

// parseSSEMessage decodes one raw SSE message ("event: task\ndata: {...}") into
// a Task, or returns null for anything that is not a task event with valid JSON.
export function parseSSEMessage(message: string): Task | null {
	let event = 'message';
	let data = '';
	for (const line of message.split('\n')) {
		if (line.startsWith('event:')) {
			event = line.slice(6).trim();
		} else if (line.startsWith('data:')) {
			data = line.slice(5).trimStart();
		}
	}
	if (event !== 'task' || !data) return null;
	try {
		return JSON.parse(data) as Task;
	} catch {
		return null;
	}
}

function scheduleReconnect() {
	if (reconnectTimer !== null || !started) return;
	reconnectTimer = setTimeout(() => {
		reconnectTimer = null;
		reconnectDelay = Math.min(30000, reconnectDelay * 1.5);
		refresh();
		connect();
	}, reconnectDelay);
}

export function shutdownTasks() {
	started = false;
	if (controller) {
		controller.abort();
		controller = null;
	}
	if (reconnectTimer !== null) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	reconnectDelay = 2000;
	tasksMap.set({});
}
