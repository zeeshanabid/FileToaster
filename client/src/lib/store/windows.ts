import { writable, type Writable } from 'svelte/store';

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

function bringToFront(id: string) {
  nextZ += 1;
  windows.update((list) =>
    list.map((w) => (w.id === id ? { ...w, zIndex: nextZ, minimized: false } : w))
  );
}

export function openWindow(root: { name: string; path: string }) {
  windows.update((list) => {
    nextZ += 1;
    const offset = (list.length % 6) * 28;
    const opened: AppWindow = {
      id: `${root.path}-${Date.now()}`,
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
