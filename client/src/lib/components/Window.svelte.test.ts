import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import { get } from 'svelte/store';
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
import { fetchListing, mkdirDirectory } from '$lib/fs/api';

vi.mock('$lib/fs/api', () => ({
	fetchListing: vi.fn(),
	mkdirDirectory: vi.fn()
}));

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
