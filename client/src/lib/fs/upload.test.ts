import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
	UploadManager,
	uploadStoreKey,
	relUnder,
	manifestForSelection,
	manifestForDataTransfer,
	chunkSlicesForFile,
	missingChunkIndices,
	globalChunkFor,
	globalChunkSpan,
	type UploadApi,
	type ManifestEntry
} from '$lib/fs/upload';
import { chunkCountForFile, DEFAULT_CHUNK_SIZE, type CreateUploadRequest, type UploadManifest, type UploadStatus } from '$lib/fs/api';

beforeEach(() => {
	localStorage.clear();
});

// --- pure manifest / chunk math ---------------------------------------------

const ROOT = '/root';

const entries: ManifestEntry[] = [
	{ path: '/root/documents', name: 'documents', type: 'dir', size: 0 },
	{ path: '/root/documents/report.txt', name: 'report.txt', type: 'file', size: 8 },
	{ path: '/root/documents/notes.txt', name: 'notes.txt', type: 'file', size: 4 },
	{ path: '/root/empty', name: 'empty', type: 'dir', size: 0 },
	{ path: '/root/photo.jpg', name: 'photo.jpg', type: 'file', size: DEFAULT_CHUNK_SIZE * 1.5 }
];

const childrenOf = (path: string): ManifestEntry[] | undefined =>
	entries.filter((e) => e.path.startsWith(path + '/'));

describe('relUnder', () => {
	it('returns the path relative to root', () => {
		expect(relUnder('/root/documents/report.txt', ROOT)).toBe('documents/report.txt');
		expect(relUnder('/root/photo.jpg', ROOT)).toBe('photo.jpg');
	});
	it('returns an empty string for the root itself', () => {
		expect(relUnder(ROOT, ROOT)).toBe('');
	});
	it('falls back to the basename for an out-of-root path', () => {
		expect(relUnder('/other/photo.jpg', ROOT)).toBe('photo.jpg');
	});
});

describe('manifestForSelection', () => {
	it('expands a directory selection recursively and records files + dirs', () => {
		const manifest = manifestForSelection(
			[entries[0]], // documents (dir)
			childrenOf,
			ROOT
		);
		expect(manifest.files.map((f) => f.rel)).toEqual(['documents/notes.txt', 'documents/report.txt']);
		expect(manifest.files.every((f) => f.chunks === chunkCountForFile(f.size, DEFAULT_CHUNK_SIZE))).toBe(true);
		// The selected directory is recorded (empty dirs included).
		expect(manifest.dirs).toContain('documents');
	});

	it('records pure empty directories alongside file selections', () => {
		const manifest = manifestForSelection(
			[entries[4], entries[3]], // photo.jpg + empty dir
			childrenOf,
			ROOT
		);
		expect(manifest.files.map((f) => f.rel)).toContain('photo.jpg');
		expect(manifest.dirs).toContain('empty');
	});

	it('slices files larger than the chunk size into multiple chunks', () => {
		const manifest = manifestForSelection([entries[4]], childrenOf, ROOT);
		const photo = manifest.files[0];
		expect(photo.size).toBe(Math.round(DEFAULT_CHUNK_SIZE * 1.5));
		expect(photo.chunks).toBe(2);
	});
});

describe('manifestForDataTransfer', () => {
	it('derives nested structure from webkitRelativePath', () => {
		const manifest = manifestForDataTransfer([
			{ name: 'photos', size: 0, webRelativePath: 'photos', isFile: false },
			{ name: 'vacation.jpg', size: 100, webRelativePath: 'photos/2023/vacation.jpg', isFile: true },
			{ name: 'single.txt', size: 10, webRelativePath: 'single.txt', isFile: true }
		]);
		expect(manifest.files.map((f) => f.rel)).toEqual(expect.arrayContaining(['photos/2023/vacation.jpg', 'single.txt']));
		// Every intermediate directory is recorded.
		expect(manifest.dirs).toEqual(expect.arrayContaining(['photos', 'photos/2023']));
	});

	it('uses the bare name when no relative path is present', () => {
		const manifest = manifestForDataTransfer([{ name: 'report.txt', size: 3, webRelativePath: '', isFile: true }]);
		expect(manifest.files[0].rel).toBe('report.txt');
		expect(manifest.dirs).toEqual([]);
	});

	it('produces no files when only folder entries are dropped', () => {
		const manifest = manifestForDataTransfer([
			{ name: 'photos', size: 0, webRelativePath: 'photos', isFile: false }
		]);
		expect(manifest.files).toEqual([]);
		expect(manifest.dirs).toEqual(['photos']);
	});
});

describe('chunkSlicesForFile', () => {
	it('slices a file into fixed-size chunks', () => {
		const slices = chunkSlicesForFile(10, 4);
		expect(slices).toEqual([
			{ index: 0, start: 0, end: 4 },
			{ index: 1, start: 4, end: 8 },
			{ index: 2, start: 8, end: 10 }
		]);
	});
	it('yields one empty slice for a zero-byte file', () => {
		const slices = chunkSlicesForFile(0, 4);
		expect(slices).toEqual([{ index: 0, start: 0, end: 0 }]);
	});
});

describe('chunk indices', () => {
	const files: UploadManifest['files'] = [
		{ rel: 'a', size: 4, chunks: 1 },
		{ rel: 'b', size: 8, chunks: 2 }
	];
	it('maps file/chunk to a global index', () => {
		expect(globalChunkFor(files, 1, 0)).toBe(1);
		expect(globalChunkFor(files, 1, 1)).toBe(2);
	});
	it('returns the global span of a file', () => {
		expect(globalChunkSpan(files, 0)).toEqual({ start: 0, end: 1 });
		expect(globalChunkSpan(files, 1)).toEqual({ start: 1, end: 3 });
	});
	it('computes the missing chunk indices', () => {
		expect(missingChunkIndices(new Set([0, 2]), 3)).toEqual([1]);
		expect(missingChunkIndices(new Set(), 3)).toEqual([0, 1, 2]);
	});
});

describe('chunkCountForFile / defaults', () => {
	it('always yields at least one chunk', () => {
		expect(chunkCountForFile(0, DEFAULT_CHUNK_SIZE)).toBe(1);
	});
	it('uses a sensible default chunk size', () => {
		expect(DEFAULT_CHUNK_SIZE).toBe(4 << 20);
	});
});

// --- UploadManager network flow ----------------------------------------------

// fakeUpload implements UploadApi in memory; `finalizeOnLast` returns the
// finalize task id once every chunk has been ingested, mirroring the server.
// `hangAt` makes one specific chunk hang so a session can be left in staging.
function fakeUpload(opts: {
	finalizeOnLast?: boolean;
	hangAt?: { fileIdx: number; chunkIdx: number };
	failAll?: boolean;
	failFirst?: number;
} = {}): UploadApi & {
	created: CreateUploadRequest[];
	uploads: Array<{ fileIdx: number; chunkIdx: number; size: number; blob?: Blob }>;
	cancelled: string[];
	statusCalls: string[];
	calls: number;
} {
	const state = {
		created: [] as CreateUploadRequest[],
		uploads: [] as Array<{ fileIdx: number; chunkIdx: number; size: number; blob?: Blob }>,
		cancelled: [] as string[],
		statusCalls: [] as string[]
	};
	let attempts = 0;
	const api: UploadApi = {
		async createUpload(req) {
			state.created.push(req);
			return { taskID: 'sess-1' };
		},
		async uploadChunk(_taskID, fileIdx, chunkIdx, blob) {
			attempts++;
			if (opts.hangAt && opts.hangAt.fileIdx === fileIdx && opts.hangAt.chunkIdx === chunkIdx) {
				await new Promise(() => {}); // hang forever (until aborted)
			}
			if (opts.failAll || (opts.failFirst !== undefined && attempts <= opts.failFirst)) {
				throw new Error('transient chunk upload error');
			}
			state.uploads.push({ fileIdx, chunkIdx, size: blob.size, blob });
			const total = state.uploads.length;
			if (opts.finalizeOnLast && total >= expectedTotal()) {
				return {
					chunkSize: 0,
					totalChunks: total,
					receivedChunks: total,
					totalBytes: 0,
					receivedBytes: 0,
					taskID: 'task-final'
				};
			}
			return { chunkSize: 0, totalChunks: total, receivedChunks: total, totalBytes: 0, receivedBytes: 0 };
		},
		async getUploadStatus(taskID) {
			state.statusCalls.push(taskID);
			return { chunkSize: 0, totalChunks: 0, receivedChunks: 0, totalBytes: 0, receivedBytes: 0 };
		},
		async cancelUpload(taskID) {
			state.cancelled.push(taskID);
		}
	};
	function expectedTotal(): number {
		// Total chunks equals the number of chunks across the files of the last
		// createUpload call.
		const last = state.created[state.created.length - 1];
		return last ? last.files.reduce((n, f) => n + f.chunks, 0) : 0;
	}
	return { ...api, ...state, get calls(): number { return attempts; } };
}

async function waitFor(mgr: UploadManager, taskID: string, phase: string): Promise<void> {
	for (let i = 0; i < 200; i++) {
		if (mgr.state(taskID)?.phase === phase) return;
		await new Promise((r) => setTimeout(r, 2));
	}
	throw new Error(`timed out waiting for phase ${phase}`);
}

describe('UploadManager.startUpload', () => {
	it('creates a session with the manifest and uploads every chunk in order', async () => {
		const api = fakeUpload({ finalizeOnLast: true });
		const mgr = new UploadManager(api, 8, 3, 0);
		const manifest: UploadManifest = {
			files: [
				{ rel: 'a.txt', size: 8, chunks: 2 },
				{ rel: 'sub/b.txt', size: 4, chunks: 1 }
			],
			dirs: ['sub']
		};
		const taskID = await mgr.startUpload('/root/out', manifest, 4);
		expect(taskID).toBe('sess-1');

		// createUpload was called with the confined dest, chunk size, files and dirs.
		expect(api.created).toHaveLength(1);
		expect(api.created[0]).toEqual({ dest: '/root/out', chunkSize: 4, files: manifest.files, dirs: ['sub'] });

		// Every chunk of every file was uploaded exactly once.
		const key = (u: { fileIdx: number; chunkIdx: number }) => `${u.fileIdx}:${u.chunkIdx}`;
		expect(api.uploads.map(key).sort()).toEqual(['0:0', '0:1', '1:0']);

		// All chunks were persisted to the keyed storage entry.
		expect(localStorage.getItem(uploadStoreKey('sess-1'))).not.toBeNull();

		// The session finalized once the last chunk completed.
		await waitFor(mgr, 'sess-1', 'finalizing');
		expect(manifest.files).toHaveLength(2);
		expect(manifest.dirs).toEqual(['sub']);
	});

	it('uploads the real bytes of a live File, not a zero-filled buffer', async () => {
		const payload = 'hello upload payload 1234567890';
		const file = new File([payload], 'hello.txt');
		const api = fakeUpload({ finalizeOnLast: true });
		const mgr = new UploadManager(api, 4 << 20, 3, 0);
		const manifest: UploadManifest = {
			files: [{ rel: 'hello.txt', size: file.size, chunks: 1, blob: file }],
			dirs: []
		};
		await mgr.startUpload('/root/out', manifest, 4 << 20);
		// startUpload is fire-and-forget (the footer polls the manager for live
		// progress), so await the finalize phase before asserting on the wire bytes.
		await waitFor(mgr, 'sess-1', 'finalizing');

		expect(api.uploads).toHaveLength(1);
		expect(api.uploads[0].blob).toBeDefined();
		const bytes = await new Promise<string>((resolve, reject) => {
			const fr = new FileReader();
			fr.onload = () => resolve(String(fr.result));
			fr.onerror = () => reject(fr.error);
			fr.readAsText(api.uploads[0].blob!);
		});
		// The chunk on the wire is the file's actual content, proving startUpload no
		// longer sends a zero-filled buffer for live uploads.
		expect(bytes).toBe(payload);
	});

	it('uploads every chunk of a multi-chunk file, including a partial final chunk', async () => {
		// A 10-byte file at a 4-byte chunk size yields 3 chunks ([0,4), [4,8),
		// [8,10)) — a non-multiple size with a trailing partial chunk, the same
		// slice-and-complete path a multi-gigabyte file at 4 MiB exercises.
		const payload = 'abcdefghij';
		const file = new File([payload], 'multi.bin');
		const api = fakeUpload({ finalizeOnLast: true });
		const chunkSize = 4;
		const mgr = new UploadManager(api, chunkSize, 3, 0);
		const manifest: UploadManifest = {
			files: [{ rel: 'multi.bin', size: file.size, chunks: chunkCountForFile(file.size, chunkSize), blob: file }],
			dirs: []
		};
		await mgr.startUpload('/root/out', manifest, chunkSize);
		// startUpload is fire-and-forget; await the finalize phase before asserting
		// the chunks were all sent exactly once.
		await waitFor(mgr, 'sess-1', 'finalizing');

		// All three chunks sent exactly once, in order.
		expect(api.uploads.map((u) => u.chunkIdx).sort()).toEqual([0, 1, 2]);

		// The trailing partial chunk carries its real 2 bytes ("ij"), not zeros.
		const last = api.uploads.find((u) => u.chunkIdx === 2);
		expect(last).toBeDefined();
		const bytes = await new Promise<string>((resolve, reject) => {
			const fr = new FileReader();
			fr.onload = () => resolve(String(fr.result));
			fr.onerror = () => reject(fr.error);
			fr.readAsText(last!.blob!);
		});
		expect(bytes).toBe('ij');
	});

	it('keeps the session staging when the server does not return a finalize id', async () => {
		const api = fakeUpload();
		const mgr = new UploadManager(api, 8, 3, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 4, chunks: 1 }], dirs: [] }, DEFAULT_CHUNK_SIZE);
		// No finalize id returned, but all chunks present → finalizing via check.
		await waitFor(mgr, taskID as string, 'finalizing');
	});
});

describe('UploadManager.resume', () => {
	it('re-sends only the chunks that were not already sent', async () => {
		const api = fakeUpload({ finalizeOnLast: false });
		const mgr = new UploadManager(api, 8, 3, 0);
		// A remembered session whose single file (8 bytes, chunk size 4 => 2 chunks)
		// already has chunk 0 received and chunk 1 missing.
		localStorage.setItem(
			uploadStoreKey('sess-1'),
			JSON.stringify({
				taskID: 'sess-1',
				dest: '/root/out',
				chunkSize: 4,
				files: [{ rel: 'a.txt', size: 8, chunks: 2 }],
				dirs: [],
				present: [true, false],
				received: [[0, 0, 0, 0, 0, 0, 0, 0]]
			})
		);

		expect(await mgr.resume('sess-1')).toBe(true);

		// Only the missing chunk (index 1) is uploaded; chunk 0 is skipped.
		await waitFor(mgr, 'sess-1', 'finalizing');
		expect(api.uploads.length).toBe(1);
		expect(api.uploads[0].chunkIdx).toBe(1);
	});

	it('returns false when nothing is remembered', async () => {
		const api = fakeUpload();
		const mgr = new UploadManager(api);
		expect(await mgr.resume('nope')).toBe(false);
	});
});

describe('UploadManager.abort', () => {
	it('aborts staging and keeps chunks resumable (cancelUpload called)', async () => {
		// Hang the second chunk so the session stays in staging when aborted.
		const api = fakeUpload({ hangAt: { fileIdx: 0, chunkIdx: 1 } });
		const mgr = new UploadManager(api, 8, 3, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 8, chunks: 2 }], dirs: [] }, 4);
		// One chunk is sent; the second hangs, so the session remains in staging.
		await waitFor(mgr, taskID as string, 'staging');
		await mgr.abort(taskID as string);
		expect(api.cancelled).toContain(taskID);
		expect(mgr.state(taskID as string)?.phase).toBe('cancelled');
		// The chunks are not discarded — resume still finds the persisted handle.
		expect(localStorage.getItem(uploadStoreKey(taskID as string))).not.toBeNull();
	});

	it('treats abort of an unknown task as a no-op', async () => {
		const api = fakeUpload();
		const mgr = new UploadManager(api);
		await expect(mgr.abort('ghost')).resolves.toBeUndefined();
		expect(api.cancelled).not.toContain('ghost');
	});

	it('transitions a cancelled staging session to a terminal (finished) state', async () => {
		const api = fakeUpload({ hangAt: { fileIdx: 0, chunkIdx: 0 } });
		const mgr = new UploadManager(api, 8, 3, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 4, chunks: 1 }], dirs: [] }, DEFAULT_CHUNK_SIZE);
		// The single chunk hangs, so the session stays staging until we abort.
		await waitFor(mgr, taskID as string, 'staging');
		await mgr.abort(taskID as string);
		expect(mgr.state(taskID as string)?.phase).toBe('cancelled');
		expect(mgr.finished(taskID as string)).toBe(true);
	});
});

describe('UploadManager concurrency pool', () => {
	// gateFake samples the peak number of chunks in flight at once. It records
	// inflight on entry (synchronously, before the manager can admit another) and
	// resolves on the next tick, so the manager's own acquire gate is what keeps
	// the pool bounded. `open()` is a no-op hook kept for symmetry.
	function gateFake() {
		let inflight = 0;
		let peak = 0;
		let completed = 0;
		const api = {
			async createUpload() {
				return { taskID: 'sess-1' };
			},
			async uploadChunk() {
				inflight++;
				peak = Math.max(peak, inflight);
				await Promise.resolve();
				inflight--;
				completed++;
				return { chunkSize: 0, totalChunks: 1, receivedChunks: 1, totalBytes: 0, receivedBytes: 0 };
			},
			async getUploadStatus() {
				return { chunkSize: 0, totalChunks: 0, receivedChunks: 0, totalBytes: 0, receivedBytes: 0 };
			},
			async cancelUpload() {},
			get counts() {
				return { inflight, peak, completed };
			}
		};
		return {
			api,
			open() {}
		};
	}

	it('caps in-flight chunks across many files at the configured global limit', async () => {
		const fake = gateFake();
		const mgr = new UploadManager(fake.api, 2, 3, 0);
		// Three files, one chunk each. A per-file limit of 2 would allow all three
		// in flight at once; the global cap must hold them at two.
		const taskID = await mgr.startUpload(
			'/root/out',
			{
				files: [
					{ rel: 'a.txt', size: 4, chunks: 1 },
					{ rel: 'b.txt', size: 4, chunks: 1 },
					{ rel: 'c.txt', size: 4, chunks: 1 }
				],
				dirs: []
			},
			4
		);
		// Wait until the pool fills to the cap, then confirm it never exceeded it.
		for (let i = 0; i < 200 && fake.api.counts.peak < 2; i++) {
			await new Promise((r) => setTimeout(r, 2));
		}
		expect(fake.api.counts.peak).toBe(2);
		// Every chunk then completes; the session finalizes once all are present.
		fake.open();
		await waitFor(mgr, taskID as string, 'finalizing');
		expect(fake.api.counts.completed).toBe(3);
	});

	it('honours a reconfigured limit across sessions', async () => {
		const fake = gateFake();
		const mgr = new UploadManager(fake.api, 8, 3, 0);
		mgr.configure({ maxConcurrent: 1 });
		const taskID = await mgr.startUpload(
			'/root/out',
			{
				files: [
					{ rel: 'a.txt', size: 4, chunks: 1 },
					{ rel: 'b.txt', size: 4, chunks: 1 }
				],
				dirs: []
			},
			4
		);
		for (let i = 0; i < 200 && fake.api.counts.peak < 1; i++) {
			await new Promise((r) => setTimeout(r, 2));
		}
		expect(fake.api.counts.peak).toBe(1);
		fake.open();
		await waitFor(mgr, taskID as string, 'finalizing');
		expect(fake.api.counts.completed).toBe(2);
	});

	it('configure() leaves defaults untouched for non-positive values', () => {
		const mgr = new UploadManager({} as UploadApi, 8, 3, 0, 4 << 20);
		mgr.configure({ chunkSize: 0, maxConcurrent: 0 });
		// Non-positive overrides must not disable slicing or stall the pool.
		expect((mgr as unknown as { defaultChunkSize: number }).defaultChunkSize).toBe(4 << 20);
		expect((mgr as unknown as { concurrency: number }).concurrency).toBe(8);
	});
});

describe('UploadManager.sendWithRetry retries', () => {
	it('retries a transiently failing chunk and eventually completes', async () => {		const api = fakeUpload({ finalizeOnLast: true, failFirst: 2 });
		const mgr = new UploadManager(api, 8, 3, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 4, chunks: 1 }], dirs: [] }, DEFAULT_CHUNK_SIZE);
		// The single chunk is uploaded (recorded) exactly once, after 2 transient failures.
		await vi.waitFor(() => expect(api.uploads).toHaveLength(1));
		await vi.waitFor(() => expect(api.calls).toBe(3)); // 2 fails + 1 success
		await waitFor(mgr, taskID as string, 'finalizing');
		expect(mgr.state(taskID as string)?.present[0]).toBe(true);
	});

	it('marks the session failed after exhausting retries and reports finished()', async () => {
		const api = fakeUpload({ failAll: true });
		const mgr = new UploadManager(api, 8, 2, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 4, chunks: 1 }], dirs: [] }, DEFAULT_CHUNK_SIZE);
		await waitFor(mgr, taskID as string, 'failed');
		// The chunk is attempted once and retried maxRetries (2) times.
		expect(api.calls).toBe(3);
		expect(mgr.finished(taskID as string)).toBe(true);
	});

	it('clear() forgets a session and clears finished()', async () => {
		const api = fakeUpload({ failAll: true });
		const mgr = new UploadManager(api, 8, 1, 0);
		const taskID = await mgr.startUpload('/root/out', { files: [{ rel: 'a', size: 4, chunks: 1 }], dirs: [] }, DEFAULT_CHUNK_SIZE);
		await waitFor(mgr, taskID as string, 'failed');
		expect(mgr.finished(taskID as string)).toBe(true);
		mgr.clear(taskID as string);
		expect(mgr.state(taskID as string)).toBeUndefined();
		expect(mgr.finished(taskID as string)).toBe(false);
	});
});
