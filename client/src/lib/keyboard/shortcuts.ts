// Keyboard shortcut catalogue. The help overlay renders this list and the
// Window component mirrors these bindings, so the two never drift apart.
export type ShortcutModifier = 'CTRL' | 'ALT' | 'SHIFT';

export interface KeyboardShortcut {
  /** Modifier keys, in display order. */
  modifiers: ShortcutModifier[];
  /** The non-modifier key ("R", "↑", "ENTER", …). */
  key: string;
  /** A one-line, human-readable description of what the combination does. */
  description: string;
}

export const KEYBOARD_SHORTCUTS: KeyboardShortcut[] = [
  { modifiers: ['CTRL', 'ALT'], key: 'R', description: 'Reload the current folder' },
  { modifiers: ['CTRL', 'ALT'], key: 'C', description: 'Copy the selected items' },
  { modifiers: ['CTRL', 'ALT'], key: 'V', description: 'Paste into the current folder' },
  { modifiers: ['CTRL', 'ALT'], key: 'X', description: 'Cut the selected items' },
  { modifiers: ['CTRL', 'ALT'], key: 'D', description: 'Delete the selected items' },
  { modifiers: ['CTRL', 'ALT'], key: 'A', description: 'Select all items in the current folder' },
  { modifiers: ['CTRL', 'ALT'], key: 'I', description: 'Show information about the selection' },
  { modifiers: ['CTRL', 'ALT'], key: 'L', description: 'Download the selected file' },
  { modifiers: ['CTRL', 'ALT'], key: 'N', description: 'Create a new directory' },
  { modifiers: ['CTRL', 'ALT'], key: 'T', description: 'Open or close the tasks list' },
  { modifiers: ['CTRL', 'ALT'], key: 'Z', description: 'Compress the selected items' },
  { modifiers: ['CTRL', 'ALT'], key: 'U', description: 'Extract the selected archive' },
  { modifiers: ['CTRL', 'ALT'], key: 'M', description: 'Rename the selected items' },
  { modifiers: ['CTRL', 'ALT'], key: '↑', description: 'Go up one directory' },
  { modifiers: [], key: '↑ / ↓', description: 'Select the previous or next item' },
  { modifiers: ['SHIFT'], key: '↑ / ↓', description: 'Extend the selection up or down' },
  { modifiers: [], key: 'ENTER', description: 'Open the selected directory' },
  { modifiers: [], key: '?', description: 'Show this keyboard shortcut help' },
  { modifiers: [], key: 'ESC', description: 'Close this keyboard shortcut help' }
];

// macOS names the Alt key "Option" and has modifier glyphs; everything else
// keeps the familiar CTRL / ALT / SHIFT spelling.
const MAC_MODIFIER_LABELS: Record<ShortcutModifier, string> = {
  CTRL: 'CTRL (^)',
  ALT: 'OPTION (⌥)',
  SHIFT: 'SHIFT (⇧)'
};

const PC_MODIFIER_LABELS: Record<ShortcutModifier, string> = {
  CTRL: 'CTRL',
  ALT: 'ALT',
  SHIFT: 'SHIFT'
};

// formatShortcut renders one catalogue entry for display, e.g.
//   "CTRL + ALT + R"   (Windows/Linux)
//   "CTRL (^) + OPTION (⌥) + R"  (macOS)
export function formatShortcut(shortcut: KeyboardShortcut, isMac: boolean): string {
  const labels = isMac ? MAC_MODIFIER_LABELS : PC_MODIFIER_LABELS;
  const parts = shortcut.modifiers.map((modifier) => labels[modifier]);
  parts.push(shortcut.key);
  return parts.join(' + ');
}

// isMacPlatform reports whether the running client is macOS (including the
// iPadOS builds that report a Mac-like platform). Safe to call without a DOM.
export function isMacPlatform(): boolean {
  if (typeof navigator === 'undefined') return false;
  const platform = navigator.platform ?? '';
  const userAgent = navigator.userAgent ?? '';
  return /mac|iphone|ipad|ipod/i.test(`${platform} ${userAgent}`);
}