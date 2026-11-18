import { writable, type Writable } from 'svelte/store';
import { basename, isWithin } from '$lib/fs/paths';

export interface AppWindow {
  id: string;
  // name is the display label in the title bar (the folder's name).
  name: string;
  // rootPath is the allowed root this window belongs to.
  rootPath: string;
  // path is the directory currently opened; it changes as the user navigates.
  path: string;
  x: number;
  y: number;
  width: number;
  height: number;
  zIndex: number;
  minimized: boolean;
}

// Window manager backed by a writable store. Clicking a folder icon always
// opens a brand-new window (even for a folder that already has a window open),
// per the file-manager model.
export const windows: Writable<AppWindow[]> = writable([]);

let nextZ = 10;
let windowIdCounter = 0;

// The label for a window's taskbar button and title bar: the root label while
// the root is open, otherwise the basename of the currently opened directory.
// Both the title bar and the taskbar derive from this so they stay in sync as
// the user navigates (navigation updates `path` in the store).
export function displayName(win: AppWindow): string {
  return win.path === win.rootPath ? win.name : basename(win.path);
}

function bringToFront(id: string) {
  nextZ += 1;
  windows.update((list) =>
    list.map((w) => (w.id === id ? { ...w, zIndex: nextZ, minimized: false } : w))
  );
}

function generateWindowId(rootPath: string): string {
  windowIdCounter += 1;
  const randomPart = Math.random().toString(36).slice(2, 9);
  return `${rootPath}-${Date.now()}-${windowIdCounter}-${randomPart}`;
}

export function openWindow(root: { name: string; path: string }) {
  windows.update((list) => {
    nextZ += 1;
    const offset = (list.length % 6) * 28;
    const opened: AppWindow = {
      id: generateWindowId(root.path),
      name: root.name,
      rootPath: root.path,
      path: root.path,
      x: 90 + offset,
      y: 70 + offset,
      width: 780,
      height: 480,
      zIndex: nextZ,
      minimized: false
    };
    return [...list, opened];
  });
}

// Find all open windows that are currently showing the given path (or a parent of it).
// Returns the window IDs that should be reloaded.
export function findWindowsByPath(targetPath: string): string[] {
  let result: string[] = [];
  windows.update((list) => {
    result = list
      .filter((w) => {
        // Reload if the window's current path is the target path or a parent of it
        // (since changes in a child directory affect the parent's listing)
        return isWithin(targetPath, w.path);
      })
      .map((w) => w.id);
    return list;
  });
  return result;
}

// Trigger reload on all open windows showing the given path.
// This calls the window's internal reload function via a custom event.
export function reloadWindowsForPath(targetPath: string): void {
  const windowIds = findWindowsByPath(targetPath);
  for (const id of windowIds) {
    // Dispatch a custom event that the Window component listens for
    window.dispatchEvent(new CustomEvent('filetoaster:reload', { detail: { windowId: id, path: targetPath } }));
  }
}

export function focusWindow(id: string) {
  bringToFront(id);
}

export function closeWindow(id: string) {
  windows.update((list) => list.filter((w) => w.id !== id));
}

export function updateWindow(id: string, patch: Partial<AppWindow>) {
  windows.update((list) =>
    list.map((w) => (w.id === id ? { ...w, ...patch } : w))
  );
}

export function toggleMinimize(id: string) {
  windows.update((list) =>
    list.map((w) => (w.id === id ? { ...w, minimized: !w.minimized } : w))
  );
}
