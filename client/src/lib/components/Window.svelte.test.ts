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
import { fetchListing, mkdirDirectory, fetchInfo } from '$lib/fs/api';
import { clipboard, clearClipboard, setClipboard, submitCopyTask } from '$lib/store/tasks';

vi.mock('$lib/fs/api', () => ({
	fetchListing: vi.fn(),
	mkdirDirectory: vi.fn(),
	fetchInfo: vi.fn()
}));

// The tasks store is mocked so copy/paste can be tested without a backend. The
// `clipboard` store is created inside the factory (where the imported `writable`
// is available); the clipboard helpers mutate it to mimic the real module.
vi.mock('$lib/store/tasks', () => {
	const store = writable<string[]>([]);
	return {
		clipboard: store,
		setClipboard: vi.fn((paths: string[]) => store.set(paths)),
		clearClipboard: vi.fn(() => store.set([])),
		submitCopyTask: vi.fn(async () => ({ taskID: 'task-1' }))
	};
});

const FS: Record<string, { path: string; name: string; type: 'file' | 'dir'; size: number; created: string; modified: string }[]> = {
	'/root': [
		{ path: '/root/documents', name: 'documents', type: 'dir', size: 0, created: '', modified: '' },
		{ path: '/root/photos', name: 'photos', type: 'dir', size: 0, created: '', modified: '' },
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
		// Root rows are sorted dirs-first then files: documents, photos, notes.txt.
		await clickRow('documents');
		await clickRow('notes.txt', 'shift');
		expect(badgeCount()).toBe(3);
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
