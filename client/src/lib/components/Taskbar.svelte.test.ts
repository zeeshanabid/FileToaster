import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { get, writable } from 'svelte/store';
import {
  fireEvent,
  screen,
  within,
  waitFor,
  getByText,
  getByLabelText,
  getByRole,
  queryByText
} from '@testing-library/dom';
import userEvent from '@testing-library/user-event';
import type { Mock, MockInstance } from 'vitest';
import Taskbar from './Taskbar.svelte';
import WindowButton from './WindowButton.svelte';
import RunningTaskIcon from './RunningTaskIcon.svelte';
import { openWindow, windows } from '$lib/store/windows';
import { tree as fileTree } from '$lib/fs/tree';

vi.mock('$lib/store/auth', () => ({
  logout: vi.fn().mockResolvedValue(undefined),
}));

vi.mock('$lib/store/tasks', async () => {
  const { writable, derived } = await import('svelte/store');
  const activeCountStore = writable(0);
  const taskPanelOpenStore = writable(false);
  return {
    initTasks: vi.fn(),
    shutdownTasks: vi.fn(),
    activeCount: activeCountStore,
    combinedActiveCount: derived(activeCountStore, ($a: number) => $a),
    taskPanelOpen: taskPanelOpenStore,
    clipboard: writable([]),
    cutMode: writable(false),
    cutEntries: writable([]),
    setClipboard: vi.fn(),
    clearClipboard: vi.fn(),
    setCut: vi.fn(),
    submitCopyTask: vi.fn(async () => ({ taskID: 'task-1' })),
    submitDeleteTask: vi.fn(async () => ({ taskID: 'task-9' })),
    submitMoveTask: vi.fn(async () => ({ taskID: 'task-5' })),
    submitCompressTask: vi.fn(async () => ({ taskID: 'task-7' })),
    submitDecompressTask: vi.fn(async () => ({ taskID: 'task-8' }))
  };
});

vi.mock('$lib/fs/api', () => ({
  fetchListing: vi.fn(),
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
  downloadFile: vi.fn()
}));

// Mock fetch for roots
const mockFetch = vi.fn();
window.fetch = mockFetch as unknown as typeof window.fetch;

const NAME = 'Documents';
const ROOT = '/root';
let changePasswordFn: Mock;

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

beforeEach(() => {
  windows.set([]);
  fileTree.resetAll();
  changePasswordFn = vi.fn();
  mockFetch.mockReset();
  // Mock the roots fetch
  mockFetch.mockResolvedValue({
    ok: true,
    json: async () => ({
      allowed_paths: [
        { name: 'Documents', path: '/root' },
        { name: 'Photos', path: '/root/photos' }
      ]
    })
  });
});

afterEach(() => {
  unmountAll();
  windows.set([]);
  vi.clearAllMocks();
});

describe('Taskbar', () => {
  it('renders the launcher, windows area, and system controls', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    // Wait for roots to load
    await waitFor(() => expect(screen.getByLabelText('FileToaster menu')).toBeInTheDocument());
    expect(screen.getByLabelText('Open windows')).toBeInTheDocument();
    expect(screen.getByLabelText('System controls')).toBeInTheDocument();
  });

  it('shows "No windows open" when no windows are open', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByText('No windows open')).toBeInTheDocument());
  });

  it('shows window buttons when windows are open', async () => {
    openWindow({ name: NAME, path: ROOT });
    const list = get(windows);
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByText('Documents')).toBeInTheDocument());
    expect(screen.queryByText('No windows open')).not.toBeInTheDocument();
  });

  it('shows the Tasks button', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByLabelText('Tasks')).toBeInTheDocument());
  });

  it('shows the Change password button', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByLabelText('Change password')).toBeInTheDocument());
  });

  it('shows the Log out button', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByLabelText('Log out')).toBeInTheDocument());
  });

  it('shows the Open path in new window button between Tasks and Change password', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => {
      const system = screen.getByLabelText('System controls');
      const buttons = [...system.querySelectorAll('button.system-btn')];
      expect(buttons.length).toBe(3);
      expect(buttons[0]).toHaveAttribute('aria-label', 'Open path in new window');
      expect(buttons[1]).toHaveAttribute('aria-label', 'Change password');
      expect(buttons[2]).toHaveAttribute('aria-label', 'Log out');
    });
  });

  it('focuses the input when the Open path modal opens', async () => {
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByLabelText('Tasks')).toBeInTheDocument());
    
    // Click the Open path button
    await userEvent.click(screen.getByLabelText('Open path in new window'));
    
    // Wait for modal to open and input to be focused
    await waitFor(() => {
      const input = screen.getByPlaceholderText('/path/to/directory') as HTMLInputElement;
      expect(input).toBeInTheDocument();
      expect(document.activeElement).toBe(input);
    });
  });

  it('opens new window with correct rootPath and name from taskbar modal', async () => {
    // Ensure clean state
    windows.set([]);
    fileTree.resetAll();
    
    renderComp(Taskbar, { onChangePassword: changePasswordFn });
    await waitFor(() => expect(screen.getByLabelText('Tasks')).toBeInTheDocument());
    
    // Mock fetch for validation (second call after roots fetch)
    mockFetch
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({
          allowed_paths: [
            { name: 'Documents', path: '/root' },
            { name: 'Photos', path: '/root/photos' }
          ]
        })
      })
      .mockResolvedValueOnce({
        ok: true,
        json: async () => ({ valid: true, path: '/root/documents/work' })
      });

    // Click the Open path button
    await userEvent.click(screen.getByLabelText('Open path in new window'));
    
    // Enter a subdirectory path
    await userEvent.type(screen.getByPlaceholderText('/path/to/directory'), '/root/documents/work');
    await userEvent.click(screen.getByRole('button', { name: 'Open' }));
    
    // Wait for modal to close
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    
    // Check that a new window was opened with correct rootPath and name
    const list = get(windows);
    expect(list.length).toBe(1);
    const newWin = list[0];
    expect(newWin.path).toBe('/root/documents/work');
    expect(newWin.rootPath).toBe('/root');
    expect(newWin.name).toBe('Documents');
  });
});