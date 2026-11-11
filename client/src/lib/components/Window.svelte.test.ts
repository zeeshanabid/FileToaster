import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import { get, writable } from 'svelte/store';
import {
	fireEvent,
	screen,
	within,
	waitFor,
	getByText,
	getByLabelText,
	getByTitle,
	getByRole,
	getByPlaceholderText,
	queryByText
} from '@testing-library/dom';
import userEvent from '@testing-library/user-event';
import type { Mock } from 'vitest';
import Window from './Window.svelte';
import { openWindow, windows } from '$lib/store/windows';
import { tree as fileTree } from '$lib/fs/tree';
import { fetchListing, mkdirDirectory, fetchInfo, submitMove, renameFile } from '$lib/fs/api';
import {
	clipboard,
	cutMode,
	cutEntries,
	clearClipboard,
	setClipboard,
	setCut,
	submitCopyTask,
	submitDeleteTask,
	submitMoveTask,
	submitCompressTask,
	submitDecompressTask
} from '$lib/store/tasks';

vi.mock('$lib/fs/api', () => ({
	fetchListing: vi.fn(),
	mkdirDirectory: vi.fn(),
	fetchInfo: vi.fn(),
	submitMove: vi.fn(),
	renameFile: vi.fn()
}));

	// The tasks store is mocked so copy/paste/delete/compress can be tested without a backend. The
// `clipboard` store is created inside the factory (where the imported `writable`
// is available); the clipboard helpers mutate it to mimic the real module.
vi.mock('$lib/store/tasks', () => {
	const store = writable<string[]>([]);
	const cutMode = writable<boolean>(false);
	const cutEntries = writable<Array<{ path: string; name: string; type: 'file' | 'dir' }>>([]);
	return {
		clipboard: store,
		cutMode,
		cutEntries,
		setClipboard: vi.fn((paths: string[]) => {
			store.set(paths);
			cutMode.set(false);
		}),
		clearClipboard: vi.fn(() => {
			store.set([]);
			cutMode.set(false);
			cutEntries.set([]);
		}),
		setCut: vi.fn((entries: Array<{ path: string; name: string; type: 'file' | 'dir' }>) => {
			store.set(entries.map((e) => e.path));
			cutEntries.set(entries);
			cutMode.set(true);
		}),
		submitCopyTask: vi.fn(async () => ({ taskID: 'task-1' })),
		submitDeleteTask: vi.fn(async () => ({ taskID: 'task-9' })),
		submitMoveTask: vi.fn(async () => ({ taskID: 'task-5' })),
		submitCompressTask: vi.fn(async () => ({ taskID: 'task-7' })),
		submitDecompressTask: vi.fn(async () => ({ taskID: 'task-8' }))
	};
});

const FS: Record<string, { path: string; name: string; type: 'file' | 'dir'; size: number; created: string; modified: string }[]> = {
	'/root': [
		{ path: '/root/documents', name: 'documents', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/photos', name: 'photos', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/myfiles.zip', name: 'myfiles.zip', type: 'file', size: 512, created: '', modified: '' },
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

const NAME = 'Documents';
const ROOT = '/root';
let id = '';

// Svelte's client `mount` resolves to the browser build of `svelte` (unlike
// testing-library's barrel, which picks up the server build here). Query helpers
// come from @testing-library/dom, which is pure DOM and never imports Svelte.
const instances: unknown[] = [];
function renderComp(Comp: unknown, props: unknown): HTMLElement {
	const target = document.createElement('div');
	document.body.appendChild(target);
	const instance = mount(Comp as never, { target, props } as never);
	instances.push(instance);
	return target;
}
function unmountAll() {
	while (instances.length) unmount(instances.pop() as never);
	document.querySelectorAll('body > div').forEach((el) => el.remove());
}

function tree(): HTMLElement {
	return screen.getByLabelText('Folder tree') as HTMLElement;
}

async function openAndRender(): Promise<void> {
	openWindow({ name: NAME, path: ROOT });
	const list = get(windows);
	id = list[list.length - 1].id;
	renderComp(Window, { window: list[list.length - 1] });
	// Let onMount's initial load (root) settle.
	await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());
}

function currentStorePath(): string {
	let p = '';
	windows.subscribe((list) => {
		const w = list.find((x) => x.id === id);
		if (w) p = w.path;
	})();
	return p;
}

async function clickNode(name: string): Promise<void> {
	const node = within(tree()).getByTitle(name) as HTMLButtonElement;
	fireEvent.click(node);
	await tick();
	await waitFor(() => expect(currentStorePath()).toBe(name));
	await tick();
}

	beforeEach(() => {
		windows.set([]);
		fileTree.resetAll();
		(fetchListing as Mock).mockClear();
		(fetchListing as Mock).mockImplementation(async (path: string) => ({ path, entries: FS[path] ?? [] }));
		(mkdirDirectory as Mock).mockReset();
		(renameFile as Mock).mockReset();
	});

afterEach(() => {
	unmountAll();
	windows.set([]);
});

describe('Window tree — store sync', () => {
	it('anchors the tree at the display name and loads the root folder', async () => {
		await openAndRender();
		// Root node shows the window's display name, not the folder basename.
		expect(within(tree()).getByText(NAME)).toBeInTheDocument();
		// Root is expanded with its sub-directories from cache.
		expect(within(tree()).getByText('documents')).toBeInTheDocument();
		expect(within(tree()).getByText('photos')).toBeInTheDocument();
	});

	it('single-clicking a tree node navigates (updates store path + main view)', async () => {
		await openAndRender();
		await clickNode('/root/documents');
		expect(currentStorePath()).toBe('/root/documents');
		// Main view now lists the opened folder (scoped to the table to avoid the
		// identical tree node).
		expect(within(screen.getByRole('table')).getByText('work')).toBeInTheDocument();
		// The active node is highlighted.
		const active = tree().querySelector('.trow.active') as HTMLElement;
		expect(active).toBeTruthy();
		expect(active.getAttribute('data-tree-active')).toBe('/root/documents');
	});

	it('twisty toggles expansion independently without navigating', async () => {
		await openAndRender();
		const documents = within(tree()).getByTitle('documents').closest('.trow') as HTMLElement;
		expect(within(tree()).queryByText('work')).not.toBeInTheDocument();
		fireEvent.click(documents.querySelector('.twisty') as Element);
		await waitFor(() => expect(within(tree()).getByText('work')).toBeInTheDocument());
		// Expansion did not change the active folder.
		expect(currentStorePath()).toBe(ROOT);
	});

	it('double-clicking a list row navigates (main-view action updates store)', async () => {
		await openAndRender();
		await clickNode('/root/documents');
		const listTable = screen.getByRole('table');
		const workRow = [...listTable.querySelectorAll('tbody tr')].find((tr) =>
			tr.textContent?.includes('work')
		) as HTMLElement;
		fireEvent.dblClick(workRow);
		await waitFor(() => expect(currentStorePath()).toBe('/root/documents/work'));
		// Main view now shows the opened folder's file.
		expect(within(screen.getByRole('table')).getByText('report.txt')).toBeInTheDocument();
		// Tree expanded root..work synchronously with the store.
		await waitFor(() => expect(tree().querySelector('[data-tree-active="/root/documents/work"]')).toBeTruthy());
	});

	it('up button is always visible, disabled at root, navigates below root', async () => {
		await openAndRender();
		// Up button is always present (even at the root)...
		const upAtRoot = screen.getByLabelText('Up one directory') as HTMLButtonElement;
		expect(upAtRoot).toBeInTheDocument();
		expect(upAtRoot).toBeDisabled();
		// ...and only becomes enabled once navigated below the root.
		await clickNode('/root/documents');
		await waitFor(() => expect(screen.getByLabelText('Up one directory')).not.toBeDisabled());
		fireEvent.click(upAtRoot);
		await waitFor(() => expect(currentStorePath()).toBe(ROOT));
	});

	it('reload button refetches the currently opened directory', async () => {
		await openAndRender();
		await clickNode('/root/documents');
		const listing = fetchListing as Mock;
		listing.mockClear();
		fireEvent.click(screen.getByLabelText('Reload'));
		await waitFor(() => expect(listing).toHaveBeenCalledWith('/root/documents'));
		// The main view re-renders from the fresh listing.
		await waitFor(() => expect(within(screen.getByRole('table')).getByText('work')).toBeInTheDocument());
	});

	it('creates a new directory in-place and navigates into it', async () => {
		await openAndRender();
		(fetchListing as Mock).mockImplementation(async (path: string) => ({ path, entries: FS[path] ?? [] }));
		(mkdirDirectory as Mock).mockResolvedValue('/root/TestFolder');
		const user = userEvent.setup();
		await user.click(screen.getByLabelText('New folder'));
		const input = await waitFor(() => screen.getByPlaceholderText('New folder') as HTMLInputElement);
		await user.type(input, 'TestFolder{enter}');
		expect(mkdirDirectory).toHaveBeenCalledWith('/root', 'TestFolder');
		await waitFor(() => expect(currentStorePath()).toBe('/root/TestFolder'));
		// The toolbar returns to its quiet state (input gone, button re-enabled).
		await waitFor(() => expect(screen.queryByPlaceholderText('New folder')).not.toBeInTheDocument());
		expect(screen.getByLabelText('New folder')).not.toBeDisabled();
	});

	it('discards an in-place new folder on Escape', async () => {
		await openAndRender();
		(mkdirDirectory as Mock).mockResolvedValue('/root/Something');
		const user = userEvent.setup();
		await user.click(screen.getByLabelText('New folder'));
		const input = await waitFor(() => screen.getByPlaceholderText('New folder') as HTMLInputElement);
		await user.type(input, 'Something{escape}');
		await waitFor(() => expect(screen.queryByPlaceholderText('New folder')).not.toBeInTheDocument());
		expect(currentStorePath()).toBe(ROOT);
		expect(mkdirDirectory).not.toHaveBeenCalled();
	});

	it('expands only the root..folder branch on navigation', async () => {
		await openAndRender();
		await clickNode('/root/documents');
		await clickNode('/root/documents/work');
		// The active node is work.
		await waitFor(() =>
			expect(tree().querySelector('.trow.active[data-tree-active="/root/documents/work"]')).toBeTruthy()
		);
		// Ancestors (root, documents) are expanded so their children render;
		// an unrelated branch (photos) is not expanded.
		expect(within(tree()).getByText('work')).toBeInTheDocument();
		expect(within(tree()).getByText('personal')).toBeInTheDocument();
		expect(within(tree()).queryByText('2023')).not.toBeInTheDocument();
	});

	it('caches child loads across navigations (no refetch)', async () => {
		await openAndRender();
		const listing = fetchListing as Mock;
		await clickNode('/root/documents');
		await clickNode('/root/documents/work');
		expect(listing).toHaveBeenCalledWith('/root/documents/work');
		listing.mockClear();
		// Navigate away then back; the cached children are reused.
		await clickNode(ROOT);
		await clickNode('/root/documents/work');
		await waitFor(() => expect(currentStorePath()).toBe('/root/documents/work'));
		expect(listing).not.toHaveBeenCalledWith('/root/documents/work');
	});

	it('auto-scrolls the highlighted active row into view on navigation', async () => {
		const scrollSpy = vi.spyOn(Element.prototype, 'scrollIntoView');
		try {
			await openAndRender();
			scrollSpy.mockClear();
			await clickNode('/root/documents');
			await waitFor(() => expect(scrollSpy).toHaveBeenCalled());
			expect(scrollSpy).toHaveBeenLastCalledWith(
				expect.objectContaining({ block: 'nearest' })
			);
		} finally {
			scrollSpy.mockRestore();
		}
	});
});

describe('Window tree — per-directory states', () => {
	it('shows a per-directory loading node while expanding', async () => {
		openWindow({ name: NAME, path: ROOT });
		const list = get(windows);
		id = list[list.length - 1].id;
		let resolveDocuments!: (v: { path: string; entries: any[] }) => void;
		const pending = new Promise((res) => {
			resolveDocuments = res;
		});
		(fetchListing as Mock).mockImplementation(async (path: string) => {
			if (path === '/root/documents') return pending;
			return { path, entries: FS[path] ?? [] };
		});
		renderComp(Window, { window: list[list.length - 1] });
		await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument()); // root loaded

		fireEvent.click(within(tree()).getByTitle('documents').closest('.trow')!.querySelector('.twisty') as Element);
		await waitFor(() => expect(within(tree()).queryByText('Loading…')).toBeInTheDocument());
		expect(within(tree()).queryByText('work')).not.toBeInTheDocument();

		resolveDocuments({ path: '/root/documents', entries: FS['/root/documents'] });
		await waitFor(() => expect(within(tree()).getByText('work')).toBeInTheDocument());
	});

	it('renders a per-directory error node without blocking the tree', async () => {
		openWindow({ name: NAME, path: ROOT });
		const list = get(windows);
		id = list[list.length - 1].id;
		(fetchListing as Mock).mockImplementation(async (path: string) => {
			if (path === '/root/documents/personal') throw new Error('could not open /root/documents/personal');
			return { path, entries: FS[path] ?? [] };
		});
		renderComp(Window, { window: list[list.length - 1] });
		await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument()); // root loaded

		// Expand documents so its children (work, personal) render.
		const documents = within(tree()).getByTitle('documents').closest('.trow') as HTMLElement;
		fireEvent.click(documents.querySelector('.twisty') as Element);
		await waitFor(() => expect(within(tree()).getByText('work')).toBeInTheDocument());

		// Expand personal → its load fails → a per-directory error node appears.
		fireEvent.click(within(tree()).getByTitle('personal').closest('.trow')!.querySelector('.twisty') as Element);
		await waitFor(() =>
			expect(within(tree()).getByText('could not open /root/documents/personal')).toBeInTheDocument()
		);
		// The rest of the tree is still intact (the error did not block it).
		expect(within(tree()).getByText('work')).toBeInTheDocument();
	});
});

describe('Window copy & paste task', () => {
	function clickRow(name: string) {
		const row = [
			...screen.getByRole('table').querySelectorAll('tbody tr.row')
		].find((tr) => tr.textContent?.includes(name)) as HTMLElement;
		if (!row) throw new Error(`no row named ${name}`);
		fireEvent.click(row);
	}
	function copyButton(): HTMLButtonElement {
		return screen.getByLabelText('Copy') as HTMLButtonElement;
	}
	function pasteButton(): HTMLButtonElement {
		return screen.getByLabelText('Paste') as HTMLButtonElement;
	}
	function status(): string | null {
		const el = screen.queryByText(/copied\.|nothing in the clipboard|could not paste/i) as HTMLElement | null;
		return el ? el.textContent : null;
	}

	beforeEach(() => {
		clipboard.set([]);
		(clearClipboard as unknown as Mock).mockClear();
		(setClipboard as unknown as Mock).mockClear();
		(submitCopyTask as Mock).mockClear();
		(submitCopyTask as Mock).mockResolvedValue({ taskID: 'task-1' });
	});

	it('copies the selection into the clipboard and clears it', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		await tick();
		expect(get(clipboard)).toEqual(['/root/notes.txt']);
		expect(setClipboard).toHaveBeenCalledTimes(1);
		// Selection is cleared after copying (badge count back to zero).
		expect(screen.getByLabelText('Information').querySelector('.badge')).toBeNull();
		// A transient status confirms the copy.
		expect(status()).toBe('1 item copied.');
	});

	it('disables paste when the clipboard is empty and enables it after a copy', async () => {
		await openAndRender();
		expect(pasteButton()).toBeDisabled();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		await tick();
		expect(pasteButton()).toBeEnabled();
	});

	it('pastes into the current folder without a prompt', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		// Navigate to the destination folder; the clipboard survives the move.
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());

		// No destination prompt is shown; paste goes straight to the open folder.
		expect(screen.queryByPlaceholderText('Paste to folder')).not.toBeInTheDocument();
		await waitFor(() =>
			expect(submitCopyTask).toHaveBeenCalledWith(['/root/notes.txt'], '/root/documents')
		);
		// The clipboard is cleared once the task is submitted.
		await waitFor(() => expect(get(clipboard)).toEqual([]));
		await waitFor(() => expect(screen.getByText(/Copying/i)).toBeInTheDocument());
	});

	it('pastes straight to the open folder with no overwrite prompt', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		// Navigate to the destination folder; the clipboard survives the move.
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());

		// No destination prompt is shown; paste goes straight to the open folder.
		expect(screen.queryByPlaceholderText('Paste to folder')).not.toBeInTheDocument();
		// The copy is submitted with exactly (paths, dest) — no overwrite/conflict
		// argument — because collisions are renamed by the server.
		await waitFor(() =>
			expect(submitCopyTask).toHaveBeenCalledWith(['/root/notes.txt'], '/root/documents')
		);
		expect((submitCopyTask as Mock).mock.calls.at(-1)!.length).toBe(2);
		// No overwrite/cancel dialog is ever rendered.
		expect(document.querySelector('.conflict-backdrop')).toBeNull();
		// The clipboard is cleared once the task is submitted.
		await waitFor(() => expect(get(clipboard)).toEqual([]));
		await waitFor(() => expect(screen.getByText(/Copying/i)).toBeInTheDocument());
	});

	it('copies again over an existing destination without asking (server renames)', async () => {
		// Even when the destination already holds a same-named item (which the
		// server will rename to " (copy)"), the UI just submits — no prompt.
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());

		await waitFor(() =>
			expect(submitCopyTask).toHaveBeenLastCalledWith(['/root/notes.txt'], '/root/documents')
		);
		expect(document.querySelector('.conflict-backdrop')).toBeNull();
	});
});

describe('Window cut & move', () => {
	function copyButton(): HTMLButtonElement {
		return screen.getByLabelText('Copy') as HTMLButtonElement;
	}
	function cutButton(): HTMLButtonElement {
		return screen.getByLabelText('Cut') as HTMLButtonElement;
	}
	function pasteButton(): HTMLButtonElement {
		return screen.getByLabelText('Paste') as HTMLButtonElement;
	}
	function clickRow(name: string) {
		const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
			.find((tr) => tr.textContent?.includes(name)) as HTMLElement;
		if (!row) throw new Error(`no row named ${name}`);
		fireEvent.click(row);
	}
	function moveDialog(): HTMLElement {
		return screen.getByTestId('move-dialog') as HTMLElement;
	}

	beforeEach(() => {
		clipboard.set([]);
		cutMode.set(false);
		cutEntries.set([]);
		(setCut as unknown as Mock).mockClear();
		(submitMoveTask as Mock).mockClear();
		(submitMoveTask as Mock).mockResolvedValue({ taskID: 'task-5' });
		(submitCopyTask as Mock).mockClear();
	});

	it('is disabled until a row is selected, then enabled', async () => {
		await openAndRender();
		expect(cutButton()).toBeDisabled();
		await clickRow('notes.txt');
		await waitFor(() => expect(cutButton()).not.toBeDisabled());
	});

	it('cuts the selection into the cut clipboard and clears it', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(cutButton());
		await tick();
		// Cut sets cut mode, copies the path into the clipboard, and records the entries.
		expect(get(cutMode)).toBe(true);
		expect(get(clipboard)).toEqual(['/root/notes.txt']);
		expect(get(cutEntries)).toEqual([{ path: '/root/notes.txt', name: 'notes.txt', type: 'file' }]);
		expect(setCut).toHaveBeenCalledTimes(1);
		// Selection is cleared after cutting (cut button disabled again).
		expect(cutButton()).toBeDisabled();
	});

	it('opens the confirm dialog on paste when cut, and does not copy', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(cutButton());
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());

		// The move dialog opens with the count, the selected name and the overwrite
		// notice — and the copy path is not taken.
		await waitFor(() => expect(moveDialog()).toBeInTheDocument());
		expect(within(moveDialog()).getByText('Are you sure you want to move 1 file and directories?')).toBeInTheDocument();
		expect(within(moveDialog()).getByText(/already exists at the destination/)).toBeInTheDocument();
		expect(within(moveDialog()).getByText('notes.txt')).toBeInTheDocument();
		expect(submitCopyTask).not.toHaveBeenCalled();
	});

	it('confirms a move, submits submitMoveTask, clears selection and flashes', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(cutButton());
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());
		await waitFor(() => expect(moveDialog()).toBeInTheDocument());

		fireEvent.click(within(moveDialog()).getByRole('button', { name: 'Move' }));
		await tick();

		await waitFor(() =>
			expect(submitMoveTask).toHaveBeenCalledWith(['/root/notes.txt'], '/root/documents')
		);
		// Selection is cleared (cut button disabled again).
		await waitFor(() => expect(cutButton()).toBeDisabled());
		// A transient status confirms the move task was submitted.
		await waitFor(() => expect(screen.getByText(/Moving/i)).toBeInTheDocument());
	});

	it('keeps the dialog closed (no submit) when Cancel is pressed', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(cutButton());
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());
		await waitFor(() => expect(moveDialog()).toBeInTheDocument());

		fireEvent.click(within(moveDialog()).getByRole('button', { name: 'Cancel' }));
		await waitFor(() => expect(screen.queryByTestId('move-dialog')).not.toBeInTheDocument());
		expect(submitMoveTask).not.toHaveBeenCalled();
	});

	it('copy → paste still calls submitCopyTask with no dialog (regression guard)', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		fireEvent.click(copyButton());
		await clickNode('/root/documents');
		fireEvent.click(pasteButton());

		await waitFor(() =>
			expect(submitCopyTask).toHaveBeenCalledWith(['/root/notes.txt'], '/root/documents')
		);
		expect(screen.queryByTestId('move-dialog')).not.toBeInTheDocument();
	});
});

describe('Window tree — per-window expansion', () => {
	it('keeps expansion independent across windows (shared cache only)', async () => {
		openWindow({ name: 'Documents', path: ROOT });
		const listA = get(windows);
		const idA = listA[listA.length - 1].id;
		renderComp(Window, { window: listA[listA.length - 1] });
		await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());

		// Expand 'photos' in window A.
		fireEvent.click(within(tree()).getByTitle('photos').closest('.trow')!.querySelector('.twisty') as Element);
		await waitFor(() => expect(within(tree()).getByText('2023')).toBeInTheDocument());
		// photos is expanded (not navigated to), so its child renders but it is not the active row.
		expect(tree().querySelector('.trow.active[data-tree-active="/root/photos"]')).toBeNull();

		unmountAll();
		// Open a second window to the same root: expansion starts fresh (reset).
		openWindow({ name: 'Photos', path: ROOT });
		const listB = get(windows);
		const idB = listB[listB.length - 1].id;
		renderComp(Window, { window: listB[listB.length - 1] });
		await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());
		// The fresh window has not expanded 'photos'.
		expect(tree().querySelector('[data-tree-active="/root/photos"]')).toBeNull();
		expect(within(tree()).queryByText('2023')).not.toBeInTheDocument();
		expect(idB).not.toBe(idA);
	});
});

describe('Window selection & information dialog', () => {
	// Main-list rows, in DOM order (which mirrors the sorted rows used for
	// shift-range selection).
	function rows(): HTMLElement[] {
		return [...screen.getByRole('table').querySelectorAll('tbody tr.row')] as HTMLElement[];
	}
	function rowByName(name: string): HTMLElement {
		const row = rows().find((r) => r.textContent?.includes(name));
		if (!row) throw new Error(`no row named ${name}`);
		return row;
	}
	function infoButton(): HTMLButtonElement {
		return screen.getByLabelText('Information') as HTMLButtonElement;
	}
	// The Window root also has role="dialog"; the info overlay is the modal one.
	// Query it by testid to stay unambiguous.
	function dialog(): HTMLElement {
		return screen.getByTestId('info-dialog') as HTMLElement;
	}
	function badgeCount(): number {
		const badge = infoButton().querySelector('.badge') as HTMLElement | null;
		return badge ? parseInt(badge.textContent ?? '0', 10) : 0;
	}

	beforeEach(() => {
		(fetchInfo as Mock).mockClear();
		(fetchInfo as Mock).mockResolvedValue([]);
	});

	async function clickRow(name: string, modifier?: 'ctrl' | 'shift') {
		const row = rowByName(name);
		if (modifier === 'ctrl') {
			fireEvent.click(row, { ctrlKey: true });
		} else if (modifier === 'shift') {
			fireEvent.click(row, { shiftKey: true });
		} else {
			fireEvent.click(row);
		}
		await tick();
	}

	it('plain-click selects a single row and shows a badge', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		expect(badgeCount()).toBe(1);
		expect(infoButton().querySelector('.badge')).toHaveTextContent('1');
	});

	it('styles the selected row with a visible selection class', async () => {
		await openAndRender();
		const notes = rowByName('notes.txt');
		const documents = rowByName('documents');
		// Nothing is selected until a row is clicked, so no row is styled.
		expect(notes).not.toHaveClass('selected');
		expect(documents).not.toHaveClass('selected');

		await clickRow('notes.txt');
		expect(notes).toHaveClass('selected');
		expect(documents).not.toHaveClass('selected');

		// A second ctrl-click keeps both rows styled as selected.
		await clickRow('documents', 'ctrl');
		expect(documents).toHaveClass('selected');

		// Deselecting a row removes its styling.
		await clickRow('notes.txt', 'ctrl');
		expect(notes).not.toHaveClass('selected');
		expect(documents).toHaveClass('selected');
	});

	it('ctrl-click toggles individual rows (accumulate then deselect)', async () => {
		await openAndRender();
		await clickRow('notes.txt', 'ctrl');
		expect(badgeCount()).toBe(1);

		await clickRow('documents', 'ctrl');
		expect(badgeCount()).toBe(2);

		// Clicking a selected row again with ctrl deselects it.
		await clickRow('notes.txt', 'ctrl');
		expect(badgeCount()).toBe(1);
	});

	it('shift-click extends the selection across the sorted order', async () => {
		await openAndRender();
		// Root rows are sorted dirs-first then files: documents, photos, myfiles.zip, notes.txt.
		await clickRow('documents');
		await clickRow('notes.txt', 'shift');
		expect(badgeCount()).toBe(4);
	});

	it('clicking the empty background clears selection', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		expect(badgeCount()).toBe(1);
		// Click the content container directly (not a row).
		fireEvent.click(screen.getByRole('table').closest('.content') as HTMLElement);
		await tick();
		expect(infoButton().querySelector('.badge')).toBeNull();
	});

	it('opens the information dialog and calls fetchInfo with selected paths', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');

		const items: { path: string; name: string; type: 'file' | 'dir'; size: number; fileCount: number; dirCount: number; perms: string; permsOctal: string; created: string; modified: string }[] = [
			{
				path: '/root/notes.txt',
				name: 'notes.txt',
				type: 'file',
				size: 42,
				fileCount: 1,
				dirCount: 0,
				perms: '-rw-r--r--',
				permsOctal: '0644',
				created: '2023-01-02T03:04:05Z',
				modified: '2024-05-06T07:08:09Z'
			},
			{
				path: '/root/documents',
				name: 'documents',
				type: 'dir',
				size: 160,
				fileCount: 3,
				dirCount: 2,
				perms: 'drwxr-xr-x',
				permsOctal: '0755',
				created: '2022-11-12T13:14:15Z',
				modified: '2025-01-02T03:04:05Z'
			}
		];
		(fetchInfo as Mock).mockResolvedValue(items);

		fireEvent.click(infoButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		expect(fetchInfo).toHaveBeenCalledTimes(1);
		// fetchInfo is called with the selected paths (order preserved).
		const arg = (fetchInfo as Mock).mock.calls[0][0] as string[];
		expect(arg).toEqual(['/root/notes.txt', '/root/documents']);

		// Summary aggregates the selected items. The four stats render in order:
		// Selected, Total files, Total directories, Total size.
		const dlg = dialog();
		expect(within(dlg).getByText('Selected (1 files, 1 directories)')).toBeInTheDocument();
		const stats = dlg.querySelectorAll('.stat');
		expect(stats[0].querySelector('.stat-val')?.textContent).toBe('2');
		expect(stats[1].querySelector('.stat-val')?.textContent).toBe('4');
		expect(stats[2].querySelector('.stat-val')?.textContent).toBe('2');
		expect(stats[3].querySelector('.stat-val')?.textContent).toBe('202 B');

		// Each selected item appears in the table with its details.
		expect(within(dlg).getByText('notes.txt')).toBeInTheDocument();
		expect(within(dlg).getByText('drwxr-xr-x')).toBeInTheDocument();
		expect(within(dlg).getByText('0644')).toBeInTheDocument();
	});

	it('closes the dialog on the close button', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		(fetchInfo as Mock).mockResolvedValue([]);
		fireEvent.click(infoButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(screen.getByLabelText('Close'));
		await waitFor(() => expect(screen.queryByTestId('info-dialog')).not.toBeInTheDocument());
	});

	it('closes the dialog with Escape', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		(fetchInfo as Mock).mockResolvedValue([]);
		fireEvent.click(infoButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.keyDown(dialog(), { key: 'Escape' });
		await waitFor(() => expect(screen.queryByTestId('info-dialog')).not.toBeInTheDocument());
	});

		it('does not open the dialog when nothing is selected', async () => {
			await openAndRender();
			expect(badgeCount()).toBe(0);
			fireEvent.click(infoButton());
			expect(screen.queryByText('Information')).not.toBeInTheDocument();
			expect(fetchInfo).not.toHaveBeenCalled();
		});
});

describe('Window delete', () => {
	function rows(): HTMLElement[] {
		return [...screen.getByRole('table').querySelectorAll('tbody tr.row')] as HTMLElement[];
	}
	function rowByName(name: string): HTMLElement {
		const row = rows().find((r) => r.textContent?.includes(name));
		if (!row) throw new Error(`no row named ${name}`);
		return row;
	}
	async function clickRow(name: string, modifier?: 'ctrl' | 'shift') {
		const row = rowByName(name);
		if (modifier === 'ctrl') {
			fireEvent.click(row, { ctrlKey: true });
		} else if (modifier === 'shift') {
			fireEvent.click(row, { shiftKey: true });
		} else {
			fireEvent.click(row);
		}
		await tick();
	}
	function deleteButton(): HTMLButtonElement {
		return screen.getByLabelText('Delete') as HTMLButtonElement;
	}
	function dialog(): HTMLElement {
		return screen.getByTestId('delete-dialog') as HTMLElement;
	}

	beforeEach(() => {
		(submitDeleteTask as Mock).mockClear();
		(submitDeleteTask as Mock).mockResolvedValue({ taskID: 'task-9' });
	});

	it('is disabled until a row is selected, then enabled', async () => {
		await openAndRender();
		expect(deleteButton()).toBeDisabled();
		await clickRow('notes.txt');
		await waitFor(() => expect(deleteButton()).not.toBeDisabled());
	});

	it('opens the modal and shows the root-level selection count', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');

		fireEvent.click(deleteButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		expect(within(dialog()).getByText('Are you sure you want to delete 2 files and directories?')).toBeInTheDocument();
		expect(within(dialog()).getByText('notes.txt')).toBeInTheDocument();
		expect(within(dialog()).getByText('documents')).toBeInTheDocument();
	});

	it('does not open the modal when nothing is selected', async () => {
		await openAndRender();
		fireEvent.click(deleteButton());
		expect(screen.queryByTestId('delete-dialog')).not.toBeInTheDocument();
		expect(submitDeleteTask).not.toHaveBeenCalled();
	});

	it('confirms delete, submits the task, clears selection, and flashes a status', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');

		fireEvent.click(deleteButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Delete' }));
		await tick();

		// The selected paths are submitted in selection order.
		await waitFor(() =>
			expect(submitDeleteTask).toHaveBeenCalledWith(['/root/notes.txt', '/root/documents'])
		);

		// Selection is cleared (delete button disabled again).
		await waitFor(() => expect(deleteButton()).toBeDisabled());

		// A transient status confirms the task was submitted.
		await waitFor(() => expect(screen.getByText(/Deleting/i)).toBeInTheDocument());
	});

	it('keeps the dialog open (no submit) when Cancel is pressed', async () => {
		await openAndRender();
		await clickRow('notes.txt');

		fireEvent.click(deleteButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Cancel' }));
		await waitFor(() => expect(screen.queryByTestId('delete-dialog')).not.toBeInTheDocument());
		expect(submitDeleteTask).not.toHaveBeenCalled();
		// Selection survives a cancel.
		expect(deleteButton()).not.toBeDisabled();
	});
});

describe('Window compress', () => {
	function compressButton(): HTMLButtonElement {
		return screen.getByLabelText('Compress') as HTMLButtonElement;
	}
	function dialog(): HTMLElement {
		return screen.getByTestId('compress-dialog') as HTMLElement;
	}
	async function clickRow(name: string, modifier?: 'ctrl' | 'shift') {
		const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
			.find((tr) => tr.textContent?.includes(name)) as HTMLElement;
		if (!row) throw new Error(`no row named ${name}`);
		if (modifier === 'ctrl') {
			fireEvent.click(row, { ctrlKey: true });
		} else if (modifier === 'shift') {
			fireEvent.click(row, { shiftKey: true });
		} else {
			fireEvent.click(row);
		}
		await tick();
	}

	beforeEach(() => {
		(submitCompressTask as Mock).mockClear();
		(submitCompressTask as Mock).mockResolvedValue({ taskID: 'task-7' });
	});

	it('is disabled until a row is selected, then enabled', async () => {
		await openAndRender();
		expect(compressButton()).toBeDisabled();
		await clickRow('notes.txt');
		await waitFor(() => expect(compressButton()).not.toBeDisabled());
	});

	it('opens the modal and shows the root-level selection count', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');

		fireEvent.click(compressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		expect(within(dialog()).getByText(/Create a \.zip archive of 2 files in the current folder/)).toBeInTheDocument();
		expect(within(dialog()).getByText('notes.txt')).toBeInTheDocument();
		expect(within(dialog()).getByText('documents')).toBeInTheDocument();
	});

	it('does not open the modal when nothing is selected', async () => {
		await openAndRender();
		fireEvent.click(compressButton());
		expect(screen.queryByTestId('compress-dialog')).not.toBeInTheDocument();
		expect(submitCompressTask).not.toHaveBeenCalled();
	});

	it('confirms compress, submits the chosen format, clears selection, and flashes a status', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');

		fireEvent.click(compressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Compress' }));
		await tick();

		await waitFor(() =>
			expect(submitCompressTask).toHaveBeenCalledWith(['/root/notes.txt', '/root/documents'], 'zip')
		);

		await waitFor(() => expect(compressButton()).toBeDisabled());
		await waitFor(() => expect(screen.getByText(/Compressing/i)).toBeInTheDocument());
	});

	it('passes the format chosen in the dialog', async () => {
		await openAndRender();
		await clickRow('notes.txt');

		fireEvent.click(compressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByLabelText('GNU tar archive, gzip (.tar.gz)'));
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Compress' }));
		await waitFor(() => expect(submitCompressTask).toHaveBeenCalledWith(['/root/notes.txt'], 'targz'));
	});

	it('keeps the dialog open (no submit) when Cancel is pressed', async () => {
		await openAndRender();
		await clickRow('notes.txt');

		fireEvent.click(compressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Cancel' }));
		await waitFor(() => expect(screen.queryByTestId('compress-dialog')).not.toBeInTheDocument());
		expect(submitCompressTask).not.toHaveBeenCalled();
		expect(compressButton()).not.toBeDisabled();
	});
});

	describe('Window rename', () => {
		function renameButton(): HTMLButtonElement {
			return screen.getByLabelText('Rename') as HTMLButtonElement;
		}
		async function clickRow(name: string, modifier?: 'ctrl' | 'shift') {
			const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
				.find((tr) => tr.textContent?.includes(name)) as HTMLElement;
			if (!row) throw new Error(`no row named ${name}`);
			if (modifier === 'ctrl') {
				fireEvent.click(row, { ctrlKey: true });
			} else if (modifier === 'shift') {
				fireEvent.click(row, { shiftKey: true });
			} else {
				fireEvent.click(row);
			}
			await tick();
		}

	beforeEach(() => {
		(renameFile as Mock).mockClear();
		(renameFile as Mock).mockResolvedValue(undefined);
	});

	it('is disabled until a row is selected, then enabled', async () => {
		await openAndRender();
		expect(renameButton()).toBeDisabled();
		await clickRow('notes.txt');
		await waitFor(() => expect(renameButton()).not.toBeDisabled());
	});

	it('opens an in-place input and renames a single selection on Enter', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		const user = userEvent.setup();
		await user.click(renameButton());
		const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
		await user.type(input, 'journal{enter}');

		expect(renameFile).toHaveBeenCalledWith(['/root/notes.txt'], 'journal');
		// Selection is cleared and the toolbar returns to its quiet state.
		await waitFor(() => expect(renameButton()).toBeDisabled());
		await waitFor(() => expect(screen.queryByPlaceholderText('Rename')).not.toBeInTheDocument());
		// A transient status confirms the rename.
		await waitFor(() => expect(screen.getByText(/Renamed 1 item/i)).toBeInTheDocument());
	});

	it('renames a multi-selection, passing every path (and the raw name) to the server', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		await clickRow('documents', 'ctrl');
		const user = userEvent.setup();
		await user.click(renameButton());
		const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
		await user.type(input, 'archive{enter}');

		// Suffixing (" (1)", " (2)") is server-side; the client forwards the raw
		// name and every selected path in selection order.
		await waitFor(() =>
			expect(renameFile).toHaveBeenCalledWith(['/root/notes.txt', '/root/documents'], 'archive')
		);
		await waitFor(() => expect(renameButton()).toBeDisabled());
	});

	it('discards the in-place rename on Escape', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		const user = userEvent.setup();
		await user.click(renameButton());
		const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
		await user.type(input, 'journal{escape}');
		await waitFor(() => expect(screen.queryByPlaceholderText('Rename')).not.toBeInTheDocument());
		expect(renameFile).not.toHaveBeenCalled();
	});

	it('reports a conflict inline and keeps the selection for a retry', async () => {
		(renameFile as Mock).mockRejectedValue(new Error('a file with that name already exists'));
		await openAndRender();
		await clickRow('notes.txt');
		const user = userEvent.setup();
		await user.click(renameButton());
		const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
		await user.type(input, 'notes{enter}');

		// The server's conflict is surfaced inline (mainError replaces the list).
		await waitFor(() => expect(screen.getByText(/already exists/i)).toBeInTheDocument());
		// The in-place input closed, but the selection survives so the user can retry.
		await waitFor(() => expect(screen.queryByPlaceholderText('Rename')).not.toBeInTheDocument());
		expect(renameButton()).not.toBeDisabled();
	});
});

describe('Window decompress', () => {
	function decompressButton(): HTMLButtonElement {
		return screen.getByLabelText('Decompress') as HTMLButtonElement;
	}
	function dialog(): HTMLElement {
		return screen.getByTestId('decompress-dialog') as HTMLElement;
	}
	async function clickRow(name: string, modifier?: 'ctrl' | 'shift'): Promise<void> {
		const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
			.find((tr) => tr.textContent?.includes(name)) as HTMLElement;
		if (!row) throw new Error(`no row named ${name}`);
		if (modifier === 'ctrl') {
			fireEvent.click(row, { ctrlKey: true });
		} else if (modifier === 'shift') {
			fireEvent.click(row, { shiftKey: true });
		} else {
			fireEvent.click(row);
		}
		await tick();
	}

	beforeEach(() => {
		(submitDecompressTask as Mock).mockClear();
		(submitDecompressTask as Mock).mockResolvedValue({ taskID: 'task-8' });
	});

	it('is disabled for a non-archive file and enabled for a single archive', async () => {
		await openAndRender();
		await clickRow('notes.txt');
		expect(decompressButton()).toBeDisabled();
		await clickRow('myfiles.zip');
		await waitFor(() => expect(decompressButton()).not.toBeDisabled());
	});

	it('is disabled for multiple selected directories', async () => {
		await openAndRender();
		await clickRow('documents', 'ctrl');
		await clickRow('photos', 'ctrl');
		expect(decompressButton()).toBeDisabled();
	});

	it('opens the modal showing the two extraction options', async () => {
		await openAndRender();
		await clickRow('myfiles.zip');
		fireEvent.click(decompressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		expect(within(dialog()).getByText('Extract here')).toBeInTheDocument();
		expect(within(dialog()).getByText(/Extract in .myfiles/)).toBeInTheDocument();
	});

	it('does not open the modal when nothing is selected', async () => {
		await openAndRender();
		fireEvent.click(decompressButton());
		expect(screen.queryByTestId('decompress-dialog')).not.toBeInTheDocument();
		expect(submitDecompressTask).not.toHaveBeenCalled();
	});

	it('confirms decompress, submits with an empty into, clears and flashes', async () => {
		await openAndRender();
		await clickRow('myfiles.zip');
		fireEvent.click(decompressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Extract' }));
		await tick();

		await waitFor(() =>
			expect(submitDecompressTask).toHaveBeenCalledWith(['/root/myfiles.zip'], '')
		);
		await waitFor(() => expect(decompressButton()).toBeDisabled());
		await waitFor(() => expect(screen.getByText(/Extracting/i)).toBeInTheDocument());
	});

	it('submits the stem into for “Extract in …”', async () => {
		await openAndRender();
		await clickRow('myfiles.zip');
		fireEvent.click(decompressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(dialog().querySelector('input[value="myfiles"]') as HTMLElement);
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Extract' }));

		await waitFor(() =>
			expect(submitDecompressTask).toHaveBeenCalledWith(['/root/myfiles.zip'], 'myfiles')
		);
	});

	it('keeps the dialog open (no submit) when Cancel is pressed', async () => {
		await openAndRender();
		await clickRow('myfiles.zip');
		fireEvent.click(decompressButton());
		await waitFor(() => expect(dialog()).toBeInTheDocument());
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Cancel' }));
		await waitFor(() => expect(screen.queryByTestId('decompress-dialog')).not.toBeInTheDocument());
		expect(submitDecompressTask).not.toHaveBeenCalled();
		expect(decompressButton()).not.toBeDisabled();
	});
});
