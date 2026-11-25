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
import type { Mock, MockInstance } from 'vitest';
import Window from './Window.svelte';
import Taskbar from './Taskbar.svelte';
import { openWindow, updateWindow, windows } from '$lib/store/windows';
import { beginSearch, appendSearchResult, completeSearch, searchSessions } from '$lib/store/search';
import { tree as fileTree } from '$lib/fs/tree';
import { uploadManager } from '$lib/fs/upload';
import { fetchListing, mkdirDirectory, fetchInfo, submitMove, renameFile, createUpload, uploadChunk, chunkCountForFile, downloadFile, submitSearch, cancelTask } from '$lib/fs/api';
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
  submitDecompressTask,
  submitTruncateTask,
  taskPanelOpen
} from '$lib/store/tasks';

  vi.mock('$lib/fs/api', () => ({
    fetchListing: vi.fn(),
    downloadFile: vi.fn(),
    mkdirDirectory: vi.fn(),
    fetchInfo: vi.fn(),
    submitMove: vi.fn(),
    renameFile: vi.fn(),
    createUpload: vi.fn(),
    uploadChunk: vi.fn(),
    getUploadStatus: vi.fn(),
    cancelUpload: vi.fn(),
    resumeUpload: vi.fn(),
    chunkCountForFile: vi.fn(() => 1),
    DEFAULT_CHUNK_SIZE: 4 << 20,
    submitSearch: vi.fn(),
    cancelTask: vi.fn()
  }));

  // The tasks store is mocked so copy/paste/delete/compress can be tested without a backend. The
// `clipboard` store is created inside the factory (where the imported `writable`
// is available); the clipboard helpers mutate it to mimic the real module.
vi.mock('$lib/store/tasks', async () => {
  const { writable, derived } = await import('svelte/store');
  const store = writable<string[]>([]);
  const cutMode = writable<boolean>(false);
  const cutEntries = writable<Array<{ path: string; name: string; type: 'file' | 'dir' }>>([]);
  const activeCountStore = writable(0);
  const runningCountStore = writable(0);
  const queuedCountStore = writable(0);
  const taskListStore = writable([]);
  const taskPanelOpenStore = writable(false);
  const taskNotificationStore = writable<{ taskId: string; label: string } | null>(null);
  return {
    clipboard: store,
    cutMode,
    initTasks: vi.fn(),
    shutdownTasks: vi.fn(),
    activeCount: activeCountStore,
    runningCount: runningCountStore,
    queuedCount: queuedCountStore,
    combinedActiveCount: derived(activeCountStore, ($a: number) => $a),
    combinedRunningCount: derived(runningCountStore, ($r: number) => $r),
    combinedQueuedCount: derived(queuedCountStore, ($q: number) => $q),
    combinedTaskList: derived(taskListStore, ($t) => $t),
    taskPanelOpen: taskPanelOpenStore,
    taskNotification: taskNotificationStore,
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
    submitDecompressTask: vi.fn(async () => ({ taskID: 'task-8' })),
    submitTruncateTask: vi.fn(async () => ({ taskID: 'task-10' }))
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

  it('creates a new folder in-place and stays in the current folder', async () => {
    await openAndRender();
    (mkdirDirectory as Mock).mockResolvedValue('/root/TestFolder');
    // The freshly created folder shows up in the listing after the reload.
    (fetchListing as Mock).mockImplementation(async (path: string) => {
      const base = FS[path] ?? [];
      if (path === ROOT) {
        return {
          path,
          entries: [
            ...base,
            { path: '/root/TestFolder', name: 'TestFolder', type: 'dir', size: 0, created: '', modified: '' }
          ]
        };
      }
      return { path, entries: base };
    });
    const user = userEvent.setup();
    await user.click(screen.getByLabelText('New directory'));
    const input = await waitFor(() => screen.getByPlaceholderText('New directory') as HTMLInputElement);
    await user.type(input, 'TestFolder{enter}');
    expect(mkdirDirectory).toHaveBeenCalledWith('/root', 'TestFolder');
    // The window stays in the folder where the new folder was created; it does
    // not navigate into the new folder.
    await waitFor(() => expect(currentStorePath()).toBe(ROOT));
    // The new folder appears in the listing.
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('TestFolder')).toBeInTheDocument());
    // The in-place input is gone; the toolbar button is re-enabled.
    await waitFor(() => expect(screen.queryByPlaceholderText('New directory')).not.toBeInTheDocument());
    expect(screen.getByLabelText('New directory')).not.toBeDisabled();
  });

  it('discards an in-place new folder on Escape', async () => {
    await openAndRender();
    (mkdirDirectory as Mock).mockResolvedValue('/root/Something');
    const user = userEvent.setup();
    await user.click(screen.getByLabelText('New directory'));
    const input = await waitFor(() => screen.getByPlaceholderText('New directory') as HTMLInputElement);
    await user.type(input, 'Something{escape}');
    await waitFor(() => expect(screen.queryByPlaceholderText('New directory')).not.toBeInTheDocument());
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

  it('clears the selection and resets the toolbar when navigating to another folder', async () => {
    await openAndRender();
    await clickRow('notes.txt', 'ctrl');
    await clickRow('myfiles.zip', 'ctrl');
    // Two items are selected: the badge counts them and action buttons enable.
    expect(badgeCount()).toBe(2);
    expect(screen.getByLabelText('Delete')).not.toBeDisabled();

    // Navigate into a different folder via the tree.
    await clickNode('/root/documents');

    // Nothing stays selected: the info badge resets to nothing.
    expect(infoButton().querySelector('.badge')).toBeNull();
    // No row in the (new) listing is styled as selected.
    expect(screen.getByRole('table').querySelector('.row.selected')).toBeNull();
    // The toolbar action buttons disable again with an empty selection.
    expect(screen.getByLabelText('Delete')).toBeDisabled();
    expect(screen.getByLabelText('Copy')).toBeDisabled();
    expect(screen.getByLabelText('Cut')).toBeDisabled();
    expect(screen.getByLabelText('Compress')).toBeDisabled();
    expect(screen.getByLabelText('Rename')).toBeDisabled();
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

  it('renders the Type column in the table header', async () => {
    await openAndRender();
    const headers = [...screen.getByRole('table').querySelectorAll('thead th')].map((th) => th.textContent?.trim().split(' ')[0]);
    expect(headers).toContain('Type');
    // Type should be between Name and Size
    const nameIdx = headers.indexOf('Name');
    const typeIdx = headers.indexOf('Type');
    const sizeIdx = headers.indexOf('Size');
    expect(typeIdx).toBe(nameIdx + 1);
    expect(sizeIdx).toBe(typeIdx + 1);
  });

  it('shows file type in the Type column for each row', async () => {
    await openAndRender();
    const table = screen.getByRole('table');
    const rows = [...table.querySelectorAll('tbody tr.row')];
    
    // Find the notes.txt row (file) and check its type
    const notesRow = rows.find((r) => r.textContent?.includes('notes.txt')) as HTMLElement;
    expect(notesRow).toBeTruthy();
    const notesTypeCell = notesRow?.querySelector('td.type');
    expect(notesTypeCell?.textContent?.trim()).toBe('TXT');
    
    // Find the documents row (dir) and check its type
    const docsRow = rows.find((r) => r.textContent?.includes('documents')) as HTMLElement;
    expect(docsRow).toBeTruthy();
    const docsTypeCell = docsRow?.querySelector('td.type');
    expect(docsTypeCell?.textContent?.trim()).toBe('DIR');
    
    // Find the myfiles.zip row (file) and check its type
    const zipRow = rows.find((r) => r.textContent?.includes('myfiles.zip')) as HTMLElement;
    expect(zipRow).toBeTruthy();
    const zipTypeCell = zipRow?.querySelector('td.type');
    expect(zipTypeCell?.textContent?.trim()).toBe('ZIP');
  });

  it('sorts by Type column when header is clicked', async () => {
    await openAndRender();
    const table = screen.getByRole('table');
    const typeHeader = [...table.querySelectorAll('thead th')].find((th) => th.textContent?.includes('Type')) as HTMLElement;
    expect(typeHeader).toBeTruthy();
    
    // Click Type header to sort ascending
    fireEvent.click(typeHeader);
    await tick();
    
    // After sorting by Type asc, DIR should come before files, and within files, by extension
    const rows = [...table.querySelectorAll('tbody tr.row')].map((r) => r.textContent?.trim() ?? '');
    // Find indices of different types
    const dirIndex = rows.findIndex((r) => r.includes('documents'));
    const txtIndex = rows.findIndex((r) => r.includes('notes.txt'));
    const zipIndex = rows.findIndex((r) => r.includes('myfiles.zip'));
    const photosIndex = rows.findIndex((r) => r.includes('photos'));
    
    // All DIRs should come before files
    expect(dirIndex).toBeLessThan(txtIndex);
    expect(dirIndex).toBeLessThan(zipIndex);
    expect(photosIndex).toBeLessThan(txtIndex);
    expect(photosIndex).toBeLessThan(zipIndex);
    
    // Click again to sort descending
    fireEvent.click(typeHeader);
    await tick();
    
    const rowsDesc = [...table.querySelectorAll('tbody tr.row')].map((r) => r.textContent?.trim() ?? '');
    const dirIndexDesc = rowsDesc.findIndex((r) => r.includes('documents'));
    const txtIndexDesc = rowsDesc.findIndex((r) => r.includes('notes.txt'));
    const zipIndexDesc = rowsDesc.findIndex((r) => r.includes('myfiles.zip'));
    const photosIndexDesc = rowsDesc.findIndex((r) => r.includes('photos'));
    
    // In descending, files should come before DIRs
    expect(txtIndexDesc).toBeLessThan(dirIndexDesc);
    expect(zipIndexDesc).toBeLessThan(dirIndexDesc);
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
      expect(submitCompressTask).toHaveBeenCalledWith(['/root/notes.txt', '/root/documents'], 'zip', 6)
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
    await waitFor(() => expect(submitCompressTask).toHaveBeenCalledWith(['/root/notes.txt'], 'targz', 6));
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

  describe('Window truncate', () => {
    function truncateButton(): HTMLButtonElement {
      return screen.getByLabelText('Truncate') as HTMLButtonElement;
    }
    function dialog(): HTMLElement {
      return screen.getByTestId('truncate-dialog') as HTMLElement;
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
      (submitTruncateTask as Mock).mockClear();
      (submitTruncateTask as Mock).mockResolvedValue({ taskID: 'task-10' });
    });

    it('is disabled until a file row is selected, then enabled', async () => {
      await openAndRender();
      expect(truncateButton()).toBeDisabled();
      await clickRow('notes.txt');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());
    });

    it('remains disabled when a directory is selected', async () => {
      await openAndRender();
      await clickRow('documents');
      expect(truncateButton()).toBeDisabled();
    });

    it('opens the modal and shows the file count', async () => {
      await openAndRender();
      await clickRow('notes.txt');
      await clickRow('myfiles.zip', 'ctrl');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());

      fireEvent.click(truncateButton());
      await waitFor(() => expect(dialog()).toBeInTheDocument());
      expect(within(dialog()).getByText('Target size for 2 files:')).toBeInTheDocument();
    });

    it('does not open the modal when nothing is selected', async () => {
      await openAndRender();
      fireEvent.click(truncateButton());
      expect(screen.queryByTestId('truncate-dialog')).not.toBeInTheDocument();
      expect(submitTruncateTask).not.toHaveBeenCalled();
    });

    it('confirms truncate with default size (0 MB), submits, clears selection, and flashes a status', async () => {
      await openAndRender();
      await clickRow('notes.txt');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());

      fireEvent.click(truncateButton());
      await waitFor(() => expect(dialog()).toBeInTheDocument());
      fireEvent.click(within(dialog()).getByRole('button', { name: 'Truncate' }));
      await tick();

      await waitFor(() =>
        expect(submitTruncateTask).toHaveBeenCalledWith(['/root/notes.txt'], 0)
      );

      await waitFor(() => expect(truncateButton()).toBeDisabled());
      await waitFor(() => expect(screen.getByText(/Truncating/i)).toBeInTheDocument());
    });

    it('passes the custom size chosen in the dialog', async () => {
      await openAndRender();
      await clickRow('notes.txt');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());

      fireEvent.click(truncateButton());
      await waitFor(() => expect(dialog()).toBeInTheDocument());

      // Select custom size
      const select = within(dialog()).getByRole('combobox') as HTMLSelectElement;
      select.value = 'custom';
      fireEvent.change(select);

      // Enter custom size
      await waitFor(() => expect(screen.getByLabelText('Enter size in MB:')).toBeInTheDocument());
      const input = within(dialog()).getByLabelText('Enter size in MB:') as HTMLInputElement;
      fireEvent.input(input, { target: { value: '100' } });

      fireEvent.click(within(dialog()).getByRole('button', { name: 'Truncate' }));
      await waitFor(() =>
        expect(submitTruncateTask).toHaveBeenCalledWith(['/root/notes.txt'], 100 * 1024 * 1024)
      );
    });

    it('passes the preset size chosen in the dialog', async () => {
      await openAndRender();
      await clickRow('notes.txt');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());

      fireEvent.click(truncateButton());
      await waitFor(() => expect(dialog()).toBeInTheDocument());

      // Select 4 MB preset
      const select = within(dialog()).getByRole('combobox') as HTMLSelectElement;
      fireEvent.change(select, { target: { value: '4 MB' } });

      fireEvent.click(within(dialog()).getByRole('button', { name: 'Truncate' }));
      await waitFor(() =>
        expect(submitTruncateTask).toHaveBeenCalledWith(['/root/notes.txt'], 4 * 1024 * 1024)
      );
    });

    it('keeps the dialog open (no submit) when Cancel is pressed', async () => {
      await openAndRender();
      await clickRow('notes.txt');
      await waitFor(() => expect(truncateButton()).not.toBeDisabled());

      fireEvent.click(truncateButton());
      await waitFor(() => expect(dialog()).toBeInTheDocument());
      const cancelBtn = within(dialog()).getByRole('button', { name: 'Cancel' });
      fireEvent.click(cancelBtn);
      await waitFor(() => expect(screen.queryByTestId('truncate-dialog')).not.toBeInTheDocument());
      expect(submitTruncateTask).not.toHaveBeenCalled();
      expect(truncateButton()).not.toBeDisabled();
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

  it('opens an in-place input pre-filled with the exact name and renames on Enter', async () => {
    await openAndRender();
    await clickRow('notes.txt');
    const user = userEvent.setup();
    await user.click(renameButton());
    const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
    // The input is pre-filled with the exact current name, extension included.
    expect(input.value).toBe('notes.txt');
    await user.clear(input);
    await user.type(input, 'journal{enter}');

    expect(renameFile).toHaveBeenCalledWith(['/root/notes.txt'], 'journal');
    // Selection is cleared and the listing returns to its quiet state.
    await waitFor(() => expect(renameButton()).toBeDisabled());
    await waitFor(() => expect(screen.queryByPlaceholderText('Rename')).not.toBeInTheDocument());
    // A transient status confirms the rename.
    await waitFor(() => expect(screen.getByText(/Renamed 1 item/i)).toBeInTheDocument());
  });

  it('pre-files a name without an extension exactly (adds no extension)', async () => {
    await openAndRender();
    await clickRow('documents');
    const user = userEvent.setup();
    await user.click(renameButton());
    const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
    // A name with no extension is shown as-is, with nothing appended.
    expect(input.value).toBe('documents');
    await user.clear(input);
    await user.type(input, 'archive{enter}');

    expect(renameFile).toHaveBeenCalledWith(['/root/documents'], 'archive');
  });

  it('renames a multi-selection, passing every path (and the raw name) to the server', async () => {
    await openAndRender();
    await clickRow('notes.txt');
    await clickRow('documents', 'ctrl');
    const user = userEvent.setup();
    await user.click(renameButton());
    const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
    await user.clear(input);
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
      expect(submitDecompressTask).toHaveBeenCalledWith(['/root/myfiles.zip'], '', '/root')
    );
    await waitFor(() => expect(decompressButton()).toBeDisabled());
    await waitFor(() => expect(screen.getByText(/Extracting/i)).toBeInTheDocument());
  });

  it('submits the stem into for "Extract in …"', async () => {
    await openAndRender();
    await clickRow('myfiles.zip');
    fireEvent.click(decompressButton());
    await waitFor(() => expect(dialog()).toBeInTheDocument());
    fireEvent.click(dialog().querySelector('input[value="myfiles"]') as HTMLElement);
    fireEvent.click(within(dialog()).getByRole('button', { name: 'Extract' }));

    await waitFor(() =>
      expect(submitDecompressTask).toHaveBeenCalledWith(['/root/myfiles.zip'], 'myfiles', '/root')
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

describe('Window drag-and-drop upload', () => {
  let uploadSpy: MockInstance<typeof uploadManager.startUpload>;

  beforeEach(() => {
    uploadSpy = vi.spyOn(uploadManager, 'startUpload').mockResolvedValue('sess-dnd');
  });
  afterEach(() => {
    uploadSpy.mockRestore();
  });

async function dropFiles(files: File[]): Promise<void> {
  const root = screen.getByRole('dialog') as HTMLElement;
  // jsdom does not implement DragEvent, so dispatch a plain drop Event and
  // pin the DataTransfer the handler reads (its `files` list).
  const event = new Event('drop', { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', { value: { files, items: [] }, configurable: true });
  root.dispatchEvent(event as unknown as DragEvent);
  await tick();
}

async function dropFilesViaFastPath(files: File[]): Promise<void> {
  const root = screen.getByRole('dialog') as HTMLElement;
  // Simulates a browser that populates dt.files (e.g., Chrome) — the fast path.
  const event = new Event('drop', { bubbles: true, cancelable: true });
  Object.defineProperty(event, 'dataTransfer', { value: { files, items: [] }, configurable: true });
  root.dispatchEvent(event as unknown as DragEvent);
  await tick();
}

  it('starts an upload into the window folder when files are dropped', async () => {
    await openAndRender();
    const dropped = [new File(['hello world'], 'dropped.txt', { type: 'text/plain' })];
    await dropFiles(dropped);

    // The handler read the dropped files and started an upload into the folder.
    expect(uploadSpy).toHaveBeenCalledTimes(1);
    const [dest, manifest] = uploadSpy.mock.calls[0];
    expect(dest).toBe(ROOT);
    expect(manifest.files.map((f) => f.rel)).toEqual(['dropped.txt']);
  });

  it('rejects a drop that carries a folder path (webkitRelativePath)', async () => {
    await openAndRender();
    const f = new File(['a'], 'a.txt');
    // A file carrying webkitRelativePath came from a dropped folder; only single
    // or multiple individual files are accepted on drag-and-drop.
    Object.defineProperty(f, 'webkitRelativePath', { value: 'photos/2023/a.txt', configurable: true });
    await dropFiles([f]);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });

    it('ignores a drop that carries no files', async () => {
    await openAndRender();
    await dropFiles([]);
    expect(uploadSpy).not.toHaveBeenCalled();
  });

   it('accepts the dragover so drops from outside the browser land on the window', async () => {
    await openAndRender();
    const root = screen.getByRole('dialog') as HTMLElement;
    const ev = new Event('dragover', { bubbles: true, cancelable: true });
    root.dispatchEvent(ev as unknown as DragEvent);
    // A drop is only honoured when a preceding dragover called preventDefault.
    expect(ev.defaultPrevented).toBe(true);
  });

  // Simulates a dropped folder whose files are not exposed on DataTransfer.files
  // (some browsers report an empty file list). A directory item is still reported
  // with kind 'file', holding a WebKit entry, so the reader walks it to enumerate
  // the folder; the fast path (non-empty files list) is intentionally skipped.
  function dropFolderViaEntries(rels: string[]): void {
    const root = screen.getByRole('dialog') as HTMLElement;
    // A real FileSystemDirectoryReader returns entries in batches and an empty
    // one when done, so count batches to stop the reader after the first.
    let batch = 0;
    const dirEntry = {
      isFile: false,
      isDirectory: true,
      createReader: () => ({
        readEntries: () =>
          Promise.resolve(
            batch++ === 0
              ? rels.map((rel) => ({
                  isFile: true,
                  isDirectory: false,
                  getFile: (onFile: (file: File) => void) => onFile(new File(['x'], rel))
                }))
              : []
          )
      }),
      webkitGetAsEntry: () => dirEntry
    };
    const item = { kind: 'file', webkitGetAsEntry: () => dirEntry };
    const event = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(event, 'dataTransfer', { value: { files: [], items: [item] }, configurable: true });
    root.dispatchEvent(event as unknown as DragEvent);
  }

  it('rejects a dropped folder via the WebKit entry API when dt.files is empty', async () => {
    await openAndRender();
    dropFolderViaEntries(['photos/2023/a.txt', 'photos/2023/b.txt']);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });

  // Regression: real browsers report a dropped folder's directory item with
  // kind 'file' (only 'file'/'string' kinds exist), and getAsFile() on it is
  // null. isDirectoryDrop still detects the directory entry and rejects the drop,
  // so folders never upload through drag-and-drop (only via the toolbar picker).
  it('rejects a dropped folder even though its directory item is reported as kind file', async () => {
    await openAndRender();
    dropFolderViaEntries(['docs/notes.txt']);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });

  // A folder drop must be rejected before any file bytes are read, so no manifest
  // is produced and nothing is cloned (there is nothing to upload).
  it('rejects a dropped folder and clones no files', async () => {
    await openAndRender();
    dropFolderViaEntries(['photos/2023/a.txt']);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });

  // Regression: the fast path (dt.files populated, e.g. Chrome) used to attach the
  // raw File as blob, which is backed by the evictable drag-and-drop blob store and
  // caused net::ERR_FILE_NOT_FOUND when a chunk was sliced later. fileItem now clones
  // any file carrying webkitRelativePath, so a dropped folder uploads from a stable
  // blob regardless of which enumeration path the browser took. A folder drop is
  // rejected before any file is read, so nothing is cloned or uploaded.
  it('rejects a dropped folder when the fast path (dt.files) is used', async () => {
    await openAndRender();
    const f = new File(['hello'], 'a.txt');
    Object.defineProperty(f, 'webkitRelativePath', { value: 'photos/2023/a.txt', configurable: true });
    await dropFilesViaFastPath([f]);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });

  // Files without webkitRelativePath are persistent (individually dragged files or
  // items from the file picker), so they are passed through directly to avoid
  // unnecessary multi-GB copies in memory.
  it('does not clone individually-dragged files (no webkitRelativePath)', async () => {
    await openAndRender();
    const f = new File(['hello world'], 'dropped.txt', { type: 'text/plain' });
    await dropFilesViaFastPath([f]);
    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    const [, manifest] = uploadSpy.mock.calls[0];
    expect(manifest.files.map((f) => f.rel)).toEqual(['dropped.txt']);
    expect(manifest.files[0].blob).toBe(f);
  });

  // A folder dropped via the fast path is enumerated by webkitRelativePath, so
  // the nested structure (files + intermediate dirs) would be preserved without
  // the WebKit entry walk, which some browsers do not support at all. Such a
  // folder drop is rejected though; only individual files land on drag-and-drop.
  it('rejects a folder dropped via the fast path even with nested structure', async () => {
    await openAndRender();
    const files = [
      new File(['a'], 'a.txt'),
      new File(['b'], 'b.jpg'),
      new File(['c'], 'c.txt')
    ];
    for (const f of files) {
      Object.defineProperty(f, 'webkitRelativePath', {
        value: f.name === 'a.txt' ? 'docs/a.txt' : f.name === 'b.jpg' ? 'docs/sub/b.jpg' : 'docs/sub/c.txt',
        configurable: true
      });
    }
    await dropFilesViaFastPath(files);
    expect(uploadSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.queryByText(/Drop files only/i)).toBeInTheDocument());
  });
});
});

describe('Window toolbar upload — file picker', () => {
  let uploadSpy: MockInstance<typeof uploadManager.startUpload>;

  beforeEach(() => {
    (createUpload as Mock).mockResolvedValue({ taskID: 'sess-picker' });
    (uploadChunk as Mock).mockResolvedValue({
      chunkSize: 0, totalChunks: 0, receivedChunks: 0, totalBytes: 0, receivedBytes: 0
    });
    (chunkCountForFile as Mock).mockReturnValue(1);
    uploadSpy = vi.spyOn(uploadManager, 'startUpload').mockResolvedValue('sess-picker');
  });
  afterEach(() => {
    uploadSpy.mockRestore();
  });

  // jsdom keeps <input type=file>.files readonly, so pin the list the way the
  // browser would when the user picks items, then fire the change it triggers.
  function pickFiles(files: File[]): void {
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', { value: files, configurable: true });
    fireEvent.change(input);
  }

  it('exposes an Upload button whose picker allows files and directories', async () => {
    await openAndRender();
    const button = screen.getByLabelText('Upload') as HTMLButtonElement;
    expect(button).toBeInTheDocument();
    expect(button).not.toBeDisabled();
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    expect(input).toHaveAttribute('multiple');
    // webkitdirectory (directory-only) is intentionally absent so single files
    // and a mixed selection remain selectable.
    expect(input).not.toHaveAttribute('webkitdirectory');
  });

  it('uploads a single file from the picker with no in-window selection', async () => {
    await openAndRender();
    // Nothing is selected in the window: the picker selection must drive it.
    pickFiles([new File(['hello'], 'single.txt')]);
    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    const [, manifest] = uploadSpy.mock.calls[0];
    expect(manifest.files.map((f) => f.rel)).toEqual(['single.txt']);
  });

  it('uploads a mix of files and directories chosen from the picker', async () => {
    await openAndRender();
    const file = new File(['a'], 'a.txt');
    const folder = new File([], 'photos');
    Object.defineProperty(folder, 'isDirectory', { value: true, configurable: true });
    // A folder member carries its path under the dropped folder.
    const member = new File(['b'], 'b.jpg');
    Object.defineProperty(member, 'webkitRelativePath', { value: 'photos/2023/b.jpg', configurable: true });
    pickFiles([file, folder, member]);
    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    const [, manifest] = uploadSpy.mock.calls[0];
    expect(manifest.files.map((f) => f.rel)).toEqual(expect.arrayContaining(['a.txt', 'photos/2023/b.jpg']));
    expect(manifest.dirs).toEqual(expect.arrayContaining(['photos', 'photos/2023']));
  });

  it('uploads multiple files in a single selection', async () => {
    await openAndRender();
    pickFiles([new File(['1'], 'one.txt'), new File(['2'], 'two.txt')]);
    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    const [, manifest] = uploadSpy.mock.calls[0];
    expect(manifest.files.map((f) => f.rel)).toEqual(expect.arrayContaining(['one.txt', 'two.txt']));
  });
});

describe('Window upload — ingest progress', () => {
  beforeEach(() => {
    (createUpload as Mock).mockResolvedValue({ taskID: 'sess-prog' });
  });

  // jsdom keeps <input type=file>.files readonly; pin the list and fire change.
  function pickFiles(files: File[]): void {
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    Object.defineProperty(input, 'files', { value: files, configurable: true });
    fireEvent.change(input);
  }

  it('starts upload and hands off to taskbar for progress', async () => {
    // Two files, each one chunk. The second chunk hangs so the session stays in
    // staging with the first chunk present.
    (uploadChunk as Mock).mockImplementation((_taskID, fileIdx) =>
      fileIdx === 1
        ? new Promise<void>(() => {})
        : Promise.resolve({ chunkSize: 0, totalChunks: 1, receivedChunks: 1, totalBytes: 0, receivedBytes: 0 })
    );
    await openAndRender();
    pickFiles([new File(['one'], 'a.bin'), new File(['two'], 'b.bin')]);
    // The window no longer shows ingest progress; it hands off to the taskbar.
    // Just verify the upload was started (createUpload was called).
    await waitFor(() => expect(createUpload).toHaveBeenCalledTimes(1));
  });
});

describe('Window download — single-file streaming download', () => {
  let locationSpy: MockInstance;
  let realLocation: Location;

  function downloadButton(): HTMLButtonElement {
    return screen.getByLabelText('Download') as HTMLButtonElement;
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
    (downloadFile as Mock).mockReset();
    (downloadFile as Mock).mockResolvedValue('tok-abc');
    // jsdom pins window.location.assign to a non-configurable constant, so
    // replace window.location with a prototype-preserving fake whose `assign`
    // is the spy — navigation behavior (href/protocol/…) still resolves via the
    // real Location.prototype.
    realLocation = window.location;
    const fake = Object.create(Object.getPrototypeOf(realLocation)) as Location & { assign: MockInstance; replace: MockInstance };
    fake.assign = vi.fn();
    fake.replace = vi.fn();
    Object.defineProperty(window, 'location', { value: fake, configurable: true });
    locationSpy = fake.assign;
  });
  afterEach(() => {
    Object.defineProperty(window, 'location', { value: realLocation, configurable: true });
  });

  it('is disabled with nothing selected and enabled for a single file', async () => {
    await openAndRender();
    expect(downloadButton()).toBeDisabled();
    await clickRow('notes.txt');
    await waitFor(() => expect(downloadButton()).not.toBeDisabled());
  });

  it('is disabled for a directory and for a multi-path selection', async () => {
    await openAndRender();
    await clickRow('notes.txt');
    await waitFor(() => expect(downloadButton()).not.toBeDisabled());
    await clickRow('documents', 'ctrl');
    expect(downloadButton()).toBeDisabled();
  });

  it('requests a download token then loads /downloads/{token}', async () => {
    await openAndRender();
    await clickRow('notes.txt');
    fireEvent.click(downloadButton());
    await waitFor(() => expect(downloadFile).toHaveBeenCalledWith('/root/notes.txt'));
    expect(locationSpy).toHaveBeenCalledWith('/downloads/tok-abc');
  });

  it('does not navigate when the endpoint fails', async () => {
    await openAndRender();
    (downloadFile as Mock).mockRejectedValue(new Error('could not download'));
    await clickRow('notes.txt');
    fireEvent.click(downloadButton());
    await waitFor(() => expect(downloadFile).toHaveBeenCalled());
    expect(locationSpy).not.toHaveBeenCalled();
    await waitFor(() => expect(screen.getByText(/could not download/i)).toBeInTheDocument());
  });
});

describe('Window drag-and-drop between windows', () => {
  const MIME = 'application/x-filetoaster-drop';
  let uploadSpy: MockInstance<typeof uploadManager.startUpload>;

  function getRow(name: string): HTMLElement {
    const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
      .find((tr) => tr.textContent?.includes(name)) as HTMLElement;
    if (!row) throw new Error(`no row named ${name}`);
    return row;
  }
  async function selectRow(name: string): Promise<void> {
    fireEvent.click(getRow(name));
    await tick();
  }
  function makeTransfer(opts?: { files?: File[]; items?: unknown[]; payload?: unknown }) {
    const data: Record<string, string> = {};
    const dt = {
      effectAllowed: 'all',
      dropEffect: 'none',
      files: opts?.files ?? [],
      items: opts?.items ?? [],
      setData(type: string, val: string) { data[type] = val; },
      getData(type: string) { return data[type] ?? ''; },
      setDragImage: vi.fn(),
      types: [] as string[]
    };
    if (opts?.payload !== undefined) dt.setData(MIME, JSON.stringify(opts.payload));
    dt.types = Object.keys(data);
    return dt;
  }
  function startDrag(row: HTMLElement) {
    const dt = makeTransfer();
    const ev = new Event('dragstart', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'dataTransfer', { value: dt, configurable: true });
    row.dispatchEvent(ev as unknown as DragEvent);
    return dt;
  }
  function dropOnWindow(dt: any) {
    const root = screen.getByRole('dialog') as HTMLElement;
    const ev = new Event('drop', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'dataTransfer', { value: dt, configurable: true });
    root.dispatchEvent(ev as unknown as DragEvent);
  }
  function dragOverOnWindow(dt: any) {
    const root = screen.getByRole('dialog') as HTMLElement;
    const ev = new Event('dragover', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'dataTransfer', { value: dt, configurable: true });
    root.dispatchEvent(ev as unknown as DragEvent);
  }
  // Pick Move or Copy in the drop chooser that opens when an intra-filetoaster
  // drag lands on the window.
  function chooseDrop(mode: 'move' | 'copy') {
    fireEvent.click(
      within(screen.getByTestId('drop-choice-dialog')).getByRole('button', {
        name: mode === 'move' ? 'Move' : 'Copy'
      })
    );
  }

  beforeEach(() => {
    uploadSpy = vi.spyOn(uploadManager, 'startUpload').mockResolvedValue('sess-dnd');
  });
  afterEach(() => {
    uploadSpy.mockRestore();
  });

  beforeEach(() => {
    (submitCopyTask as Mock).mockClear();
    (submitCopyTask as Mock).mockResolvedValue({ taskID: 'task-1' });
    (submitMoveTask as Mock).mockClear();
    (submitMoveTask as Mock).mockResolvedValue({ taskID: 'task-5' });
  });

  it('a drag starts a payload listing the selected items (no mode)', async () => {
    await openAndRender();
    await selectRow('notes.txt');
    const dt = startDrag(getRow('notes.txt'));
    // The move/copy choice is deferred to the drop dialog, so the payload only
    // carries the items; without a modifier the drag effect is "move".
    expect(dt.effectAllowed).toBe('move');
    expect(dt.dropEffect).toBe('move');
    const parsed = JSON.parse(dt.getData(MIME)) as {
      mode?: string;
      items: { path: string; name: string; type: string }[];
    };
    expect(parsed.mode).toBeUndefined();
    expect(parsed.items).toEqual([{ path: '/root/notes.txt', name: 'notes.txt', type: 'file' }]);
  });

  it('does not start a drag from an unselected row', async () => {
    await openAndRender();
    const dt = startDrag(getRow('notes.txt'));
    expect(dt.getData(MIME)).toBe('');
  });

  it('opens a copy-or-move chooser when an intra-filetoaster drag lands', async () => {
    await openAndRender();
    // Cross-directory drop so the chooser opens (a drop into the items' own
    // folder is ignored instead).
    const dt = makeTransfer({
      payload: {
        items: [{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file' }]
      }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.getByTestId('drop-choice-dialog')).toBeInTheDocument());
    expect(
      within(screen.getByTestId('drop-choice-dialog')).getByText('vacation.jpg')
    ).toBeInTheDocument();
    // Neither operation is submitted until the user picks one.
    expect(submitCopyTask).not.toHaveBeenCalled();
    expect(submitMoveTask).not.toHaveBeenCalled();
  });

  it('submits a copy task when Copy is chosen from the drop chooser', async () => {
    await openAndRender();
    // Cross-directory drop so the chooser opens.
    const dt = makeTransfer({
      payload: {
        items: [{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file' }]
      }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.getByTestId('drop-choice-dialog')).toBeInTheDocument());
    chooseDrop('copy');
    await waitFor(() =>
      expect(submitCopyTask).toHaveBeenCalledWith(['/root/photos/2023/vacation.jpg'], ROOT)
    );
    expect(submitMoveTask).not.toHaveBeenCalled();
    expect(screen.queryByTestId('move-dialog')).not.toBeInTheDocument();
  });

  it('opens the move confirm dialog when Move is chosen from the chooser, without submitting', async () => {
    await openAndRender();
    // Cross-directory move: item lives in /root/photos/2023, dropped into /root.
    const dt = makeTransfer({
      payload: {
        items: [{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file' }]
      }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.getByTestId('drop-choice-dialog')).toBeInTheDocument());
    chooseDrop('move');

    // Move mirrors the toolbar Cut→Paste: the confirm dialog opens and nothing
    // is submitted until the user confirms. The dragged item is listed in it.
    await waitFor(() => expect(screen.getByTestId('move-dialog')).toBeInTheDocument());
    expect(
      within(screen.getByTestId('move-dialog')).getByText('vacation.jpg')
    ).toBeInTheDocument();
    expect(submitMoveTask).not.toHaveBeenCalled();

    // Confirming submits the tracked move into the open folder; copy is not used.
    fireEvent.click(
      within(screen.getByTestId('move-dialog')).getByRole('button', { name: 'Move' })
    );
    await waitFor(() =>
      expect(submitMoveTask).toHaveBeenCalledWith(['/root/photos/2023/vacation.jpg'], ROOT)
    );
    expect(submitCopyTask).not.toHaveBeenCalled();
  });

  it('keeps the chooser open (no submit) when Cancel is pressed on a drop', async () => {
    await openAndRender();
    const dt = makeTransfer({
      payload: {
        items: [{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file' }]
      }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.getByTestId('drop-choice-dialog')).toBeInTheDocument());

    fireEvent.click(within(screen.getByTestId('drop-choice-dialog')).getByRole('button', { name: 'Cancel' }));
    await waitFor(() => expect(screen.queryByTestId('drop-choice-dialog')).not.toBeInTheDocument());
    expect(screen.queryByTestId('move-dialog')).not.toBeInTheDocument();
    expect(submitMoveTask).not.toHaveBeenCalled();
    expect(submitCopyTask).not.toHaveBeenCalled();
  });

  it('ignores a drop into the items own folder (no dialog, no task)', async () => {
    await openAndRender();
    // Item lives in /root and is dropped into /root → same folder. Ignored
    // entirely: no chooser, no copy, no move.
    const dt = makeTransfer({
      payload: { items: [{ path: '/root/notes.txt', name: 'notes.txt', type: 'file' }] }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.queryByTestId('drop-choice-dialog')).not.toBeInTheDocument());
    expect(submitMoveTask).not.toHaveBeenCalled();
    expect(submitCopyTask).not.toHaveBeenCalled();
  });

  it('renders the drop dialog in a top-level portal so it sits above all windows', async () => {
    await openAndRender();
    const dt = makeTransfer({
      payload: {
        items: [{ path: '/root/photos/2023/vacation.jpg', name: 'vacation.jpg', type: 'file' }]
      }
    });
    dropOnWindow(dt);
    await waitFor(() => expect(screen.getByTestId('drop-choice-dialog')).toBeInTheDocument());
    // The dialog is lifted out of the destination window into a body-level layer,
    // so it is not trapped under any other window's stacking context.
    const portal = (screen.getByTestId('drop-choice-dialog') as HTMLElement)
      .closest('.window-portal') as HTMLElement;
    expect(portal).toBeInTheDocument();
    expect(portal.parentElement).toBe(document.body);
  });

  it('sets a copy drop effect on dragover for an internal drag', async () => {
    await openAndRender();
    const dt = makeTransfer({
      payload: { items: [{ path: '/root/x', name: 'x', type: 'file' }] }
    });
    dragOverOnWindow(dt);
    expect(dt.dropEffect).toBe('copy');
  });

  it('treats a drop without the internal type as a file upload', async () => {
    await openAndRender();
    const dt = makeTransfer({ files: [new File(['a'], 'a.txt')] });
    dropOnWindow(dt);
    await waitFor(() => expect(uploadSpy).toHaveBeenCalledTimes(1));
    const [, manifest] = uploadSpy.mock.calls[0] as unknown as [string, { files: { rel: string }[] }];
    expect(manifest.files.map((f) => f.rel)).toEqual(['a.txt']);
  });
});

describe('Window title + taskbar — directory sync', () => {
  // The title bar label and the taskbar button label both derive from the
  // shared store, so they must move together as the opened folder changes.
  function windowTitleText(): string {
    const el = document.querySelector('.header .title') as HTMLElement;
    return el ? el.textContent ?? '' : '';
  }
  function taskbarButtonText(): string {
    const el = document.querySelector('.window-button .name') as HTMLElement;
    return el ? el.textContent ?? '' : '';
  }

  function renderApp(): void {
    openWindow({ name: NAME, path: ROOT });
    const list = get(windows);
    id = list[list.length - 1].id;
    renderComp(Window, { window: list[list.length - 1] });
    // Mount the taskbar alongside the window so its WindowButton reads the
    // same store entry the window derives its title from.
    renderComp(Taskbar, { onChangePassword: () => {} });
  }

  it('shows the root label in both the title bar and the taskbar at root', async () => {
    renderApp();
    await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());
    expect(windowTitleText()).toBe(NAME);
    expect(taskbarButtonText()).toBe(NAME);
  });

  it('updates both the title bar and the taskbar when navigating into a folder', async () => {
    renderApp();
    await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());

    await clickNode('/root/documents');
    await waitFor(() => expect(windowTitleText()).toBe('documents'));
    await waitFor(() => expect(taskbarButtonText()).toBe('documents'));

    await clickNode('/root/documents/work');
    await waitFor(() => expect(windowTitleText()).toBe('work'));
    await waitFor(() => expect(taskbarButtonText()).toBe('work'));
  });

  it('reverts both labels to the root label when navigating back to root', async () => {
    renderApp();
    await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());

    await clickNode('/root/documents');
    await waitFor(() => expect(windowTitleText()).toBe('documents'));

    await clickNode(ROOT);
    await waitFor(() => expect(windowTitleText()).toBe(NAME));
    await waitFor(() => expect(taskbarButtonText()).toBe(NAME));
  });

  it('derives the label from the store path alone, without re-mounting', async () => {
    openWindow({ name: NAME, path: ROOT });
    const list = get(windows);
    const win = list[list.length - 1];
    renderComp(Window, { window: win });
    renderComp(Taskbar, { onChangePassword: () => {} });
    await waitFor(() => expect(within(tree()).getByText('documents')).toBeInTheDocument());

    expect(windowTitleText()).toBe(NAME);
    expect(taskbarButtonText()).toBe(NAME);

    // Drive the change through the store, as navigate() does, without
    // re-mounting either component.
    updateWindow(win.id, { path: '/root/photos' });
    await waitFor(() => expect(windowTitleText()).toBe('photos'));
    await waitFor(() => expect(taskbarButtonText()).toBe('photos'));
  });
});

describe('Window context menu', () => {
  function menu(): HTMLElement {
    return screen.getByTestId('context-menu') as HTMLElement;
  }
  function menuClosed(): HTMLElement | null {
    return screen.queryByTestId('context-menu');
  }
  function menuItem(action: string): HTMLElement {
    return menu().querySelector(`[data-action="${action}"]`) as HTMLElement;
  }
  function rows(): HTMLElement[] {
    return [...screen.getByRole('table').querySelectorAll('tbody tr.row')] as HTMLElement[];
  }
  function rowByName(name: string): HTMLElement {
    const row = rows().find((r) => r.textContent?.includes(name));
    if (!row) throw new Error(`no row named ${name}`);
    return row as HTMLElement;
  }
  function clickRow(name: string, modifier?: 'ctrl' | 'shift'): void {
    const row = rowByName(name);
    if (modifier === 'ctrl') {
      fireEvent.click(row, { ctrlKey: true });
    } else if (modifier === 'shift') {
      fireEvent.click(row, { shiftKey: true });
    } else {
      fireEvent.click(row);
    }
  }
  async function rightClick(target: HTMLElement): Promise<void> {
    const ev = new Event('contextmenu', { bubbles: true, cancelable: true });
    Object.defineProperty(ev, 'clientX', { value: 150, configurable: true });
    Object.defineProperty(ev, 'clientY', { value: 150, configurable: true });
    target.dispatchEvent(ev as unknown as MouseEvent);
    await tick();
  }
  async function rightClickRow(name: string): Promise<void> {
    await rightClick(rowByName(name));
  }
  async function rightClickEmpty(): Promise<void> {
    await rightClick(screen.getByRole('table').closest('.content') as HTMLElement);
  }
  async function rightClickTree(path: string): Promise<void> {
    await rightClick(within(tree()).getByTitle(path) as HTMLElement);
  }

  beforeEach(() => {
    clipboard.set([]);
    (setClipboard as unknown as Mock).mockClear();
    (setCut as unknown as Mock).mockClear();
    (submitCompressTask as unknown as Mock).mockClear();
    (submitDeleteTask as unknown as Mock).mockClear();
  });

  it('opens a folder menu with folder-applicable actions enabled', async () => {
    await openAndRender();
    await rightClickRow('documents');
    expect(menu()).toBeInTheDocument();
    // Enabled for a folder.
    for (const action of [
      'cut',
      'copy',
      'delete',
      'rename',
      'compress',
      'info',
      'newfolder',
      'upload',
      'reload'
    ]) {
      expect(menuItem(action)).not.toBeDisabled();
    }
    // Disabled for a single directory.
    for (const action of ['download', 'decompress', 'paste']) {
      expect(menuItem(action)).toBeDisabled();
    }
  });

  it('opens a file menu with file-applicable actions enabled', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menu()).toBeInTheDocument();
    for (const action of ['cut', 'copy', 'delete', 'rename', 'compress', 'download', 'info', 'reload']) {
      expect(menuItem(action)).not.toBeDisabled();
    }
    for (const action of ['decompress', 'paste', 'newfolder', 'upload']) {
      expect(menuItem(action)).toBeDisabled();
    }
  });

  it('enables Extract only for a single archive file', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menuItem('decompress')).toBeDisabled();
    await rightClickRow('myfiles.zip');
    expect(menuItem('decompress')).not.toBeDisabled();
  });

  it('opens a multi-selection menu with multi-applicable actions enabled', async () => {
    await openAndRender();
    clickRow('notes.txt');
    clickRow('documents', 'ctrl');
    // Right-click a row that is part of the selection: the multi-selection is kept.
    await rightClickRow('notes.txt');
    expect(menu()).toBeInTheDocument();
    for (const action of ['cut', 'copy', 'delete', 'compress', 'info', 'reload']) {
      expect(menuItem(action)).not.toBeDisabled();
    }
    for (const action of ['rename', 'download', 'decompress', 'paste', 'newfolder', 'upload']) {
      expect(menuItem(action)).toBeDisabled();
    }
  });

  it('opens an empty-space menu', async () => {
    await openAndRender();
    await rightClickEmpty();
    expect(menu()).toBeInTheDocument();
    for (const action of ['newfolder', 'upload', 'reload']) {
      expect(menuItem(action)).not.toBeDisabled();
    }
    for (const action of [
      'cut',
      'copy',
      'delete',
      'rename',
      'compress',
      'download',
      'decompress',
      'info',
      'paste'
    ]) {
      expect(menuItem(action)).toBeDisabled();
    }
  });

  it('enables Paste on empty space when the clipboard holds items', async () => {
    await openAndRender();
    clipboard.set(['/root/notes.txt']);
    await tick();
    await rightClickEmpty();
    expect(menuItem('paste')).not.toBeDisabled();
  });

  it('right-clicking the tree shows a folder menu and rename targets that folder', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    expect(menu()).toBeInTheDocument();
    expect(menuItem('rename')).not.toBeDisabled();
    expect(menuItem('compress')).not.toBeDisabled();
    fireEvent.click(menuItem('rename'));
    await tick();
    // Rename opens an in-place input pre-filled with the folder name.
    const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
    expect(input.value).toBe('documents');
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
  });

  it('copies the right-clicked selection via the menu', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    fireEvent.click(menuItem('copy'));
    await tick();
    expect(setClipboard).toHaveBeenCalledWith(['/root/notes.txt']);
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
  });

  it('cuts the right-clicked selection via the menu', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    fireEvent.click(menuItem('cut'));
    await tick();
    expect(setCut).toHaveBeenCalledTimes(1);
    expect(get(cutMode)).toBe(true);
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
  });

  it('compresses via the menu', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    fireEvent.click(menuItem('compress'));
    await waitFor(() => expect(screen.getByTestId('compress-dialog')).toBeInTheDocument());
    fireEvent.click(within(screen.getByTestId('compress-dialog')).getByRole('button', { name: 'Compress' }));
    await tick();
    await waitFor(() =>
      expect(submitCompressTask).toHaveBeenCalledWith(['/root/notes.txt'], 'zip', 6)
    );
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
  });

  it('closes the menu on Escape', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menu()).toBeInTheDocument();
    fireEvent.keyDown(menu(), { key: 'Escape' });
    await waitFor(() => expect(menuClosed()).not.toBeInTheDocument());
  });

  it('closes the menu on a click outside it', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menu()).toBeInTheDocument();
    fireEvent.mouseDown(screen.getByRole('table').closest('.content') as HTMLElement);
    await waitFor(() => expect(menuClosed()).not.toBeInTheDocument());
  });

  it('disables the Information toolbar button when nothing is selected', async () => {
    await openAndRender();
    const infoBtn = screen.getByLabelText('Information') as HTMLButtonElement;
    expect(infoBtn).toBeDisabled();
    await clickRow('notes.txt');
    await waitFor(() => expect(infoBtn).not.toBeDisabled());
  });

  it('pastes as move (not copy) from the context menu when the clipboard is cut', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    fireEvent.click(menuItem('cut'));
    await tick();
    expect(get(cutMode)).toBe(true);
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();

    // Right-click empty space and choose Paste: should open the move confirm,
    // not submit a copy task.
    await rightClickEmpty();
    fireEvent.click(menuItem('paste'));
    await waitFor(() => expect(screen.getByTestId('move-dialog')).toBeInTheDocument());
    expect(submitCopyTask).not.toHaveBeenCalled();
  });

  it('renames a folder via the tree context menu', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    expect(menuItem('rename')).not.toBeDisabled();
    fireEvent.click(menuItem('rename'));
    await tick();
    const input = await waitFor(() =>
      screen.getByPlaceholderText('Rename') as HTMLInputElement
    );
    expect(input.value).toBe('documents');
    // Rename via Enter should submit the rename task for the tree path.
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() =>
      expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument()
    );
  });

  it('compresses via the tree context menu', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    expect(menuItem('compress')).not.toBeDisabled();
    fireEvent.click(menuItem('compress'));
    await waitFor(() => expect(screen.getByTestId('compress-dialog')).toBeInTheDocument());
    fireEvent.click(within(screen.getByTestId('compress-dialog')).getByRole('button', { name: 'Compress' }));
    await tick();
    await waitFor(() =>
      expect(submitCompressTask).toHaveBeenCalledWith(['/root/documents'], 'zip', 6)
    );
  });

  it('opens “Open” for a single directory and navigates into it', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    expect(menuItem('open')).not.toBeDisabled();
    fireEvent.click(menuItem('open'));
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('work')).toBeInTheDocument());
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
  });

  it('disables “Open” for a file and for a multi-selection', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menuItem('open')).toBeDisabled();
    clickRow('notes.txt');
    clickRow('documents', 'ctrl');
    await rightClickRow('notes.txt');
    expect(menuItem('open')).toBeDisabled();
  });

  it('opens “Up one directory” on empty space below the root and navigates up', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    await rightClickEmpty();
    expect(menuItem('up')).not.toBeDisabled();
    fireEvent.click(menuItem('up'));
    await waitFor(() => expect(currentStorePath()).toBe(ROOT));
  });

  it('disables "Up one directory" at the root and in non-empty menus', async () => {
    await openAndRender();
    await rightClickEmpty();
    expect(menuItem('up')).toBeDisabled();
    await rightClickRow('notes.txt');
    expect(menuItem('up')).toBeDisabled();
  });

  it('opens "Open in new window" for a single directory via tree context menu', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    expect(menuItem('open-new-window')).not.toBeDisabled();
    fireEvent.click(menuItem('open-new-window'));
    await waitFor(() => expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument());
    // A new window should be opened with the documents path
    const list = get(windows);
    expect(list.length).toBe(2);
    const newWin = list[list.length - 1];
    expect(newWin.path).toBe('/root/documents');
    // name should be the root's display name
    expect(newWin.name).toBe('Documents');
  });

  it('opens "Open in new window" for a single directory via list row context menu', async () => {
    await openAndRender();
    await rightClickRow('documents');
    expect(menuItem('open-new-window')).not.toBeDisabled();
    fireEvent.click(menuItem('open-new-window'));
    await waitFor(() => expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument());
    // A new window should be opened with the documents path
    const list = get(windows);
    expect(list.length).toBe(2);
    const newWin = list[list.length - 1];
    expect(newWin.path).toBe('/root/documents');
    // name should be the root's display name
    expect(newWin.name).toBe('Documents');
  });

  it('disables "Open in new window" for a file and for a multi-selection', async () => {
    await openAndRender();
    await rightClickRow('notes.txt');
    expect(menuItem('open-new-window')).toBeDisabled();
    clickRow('notes.txt');
    clickRow('documents', 'ctrl');
    await rightClickRow('notes.txt');
    expect(menuItem('open-new-window')).toBeDisabled();
  });

  it('opens "Open in new window" with correct rootPath so tree shows full path from root', async () => {
    await openAndRender();
    await rightClickTree('/root/documents');
    fireEvent.click(menuItem('open-new-window'));
    await waitFor(() => expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument());
    
    // A new window should be opened with the documents path and original rootPath
    const list = get(windows);
    expect(list.length).toBe(2);
    const newWin = list[list.length - 1];
    expect(newWin.path).toBe('/root/documents');
    expect(newWin.rootPath).toBe('/root');
    // name should be the root's display name
    expect(newWin.name).toBe('Documents');
  });

  it('expands tree to show full path from root when opening subdirectory in new window', async () => {
    await openAndRender();
    // Navigate to a deeper directory first
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    await rightClickTree('/root/documents/work');
    fireEvent.click(menuItem('open-new-window'));
    await waitFor(() => expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument());
    
    // A new window should be opened with the work path and original rootPath
    const list = get(windows);
    expect(list.length).toBe(2);
    const newWin = list[list.length - 1];
    expect(newWin.path).toBe('/root/documents/work');
    expect(newWin.rootPath).toBe('/root');
    // name should be the root's display name
    expect(newWin.name).toBe('Documents');
    
    // Render the new window and verify tree is expanded to show root -> documents -> work
    const newWinId = newWin.id;
    renderComp(Window, { window: newWin });
    
    // Wait for tree to load - find the tree for the new window
    await waitFor(() => {
      const trees = document.querySelectorAll('[aria-label="Folder tree"]');
      const newTree = Array.from(trees).find(t => t.querySelector('[data-tree-active="/root/documents/work"]'));
      expect(newTree).toBeTruthy();
    });
    
    // Verify the active node is 'work' in the new window's tree
    const trees = document.querySelectorAll('[aria-label="Folder tree"]');
    const newTree = Array.from(trees).find(t => t.querySelector('[data-tree-active="/root/documents/work"]'));
    expect(newTree).toBeTruthy();
    const activeNode = newTree!.querySelector('.trow.active[data-tree-active="/root/documents/work"]');
    expect(activeNode).toBeTruthy();
  });

  it('selects all items via the empty-space context menu', async () => {
    await openAndRender();
    await rightClickEmpty();
    expect(menuItem('selectall')).not.toBeDisabled();
    fireEvent.click(menuItem('selectall'));
    await tick();
    expect(screen.queryByTestId('context-menu')).not.toBeInTheDocument();
    const selectedRows = screen.getByRole('table').querySelectorAll('tbody tr.row.selected');
    expect(selectedRows.length).toBe(4);
  });

  it('renames a tree node via an inplace input opened in the tree', async () => {
    await openAndRender();
    // Navigate into documents so the listing no longer contains 'photos';
    // renaming the tree node then uses the tree branch (not the listing row).
    await clickNode('/root/documents');
    await rightClickTree('/root/photos');
    expect(menuItem('rename')).not.toBeDisabled();
    fireEvent.click(menuItem('rename'));
    await tick();
    // The rename input opens inplace in the tree node, not in the listing.
    const input = await waitFor(() => screen.getByPlaceholderText('Rename') as HTMLInputElement);
    expect(input.value).toBe('photos');
    fireEvent.input(input, { target: { value: 'pics' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() =>
      expect(renameFile).toHaveBeenCalledWith(['/root/photos'], 'pics')
    );
    await waitFor(() => expect(screen.queryByPlaceholderText('Rename')).not.toBeInTheDocument());
  });
});

describe('Window toolbar — select all', () => {
  function selectallButton(): HTMLButtonElement {
    return screen.getByLabelText('Select all') as HTMLButtonElement;
  }
  function uploadButton(): HTMLButtonElement {
    return screen.getByLabelText('Upload') as HTMLButtonElement;
  }
  function infoButton(): HTMLButtonElement {
    return screen.getByLabelText('Information') as HTMLButtonElement;
  }

  it('places the Select all button between Upload and Information', async () => {
    await openAndRender();
    const toolbar = document.querySelector('.toolbar') as HTMLElement;
    const tools = [...toolbar.querySelectorAll('.tool')];
    const byClass = (cls: string) => tools.findIndex((t) => t.classList.contains(cls));
    const iUpload = byClass('upload');
    const iSelect = byClass('selectall');
    const iInfo = byClass('info');
    expect(iSelect).not.toBe(-1);
    expect(iSelect).toBe(iUpload + 1);
    expect(iInfo).toBe(iSelect + 1);
    expect(selectallButton()).not.toBeDisabled();
  });

  it('selects every item in the opened folder', async () => {
    await openAndRender();
    // 4 entries at the root: documents, photos, myfiles.zip, notes.txt.
    const rows = screen.getByRole('table').querySelectorAll('tbody tr.row');
    expect(rows.length).toBe(4);
    fireEvent.click(selectallButton());
    await tick();
    const selectedRows = screen.getByRole('table').querySelectorAll('tbody tr.row.selected');
    expect(selectedRows.length).toBe(4);
    // The Information badge reflects the selection count.
    expect(infoButton().querySelector('.badge')?.textContent).toBe('4');
  });

  it('disables Select all when the folder is empty', async () => {
    await openAndRender();
    (fetchListing as Mock).mockResolvedValueOnce({ path: ROOT, entries: [] });
    fireEvent.click(screen.getByLabelText('Reload'));
    await waitFor(() => expect(selectallButton()).toBeDisabled());
  });
});

describe('Window reload — task completion', () => {
  it('re-fetches the opened directory when a task-completion reload event arrives', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    (fetchListing as Mock).mockClear();
    // Simulate the SSE task-completion handler reloading windows for this path.
    window.dispatchEvent(
      new CustomEvent('filetoaster:reload', { detail: { windowId: id, path: '/root/documents' } })
    );
    await tick();
    await waitFor(() => expect(fetchListing).toHaveBeenCalledWith('/root/documents'));
  });

  it('does not reload when the event targets a different window', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    (fetchListing as Mock).mockClear();
    const before = (fetchListing as Mock).mock.calls.length;
    // An event addressed to another window must be ignored by this one.
    window.dispatchEvent(
      new CustomEvent('filetoaster:reload', {
        detail: { windowId: 'some-other-window-id', path: '/root/documents' }
      })
    );
    await tick();
    await waitFor(() => expect((fetchListing as Mock).mock.calls.length).toBe(before));
  });

  it('reloads the tree and clears loading after compress task completion', async () => {
    await openAndRender();
    (fetchListing as Mock).mockClear();
    window.dispatchEvent(
      new CustomEvent('filetoaster:reload', { detail: { windowId: id, path: '/root' } })
    );
    await tick();
    await waitFor(() => expect(fetchListing).toHaveBeenCalledWith('/root'));
    await waitFor(() => expect(fileTree.isLoading('/root')).toBe(false));
    expect(within(tree()).getByText('documents')).toBeInTheDocument();
  });

  it('ignores concurrent reload events for the same window (reentrancy guard)', async () => {
    await openAndRender();
    (fetchListing as Mock).mockClear();
    // Dispatch two reload events in quick succession for the same window.
    window.dispatchEvent(
      new CustomEvent('filetoaster:reload', { detail: { windowId: id, path: '/root' } })
    );
    window.dispatchEvent(
      new CustomEvent('filetoaster:reload', { detail: { windowId: id, path: '/root' } })
    );
    await tick();
    // Only one fetch should occur (the second is ignored by the reentrancy guard).
    await waitFor(() => expect(fetchListing).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(fetchListing).toHaveBeenCalledWith('/root'));
    await waitFor(() => expect(fileTree.isLoading('/root')).toBe(false));
    await waitFor(() => expect(fileTree.isLoading('/root')).toBe(false));
  });
});

// Regression: when the configured allowed root is the filesystem root ("/"),
// naive `root + '/'` checks produced "//", so every tree click (and every jump
// to a deeper folder) silently no-oped and the window stayed on the previously
// opened directory.
describe('Window tree — filesystem root anchor', () => {
  type Row = { path: string; name: string; type: 'file' | 'dir'; size: number; created: string; modified: string };
  const ROOT_FS: Record<string, Row[]> = {
    '/': [
      { path: '/data', name: 'data', type: 'dir', size: 0, created: '', modified: '' },
      { path: '/home', name: 'home', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/data': [
      { path: '/data/docs', name: 'docs', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/data/docs': [
      { path: '/data/docs/archive', name: 'archive', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/data/docs/archive': [
      { path: '/data/docs/archive/2024', name: '2024', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/data/docs/archive/2024': [],
    '/home': [
      { path: '/home/readme.txt', name: 'readme.txt', type: 'file', size: 4, created: '', modified: '' }
    ]
  };

  async function expand(name: string, child: string): Promise<void> {
    const row = within(tree()).getByTitle(name).closest('.trow') as HTMLElement;
    fireEvent.click(row.querySelector('.twisty') as Element);
    await waitFor(() => expect(within(tree()).getByTitle(child)).toBeInTheDocument());
  }

  it('opens a root-level folder after expanding several levels, and jumps deep directly', async () => {
    (fetchListing as Mock).mockImplementation(async (path: string) => ({
      path,
      entries: ROOT_FS[path] ?? []
    }));
    openWindow({ name: 'Files', path: '/' });
    const list = get(windows);
    id = list[list.length - 1].id;
    renderComp(Window, { window: list[list.length - 1] });
    await waitFor(() => expect(within(tree()).getByTitle('/data')).toBeInTheDocument());

    await expand('/data', '/data/docs');
    await expand('/data/docs', '/data/docs/archive');
    await expand('/data/docs/archive', '/data/docs/archive/2024');

    // Root-level folder click must open it, not revert to the previous folder.
    fireEvent.click(within(tree()).getByTitle('/home'));
    await waitFor(() => expect(currentStorePath()).toBe('/home'));
    expect(within(screen.getByRole('table')).getByText('readme.txt')).toBeInTheDocument();

    // A deeply nested folder must open in one direct click as well.
    fireEvent.click(within(tree()).getByTitle('/data/docs/archive/2024'));
    await waitFor(() => expect(currentStorePath()).toBe('/data/docs/archive/2024'));
  });
});

// Regression: tree rows used to navigate from the synthesized `click` only.
// Expanding a deep branch renders per-directory "Loading…" rows and re-renders
// the tree, so the row under the pointer can move between mousedown and mouseup.
// The browser then retargets the click to an ancestor <li> that has no handler,
// the navigation is dropped, and the window stays on the previously opened
// folder (which looks like the selection "jumping back"). Rows now navigate on
// the initial press, with the trailing click deduplicated.
describe('Window tree — press-to-navigate', () => {
  function node(path: string): HTMLElement {
    return within(tree()).getByTitle(path) as HTMLElement;
  }

  async function expandTwisty(path: string, child: string): Promise<void> {
    fireEvent.click(node(path).querySelector('.twisty') as Element);
    await waitFor(() => expect(within(tree()).getByTitle(child)).toBeInTheDocument());
  }

  // Deeper fixture so a real 3+ level chain exists under the normal root.
  const DEEP: typeof FS = {
    ...FS,
    '/root/documents/work': [
      { path: '/root/documents/work/projects', name: 'projects', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/root/documents/work/projects': [
      { path: '/root/documents/work/projects/archive', name: 'archive', type: 'dir', size: 0, created: '', modified: '' }
    ],
    '/root/documents/work/projects/archive': []
  };
  function useDeepFixture(): void {
    (fetchListing as Mock).mockImplementation(async (path: string) => ({
      path,
      entries: DEEP[path] ?? []
    }));
  }

  it('navigates on mousedown and ignores the trailing mouse click', async () => {
    await openAndRender();
    (fetchListing as Mock).mockClear();

    const documents = node('/root/documents');
    fireEvent.mouseDown(documents.querySelector('.tname') as Element, { button: 0 });
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));

    // A real mouse click follows the press (detail > 0); it must not navigate
    // again and must not issue another listing request.
    const calls = (fetchListing as Mock).mock.calls.length;
    fireEvent.click(documents.querySelector('.tname') as Element, { detail: 1 });
    await tick();
    expect((fetchListing as Mock).mock.calls.length).toBe(calls);
    expect(currentStorePath()).toBe('/root/documents');
  });

  it('opens a root-level folder after several levels are expanded', async () => {
    await openAndRender();
    useDeepFixture();
    await expandTwisty('/root/documents', '/root/documents/work');
    await expandTwisty('/root/documents/work', '/root/documents/work/projects');
    await expandTwisty('/root/documents/work/projects', '/root/documents/work/projects/archive');
    await expandTwisty('/root/photos', '/root/photos/2023');

    fireEvent.mouseDown(node('/root/photos').querySelector('.tname') as Element, { button: 0 });
    await waitFor(() => expect(currentStorePath()).toBe('/root/photos'));
    expect(within(screen.getByRole('table')).getByText('2023')).toBeInTheDocument();
  });

  it('jumps to a deep folder directly from a fully expanded tree', async () => {
    await openAndRender();
    useDeepFixture();
    await expandTwisty('/root/documents', '/root/documents/work');
    await expandTwisty('/root/documents/work', '/root/documents/work/projects');
    await expandTwisty('/root/documents/work/projects', '/root/documents/work/projects/archive');

    fireEvent.mouseDown(
      node('/root/documents/work/projects/archive').querySelector('.tname') as Element,
      { button: 0 }
    );
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents/work/projects/archive'));
  });

  it('keeps deep rows full-width clickable: children render in a nested list', async () => {
    await openAndRender();
    useDeepFixture();
    await expandTwisty('/root/documents', '/root/documents/work');
    await expandTwisty('/root/documents/work', '/root/documents/work/projects');
    await expandTwisty('/root/documents/work/projects', '/root/documents/work/projects/archive');

    // Children live in a nested <ul>, not directly inside a padded <li>, so the
    // row button still spans the whole tree width at any depth.
    const childList = node('/root/documents/work/projects/archive').closest('.tchildren');
    expect(childList).not.toBeNull();
    expect(node('/root/documents').closest('.trow')?.getAttribute('style')).toBeNull();
    expect(node('/root/documents').style.paddingLeft).toBe('20px');
    expect(node('/root/documents/work').style.paddingLeft).toBe('32px');
    expect(node('/root/documents/work/projects').style.paddingLeft).toBe('44px');
    expect(node('/root/documents/work/projects/archive').style.paddingLeft).toBe('56px');
  });

  it('does not navigate when the press lands on the expand twisty', async () => {
    await openAndRender();
    fireEvent.mouseDown(node('/root/documents').querySelector('.twisty') as Element, { button: 0 });
    await tick();
    expect(currentStorePath()).toBe('/root');
  });

  it('lets the newest press win even when an older deep navigation resolves later', async () => {
    await openAndRender();
    await expandTwisty('/root/documents', '/root/documents/work');

    let resolveDeep!: (listing: { path: string; entries: unknown[] }) => void;
    const deep = new Promise<{ path: string; entries: unknown[] }>((resolve) => {
      resolveDeep = resolve;
    });
    (fetchListing as Mock).mockImplementation((path: string) => {
      if (path === '/root/documents/work') return deep;
      return Promise.resolve({ path, entries: FS[path] ?? [] });
    });

    // Start a slow deep navigation, then press a root-level folder before it
    // settles. The newer press must be the folder the window ends up showing.
    fireEvent.mouseDown(
      node('/root/documents/work').querySelector('.tname') as Element,
      { button: 0 }
    );
    fireEvent.mouseDown(node('/root/photos').querySelector('.tname') as Element, { button: 0 });
    await waitFor(() => expect(currentStorePath()).toBe('/root/photos'));

    resolveDeep({ path: '/root/documents/work', entries: FS['/root/documents/work'] });
    await tick();
    await tick();
    expect(currentStorePath()).toBe('/root/photos');
  });
});

describe('Window status bar — copy path button', () => {
  beforeEach(() => {
    vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
  });

  it('shows the copy path button next to the path', async () => {
    await openAndRender();
    const copyBtn = screen.getByLabelText('Copy path');
    expect(copyBtn).toBeInTheDocument();
  });

  it('copies the current path to clipboard on click', async () => {
    await openAndRender();
    const copyBtn = screen.getByLabelText('Copy path');
    fireEvent.click(copyBtn);
    await tick();
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('/root');
  });

  it('copies the navigated path after navigation', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    const copyBtn = screen.getByLabelText('Copy path');
    fireEvent.click(copyBtn);
    await tick();
    expect(navigator.clipboard.writeText).toHaveBeenCalledWith('/root/documents');
  });

  it('flashes a success message on copy', async () => {
    await openAndRender();
    const copyBtn = screen.getByLabelText('Copy path');
    fireEvent.click(copyBtn);
    await waitFor(() => expect(screen.getByText('Path copied to clipboard.')).toBeInTheDocument());
  });

  it('flashes an error message if clipboard write fails', async () => {
    vi.spyOn(navigator.clipboard, 'writeText').mockRejectedValue(new Error('permission denied'));
    await openAndRender();
    const copyBtn = screen.getByLabelText('Copy path');
    fireEvent.click(copyBtn);
    await waitFor(() => expect(screen.getByText('Failed to copy path.')).toBeInTheDocument());
  });
});

// Keyboard shortcuts. Every window listens on `window`, but only the active
// (top-most, non-minimized) window reacts; these tests cover the main-list
// navigation and each ctrl+alt binding, plus the per-window isolation rule.
describe('Window keyboard — arrow navigation', () => {
  function listingRows(): HTMLElement[] {
    return [...screen.getByRole('table').querySelectorAll('tbody tr.row')] as HTMLElement[];
  }
  function selectedRows(): HTMLElement[] {
    return [...screen.getByRole('table').querySelectorAll('tbody tr.row.selected')] as HTMLElement[];
  }
  function selectedNames(): string[] {
    return selectedRows().map((r) => r.getAttribute('data-row-path') ?? '');
  }
  function press(key: string, opts: KeyboardEventInit = {}): void {
    fireEvent.keyDown(document.body, { key, ...opts });
  }

  it('ArrowDown selects the first row and moves down through the sorted list', async () => {
    await openAndRender();
    expect(selectedRows()).toHaveLength(0);
    press('ArrowDown');
    await tick();
    expect(selectedNames()).toEqual(['/root/documents']);
    press('ArrowDown');
    await tick();
    expect(selectedNames()).toEqual(['/root/photos']);
    press('ArrowDown');
    await tick();
    expect(selectedNames()).toEqual(['/root/myfiles.zip']);
  });

  it('ArrowUp moves back up and clamps at the first row', async () => {
    await openAndRender();
    press('ArrowDown');
    await tick();
    press('ArrowDown');
    await tick();
    expect(selectedNames()).toEqual(['/root/photos']);
    press('ArrowUp');
    await tick();
    expect(selectedNames()).toEqual(['/root/documents']);
    press('ArrowUp');
    await tick();
    expect(selectedNames()).toEqual(['/root/documents']);
  });

  it('ArrowUp with nothing selected selects the last row', async () => {
    await openAndRender();
    press('ArrowUp');
    await tick();
    expect(selectedNames()).toEqual(['/root/notes.txt']);
  });

  it('Shift+ArrowDown extends the selection downwards from the anchor', async () => {
    await openAndRender();
    press('ArrowDown');
    await tick();
    press('ArrowDown', { shiftKey: true });
    await tick();
    expect(selectedNames()).toEqual(['/root/documents', '/root/photos']);
    press('ArrowDown', { shiftKey: true });
    await tick();
    expect(selectedNames()).toEqual(['/root/documents', '/root/photos', '/root/myfiles.zip']);
  });

  it('Shift+ArrowUp shrinks the selection back towards the anchor', async () => {
    await openAndRender();
    press('ArrowDown');
    await tick();
    press('ArrowDown', { shiftKey: true });
    await tick();
    press('ArrowDown', { shiftKey: true });
    await tick();
    expect(selectedRows()).toHaveLength(3);
    press('ArrowUp', { shiftKey: true });
    await tick();
    expect(selectedNames()).toEqual(['/root/documents', '/root/photos']);
  });

  it('keeps the cursor row scrolled into view', async () => {
    const scrollSpy = vi.spyOn(Element.prototype, 'scrollIntoView');
    try {
      await openAndRender();
      scrollSpy.mockClear();
      press('ArrowDown');
      await waitFor(() => expect(scrollSpy).toHaveBeenCalled());
    } finally {
      scrollSpy.mockRestore();
    }
  });

  it('ignores arrow keys while typing in an input', async () => {
    await openAndRender();
    const user = userEvent.setup();
    await user.click(screen.getByLabelText('New directory'));
    const input = await waitFor(() => screen.getByPlaceholderText('New directory') as HTMLInputElement);
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    await tick();
    expect(selectedRows()).toHaveLength(0);
  });
});

describe('Window keyboard — Enter opens a directory', () => {
  function press(key: string, opts: KeyboardEventInit = {}): void {
    fireEvent.keyDown(document.body, { key, ...opts });
  }

  it('opens the selected directory', async () => {
    await openAndRender();
    press('ArrowDown');
    await tick();
    press('Enter');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    expect(within(screen.getByRole('table')).getByText('work')).toBeInTheDocument();
  });

  it('does nothing on a selected file', async () => {
    await openAndRender();
    press('ArrowDown');
    press('ArrowDown');
    press('ArrowDown');
    await tick();
    expect(screen.getByRole('table').querySelector('.row.selected')?.textContent).toContain('myfiles.zip');
    press('Enter');
    await tick();
    expect(currentStorePath()).toBe(ROOT);
    expect(within(screen.getByRole('table')).getByText('notes.txt')).toBeInTheDocument();
  });

  it('does nothing when the selection is empty', async () => {
    await openAndRender();
    press('Enter');
    await tick();
    expect(currentStorePath()).toBe(ROOT);
  });
});

describe('Window keyboard — ctrl+alt actions', () => {
  function press(key: string, opts: KeyboardEventInit = {}): void {
    fireEvent.keyDown(document.body, { key, ctrlKey: true, altKey: true, ...opts });
  }
  function clickRow(name: string): void {
    const row = [...screen.getByRole('table').querySelectorAll('tbody tr.row')]
      .find((tr) => tr.textContent?.includes(name)) as HTMLElement;
    if (!row) throw new Error(`no row named ${name}`);
    fireEvent.click(row);
  }

  beforeEach(() => {
    clipboard.set([]);
    cutMode.set(false);
    cutEntries.set([]);
    taskPanelOpen.set(false);
    (submitCopyTask as Mock).mockClear();
    (submitCopyTask as Mock).mockResolvedValue({ taskID: 'task-1' });
    (setClipboard as Mock).mockClear();
    (setCut as Mock).mockClear();
    (submitDeleteTask as Mock).mockClear();
    (submitDeleteTask as Mock).mockResolvedValue({ taskID: 'task-9' });
    (submitDecompressTask as Mock).mockClear();
  });

  it('CTRL + ALT + R reloads the current folder', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    const listing = fetchListing as Mock;
    listing.mockClear();
    press('r');
    await waitFor(() => expect(listing).toHaveBeenCalledWith('/root/documents'));
  });

  it('CTRL + ALT + C copies the selection', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    press('c');
    await tick();
    expect(get(clipboard)).toEqual(['/root/notes.txt']);
    expect(setClipboard).toHaveBeenCalledWith(['/root/notes.txt']);
  });

  it('CTRL + ALT + V pastes into the open folder', async () => {
    await openAndRender();
    clipboard.set(['/root/notes.txt']);
    await tick();
    press('v');
    await waitFor(() =>
      expect(submitCopyTask).toHaveBeenCalledWith(['/root/notes.txt'], ROOT)
    );
  });

  it('CTRL + ALT + X cuts the selection', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    press('x');
    await tick();
    expect(get(cutMode)).toBe(true);
    expect(get(clipboard)).toEqual(['/root/notes.txt']);
  });

  it('CTRL + ALT + D opens the delete confirmation', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    press('d');
    await waitFor(() => expect(screen.getByTestId('delete-dialog')).toBeInTheDocument());
  });

  it('CTRL + ALT + A selects every row', async () => {
    await openAndRender();
    press('a');
    await tick();
    expect(screen.getByRole('table').querySelectorAll('tbody tr.row.selected')).toHaveLength(4);
  });

  it('CTRL + ALT + L downloads the selected file', async () => {
    const realLocation = window.location;
    const fake = Object.create(Object.getPrototypeOf(realLocation)) as Location & { assign: MockInstance };
    fake.assign = vi.fn();
    Object.defineProperty(window, 'location', { value: fake, configurable: true });
    try {
      (downloadFile as Mock).mockReset();
      (downloadFile as Mock).mockResolvedValue('tok-xyz');
      await openAndRender();
      clickRow('notes.txt');
      await tick();
      press('l');
      await waitFor(() => expect(downloadFile).toHaveBeenCalledWith('/root/notes.txt'));
      expect(fake.assign).toHaveBeenCalledWith('/downloads/tok-xyz');
    } finally {
      Object.defineProperty(window, 'location', { value: realLocation, configurable: true });
    }
  });

  it('CTRL + ALT + N starts a new directory', async () => {
    await openAndRender();
    press('n');
    await waitFor(() => expect(screen.getByPlaceholderText('New directory')).toBeInTheDocument());
  });

  it('CTRL + ALT + T opens and closes the tasks list', async () => {
    await openAndRender();
    expect(get(taskPanelOpen)).toBe(false);
    press('t');
    await tick();
    expect(get(taskPanelOpen)).toBe(true);
    press('t');
    await tick();
    expect(get(taskPanelOpen)).toBe(false);
  });

  it('CTRL + ALT + ArrowUp goes up one directory', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    press('ArrowUp');
    await waitFor(() => expect(currentStorePath()).toBe(ROOT));
  });

  it('CTRL + ALT + Z opens the compress dialog', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    press('z');
    await waitFor(() => expect(screen.getByTestId('compress-dialog')).toBeInTheDocument());
  });

  it('CTRL + ALT + U opens the decompress dialog for an archive', async () => {
    await openAndRender();
    clickRow('myfiles.zip');
    await tick();
    press('u');
    await waitFor(() => expect(screen.getByTestId('decompress-dialog')).toBeInTheDocument());
  });

  it('CTRL + ALT + M opens the rename dialog', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    press('m');
    await waitFor(() => expect(screen.getByPlaceholderText('Rename')).toBeInTheDocument());
  });
});

describe('Window keyboard — per-window isolation', () => {
  async function renderTwoWindows(): Promise<{ first: HTMLElement; second: HTMLElement; ids: [string, string] }> {
    openWindow({ name: 'First', path: ROOT });
    openWindow({ name: 'Second', path: ROOT });
    const list = get(windows);
    const ids: [string, string] = [list[0].id, list[1].id];
    renderComp(Window, { window: list[0] });
    renderComp(Window, { window: list[1] });
    await waitFor(() => {
      expect(document.querySelectorAll('.window').length).toBe(2);
      expect(document.querySelectorAll('.window tbody tr.row').length).toBe(8);
    });
    const els = [...document.querySelectorAll('.window')] as HTMLElement[];
    return { first: els[0], second: els[1], ids };
  }
  function selectedIn(el: HTMLElement): number {
    return el.querySelectorAll('tbody tr.row.selected').length;
  }
  function press(key: string, opts: KeyboardEventInit = {}): void {
    fireEvent.keyDown(document.body, { key, ...opts });
  }

  it('only the active window responds to arrows and select all', async () => {
    const { first, second } = await renderTwoWindows();
    // The second window was created last, so it is on top / active.
    press('ArrowDown');
    await tick();
    expect(selectedIn(first)).toBe(0);
    expect(selectedIn(second)).toBe(1);

    press('a', { ctrlKey: true, altKey: true });
    await tick();
    expect(selectedIn(first)).toBe(0);
    expect(selectedIn(second)).toBe(4);
  });

  it('shift+arrow ranges stay confined to the active window', async () => {
    const { first, second } = await renderTwoWindows();
    press('ArrowDown');
    await tick();
    press('ArrowDown', { shiftKey: true });
    await tick();
    expect(selectedIn(first)).toBe(0);
    expect(selectedIn(second)).toBe(2);
  });

  it('Enter only navigates the active window', async () => {
    const { ids } = await renderTwoWindows();
    press('ArrowDown');
    await tick();
    press('Enter');
    await waitFor(() => {
      const list = get(windows);
      expect(list.find((w) => w.id === ids[1])?.path).toBe('/root/documents');
      expect(list.find((w) => w.id === ids[0])?.path).toBe(ROOT);
    });
  });
});

describe('Window keyboard — targeted events address one window', () => {
  async function renderTwoWindows(): Promise<{ first: HTMLElement; second: HTMLElement }> {
    openWindow({ name: 'First', path: ROOT });
    openWindow({ name: 'Second', path: ROOT });
    const list = get(windows);
    renderComp(Window, { window: list[0] });
    renderComp(Window, { window: list[1] });
    await waitFor(() => {
      expect(document.querySelectorAll('.window').length).toBe(2);
      expect(document.querySelectorAll('.window tbody tr.row').length).toBe(8);
    });
    const els = [...document.querySelectorAll('.window')] as HTMLElement[];
    return { first: els[0], second: els[1] };
  }
  function selectedIn(el: HTMLElement): number {
    return el.querySelectorAll('tbody tr.row.selected').length;
  }

  it('an event dispatched to a background window does not move the active one', async () => {
    const { first, second } = await renderTwoWindows();
    fireEvent.keyDown(first, { key: 'ArrowDown' });
    await tick();
    expect(selectedIn(first)).toBe(1);
    expect(selectedIn(second)).toBe(0);
  });
});

// Regression: with Ctrl+Alt held, many browsers/layouts report the *produced*
// character in `e.key` (AltGr behavior: "®" instead of "r", "@" instead of "q")
// or leave it as "Unidentified". The shortcut must match the physical `e.code`
// ("KeyR") so the letter bindings still fire.
describe('Window keyboard — ctrl+alt letters via the physical key code', () => {
  function pressCode(letter: string): void {
    fireEvent.keyDown(document.body, {
      key: 'Unidentified',
      code: `Key${letter.toUpperCase()}`,
      ctrlKey: true,
      altKey: true
    });
  }
  function clickRow(name: string, modifier?: 'ctrl' | 'shift'): void {
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
  }
  function infoButton(): HTMLButtonElement {
    return screen.getByLabelText('Information') as HTMLButtonElement;
  }

  beforeEach(() => {
    clipboard.set([]);
    cutMode.set(false);
    cutEntries.set([]);
    taskPanelOpen.set(false);
    (fetchInfo as Mock).mockClear();
    (fetchInfo as Mock).mockResolvedValue([]);
    (submitCopyTask as Mock).mockClear();
    (submitCopyTask as Mock).mockResolvedValue({ taskID: 'task-1' });
    (submitDeleteTask as Mock).mockClear();
    (submitDeleteTask as Mock).mockResolvedValue({ taskID: 'task-9' });
  });

  it('CTRL + ALT + R reloads (AltGr reports a produced character)', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    const listing = fetchListing as Mock;
    listing.mockClear();
    fireEvent.keyDown(document.body, { key: '®', code: 'KeyR', ctrlKey: true, altKey: true });
    await waitFor(() => expect(listing).toHaveBeenCalledWith('/root/documents'));
  });

  it('CTRL + ALT + C copies', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    pressCode('C');
    await tick();
    expect(get(clipboard)).toEqual(['/root/notes.txt']);
  });

  it('CTRL + ALT + V pastes', async () => {
    await openAndRender();
    clipboard.set(['/root/notes.txt']);
    await tick();
    pressCode('V');
    await waitFor(() =>
      expect(submitCopyTask).toHaveBeenCalledWith(['/root/notes.txt'], ROOT)
    );
  });

  it('CTRL + ALT + X cuts', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    pressCode('X');
    await tick();
    expect(get(cutMode)).toBe(true);
  });

  it('CTRL + ALT + D opens the delete confirmation', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    pressCode('D');
    await waitFor(() => expect(screen.getByTestId('delete-dialog')).toBeInTheDocument());
  });

  it('CTRL + ALT + A selects all', async () => {
    await openAndRender();
    pressCode('A');
    await tick();
    expect(screen.getByRole('table').querySelectorAll('tbody tr.row.selected')).toHaveLength(4);
  });

  it('CTRL + ALT + I opens the information dialog', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await tick();
    pressCode('I');
    await waitFor(() => expect(screen.getByTestId('info-dialog')).toBeInTheDocument());
    expect(fetchInfo).toHaveBeenCalledWith(['/root/notes.txt']);
  });

  it('shows file type in the Information dialog', async () => {
    await openAndRender();
    clickRow('notes.txt');
    await clickRow('documents', 'ctrl');
    await tick();

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
    await waitFor(() => expect(screen.getByTestId('info-dialog')).toBeInTheDocument());

    const dlg = screen.getByTestId('info-dialog');
    const headers = [...dlg.querySelectorAll('thead th')].map((th) => th.textContent?.trim());
    expect(headers).toContain('Type');
    const nameIdx = headers.indexOf('Name');
    const typeIdx = headers.indexOf('Type');
    const sizeIdx = headers.indexOf('Size');
    expect(typeIdx).toBe(nameIdx + 1);
    expect(sizeIdx).toBe(typeIdx + 1);

    // Check the type column values in the dialog table
    const rows = [...dlg.querySelectorAll('tbody tr.row')];
    const notesRow = rows.find((r) => r.textContent?.includes('notes.txt')) as HTMLElement;
    expect(notesRow).toBeTruthy();
    const notesTypeCell = notesRow?.querySelector('td.type');
    expect(notesTypeCell?.textContent?.trim()).toBe('TXT');

    const docsRow = rows.find((r) => r.textContent?.includes('documents')) as HTMLElement;
    expect(docsRow).toBeTruthy();
    const docsTypeCell = docsRow?.querySelector('td.type');
    expect(docsTypeCell?.textContent?.trim()).toBe('DIR');
  });

  it('CTRL + ALT + L downloads', async () => {
    const realLocation = window.location;
    const fake = Object.create(Object.getPrototypeOf(realLocation)) as Location & { assign: MockInstance };
    fake.assign = vi.fn();
    Object.defineProperty(window, 'location', { value: fake, configurable: true });
    try {
      (downloadFile as Mock).mockReset();
      (downloadFile as Mock).mockResolvedValue('tok-code');
      await openAndRender();
      clickRow('notes.txt');
      await tick();
      pressCode('L');
      await waitFor(() => expect(downloadFile).toHaveBeenCalledWith('/root/notes.txt'));
    } finally {
      Object.defineProperty(window, 'location', { value: realLocation, configurable: true });
    }
  });

  it('CTRL + ALT + N starts a new directory', async () => {
    await openAndRender();
    pressCode('N');
    await waitFor(() => expect(screen.getByPlaceholderText('New directory')).toBeInTheDocument());
  });

  it('CTRL + ALT + T toggles the tasks list', async () => {
    await openAndRender();
    pressCode('T');
    await tick();
    expect(get(taskPanelOpen)).toBe(true);
  });

  it('keeps CTRL + ALT + ArrowUp working (arrows are unaffected by AltGr)', async () => {
    await openAndRender();
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    fireEvent.keyDown(document.body, { key: 'ArrowUp', code: 'ArrowUp', ctrlKey: true, altKey: true });
    await waitFor(() => expect(currentStorePath()).toBe(ROOT));
  });
});

describe('Window search', () => {
  const submitSearchMock = submitSearch as unknown as Mock;
  const cancelTaskMock = cancelTask as unknown as Mock;

  function searchInput(): HTMLInputElement {
    return screen.getByTestId('search-input') as HTMLInputElement;
  }

  function resultItem(name: string, path?: string, type: 'file' | 'dir' = 'file') {
    return {
      path: path ?? `/root/${name}`,
      name,
      type,
      size: 12,
      created: '2024-01-01T00:00:00Z',
      modified: '2024-01-01T00:00:00Z'
    };
  }

  async function startSearch(query: string, taskID = 'search-1'): Promise<void> {
    submitSearchMock.mockResolvedValue({ taskID });
    fireEvent.click(screen.getByLabelText('Search'));
    await tick();
    const input = searchInput();
    fireEvent.input(input, { target: { value: query } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(submitSearchMock).toHaveBeenCalled());
    await tick();
  }

  beforeEach(() => {
    searchSessions.set({});
    submitSearchMock.mockReset();
    cancelTaskMock.mockReset().mockResolvedValue(undefined);
  });

  it('toggles the search bar with the toolbar button', async () => {
    await openAndRender();
    expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
    fireEvent.click(screen.getByLabelText('Search'));
    await tick();
    expect(searchInput()).toBeInTheDocument();
    fireEvent.click(screen.getByTestId('close-search'));
    await tick();
    expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
  });

  it('submits a search for the opened folder with the typed query', async () => {
    await openAndRender();
    await startSearch('report');
    expect(submitSearchMock).toHaveBeenCalledTimes(1);
    expect(submitSearchMock.mock.calls[0][0]).toMatchObject({ rootPath: ROOT, query: 'report' });
  });

  it('only searches on Enter or search button press, not on each keystroke', async () => {
    await openAndRender();
    submitSearchMock.mockResolvedValue({ taskID: 'search-debounce' });
    fireEvent.click(screen.getByLabelText('Search'));
    await tick();
    const input = searchInput();
    fireEvent.input(input, { target: { value: 'a' } });
    fireEvent.input(input, { target: { value: 'ab' } });
    fireEvent.input(input, { target: { value: 'abc' } });
    // Nothing fires until Enter is pressed or search button clicked
    expect(submitSearchMock).not.toHaveBeenCalled();
    await new Promise((r) => setTimeout(r, 350));
    expect(submitSearchMock).not.toHaveBeenCalled();
    // Press Enter to trigger search
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(submitSearchMock).toHaveBeenCalledTimes(1));
    expect(submitSearchMock.mock.calls[0][0]).toMatchObject({ query: 'abc' });
  });

  it('renders streamed results in the listing table', async () => {
    await openAndRender();
    await startSearch('report');
    appendSearchResult('search-1', resultItem('report.txt', '/root/documents/work/report.txt'));
    completeSearch('search-1', 'finished', undefined, { scanned: 5, matches: 1 });
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('report.txt')).toBeInTheDocument());
    // The folder listing was replaced by the search results.
    expect(within(screen.getByRole('table')).queryByText('documents')).not.toBeInTheDocument();
  });

  it('shows a no-results message when a search completes empty', async () => {
    await openAndRender();
    await startSearch('nothing');
    completeSearch('search-1', 'finished', undefined, { scanned: 4, matches: 0 });
    await waitFor(() => expect(screen.getByTestId('search-empty')).toBeInTheDocument());
  });

  it('selects a search result row on click', async () => {
    await openAndRender();
    await startSearch('report');
    appendSearchResult('search-1', resultItem('report.txt', '/root/documents/work/report.txt'));
    completeSearch('search-1', 'finished');
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('report.txt')).toBeInTheDocument());
    const row = screen.getByText('report.txt').closest('tr') as HTMLElement;
    fireEvent.click(row);
    await tick();
    expect(row.classList.contains('selected')).toBe(true);
  });

  it('closes the search on Escape and cancels the task', async () => {
    await openAndRender();
    await startSearch('report');
    fireEvent.keyDown(searchInput(), { key: 'Escape' });
    await tick();
    expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
    expect(cancelTaskMock).toHaveBeenCalledWith('search-1');
  });

  it('cancels the search when navigating to a folder', async () => {
    await openAndRender();
    await startSearch('report');
    await clickNode('/root/documents');
    await waitFor(() => expect(currentStorePath()).toBe('/root/documents'));
    // Search mode ended and the task was cancelled.
    expect(screen.queryByTestId('search-input')).not.toBeInTheDocument();
    expect(cancelTaskMock).toHaveBeenCalledWith('search-1');
  });

  it('opens the advanced search dialog from the search bar', async () => {
    await openAndRender();
    fireEvent.click(screen.getByLabelText('Search'));
    await tick();
    fireEvent.click(screen.getByTestId('advanced-search'));
    await tick();
    expect(screen.getByTestId('advanced-search-dialog')).toBeInTheDocument();
  });

  it('opens the containing directory from a search result context menu', async () => {
    await openAndRender();
    await startSearch('report');
    appendSearchResult('search-1', resultItem('report.txt', '/root/documents/work/report.txt'));
    completeSearch('search-1', 'finished');
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('report.txt')).toBeInTheDocument());

    const row = screen.getByText('report.txt').closest('tr') as HTMLElement;
    fireEvent.contextMenu(row);
    await tick();
    const openContaining = screen.getByText('Open containing directory') as HTMLButtonElement;
    expect(openContaining).not.toBeDisabled();
    fireEvent.click(openContaining);
    await tick();
    // A new window opened at the result's parent folder.
    expect(get(windows).some((w) => w.path === '/root/documents/work')).toBe(true);
  });
});
