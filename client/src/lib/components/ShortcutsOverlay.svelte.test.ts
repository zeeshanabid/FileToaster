import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import { fireEvent, screen, within, waitFor } from '@testing-library/dom';
import ShortcutsOverlay from './ShortcutsOverlay.svelte';
import { KEYBOARD_SHORTCUTS, formatShortcut } from '$lib/keyboard/shortcuts';

const instances: unknown[] = [];

function render(): void {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instances.push(mount(ShortcutsOverlay as never, { target } as never));
}

function unmountAll(): void {
  while (instances.length) unmount(instances.pop() as never);
  document.querySelectorAll('body > div').forEach((el) => el.remove());
}

function dialog(): HTMLElement {
  return screen.getByTestId('shortcuts-dialog') as HTMLElement;
}

function backdrop(): HTMLElement {
  return screen.getByTestId('shortcuts-backdrop') as HTMLElement;
}

afterEach(unmountAll);

describe('ShortcutsOverlay', () => {
  it('opens on "?" and lists every key combination with its description', async () => {
    render();
    await tick();

    expect(screen.queryByTestId('shortcuts-dialog')).not.toBeInTheDocument();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    // Every catalogue entry is rendered once, key combination and description.
    const items = within(dialog()).getAllByRole('listitem');
    expect(items).toHaveLength(KEYBOARD_SHORTCUTS.length);
    for (const shortcut of KEYBOARD_SHORTCUTS) {
      expect(within(dialog()).getByText(formatShortcut(shortcut, false))).toBeInTheDocument();
      expect(within(dialog()).getByText(shortcut.description)).toBeInTheDocument();
    }
  });

  it('shows key combinations in the CTRL + ALT + R format, styled apart from descriptions', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    for (const keys of ['CTRL + ALT + R', 'CTRL + ALT + C', 'CTRL + ALT + V', 'CTRL + ALT + ↑']) {
      const el = within(dialog()).getByText(keys);
      expect(el).toHaveClass('shortcut-keys');
    }
    // The key combination and the description use different elements/classes,
    // which the stylesheet colors differently.
    const desc = within(dialog()).getByText('Reload the current folder');
    expect(desc).toHaveClass('shortcut-desc');
    expect(desc).not.toHaveClass('shortcut-keys');
  });

  it('is rendered above every window through the body-level portal', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());
    const portal = backdrop().closest('.window-portal') as HTMLElement;
    expect(portal).toBeInTheDocument();
    expect(portal.parentElement).toBe(document.body);
  });

  it('closes on Escape', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    fireEvent.keyDown(dialog(), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByTestId('shortcuts-dialog')).not.toBeInTheDocument());
  });

  it('closes when clicking outside the panel', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    fireEvent.click(backdrop());
    await waitFor(() => expect(screen.queryByTestId('shortcuts-dialog')).not.toBeInTheDocument());
  });

  it('stays open when clicking inside the panel', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    fireEvent.click(dialog());
    await tick();
    expect(screen.getByTestId('shortcuts-dialog')).toBeInTheDocument();
  });

  it('does not open while typing "?" into an input', async () => {
    render();
    await tick();
    const input = document.createElement('input');
    document.body.appendChild(input);
    fireEvent.keyDown(input, { key: '?' });
    await tick();
    expect(screen.queryByTestId('shortcuts-dialog')).not.toBeInTheDocument();
  });

  it('does not open for ctrl/alt modified ? presses', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?', ctrlKey: true });
    fireEvent.keyDown(document.body, { key: '?', altKey: true });
    await tick();
    expect(screen.queryByTestId('shortcuts-dialog')).not.toBeInTheDocument();
  });
});
describe('ShortcutsOverlay — macOS modifier labels', () => {
  const originalPlatform = Object.getOwnPropertyDescriptor(navigator, 'platform');

  beforeEach(() => {
    Object.defineProperty(navigator, 'platform', { value: 'MacIntel', configurable: true });
  });

  afterEach(() => {
    if (originalPlatform) {
      Object.defineProperty(navigator, 'platform', originalPlatform);
    } else {
      delete (navigator as { platform?: string }).platform;
    }
  });

  it('uses OPTION instead of ALT and shows the modifier symbols in brackets', async () => {
    render();
    await tick();
    fireEvent.keyDown(document.body, { key: '?' });
    await waitFor(() => expect(dialog()).toBeInTheDocument());

    expect(within(dialog()).getByText('CTRL (^) + OPTION (⌥) + R')).toBeInTheDocument();
    expect(within(dialog()).getByText('CTRL (^) + OPTION (⌥) + ↑')).toBeInTheDocument();
    expect(within(dialog()).getByText('SHIFT (⇧) + ↑ / ↓')).toBeInTheDocument();
    // The Windows/Linux spelling is not shown on macOS.
    expect(within(dialog()).queryByText('CTRL + ALT + R')).not.toBeInTheDocument();
    expect(within(dialog()).getAllByText(/OPTION/).length).toBeGreaterThan(0);
  });
});
