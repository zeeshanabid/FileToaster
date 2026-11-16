import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { get, writable } from 'svelte/store';
import { openWindow, closeWindow, updateWindow, findWindowsByPath, reloadWindowsForPath, windows, type AppWindow } from './windows';

// Reset the module-level windows store between tests so state does not leak.
function resetWindows() {
  closeWindow('does-not-exist');
  // Clear the store by replacing it via a test hook: since `windows` is a
  // module singleton, we use the exported functions to build state. To fully
  // reset we instead use the writable update through the exported store.
  // Simplest: push an empty list by closing all windows via a derived helper.
  // We cannot assign to the store directly, so we close windows we opened.
}

describe('findWindowsByPath', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    // Close any windows left over from previous tests.
    windows.update((list) => list.filter((w) => !w.path.startsWith('/tmp/')));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('finds windows showing the exact target path', () => {
    openWindow({ name: 'Root', path: '/tmp/win-test' });
    const win1 = get(windows).find((w) => w.rootPath === '/tmp/win-test');
    expect(win1).toBeDefined();

    openWindow({ name: 'Root', path: '/tmp/win-test' });
    const win2 = get(windows).find((w) => w.rootPath === '/tmp/win-test' && w.id !== win1!.id);
    expect(win2).toBeDefined();

    const found = findWindowsByPath('/tmp/win-test');
    expect(found.length).toBe(2);
    expect(found).toContain(win1!.id);
    expect(found).toContain(win2!.id);
  });

  it('finds windows showing a subdirectory of the target path', () => {
    openWindow({ name: 'Root', path: '/tmp/win-test' });
    const win = get(windows).find((w) => w.rootPath === '/tmp/win-test');
    expect(win).toBeDefined();

    updateWindow(win!.id, { path: '/tmp/win-test/documents' });

    const found = findWindowsByPath('/tmp/win-test/documents');
    expect(found.length).toBe(1);
    expect(found).toContain(win!.id);
  });

  it('does not find unrelated windows', () => {
    openWindow({ name: 'RootA', path: '/tmp/win-test-a' });
    openWindow({ name: 'RootB', path: '/tmp/win-test-b' });

    const found = findWindowsByPath('/tmp/win-test-a/documents');
    expect(found.length).toBe(1);
    const foundWin = get(windows).find((w) => w.id === found[0]);
    expect(foundWin?.rootPath).toBe('/tmp/win-test-a');
  });

  it('returns empty array when no windows are open', () => {
    const found = findWindowsByPath('/tmp/win-test');
    expect(found).toEqual([]);
  });
});

describe('reloadWindowsForPath', () => {
  let dispatchEventSpy: any;

  beforeEach(() => {
    vi.useFakeTimers();
    dispatchEventSpy = vi.spyOn(window, 'dispatchEvent');
  });

  afterEach(() => {
    dispatchEventSpy.mockRestore();
    vi.useRealTimers();
  });

  it('dispatches a filetoaster:reload event for each open matching window', () => {
    openWindow({ name: 'Root', path: '/tmp/win-reload' });
    const win = get(windows).find((w) => w.rootPath === '/tmp/win-reload');
    expect(win).toBeDefined();

    reloadWindowsForPath('/tmp/win-reload');

    expect(dispatchEventSpy).toHaveBeenCalled();
    const event = dispatchEventSpy.mock.calls[0][0] as CustomEvent;
    expect(event.type).toBe('filetoaster:reload');
    expect(event.detail).toEqual({ windowId: win!.id, path: '/tmp/win-reload' });
  });

  it('dispatches one event per matching open window', () => {
    openWindow({ name: 'Root', path: '/tmp/win-reload-multi' });
    const win1 = get(windows).find((w) => w.rootPath === '/tmp/win-reload-multi');
    expect(win1).toBeDefined();

    openWindow({ name: 'Root', path: '/tmp/win-reload-multi' });
    const win2 = get(windows).find((w) => w.rootPath === '/tmp/win-reload-multi' && w.id !== win1!.id);
    expect(win2).toBeDefined();

    reloadWindowsForPath('/tmp/win-reload-multi');

    expect(dispatchEventSpy).toHaveBeenCalledTimes(2);
    const dispatchedIds = dispatchEventSpy.mock.calls.map((c: unknown[]) => (c[0] as CustomEvent).detail.windowId);
    expect(dispatchedIds).toContain(win1!.id);
    expect(dispatchedIds).toContain(win2!.id);
  });

  it('does not dispatch when no windows match', () => {
    reloadWindowsForPath('/tmp/win-reload-none');
    expect(dispatchEventSpy).not.toHaveBeenCalled();
  });
});
