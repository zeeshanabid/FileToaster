import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { Mock } from 'vitest';
import { get } from 'svelte/store';
import { tick } from 'svelte';
import { taskList, initTasks, shutdownTasks } from '$lib/store/tasks';
import { listTasks, cancelTask, submitCopy } from '$lib/fs/api';
import type { Task } from '$lib/fs/api';

// The live SSE path lives entirely inside tasks.ts and is exercised through the
// exported store + initTasks. We mock the api module (its `listTasks` is used by
// the initial snapshot) and stub the global `fetch` used by the SSE connector.
	vi.mock('$lib/fs/api', () => ({
		listTasks: vi.fn(),
		cancelTask: vi.fn(),
		submitCopy: vi.fn(),
		createUpload: vi.fn(),
		uploadChunk: vi.fn(),
		getUploadStatus: vi.fn(),
		cancelUpload: vi.fn(),
		chunkCountForFile: vi.fn(() => 1),
		DEFAULT_CHUNK_SIZE: 4 << 20
	}));

const listTasksMock = listTasks as unknown as Mock;
const enc = new TextEncoder();
let ctrl: ReadableStreamDefaultController<Uint8Array> | null = null;

function iso(n = 0): string {
	return new Date(Date.now() - n * 1000).toISOString();
}

function copyTask(overrides: Partial<Task>): Task {
	return {
		id: 'task-1',
		type: 'copy',
		status: 'running',
		progress: 0,
		created: iso(),
		updated: iso(),
		bytes: { total: 0, done: 0, filesTotal: 0, filesDone: 0, ratePerSec: 0 },
		...overrides
	} as Task;
}

function sseMessage(task: Task): string {
	return `event: task\ndata: ${JSON.stringify(task)}\n\n`;
}

let lastStream: ReadableStream<Uint8Array> | null;

function stubFetch() {
	listTasksMock.mockResolvedValue([]);
	vi.stubGlobal(
		'fetch',
		vi.fn(async (url: string) => {
			if (String(url).includes('/api/tasks')) {
				return new Response(JSON.stringify({ tasks: [] }), {
					status: 200,
					headers: { 'Content-Type': 'application/json' }
				});
			}
			// /api/events: an event-stream whose bytes we push on demand.
			lastStream = new ReadableStream<Uint8Array>({
				start(c) {
					ctrl = c;
				}
			});
			return new Response(lastStream, {
				status: 200,
				headers: { 'Content-Type': 'text/event-stream' }
			});
		})
	);
}

function push(task: Task) {
	if (!ctrl) throw new Error('sse stream not started');
	ctrl.enqueue(enc.encode(sseMessage(task)));
}

function delay(ms = 30) {
	return new Promise((r) => setTimeout(r, ms));
}

beforeEach(() => {
	stubFetch();
	shutdownTasks();
	initTasks();
});

afterEach(() => {
	shutdownTasks();
	ctrl?.close();
	vi.unstubAllGlobals();
});

describe('live SSE task progress', () => {
	it('updates the store (and thus the progress bar) when the server streams deltas', async () => {
		await tick();

		// The server starts the copy: queued, then running with partial bytes.
		push(copyTask({ status: 'running', progress: 30, bytes: { total: 1000, done: 300, filesTotal: 4, filesDone: 1, ratePerSec: 2048 } }));
		await tick();
		await delay();

		const afterFirst = get(taskList).find((t) => t.id === 'task-1');
		expect(afterFirst).toBeDefined();
		expect(afterFirst!.status).toBe('running');
		expect(afterFirst!.progress).toBe(30);
		expect(afterFirst!.bytes.done).toBe(300);
		expect(afterFirst!.bytes.total).toBe(1000);

		// The bar must climb as more bytes stream through — the reported symptom
		// is that it never increases while the copy runs.
		push(copyTask({ status: 'running', progress: 75, bytes: { total: 1000, done: 750, filesTotal: 4, filesDone: 3, ratePerSec: 4096 } }));
		await tick();
		await delay();

		const afterSecond = get(taskList).find((t) => t.id === 'task-1');
		expect(afterSecond).toBeDefined();
		expect(afterSecond!.progress).toBe(75);
		expect(afterSecond!.bytes.done).toBe(750);
		expect(afterSecond!.bytes.filesDone).toBe(3);
	});
});
