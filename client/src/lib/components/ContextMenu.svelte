<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { isArchiveFile } from '$lib/fs/archive';

  type MenuKind = 'empty' | 'file' | 'folder' | 'multi';

  interface MenuItem {
    type: 'item';
    id: string;
    label: string;
    action: string;
    svg: string;
    enabled: boolean;
  }

  interface MenuSeparator {
    type: 'sep';
  }

  let {
    open = false,
    x = 0,
    y = 0,
    kind,
    selection = [] as Array<{ path: string; name: string; type: 'file' | 'dir' }>,
    contextPath = '/',
    canPaste = false,
    canSelectAll = false,
    canGoUp = false,
    searchActive = false,
    onClose,
    onAction
  }: {
    open?: boolean;
    x?: number;
    y?: number;
    kind: MenuKind;
    selection?: Array<{ path: string; name: string; type: 'file' | 'dir' }>;
    contextPath?: string;
    canPaste?: boolean;
    canSelectAll?: boolean;
    canGoUp?: boolean;
    searchActive?: boolean;
    onClose: () => void;
    onAction: (action: string) => void;
  } = $props();

  // Clamp the menu into the viewport once it is rendered.
  let clampedX = $state(0);
  let clampedY = $state(0);

  $effect(() => {
    if (!open) return;
    const menu = document.querySelector('.context-menu');
    const rect = menu?.getBoundingClientRect();
    const pad = 8;
    let nx = x;
    let ny = y;
    if (rect) {
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      if (x + rect.width > vw - pad) nx = Math.max(pad, vw - rect.width - pad);
      if (nx < pad) nx = pad;
      if (y + rect.height > vh - pad) ny = Math.max(pad, vh - rect.height - pad);
      if (ny < pad) ny = pad;
    }
    clampedX = nx;
    clampedY = ny;
  });

  function dismiss(e: MouseEvent) {
    // Clicking anywhere outside the menu dismisses it.
    if (!(e.target as HTMLElement).closest('.context-menu')) {
      onClose();
    }
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') onClose();
  }

  onMount(() => {
    document.addEventListener('mousedown', dismiss, true);
    document.addEventListener('keydown', onKey);
  });
  onDestroy(() => {
    document.removeEventListener('mousedown', dismiss, true);
    document.removeEventListener('keydown', onKey);
  });

  const single = $derived(selection.length === 1 ? selection[0] : null);
  const canCutCopy = $derived(selection.length > 0);
  const canDelete = $derived(selection.length > 0);
  const canRename = $derived(selection.length === 1);
  const canCompress = $derived(selection.length > 0);
  const canDownload = $derived(single !== null && single.type === 'file');
  const canDecompress = $derived(canDownload && single !== null && isArchiveFile(single.name));
  const canInfo = $derived(selection.length > 0);
  const canNewFolder = $derived(kind === 'empty' || kind === 'folder');
  const canUpload = $derived(kind === 'empty' || kind === 'folder');
  const canPasteHere = $derived(canPaste && (kind === 'empty' || kind === 'folder'));
  const canOpen = $derived(kind === 'folder' && selection.length === 1 && selection[0]?.type === 'dir');
  const canOpenInNewWindow = $derived(kind === 'folder' && selection.length === 1 && selection[0]?.type === 'dir');
  const canGoUpHere = $derived(kind === 'empty' && canGoUp);
  const canTruncate = $derived(selection.length > 0 && selection.every((s) => s.type === 'file'));
  // "Open containing directory" is only meaningful for a search result, where
  // the shown item lives in a folder other than the one the window has open.
  const canOpenContainingDir = $derived(kind !== 'empty' && searchActive && selection.length > 0);

  // Icon markup (24x24 viewBox, stroke currentColor) reused from the toolbar so
  // the menu looks consistent with the rest of the UI. Declared before `items`
  // because the derived block references them.
  const CUT =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><circle cx="6" cy="6" r="2.75" /><circle cx="6" cy="18" r="2.75" /><line x1="8.5" y1="8.5" x2="19" y2="19" /><line x1="8.5" y1="15.5" x2="19" y2="5" /></svg>';
  const COPY =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M9 9h10v10H9Z" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>';
  const PASTE =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M8 5h8v3H8Z" /><path d="M5 8h14v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2Z" /></svg>';
  const DELETE =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M5 7h14" /><path d="M8 7V5a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" /><path d="M6 7l1 12h10l1-12" /></svg>';
  const RENAME =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M3 17.25V21h3.75L17.89 9.64a1 1 0 0 0 0-1.41L15.77 6.11a1 1 0 0 0-1.41 0L3 15.82Z" /><path d="M17.5 4.5l2.5 2.5L19 9l-2.5-2.5Z" /></svg>';
  const COMPRESS =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M4 7a2 2 0 0 1 2-2h8l2 2h4a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1Z" /><path d="M12 10v4m0 0-2-2m2 2 2-2" /><path d="M9 18h6" /></svg>';
  const DECOMPRESS =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M4 7a2 2 0 0 1 2-2h8l2 2h4a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1Z" /><path d="M12 14v-4m0 0-2 2m2-2 2 2" /><path d="M9 18h6" /></svg>';
  const DOWNLOAD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M12 3v11" /><path d="M7 12l5 5 5-5" /><path d="M5 20v1h14v-1" /></svg>';
  const INFO =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z" /><path d="M12 16v-4M12 8h.01" /></svg>';
  const NEW_FOLDER =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" /><path d="M12 11v6M9 14h6" /></svg>';
  const UPLOAD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M12 19V5" /><path d="M6 9l6-6 6 6" /><path d="M4 15v4h16v-4" /></svg>';
  const RELOAD =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M21 12a9 9 0 1 1-3.2-6.9" /><path d="M21 4v5h-5" /></svg>';
  const OPEN =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2H7l-2-2H3a2 2 0 0 0-2 2z" /><path d="M17 4v4h4" /></svg>';
  const OPEN_NEW_WINDOW =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2H7l-2-2H3a2 2 0 0 0-2 2z" /><path d="M17 4v4h4" /><path d="M21 3v5h-5" /><path d="M14 14l7-7" /></svg>';
  const UP =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M12 19V5M5 12l7-7 7 7" /></svg>';
  const SELECT_ALL =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M3 3h18v18H3z" /><path d="M7 7l3 3 7-7" /></svg>';
  const TRUNCATE =
    '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="16" height="16" aria-hidden="true"><path d="M21 12H3" /><path d="M18 6v12" /><path d="M6 6v12" /></svg>';

  const items = $derived<Array<MenuItem | MenuSeparator>>([
    { type: 'item', id: 'open-containing-directory', label: 'Open containing directory', action: 'open-containing-directory', svg: OPEN, enabled: canOpenContainingDir },
    { type: 'sep' },
    { type: 'item', id: 'open', label: 'Open', action: 'open', svg: OPEN, enabled: canOpen },
    { type: 'item', id: 'open-new-window', label: 'Open in new window', action: 'open-new-window', svg: OPEN_NEW_WINDOW, enabled: canOpenInNewWindow },
    { type: 'item', id: 'cut', label: 'Cut', action: 'cut', svg: CUT, enabled: canCutCopy },
    { type: 'item', id: 'copy', label: 'Copy', action: 'copy', svg: COPY, enabled: canCutCopy },
    { type: 'item', id: 'paste', label: 'Paste', action: 'paste', svg: PASTE, enabled: canPasteHere },
    { type: 'sep' },
    { type: 'item', id: 'selectall', label: 'Select all', action: 'selectall', svg: SELECT_ALL, enabled: canSelectAll },
    { type: 'sep' },
    { type: 'item', id: 'delete', label: 'Delete', action: 'delete', svg: DELETE, enabled: canDelete },
    { type: 'sep' },
    { type: 'item', id: 'rename', label: 'Rename', action: 'rename', svg: RENAME, enabled: canRename },
    { type: 'sep' },
    { type: 'item', id: 'truncate', label: 'Truncate', action: 'truncate', svg: TRUNCATE, enabled: canTruncate },
    { type: 'sep' },
    { type: 'item', id: 'compress', label: 'Compress', action: 'compress', svg: COMPRESS, enabled: canCompress },
    { type: 'item', id: 'download', label: 'Download', action: 'download', svg: DOWNLOAD, enabled: canDownload },
    { type: 'item', id: 'decompress',
      label: 'Extract',
      action: 'decompress',
      svg: DECOMPRESS,
      enabled: canDecompress
    },
    { type: 'sep' },
    { type: 'item', id: 'info', label: 'Information', action: 'info', svg: INFO, enabled: canInfo },
    { type: 'sep' },
    {
      type: 'item',
      id: 'newfolder',
      label: 'New directory',
      action: 'newfolder',
      svg: NEW_FOLDER,
      enabled: canNewFolder
    },
    { type: 'item', id: 'upload', label: 'Upload', action: 'upload', svg: UPLOAD, enabled: canUpload },
    { type: 'item', id: 'up', label: 'Up one directory', action: 'up', svg: UP, enabled: canGoUpHere },
    { type: 'sep' },
    { type: 'item', id: 'reload', label: 'Reload', action: 'reload', svg: RELOAD, enabled: true }
  ]);

  function run(action: string) {
    const item = items.find((i): i is MenuItem => i.type !== 'sep' && i.id === action);
    if (!item || !item.enabled) return;
    onClose();
    onAction(action);
  }
</script>

{#if open}
  <div
    class="context-menu"
    role="menu"
    aria-label="Context menu"
    data-testid="context-menu"
    style="left:{clampedX}px; top:{clampedY}px"
  >
    {#each items as item}
      {#if item.type === 'sep'}
        <div class="sep" role="separator" aria-orientation="horizontal"></div>
      {:else}
        {@const it = item as MenuItem}
        <button
          class="item"
          class:disabled={!it.enabled}
          disabled={!it.enabled}
          type="button"
          role="menuitem"
          aria-disabled={!it.enabled}
          data-action={it.id}
          data-testid={"context-menu-" + it.id}
          onclick={() => run(it.action)}
        >
          <span class="icon" aria-hidden="true">{@html it.svg}</span>
          <span class="label">{it.label}</span>
        </button>
      {/if}
    {/each}
  </div>
{/if}

<style>
  .context-menu {
    position: fixed;
    z-index: 50;
    min-width: 172px;
    padding: 4px 0;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: var(--shadow-window);
    outline: none;
  }
  .item {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    padding: 7px 12px;
    background: transparent;
    border: none;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    text-align: left;
    cursor: pointer;
  }
  .item:not(:disabled):hover {
    background: var(--surface-soft);
    color: var(--text);
  }
  .item:disabled {
    opacity: 0.45;
    cursor: default;
  }
  .sep {
    height: 1px;
    background: var(--border);
    margin: 4px 0;
  }
</style>
