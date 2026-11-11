import { describe, it, expect } from 'vitest';
import { parseSSEMessage, taskLabel } from '$lib/store/tasks';
import type { Task } from '$lib/fs/api';

function task(overrides: Partial<Task> & Pick<Task, 'id' | 'type' | 'status'>): Task {
	const now = new Date().toISOString();
	const base: Partial<Task> = {
		progress: 0,
		created: now,
		updated: now,
		bytes: { total: 0, done: 0, filesTotal: 0, filesDone: 0, ratePerSec: 0 },
		meta: undefined
	};
	return { ...base, ...overrides } as Task;
}

describe('parseSSEMessage', () => {
	it('decodes a task event with JSON data', () => {
		const raw = 'event: task\ndata: {"id":"task-1","type":"copy","status":"running","progress":50,"bytes":{"total":100,"done":50,"filesTotal":2,"filesDone":1,"ratePerSec":2048},"created":"2023-01-01T00:00:00Z","updated":"2023-01-01T00:00:01Z"}';
		const parsed = parseSSEMessage(raw);
		expect(parsed).not.toBeNull();
		expect(parsed!.id).toBe('task-1');
		expect(parsed!.status).toBe('running');
		expect(parsed!.bytes.ratePerSec).toBe(2048);
	});

	it('returns null for a non-task event', () => {
		expect(parseSSEMessage('event: other\ndata: {}')).toBeNull();
	});

	it('returns null when the data is not JSON', () => {
		expect(parseSSEMessage('event: task\ndata: {not json}')).toBeNull();
	});

	it('ignores leading keepalive comments', () => {
		const raw = ': keepalive\n\nevent: task\ndata: {"id":"t","type":"copy","status":"finished","progress":100,"bytes":{},"created":"2023-01-01T00:00:00Z","updated":"2023-01-01T00:00:00Z"}';
		const parsed = parseSSEMessage(raw);
		expect(parsed?.id).toBe('t');
	});
});

describe('taskLabel', () => {
	it('describes a single-file copy by its destination basename', () => {
		const t = task({
			id: '1',
			type: 'copy',
			status: 'running',
			meta: { items: [{ src: '/a/b/photo.jpg', dst: '/c/photo.jpg' }], conflict: '' }
		});
		expect(taskLabel(t)).toBe('Copy photo.jpg');
	});

	it('describes a multi-item copy', () => {
		const t = task({
			id: '2',
			type: 'copy',
			status: 'running',
			meta: {
				items: [
					{ src: '/a/1.txt', dst: '/c/1.txt' },
					{ src: '/a/2.txt', dst: '/c/2.txt' }
				],
				conflict: ''
			}
		});
		expect(taskLabel(t)).toBe('Copy 2 items');
	});

	it('describes a single delete', () => {
		const t = task({
			id: '4',
			type: 'delete',
			status: 'running',
			meta: { paths: ['/root/notes.txt'] }
		});
		expect(taskLabel(t)).toBe('Delete');
	});

	it('describes a multi-item delete', () => {
		const t = task({
			id: '5',
			type: 'delete',
			status: 'running',
			meta: { paths: ['/root/a.txt', '/root/b.txt', '/root/documents'] }
		});
		expect(taskLabel(t)).toBe('Delete 3 items');
	});

	it('falls back to the bare operation type for unknown operations', () => {
		expect(taskLabel(task({ id: '3', type: 'foobar', status: 'running' }))).toBe('foobar');
	});

	it('describes a single-item compress by count', () => {
		expect(taskLabel(task({ id: '6', type: 'compress', status: 'running', meta: { format: 'zip', dest: '/a/b.zip', source: 1 } }))).toBe('Compress');
	});

	it('describes a multi-item compress by count', () => {
		expect(taskLabel(task({ id: '7', type: 'compress', status: 'running', meta: { format: 'zip', dest: '/a/b.zip', source: 4 } }))).toBe('Compress 4 items');
	});
});
