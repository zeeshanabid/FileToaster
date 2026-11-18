<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { derived } from 'svelte/store';
  import {
    closeWindow,
    displayName,
    focusWindow,
    isActiveWindow,
    openWindow,
    reloadWindowsForPath,
    toggleMinimize,
    updateWindow,
    windows,
    type AppWindow
  } from '$lib/store/windows';
  import { downloadFile, type Entry, fetchInfo, type FileInfo, mkdirDirectory, renameFile } from '$lib/fs/api';
  import { tree } from '$lib/fs/tree';
import {
  uploadManager,
  manifestForDataTransfer,
  type DnDItem
} from '$lib/fs/upload';
import {
  clipboard,
  cutEntries,
  cutMode,
  clearClipboard,
  setClipboard,
  setCut,
  submitCopyTask,
  submitDeleteTask,
  submitMoveTask,
  submitCompressTask,
  submitDecompressTask,
  taskPanelOpen
} from '$lib/store/tasks';
import { isArchiveFile } from '$lib/fs/archive';
import { basename, isWithin } from '$lib/fs/paths';
import FileIcon from './FileIcon.svelte';
import ContextMenu from './ContextMenu.svelte';
import InfoDialog from './InfoDialog.svelte';
import ConfirmDelete from './ConfirmDelete.svelte';
import ConfirmMove from './ConfirmMove.svelte';
import ConfirmDropMode from './ConfirmDropMode.svelte';
import Portal from './Portal.svelte';
import ConfirmCompress from './ConfirmCompress.svelte';
import ConfirmDecompress from './ConfirmDecompress.svelte';
import { formatSize, formatDate } from '$lib/fs/format';

  let { window: winProp }: { window: AppWindow } = $props();

  // Subscribe to this window's slice of the store so the tree highlight, title
  // and main view all react to navigation performed anywhere (tree click,
  // double-click, up button). Both views observe the same store, so they cannot
  // diverge. `winProp` only supplies the values set once at window creation.
  const selection = derived(windows, ($all) => $all.find((w) => w.id === winProp.id) ?? null);
  let win = $state<AppWindow | null>(null);
  let stop: (() => void) | undefined;
  onMount(() => {
    tree.resetWindow(winProp.id);
    // `directory` (plus `multiple`) lets the OS file picker expose folders
    // alongside files where the browser supports it; it is not a standard
    // typed attribute, so set it imperatively on the bound files picker input.
    fileInput?.setAttribute('directory', '');
    stop = selection.subscribe((w) => {
      win = w;
    });
    void navigate(winProp.path);
    document.addEventListener('click', dismissUploadMenu);
    window.addEventListener('keydown', onWindowKeydown);
    // Listen for reload events triggered by task completion
    window.addEventListener('filetoaster:reload', onReloadEvent as EventListener);
  });
  onDestroy(() => {
    stop?.();
    document.removeEventListener('click', dismissUploadMenu);
    window.removeEventListener('keydown', onWindowKeydown);
    window.removeEventListener('filetoaster:reload', onReloadEvent as EventListener);
  });

  // `tree` is an external cache; Svelte only re-renders when a $state changes.
  // Bump `frame` after every tree mutation so the fold/outlines and per-directory
  // loading/error nodes update even when the store path did not change (twisty).
  let frame = $state(0);

  // --- geometry: drag (header) and resize (bottom-right handle) ---
  let rootEl: HTMLElement | null = $state(null);
  let pos = $state<{ x: number; y: number } | null>(null);
  let dragging = $state(false);
  let resizeStart = $state({ x: 0, y: 0, w: 0, h: 0, tree: 0 });
  let resizing = $state(false);
  let resizingTree = $state(false);
  // Width of the folder-tree pane, set once at creation and adjustable with the
  // vertical divider handle between the tree and the listing.
  let treeWidth = $state(300);
  // svelte-ignore state_referenced_locally
  let size = $state({ width: winProp.width, height: winProp.height });

  const MIN_W = 480;
  const MIN_H = 300;
  const MIN_TREE = 140;
  const MAX_TREE = 520;

  function addMoveListeners() {
    window.addEventListener('pointermove', onPointerMove);
    window.addEventListener('pointerup', onPointerUp);
  }
  function removeMoveListeners() {
    window.removeEventListener('pointermove', onPointerMove);
    window.removeEventListener('pointerup', onPointerUp);
  }
  function onHeaderPointerDown(e: PointerEvent) {
    if (pos === null) pos = { x: winProp.x, y: winProp.y };
    dragStart = { x: e.clientX, y: e.clientY };
    dragOrigin = pos ? { ...pos } : { x: winProp.x, y: winProp.y };
    dragging = true;
    addMoveListeners();
  }
  let dragStart = $state({ x: 0, y: 0 });
  let dragOrigin = $state<{ x: number; y: number } | null>(null);
  function onResizePointerDown(e: PointerEvent) {
    e.stopPropagation();
    resizeStart = { x: e.clientX, y: e.clientY, w: size.width, h: size.height, tree: treeWidth };
    resizing = true;
    resizingTree = false;
    addMoveListeners();
  }
  function onTreeResizePointerDown(e: PointerEvent) {
    e.stopPropagation();
    e.preventDefault();
    resizeStart = { x: e.clientX, y: e.clientY, w: size.width, h: size.height, tree: treeWidth };
    resizingTree = true;
    resizing = false;
    addMoveListeners();
  }
  function onPointerMove(e: PointerEvent) {
    if (resizingTree) {
      treeWidth = Math.max(MIN_TREE, Math.min(MAX_TREE, resizeStart.tree + (e.clientX - resizeStart.x)));
    } else if (resizing) {
      size.width = Math.max(MIN_W, resizeStart.w + (e.clientX - resizeStart.x));
      size.height = Math.max(MIN_H, resizeStart.h + (e.clientY - resizeStart.y));
    } else if (dragOrigin) {
      pos = {
        x: Math.max(0, dragOrigin.x + (e.clientX - dragStart.x)),
        y: Math.max(0, dragOrigin.y + (e.clientY - dragStart.y))
      };
    }
  }
  function onPointerUp() {
    dragging = false;
    resizing = false;
    resizingTree = false;
    removeMoveListeners();
  }

  // --- path helpers ---
  function dirname(p: string): string {
    const i = p.lastIndexOf('/');
    return i <= 0 ? '/' : p.slice(0, i);
  }

  // --- data: main listing view state (derived from the store's `path`) ---
  let entries = $state<Entry[]>([]);
  let mainLoading = $state(false);
  let mainError = $state<string | null>(null);
  // Bound to the scrolling listing container so keyboard navigation can keep
  // the cursor row in view (scoped to this window, never another one).
  let contentEl: HTMLElement | null = $state(null);

  type SortKey = 'name' | 'size' | 'created' | 'updated';
  const SORT_KEYS: SortKey[] = ['name', 'size', 'created', 'updated'];
  let sortKey = $state<SortKey>('name');
  let sortDir = $state<'asc' | 'desc'>('asc');

  function toggleSort(key: SortKey) {
    if (sortKey === key) {
      sortDir = sortDir === 'asc' ? 'desc' : 'asc';
    } else {
      sortKey = key;
      sortDir = 'asc';
    }
  }

  // --- selection (files/folders in the main list) ---
  // `selected` holds the paths of the currently selected rows; `lastClicked` is
  // the most recently clicked path, used as the anchor for shift-range selects.
  // Selection mirrors macOS/Windows file managers: a plain click selects a single
  // item, ctrl/cmd selects (or deselects) individually, and shift extends the
  // selection across the visible (sorted) order.
  let selected = $state<Set<string>>(new Set());
  let lastClicked: string | null = $state(null);
  // `cursor` is the row the keyboard is currently on (the active row for
  // arrow-key navigation). `kbAnchor` is the stable anchor for shift+arrow
  // range extension, kept separately from `lastClicked` so extending the range
  // repeatedly from the keyboard does not drift the anchor.
  let cursor: string | null = $state(null);
  let kbAnchor: string | null = $state(null);

  // --- context (right-click) menu state ---
  // `menuKind` is one of 'empty' | 'file' | 'folder' | 'multi'. `menuSelection`
  // mirrors `selected` so the menu can render enablement independently of the
  // `menuContextPath` is the folder New directory / Paste / Upload target.
  let menuOpen = $state(false);
  let menuX = $state(0);
  let menuY = $state(0);
  let menuKind = $state<'empty' | 'file' | 'folder' | 'multi'>('empty');
  let menuSelection = $state<Array<{ path: string; name: string; type: 'file' | 'dir' }>>([]);
  let menuContextPath = $state('/');
  // `uploadDest` overrides the upload destination for a context-menu "Upload"
  // (right-click a folder → upload into that folder); null means the open folder.
  let uploadDest = $state<string | null>(null);

  function selectOnly(p: string) {
    selected = new Set([p]);
    lastClicked = p;
    cursor = p;
    kbAnchor = p;
  }
  function togglePath(p: string) {
    const next = new Set(selected);
    if (next.delete(p)) {
      // Nothing left selected: forget the range anchor; otherwise keep it so
      // a following shift-click extends from the new last item.
      if (next.size === 0) lastClicked = null;
    } else {
      next.add(p);
    }
    selected = next;
    lastClicked = p;
    cursor = p;
    kbAnchor = p;
  }
  function rangeSelect(anchor: string, target: string) {
    const order = rows.map((r) => r.path);
    const i = order.indexOf(anchor);
    const j = order.indexOf(target);
    if (i === -1 || j === -1) {
      selectOnly(target);
      return;
    }
    const lo = Math.min(i, j);
    const hi = Math.max(i, j);
    selected = new Set(order.slice(lo, hi + 1));
    lastClicked = target;
    cursor = target;
    // Keep the original range start stable so repeated shift-clicks (and
    // shift+arrow afterwards) extend from where the range began.
    kbAnchor = anchor;
  }
  function onRowClick(e: MouseEvent, row: Entry) {
    if (e.ctrlKey || e.metaKey) {
      togglePath(row.path);
    } else if (e.shiftKey && lastClicked !== null) {
      rangeSelect(lastClicked, row.path);
    } else {
      selectOnly(row.path);
    }
  }
  function clearSelectionIfBackground(e: MouseEvent) {
    if (e.target === e.currentTarget) selected = new Set();
  }

  // Entries of the current selection, used to render the context menu and to
  // pass to operations that need the per-item name/type. Includes tree nodes
  // that are not part of the current listing.
  function selectionEntries(): Array<{ path: string; name: string; type: 'file' | 'dir' }> {
    return [...selected].map((path) => {
      const found = entries.find((it) => it.path === path);
      if (found) return { path: found.path, name: found.name, type: found.type };
      const name = path.slice(path.lastIndexOf('/') + 1);
      return { path, name, type: 'dir' };
    });
  }

  // Select every item in the currently opened folder.
  function selectAll() {
    if (!win || entries.length === 0) return;
    selected = new Set(entries.map((it) => it.path));
    lastClicked = null;
    cursor = null;
    kbAnchor = null;
  }

  // --- context (right-click) menu -------------------------------------------
  // Three menu kinds: 'folder' / 'file' (single row), 'multi' (several rows)
  // and 'empty' (background). Actions are gated so only the ones applicable to
  // the clicked target are enabled; the rest are shown disabled. Every action
  // reuses the existing toolbar flows (cut/copy/paste/delete/rename/compress/
  // download/decompress/info/new-folder/upload/reload).
  function openContextMenu(
    e: MouseEvent,
    kind: 'empty' | 'file' | 'folder' | 'multi',
    selection: Array<{ path: string; name: string; type: 'file' | 'dir' }>,
    contextPath: string
  ) {
    e.preventDefault();
    e.stopPropagation();
    // Sync the window selection so the existing action functions (which read
    // `selected`) operate on the context-menu target.
    selected = new Set(selection.map((it) => it.path));
    lastClicked = selection.length === 1 ? selection[0].path : null;
    menuX = e.clientX;
    menuY = e.clientY;
    menuKind = kind;
    menuSelection = selection;
    menuContextPath = contextPath;
    menuOpen = true;
  }
  function closeContextMenu() {
    menuOpen = false;
    uploadDest = null;
  }
  function onContextAction(action: string) {
    closeContextMenu();
    switch (action) {
      case 'cut':
        cutSelection();
        break;
      case 'copy':
        copySelection();
        break;
      case 'paste':
        if ($cutMode) {
          moveOpen = true;
        } else {
          pasteInto(menuContextPath);
        }
        break;
      case 'delete':
        startDelete();
        break;
      case 'rename':
        startRename();
        reload();
        break;
      case 'compress':
        startCompress();
        break;
      case 'download':
        confirmDownload();
        break;
      case 'decompress':
        startDecompress();
        break;
      case 'info':
        openInfo();
        break;
      case 'newfolder':
        startNewDirIn(menuContextPath);
        reload();
        break;
      case 'upload':
        uploadDest = menuContextPath;
        openUploadMenu();
        break;
      case 'reload':
        reload();
        break;
      case 'open':
        // Open a single directory (double-click equivalent). Only enabled for a
        // single folder target.
        void navigate(menuContextPath);
        break;
      case 'open-new-window':
        // Open the directory in a new window with the original root path
        // so the tree shows the full path from the root
        if (menuSelection.length === 1 && menuSelection[0].type === 'dir') {
          const targetPath = menuSelection[0].path;
          const rootPath = winProp.rootPath;
          // Use the root's display name (winProp.name) not the target directory's name
          openWindow({ name: winProp.name, path: targetPath, rootPath });
        }
        break;
      case 'up':
        goUp();
        break;
      case 'selectall':
        selectAll();
        break;
    }
  }
  // `row.type` is 'file'|'dir'; the menu kind uses 'file'|'folder'.
  function rowKind(type: Entry['type']): 'file' | 'folder' {
    return type === 'dir' ? 'folder' : 'file';
  }

  function onRowContextMenu(e: MouseEvent, row: Entry) {
    const entry = { path: row.path, name: row.name, type: row.type };
    const contextPath = row.type === 'dir' ? row.path : win ? win.path : '/';
    const kind = rowKind(row.type);
    if (selected.has(row.path)) {
      if (selected.size === 1) {
        openContextMenu(e, kind, [entry], contextPath);
      } else {
        openContextMenu(e, 'multi', selectionEntries(), win ? win.path : '/');
      }
    } else {
      // Right-clicking an unselected row selects just that row.
      openContextMenu(e, kind, [entry], contextPath);
    }
  }
  function onEmptyContextMenu(e: MouseEvent) {
    e.preventDefault();
    e.stopPropagation();
    selected = new Set();
    lastClicked = null;
    menuX = e.clientX;
    menuY = e.clientY;
    menuKind = 'empty';
    menuSelection = [];
    menuContextPath = win ? win.path : '/';
    menuOpen = true;
  }

  // --- information dialog ---
  let infoOpen = $state(false);
  let infoItems = $state<FileInfo[] | null>(null);
  let infoLoading = $state(false);
  let infoError = $state<string | null>(null);

  async function openInfo() {
    if (selected.size === 0) return;
    infoLoading = true;
    infoError = null;
    try {
      infoItems = await fetchInfo([...selected]);
      infoOpen = true;
    } catch (err) {
      infoItems = null;
      infoError = err instanceof Error ? err.message : 'could not load information';
    } finally {
      infoLoading = false;
    }
  }
  function closeInfo() {
    infoOpen = false;
    infoItems = null;
    infoError = null;
  }

  // --- delete: confirm dialog then submit a tracked async task ---------------
  // `deleteOpen` toggles the confirm modal; `deleteEntries` is the selected rows
  // rendered inside it. The heading counts only the root-level selection
  // (entries.length), while the task's progress bar reports the recursive
  // files+directories total — both are computed by the engine.
  let deleteOpen = $state(false);
  let deleteEntries = $derived(
    entries.filter((it) => selected.has(it.path)).map((it) => ({ path: it.path, name: it.name, type: it.type }))
  );

  function startDelete() {
    if (selected.size === 0) return;
    deleteOpen = true;
  }
  function cancelDelete() {
    deleteOpen = false;
  }
  async function confirmDelete() {
    deleteOpen = false;
    try {
      const res = await submitDeleteTask([...selected]);
      clearSelection();
      if (res.taskID) flash(`Deleting… (task ${res.taskID}).`);
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not delete');
    }
  }

  // --- copy / paste: the task engine -----------------------------------------
  // `status` is a transient one-line message in the toolbar (copied / pasted /
  // error). Colliding destinations are renamed by the server (" (copy)",
  // " (copy) 1", …) with no prompt, so paste goes straight to the destination
  // folder the window has open.
  let status = $state<string | null>(null);
  let statusTimer: ReturnType<typeof setTimeout> | null = null;
  let pasteError = $state<string | null>(null);

  function flash(message: string | null) {
    status = message;
    if (statusTimer !== null) {
      clearTimeout(statusTimer);
      statusTimer = null;
    }
    if (message) {
      statusTimer = setTimeout(() => {
        status = null;
        statusTimer = null;
      }, 4000);
    }
  }

  // Copy the current window's path to the system clipboard.
  async function copyPathToClipboard() {
    if (!win) return;
    try {
      await navigator.clipboard.writeText(win.path);
      flash('Path copied to clipboard.');
    } catch {
      flash('Failed to copy path.');
    }
  }

  // Copy the current selection into the clipboard (no server call yet).
  function copySelection() {
    if (selected.size === 0) {
      flash('Select one or more items to copy.');
      return;
    }
    setClipboard([...selected]);
    const n = selected.size;
    clearSelection();
    flash(`${n} item${n === 1 ? '' : 's'} copied.`);
  }

  // Paste: branches to move when the clipboard holds a cut, else copies into the
  // folder this window has open. A move confirms first (overwrite notice); a copy
  // goes straight to the destination (the server renames collisions).
  function paste() {
    if ($clipboard.length === 0) {
      flash('Nothing to cut or copy. Cut items first.');
      return;
    }
    if ($cutMode) {
      moveOpen = true;
      return;
    }
    pasteInto(win ? win.path : '/');
  }

  // Submit a paste into `dest`. The destination is always the folder the window
  // has open; collisions are renamed by the server (" (copy)", " (copy) 1", …),
  // so there is nothing to prompt for.
  async function pasteInto(dest: string) {
    const d = (dest ?? '').trim();
    if (!d) {
      pasteError = 'Enter a destination folder.';
      return;
    }
    pasteError = null;
    try {
      const res = await submitCopyTask([...$clipboard], d);
      if (res.taskID) {
        clearClipboard();
        clearSelection();
        flash(`Copying… (task ${res.taskID}).`);
      }
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not paste');
    }
  }

    function clearSelection() {
    selected = new Set();
    lastClicked = null;
    cursor = null;
    kbAnchor = null;
  }

  // --- cut + move: paste branches to a tracked move task when cutMode is set ---
  // `moveOpen` toggles the confirm modal; `cutEntries` (the store) holds the cut
  // rows rendered inside it. Cut also stores the entries in the clipboard, so the
  // same Paste button works for both copy and move.
   let moveOpen = $state(false);
   // pendingDrop / dropChoiceOpen hold the items dropped onto another window so a
   // dialog can let the user pick Move or Copy (see onWindowDrop).
   let pendingDrop = $state<{ items: InternalDropItem[]; dest: string } | null>(null);
   let dropChoiceOpen = $state(false);

  function cutSelection() {
    if (selected.size === 0) {
      flash('Select one or more items to cut.');
      return;
    }
    const n = selected.size;
    const selectedEntries = entries
      .filter((it) => selected.has(it.path))
      .map((it) => ({ path: it.path, name: it.name, type: it.type }));
    setCut(selectedEntries);
    clearSelection();
    flash(`${n} item${n === 1 ? '' : 's'} cut.`);
  }

  async function confirmMove() {
    moveOpen = false;
    const dest = win ? win.path : '/';
    try {
      const res = await submitMoveTask([...$clipboard], dest);
      clearClipboard();
      clearSelection();
      if (res.taskID) flash(`Moving… (task ${res.taskID}).`);
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not move');
    }
  }

  // --- intra-filetoaster drag-and-drop between windows -----------------------
  // Files can be dragged from one opened window to another. The drag carries an
  // intra-app payload on a custom MIME type so only our own drags are honoured;
  // any other drop (OS/browser files) falls through to the upload path. The drag
  // carries no mode — the actual copy-vs-move choice is offered in a dialog when
  // the drag lands. Copy/move reuse the same server
  // tasks and rules as the toolbar buttons, so collisions and overwrites behave
  // identically.
    type InternalDropItem = { path: string; name: string; type: 'file' | 'dir' };
    interface InternalDrop {
      items: InternalDropItem[];
    }
    const DND_MIME = 'application/x-filetoaster-drop';

    // onRowDragStart records the current selection as an intra-filetoaster drag
    // payload. The move/copy choice is decided by a dialog when the drag lands,
    // not by a modifier key, so the payload only carries the items. Draggable is
    // only set on selected rows, so the payload is always the full multi-
    // selection, matching the toolbar.
    function onRowDragStart(e: DragEvent, row: Entry) {
     if (!selected.has(row.path)) return;
     const dt = e.dataTransfer;
     if (!dt) return;
     const items = entries
       .filter((it) => selected.has(it.path))
       .map((it) => ({ path: it.path, name: it.name, type: it.type }));
      const payload: InternalDrop = { items };
      // No modifier key decides the operation: the copy-vs-move choice is made in
      // the drop dialog. The effect is simply "move"; dragover may override the
      // drop effect to "copy" for an internal drag.
      dt.effectAllowed = 'move';
      dt.dropEffect = 'move';
      // text/plain keeps the drag live where required; DND_MIME is how the
      // destination recognises our drags and distinguishes them from a file upload.
      dt.setData('text/plain', JSON.stringify(payload));
      dt.setData(DND_MIME, JSON.stringify(payload));
     }

   // readInternalTransfer returns the intra-filetoaster payload when the transfer
  // carries DND_MIME, else null. A guard on getData keeps it safe for the plain
  // dataTransfer objects the upload tests use.
  function readInternalTransfer(dt: DataTransfer | null | undefined): InternalDrop | null {
    if (!dt || typeof dt.getData !== 'function') return null;
    const raw = dt.getData(DND_MIME);
    if (!raw) return null;
    try {
      const parsed = JSON.parse(raw) as Partial<InternalDrop>;
      const items = Array.isArray(parsed.items)
        ? parsed.items.filter((it): it is InternalDropItem => !!it && typeof it.path === 'string')
        : [];
       if (items.length > 0) {
        return { items };
      }
    } catch {
      // malformed payload: treat as a non-internal drop
    }
    return null;
  }

   // onWindowDrop routes a drop on the window: an intra-filetoaster drag opens a
   // dialog letting the user pick Move or Copy into the folder the window has
   // open; anything else is a file upload. No modifier key is required.
    async function onWindowDrop(e: DragEvent) {
      e.preventDefault();
      const dt = e.dataTransfer;
      if (!dt) return;
      const internal = readInternalTransfer(dt);
      if (internal && internal.items.length > 0 && win) {
        const dest = win.path;
        // Dropping onto the folder the items already live in is pointless — you'd
        // copy or move items onto themselves. Ignore it entirely, with no dialog
        // and no task, rather than offering the choice.
        if (internal.items.every((it) => dirname(it.path) === dest)) {
          return;
        }
        pendingDrop = { items: internal.items, dest };
        dropChoiceOpen = true;
        return;
      }
      await onDrop(e);
    }

    // executeDrop runs the dragged items into `dest` following the user's choice
    // from the drop dialog. Copy goes straight to the destination (server renames
    // collisions, like toolbar copy→paste). Move mirrors the toolbar Cut→Paste: it
    // stages the items as a cut and opens the confirm dialog; the tracked move task
    // is submitted from the dialog's confirm (overwrite rule identical to the
    // toolbar move).
    async function executeDrop(items: InternalDropItem[], dest: string, mode: 'move' | 'copy') {
      const clean = items.filter((it) => it.path);
      if (clean.length === 0 || !dest) {
        flash('Nothing to move or copy.');
        return;
      }
      const paths = clean.map((it) => it.path);
      try {
        if (mode === 'copy') {
         const res = await submitCopyTask(paths, dest);
         if (res.taskID) flash(`Copying… (task ${res.taskID}).`);
       } else {
         setCut(clean);
         moveOpen = true;
       }
     } catch (err) {
       flash(err instanceof Error ? err.message : 'could not move');
     }
   }

  // --- compress: archive the current selection in place ----------------------
  // `compressOpen` toggles the format-confirm modal; `selectionEntries()` feeds
  // the ConfirmCompress dialog and works for tree nodes not in the current listing.
  let compressOpen = $state(false);

  function startCompress() {
    if (selected.size === 0) {
      flash('Select one or more items to compress.');
      return;
    }
    compressOpen = true;
  }
  function cancelCompress() {
    compressOpen = false;
  }
  async function confirmCompress(format: string) {
    compressOpen = false;
    try {
      const res = await submitCompressTask([...selected], format);
      clearSelection();
      if (res.taskID) flash(`Compressing… (task ${res.taskID}).`);
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not compress');
    }
  }

  // --- decompress: extract a single archive in place -----------------------
  // `decompressOpen` toggles the extract-confirm modal; `decompressEntries` are
  // the selected rows rendered inside it. Decompress applies to one archive
  // file only, so the button enables solely when exactly that row is selected.
  let decompressOpen = $state(false);
  let decompressEntries = $derived(
    entries.filter((it) => selected.has(it.path)).map((it) => ({ path: it.path, name: it.name, type: it.type }))
  );
  // Enabled only for a single selected row that is a file and is an archive
  // (zip, tar, tar.gz); the archive-format check is the client half of the
  // "unsupported → disabled" rule the backend backstops with a 400.
  const canDecompress = $derived(
    decompressEntries.length === 1 &&
      decompressEntries[0].type === 'file' &&
      isArchiveFile(decompressEntries[0].name)
  );

  function startDecompress() {
    if (!canDecompress) return;
    decompressOpen = true;
  }
  function cancelDecompress() {
    decompressOpen = false;
  }
  async function confirmDecompress(choice: { into: string }) {
    decompressOpen = false;
    try {
      const res = await submitDecompressTask([...selected], choice.into);
      clearSelection();
      if (res.taskID) flash(`Extracting… (task ${res.taskID}).`);
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not decompress');
    }
  }

  // --- download: stream a single selected file to the browser --------------
  // `downloadFile` POSTs the selected path to /api/downloads, which returns a
  // one-time token; the caller then loads /downloads/{token}, which streams the
  // bound file and consumes the token. The source path never appears in that URL
  // — only the unguessable token does — so the streaming request is confined
  // server-side. Download applies to exactly one file, so the button enables
  // solely when that single file row is selected (a folder or a multi-path
  // selection is rejected by the server).
  let downloadEntries = $derived(
    entries.filter((it) => selected.has(it.path)).map((it) => ({ path: it.path, name: it.name, type: it.type }))
  );
  const canDownload = $derived(
    downloadEntries.length === 1 && downloadEntries[0].type === 'file'
  );

  async function confirmDownload() {
    if (!canDownload) return;
    try {
      const token = await downloadFile(downloadEntries[0].path);
      clearSelection();
      if (token) window.location.assign('/downloads/' + token);
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not download');
    }
  }

  // --- upload: chunked, resumable uploads into this window's open folder ------
  // The upload progress is now shown in the taskbar (RunningTaskIcon).
  // This window only tracks the task ID for the finalize phase to know when to stop polling.
  let uploadState = $state<{ taskID: string; phase: string } | null>(null);
  // progressPct is the ingest completion (sent chunks ÷ total) for the active
  // staging upload, written to below so the footer bar can update live.
  let progressPct = $state(0);
  let fileInput: HTMLInputElement | null = $state(null);
  let folderInput: HTMLInputElement | null = $state(null);
  let uploadMenuOpen = $state(false);

  // Close the upload menu when the user clicks anything outside it.
  function dismissUploadMenu(e: MouseEvent) {
    if ((e.target as HTMLElement).closest('.upload-menu-wrap') === null) {
      uploadMenuOpen = false;
    }
  }

  // Open the hidden file picker. It allows files and directories (single or
  // multiple): `multiple` selects several items, and `directory` lets the OS
  // file picker expose folders (in addition to plain files) where supported;
  // webkitdirectory is intentionally omitted so single files remain selectable.
  function openUploadMenu() {
    uploadMenuOpen = true;
  }

// Files picker (`multiple`): individual files, and a mix of files where the OS
// picker offers it. Each picked File is uploaded from its real bytes.
async function onPickFiles() {
  const picked = fileInput?.files;
  if (!picked || picked.length === 0) {
    flash('Select one or more items to upload.');
    return;
  }
  const items: DnDItem[] = [];
  for (let i = 0; i < picked.length; i++) {
    const f = picked[i] as File & { isDirectory?: boolean };
    if (f.isDirectory) continue; // directories from picker have no blob; they're expanded via webkitdirectory
    items.push(await fileItemFromPicker(f));
  }
  await startUploadFromItems(items, uploadDest ?? (win?.path ?? '/'));
  uploadDest = null;
}

// Folder picker (`multiple` + `webkitdirectory`): the OS returns every file in
// the chosen folder, each carrying its path under the folder as
// webkitRelativePath, so whole folders (and their structure) upload. This is
// the cross-browser way to upload directories, since no native picker selects a
// folder together with individual files in a single dialog everywhere.
async function onPickFolder() {
  const picked = folderInput?.files;
  if (!picked || picked.length === 0) {
    flash('Select a folder to upload.');
    return;
  }
  const items: DnDItem[] = [];
  for (let i = 0; i < picked.length; i++) {
    const f = picked[i] as File;
    items.push(await fileItemFromPicker(f));
  }
  await startUploadFromItems(items, uploadDest ?? (win?.path ?? '/'));
  uploadDest = null;
}

  // startUploadFromItems builds a manifest from picked/dropped items (their real
  // bytes carried as blobs) and starts the chunked upload into `dest`, which
  // defaults to the window's open folder. A context-menu "Upload" overrides it
  // to upload into the right-clicked folder.
  async function startUploadFromItems(items: ReadonlyArray<DnDItem>, dest: string = win?.path ?? '/') {
    if (!win) return;
    if (items.length === 0) {
      flash('No files to upload.');
      return;
    }
    try {
      const manifest = manifestForDataTransfer(items);
      if (manifest.files.length === 0) {
        flash('No files to upload.');
        return;
      }
      const taskID = await uploadManager.startUpload(dest, manifest);
      uploadMenuOpen = false;
      clearSelection();
      if (taskID) {
        const st = uploadManager.state(taskID);
        uploadState = st ? { taskID: st.taskID, phase: st.phase } : { taskID, phase: 'staging' };
        flash(`Uploading… (task ${taskID}).`);
        taskPanelOpen.set(true);
      }
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not upload');
    }
  }
  // --- drag-and-drop directory enumeration -----------------------------------
  // Minimal typing for the non-standard WebKit drag-and-drop directory APIs,
  // which are not in the DOM lib. They let a dropped folder be walked even when
  // the browser leaves DataTransfer.files empty.
  interface DropEntry {
    isFile: boolean;
    isDirectory: boolean;
    getFile: (onFile: (f: File) => void, onError?: (err?: unknown) => void) => void;
    createReader: () => DropReader;
    webkitGetAsEntry: () => DropEntry | null;
  }
  interface DropReader {
    readEntries: () => Promise<DropEntry[]>;
  }

// fileItemFromPicker adapts a File from a file picker (not drag-and-drop) into
// the manifest item shape. Picker files are backed by the actual filesystem and
// won't be evicted, so we use the File directly without cloning to avoid OOM
// on large directories.
async function fileItemFromPicker(f: File): Promise<DnDItem> {
  return {
    name: f.name,
    size: f.size,
    webRelativePath: f.webkitRelativePath ?? '',
    isFile: true,
    blob: f
  };
}

// fileItem adapts a picked/dropped File into the manifest item shape.
// Files from a dropped folder carry webkitRelativePath and are backed by the
// browser's temporary drag-and-drop blob store, which can be evicted shortly
// after the drop. Reading the file into an ArrayBuffer and creating a new Blob
// from it produces a fully independent copy, preventing net::ERR_FILE_NOT_FOUND
// when upload slices are read later.
async function fileItem(f: File): Promise<DnDItem> {
  const rel = f.webkitRelativePath ?? '';
  let blob: Blob = f;
  if (rel) {
    const ab = await f.arrayBuffer();
    blob = new Blob([ab], { type: f.type });
  }
  return {
    name: f.name,
    size: f.size,
    webRelativePath: rel,
    isFile: true,
    blob
  };
}

  // readTransfer enumerates the dropped items into manifest items. Most drops
  // surface their files on DataTransfer.files, but a dropped folder may not (some
  // browsers leave it empty). Every dropped item exposes a WebKit entry, and
  // directory items are still reported with kind 'file' (only 'file' and 'string'
  // kinds exist), so collect each entry via webkitGetAsEntry() and route on it to
  // enumerate folders cross-browser; a non-empty files list is the fast path.
async function readTransfer(dt: DataTransfer): Promise<DnDItem[]> {
  const files = Array.from(dt.files ?? []);
  const fromFiles = await Promise.all(files.map(fileItem));
  if (fromFiles.length > 0) return fromFiles;
    const items: DnDItem[] = [];
    const entries: DropEntry[] = [];
    for (let i = 0; i < dt.items.length; i++) {
      const item = dt.items[i];
      const asEntry = item as unknown as { webkitGetAsEntry?: () => DropEntry | null };
      // Collect the entry regardless of kind: a directory item is still
      // reported as kind 'file', and getAsFile() on it returns null, so a
      // kind check would skip folders entirely.
      if (typeof asEntry.webkitGetAsEntry === 'function') {
        const entry = asEntry.webkitGetAsEntry();
        if (entry) entries.push(entry);
      }
    }
    await Promise.all(entries.map((entry) => readEntry(entry, items)));
    return items;
  }

  // readEntry reads one entry into `into`, recursing into directories. A file
  // from a dropped folder is backed by the browser's temporary drag-and-drop
  // blob store, which can be evicted shortly after the drop: slicing a chunk from
  // that file later makes the browser report net::ERR_FILE_NOT_FOUND. The bytes
  // are cloned into a long-lived Blob now, while the source is still valid, so
  // the upload slices from that stable blob; metadata comes from the original File.
  async function readEntry(entry: DropEntry, into: DnDItem[]): Promise<void> {
    if (entry.isDirectory) return readDirectory(entry, into);
    await new Promise<void>((resolve, reject) =>
      entry.getFile(async (f) => {
        try {
          const ab = await f.arrayBuffer();
          const stable = new Blob([ab], { type: f.type });
          into.push({
            name: f.name,
            size: f.size,
            webRelativePath: f.webkitRelativePath ?? '',
            isFile: true,
            blob: stable
          });
          resolve();
        } catch (err) {
          reject(err instanceof Error ? err : new Error(String(err)));
        }
      }, (err) => reject(err instanceof Error ? err : new Error(String(err))))
    );
  }

  // readDirectory reads a directory's entries in batches (readEntries returns a
  // batch at a time) until none remain, recursing into subdirectories.
  function readDirectory(entry: DropEntry, into: DnDItem[]): Promise<void> {
    return new Promise<void>((resolve, reject) => {
      const reader = entry.createReader() as DropReader;
      const drain = () =>
        reader.readEntries().then((batch) => {
          if (!batch || batch.length === 0) return resolve();
          void Promise.all(
            batch.map((child) => (child.isDirectory ? readDirectory(child, into) : readEntry(child, into)))
          ).then(drain, () => resolve());
        });
      drain().catch(reject);
    });
  }

  // isDirectoryDrop reports whether a drop represents a folder/directory. We
  // only accept files on drag-and-drop (folders upload through the toolbar
  // "Upload directory…" picker), so onDrop rejects a directory drop. Two browser
  // behaviors are covered: Blink (Chrome/Edge) flattens a dropped folder into
  // DataTransfer.files, each file carrying a non-empty webkitRelativePath, while
  // Gecko (Firefox) leaves DataTransfer.files empty and exposes the folder as a
  // directory entry in DataTransfer.items. Individually dragged files carry no
  // webkitRelativePath and resolve to a file entry, so they are allowed.
  function isDirectoryDrop(dt: DataTransfer): boolean {
    if (dt.items && dt.items.length > 0) {
      for (let i = 0; i < dt.items.length; i++) {
        const item = dt.items[i];
        const asEntry = item as unknown as { webkitGetAsEntry?: () => DropEntry | null };
        if (typeof asEntry.webkitGetAsEntry === 'function') {
          const entry = asEntry.webkitGetAsEntry();
          if (entry && entry.isDirectory) return true;
        }
      }
    }
    if (dt.files) {
      for (let i = 0; i < dt.files.length; i++) {
        if (dt.files[i].webkitRelativePath) return true;
      }
    }
    return false;
  }

  // Drag-and-drop onto the window: accept files only, never folders. Folder
  // upload is offered through the toolbar "Upload directory…" picker.
  async function onDrop(e: DragEvent) {
    e.preventDefault();
    const dt = e.dataTransfer;
    if (!dt) return;
    if (!win) return;
    if (isDirectoryDrop(dt)) {
      flash('Drop files only; upload a folder from the toolbar.');
      return;
    }
    let items: DnDItem[] = [];
    try {
      items = await readTransfer(dt);
    } catch {
      flash('could not read the dropped items');
      return;
    }
    if (items.length === 0) {
      flash('No files to upload.');
      return;
    }
    try {
      const manifest = manifestForDataTransfer(items);
      if (manifest.files.length === 0) {
        flash('No files to upload.');
        return;
      }
      const taskID = await uploadManager.startUpload(win.path, manifest);
      if (taskID) {
        const st = uploadManager.state(taskID);
        uploadState = st ? { taskID: st.taskID, phase: st.phase } : { taskID, phase: 'staging' };
        flash(`Uploading… (task ${taskID}).`);
      }
    } catch (err) {
      flash(err instanceof Error ? err.message : 'could not upload');
    }
  }

  // Abort ingest while staging. Already-received chunks are kept (resumable);
  // once the finalize copy begins, the taskbar cancel applies instead.
  function abortUpload() {
    const id = uploadState?.taskID;
    if (!id) return;
    void uploadManager.abort(id);
    uploadState = null;
    flash('Upload aborted.');
  }

  // The tree is anchored at the window's root and never re-rooted on navigation.
  // The title bar shows the currently opened folder; the store's `name` stays as
  // the window's display name (the root label) and never changes after open.
  let title = $derived(win ? displayName(win) : winProp.name);

  // Navigate to `path`: it becomes the active folder (store `path`), the tree
  // expands root..path (lazy child-load, cached), and the main listing reloads.
  // `navSeq` makes the last requested navigation win: expanding a deep branch
  // can take several sequential loads, and a slower, older request must never
  // land after a newer click and pull the window back to the previous folder.
  let navSeq = 0;
  async function navigate(path: string): Promise<void> {
    const root = winProp.rootPath;
    if (!isWithin(path, root)) return;
    const seq = ++navSeq;
    clearSelection();
    try {
      mainLoading = true;
      mainError = null;
      const listing = await tree.navigate(winProp.id, path, root);
      if (seq !== navSeq) return;
      entries = listing.entries;
      updateWindow(winProp.id, { path });
      frame += 1;
    } catch (err) {
      if (seq !== navSeq) return;
      mainError = err instanceof Error ? err.message : 'could not open this folder';
    } finally {
      if (seq === navSeq) mainLoading = false;
    }
  }

  // Tree rows must act on the initial press, not on the synthesized `click`.
  // Expanding a deep branch renders per-directory "Loading…" nodes and the tree
  // re-renders on focus/navigation, so between mousedown and mouseup the row
  // under the pointer can move or be replaced. The browser then retargets the
  // click to an ancestor <li> that has no handler, the navigation is silently
  // dropped, and the window keeps showing the previously opened folder. Starting
  // the navigation on mousedown registers the choice before any re-render can
  // shift the row. The trailing pointer `click` is ignored (real mouse clicks
  // carry detail > 0); keyboard activation produces detail === 0 and still
  // navigates.
  function onTreeRowMouseDown(e: MouseEvent, dir: string) {
    if (e.button !== 0) return;
    if ((e.target as Element | null)?.closest?.('.twisty')) return;
    void navigate(dir);
  }
  function onTreeRowClick(e: MouseEvent, dir: string) {
    if (e.detail !== 0) return;
    void navigate(dir);
  }

  // Move up one directory from the currently opened folder (the toolbar "up"
  // button). Never navigates above the window's root path.
  function goUp() {
    if (!win || !win.path || win.path === win.rootPath) return;
    const parent = dirname(win.path);
    if (!isWithin(parent, win.rootPath)) {
      void navigate(win.rootPath);
      return;
    }
    void navigate(parent);
  }

  // --- keyboard shortcuts (per window) -------------------------------------
  // Every open window listens, but only the active (top-most, non-minimized)
  // window reacts, so arrows, Enter, select-all and the ctrl+alt actions never
  // leak into another open file window.
  function isTextInputTarget(target: EventTarget | null): boolean {
    const el = target as HTMLElement | null;
    if (!el || typeof el.tagName !== 'string') return false;
    const tag = el.tagName;
    return (
      tag === 'INPUT' ||
      tag === 'TEXTAREA' ||
      tag === 'SELECT' ||
      el.isContentEditable === true
    );
  }

  // Enter / arrow handling must not hijack a focused native control (a toolbar
  // button still activates on Enter; links keep their own keyboard behavior).
  function isNativeControlTarget(target: EventTarget | null): boolean {
    const el = target as HTMLElement | null;
    if (isTextInputTarget(target)) return true;
    if (!el || typeof el.tagName !== 'string') return false;
    return el.tagName === 'BUTTON' || el.tagName === 'A';
  }

  function onWindowKeydown(e: KeyboardEvent) {
    if (!win || win.minimized) return;
    // A key event targeted inside this window only drives this window; events
    // that arrive from elsewhere (body/document) drive the active window and
    // are ignored by every other one, so windows never affect each other.
    const targetNode = e.target as Node | null;
    const targetEl = e.target as HTMLElement | null;
    const insideThisWindow = !!targetNode && !!rootEl && rootEl.contains(targetNode);
    const insideOtherWindow =
      !insideThisWindow &&
      !!targetEl &&
      typeof targetEl.closest === 'function' &&
      !!targetEl.closest('.window');
    if (insideOtherWindow) return;
    if (!insideThisWindow && !isActiveWindow(winProp.id)) return;
    if (isTextInputTarget(e.target)) return;
    // While a modal or menu belonging to this window is open, the background
    // must not react to shortcuts.
    if (
      infoOpen ||
      deleteOpen ||
      moveOpen ||
      dropChoiceOpen ||
      compressOpen ||
      decompressOpen ||
      menuOpen ||
      uploadMenuOpen
    ) {
      return;
    }

    // Ctrl+Alt is reported as AltGr on many layouts/browsers, where `e.key` is the
    // *character* the combination produces (e.g. "®" for R, "@" for Q) rather
    // than the letter. The physical `e.code` ("KeyR") is layout-independent, so
    // prefer it and only fall back to an ASCII `e.key`.
    function shortcutLetter(e: KeyboardEvent): string | null {
      if (typeof e.code === 'string' && /^Key[A-Z]$/.test(e.code)) {
        return e.code.slice(3).toLowerCase();
      }
      if (typeof e.key === 'string' && e.key.length === 1) {
        const lower = e.key.toLowerCase();
        if (lower >= 'a' && lower <= 'z') return lower;
      }
      return null;
    }

    // Ctrl+Alt combinations are the app's file actions.
    if (e.ctrlKey && e.altKey && !e.metaKey) {
      if (e.key === 'ArrowUp' || e.code === 'ArrowUp') {
        e.preventDefault();
        goUp();
        return;
      }
      switch (shortcutLetter(e)) {
        case 'r':
          e.preventDefault();
          void reload();
          return;
        case 'c':
          e.preventDefault();
          copySelection();
          return;
        case 'v':
          e.preventDefault();
          paste();
          return;
        case 'x':
          e.preventDefault();
          cutSelection();
          return;
        case 'd':
          e.preventDefault();
          startDelete();
          return;
        case 'a':
          e.preventDefault();
          selectAll();
          return;
        case 'i':
          e.preventDefault();
          void openInfo();
          return;
        case 'l':
          e.preventDefault();
          void confirmDownload();
          return;
        case 'n':
          e.preventDefault();
          startNewDir();
          return;
        case 't':
          e.preventDefault();
          taskPanelOpen.update((open) => !open);
          return;
        default:
          return;
      }
    }

    if (e.ctrlKey || e.altKey || e.metaKey) return;
    if (isNativeControlTarget(e.target)) return;

    // Arrow navigation belongs to the main listing only, never the tree view.
    const target = e.target as HTMLElement | null;
    if (target?.closest?.('.tree')) return;

    if (e.key === 'ArrowDown') {
      e.preventDefault();
      moveCursor(1, e.shiftKey);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      moveCursor(-1, e.shiftKey);
    } else if (e.key === 'Enter') {
      e.preventDefault();
      openCursorDirectory();
    }
  }

  // Move the keyboard cursor one row up/down. A plain move selects exactly that
  // row; a shift move extends the selection from the stable anchor (kbAnchor).
  function moveCursor(delta: number, extend: boolean) {
    if (rows.length === 0) return;
    const current = cursor ? rows.findIndex((r) => r.path === cursor) : -1;
    const next =
      current === -1
        ? delta > 0
          ? 0
          : rows.length - 1
        : Math.min(rows.length - 1, Math.max(0, current + delta));
    const target = rows[next];
    if (!target) return;
    const previous = current === -1 ? null : rows[current];
    cursor = target.path;
    if (!extend) {
      selected = new Set([target.path]);
      lastClicked = target.path;
      kbAnchor = target.path;
    } else {
      if (kbAnchor === null) kbAnchor = previous ? previous.path : target.path;
      const anchorIndex = rows.findIndex((r) => r.path === kbAnchor);
      if (anchorIndex === -1) {
        selected = new Set([target.path]);
        lastClicked = target.path;
        kbAnchor = target.path;
      } else {
        const lo = Math.min(anchorIndex, next);
        const hi = Math.max(anchorIndex, next);
        selected = new Set(rows.slice(lo, hi + 1).map((r) => r.path));
        lastClicked = target.path;
      }
    }
    scrollPathIntoView(target.path);
  }

  // Enter opens the directory under the keyboard cursor (or the single selected
  // directory); Enter on a file is intentionally a no-op.
  function openCursorDirectory() {
    const candidate = cursor ? rows.find((r) => r.path === cursor) : undefined;
    const single = selected.size === 1 ? rows.find((r) => selected.has(r.path)) : undefined;
    const target = candidate ?? single;
    if (target && target.type === 'dir') void navigate(target.path);
  }

  function scrollPathIntoView(path: string) {
    requestAnimationFrame(() => {
      if (!contentEl) return;
      const row = [...contentEl.querySelectorAll<HTMLElement>('[data-row-path]')].find(
        (el) => el.getAttribute('data-row-path') === path
      );
      row?.scrollIntoView?.({ block: 'nearest' });
    });
  }

  let reloading = $state(new Set<string>());

  // Reload the currently opened directory from the server, bypassing cache, so
  // changes made outside the UI become visible without navigating away.
  // A per-path reentrancy guard prevents concurrent reloads for the same path
  // while allowing different paths to reload simultaneously.
  async function reload(path?: string) {
    const targetPath = path ?? win?.path;
    if (!targetPath || reloading.has(targetPath)) return;
    reloading.add(targetPath);
    try {
      mainLoading = true;
      mainError = null;
      const listing = await tree.refresh(targetPath);
      if (targetPath === win?.path) {
        entries = listing.entries;
      }
      frame += 1;
    } catch (err) {
      mainError = err instanceof Error ? err.message : 'could not reload this folder';
    } finally {
      mainLoading = false;
      reloading.delete(targetPath);
    }
  }

  // Handle reload events from task completion. Only reload if this window's
  // ID matches and the target path is this window's current path or a parent.
  function onReloadEvent(e: CustomEvent) {
    const { windowId, path } = e.detail;
    if (windowId !== winProp.id) return;
    if (win && isWithin(path, win.path)) {
      void reload(path);
    }
  }

  // In-place directory naming in the toolbar. `editingNew` toggles the input;
  // Enter creates and navigates into the folder, Escape/blur discards.
  let editingNew = $state(false);
  let newName = $state('');
  let inputEl: HTMLInputElement | null = $state(null);
  // `newDirParent` overrides the folder a new folder is created in (set by the
  // context menu's "New folder" on a folder/background); null means the open
  // folder (`win.path`), which the toolbar uses.
  let newDirParent: string | null = $state(null);
  let isAtRoot = $derived(win ? win.path === win.rootPath : true);

  function startNewDir() {
    if (!win) return;
    startNewDirIn(win.path);
  }
  function startNewDirIn(parent: string) {
    newDirParent = parent;
    newName = '';
    editingNew = true;
  }
  // Focus the in-place input once it is rendered (it does not exist while
  // `editingNew` is false, so a plain call in startNewDir would be a no-op).
  $effect(() => {
    if (editingNew) inputEl?.focus();
  });
  function cancelNewDir() {
    if (!editingNew) return;
    editingNew = false;
    newName = '';
    newDirParent = null;
  }
    async function confirmNewDir() {
      const name = newName.trim();
      if (!name || !editingNew) return;
      const parent = newDirParent ?? (win ? win.path : '/');
      editingNew = false;
      newName = '';
      newDirParent = null;
      // View the target folder first so the new folder shows up in the current
      // window when the context menu created it inside another directory.
      if (win && parent !== win.path) await navigate(parent);
      try {
        await mkdirDirectory(parent, name);
        reloadWindowsForPath(parent);
      } catch (err) {
        mainError = err instanceof Error ? err.message : 'could not create folder';
      }
    }

    // --- rename: synchronous, like new folder --------------------------------
    // `editingRename` toggles the in-place input; Enter renames the current
    // selection (the first item to the exact name, the rest to "name (N)"),
    // Escape/blur discards. A conflict (a target already exists) or a blank
    // name surfaces as `mainError` without clearing the selection, so the user
    // can adjust and retry.
    let editingRename = $state(false);
    let renameName = $state('');
    let renameInputEl: HTMLInputElement | null = $state(null);
    // In-place rename of a tree node that is not part of the current listing.
    let editingTreeRename = $state(false);
    let treeRenamePath = $state('');
    let treeRenameName = $state('');
    let treeRenameInputEl: HTMLInputElement | null = $state(null);

    function startRename() {
      if (!win || selected.size === 0) return;
      // Pre-fill with the exact current name (including the extension for files,
      // and none for names without one) so the user edits in place. Tree nodes
      // may not appear in `entries` (the current listing), so fall back to the
      // context-menu selection when the entry isn't in the listing.
      const first =
        entries.find((it) => selected.has(it.path)) ??
        menuSelection.find((it) => selected.has(it.path));
      if (!first) return;
      // A tree node that isn't listed in the current folder is renamed inplace
      // inside the tree itself, not in the main listing.
      if (!entries.some((it) => it.path === first.path)) {
        editingTreeRename = true;
        treeRenamePath = first.path;
        treeRenameName = first.name;
        return;
      }
      renameName = first.name;
      editingRename = true;
    }
    $effect(() => {
      if (editingRename) renameInputEl?.focus();
      if (editingTreeRename) treeRenameInputEl?.focus();
    });
    function cancelRename() {
      if (!editingRename) return;
      editingRename = false;
      renameName = '';
    }
    function cancelTreeRename() {
      if (!editingTreeRename) return;
      editingTreeRename = false;
      treeRenamePath = '';
      treeRenameName = '';
    }
    async function confirmRename() {
      const name = renameName.trim();
      if (!win || !editingRename || !name || selected.size === 0) return;
      editingRename = false;
      renameName = '';
      const count = selected.size;
      const dir = win.path;
      try {
        await renameFile([...selected], name);
        clearSelection();
        // Reload every open window showing this directory (not just this one),
        // since rename is synchronous and never flows through the task SSE path.
        reloadWindowsForPath(dir);
        flash(`Renamed ${count} item${count === 1 ? '' : 's'}.`);
      } catch (err) {
        mainError = err instanceof Error ? err.message : 'could not rename';
      }
    }

    async function confirmTreeRename() {
      const name = treeRenameName.trim();
      if (!editingTreeRename || !treeRenamePath || !name) return;
      editingTreeRename = false;
      const path = treeRenamePath;
      treeRenamePath = '';
      treeRenameName = '';
      try {
        await renameFile([path], name);
        // Reload the parent folder so the renamed node appears in listings and
        // refresh the tree cache for it so the renamed node shows its new name.
        const parent = dirname(path);
        reloadWindowsForPath(parent);
        if (win) await tree.refresh(parent).catch(() => {});
        frame += 1;
        flash('Renamed 1 item.');
      } catch (err) {
        mainError = err instanceof Error ? err.message : 'could not rename';
      }
    }

  // Snapshot of the reactive shape the tree row renders from. Reading `frame`
  // (bumped on every tree mutation) makes this snapshot re-subscribe to the
  // shared `tree` cache, which is not itself reactive, so the row re-renders
  // when expansion, loading, or error state changes.
  function rowSnapshot(dir: string) {
    const version = frame;
    void version;
    const active = win?.path;
    void active;
    return {
      version,
      expanded: tree.isExpanded(winProp.id, dir),
      loading: tree.isLoading(dir),
      error: tree.getError(dir),
      kids: tree.isExpanded(winProp.id, dir) ? tree.childEntries(dir) : []
    };
  }

  // The path of the row that the rename input replaces in the listing while
  // `editingRename` is set (the first selected entry); `null` otherwise so the
  // listing can show the input in place of that row.
  const renameTarget = $derived(
    editingRename && selected.size > 0
      ? entries.find((it) => selected.has(it.path))?.path ?? null
      : null
  );

  // Sorted view of the current entries. For name, folders sort first; the other
  // keys sort purely by value.
  const rows = $derived(
    [...entries].sort((a, b) => {
      const mul = sortDir === 'asc' ? 1 : -1;
      if (sortKey === 'name') {
        if (a.type !== b.type) return a.type === 'dir' ? -1 : 1;
        return a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }) * mul;
      }
      let cmp = 0;
      if (sortKey === 'size') cmp = a.size - b.size;
      else if (sortKey === 'created') cmp = Date.parse(a.created) - Date.parse(b.created);
      else cmp = Date.parse(b.modified) - Date.parse(a.modified);
      return cmp * mul;
    })
  );

  function headerLabel(key: SortKey): string {
    switch (key) {
      case 'name':
        return 'Name';
      case 'size':
        return 'Size';
      case 'created':
        return 'Created';
      case 'updated':
        return 'Updated';
    }
  }

  // Tree node label: the window's display name at the root, folder name below.
  function label(dir: string): string {
    if (!win) return basename(dir);
    return dir === win.rootPath ? win.name : basename(dir);
  }

  function arrow(dir: string): string {
    // The arrow tracks expansion state immediately, so it points down the
    // moment a directory is expanded — even while its children are still
    // loading — and points right while the directory is collapsed.
    if (!win) return '▸';
    return tree.isExpanded(win.id, dir) ? '▾' : '▸';
  }

  // --- formatting helpers (shared with the info dialog via $lib/fs/format) ---

  // Keep the highlighted active row scrolled into view on navigation.
  $effect(() => {
    const active = win?.path;
    if (!active) return;
    requestAnimationFrame(() => {
      const el = document.querySelector<HTMLElement>(
        `[data-tree-active="${CSS.escape(active)}"]`
      );
      if (el && typeof el.scrollIntoView === 'function') {
        el.scrollIntoView({ block: 'nearest' });
      }
    });
  });
</script>

  <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
  <div
    class="window"
    class:minimized={winProp.minimized}
    role="dialog"
    tabindex="0"
    bind:this={rootEl}
    aria-label={title}
    style="left:{pos ? pos.x : winProp.x}px; top:{pos ? pos.y : winProp.y}px; width:{size.width}px; height:{size.height}px; z-index:{winProp.zIndex};"
    onmousedown={() => focusWindow(winProp.id)}
    ondragover={(e) => {
      e.preventDefault();
      const dt = e.dataTransfer;
      if (dt) {
         const dnd = readInternalTransfer(dt);
         if (dnd) dt.dropEffect = 'copy';
      }
    }}
    ondrop={onWindowDrop}
  >
  {#snippet newFolderInput()}
    <form class="new-input-form" onsubmit={() => confirmNewDir()}>
      <input class="new-input" bind:this={inputEl} bind:value={newName} autocomplete="off" spellcheck="false"
        placeholder="New directory"
        onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); confirmNewDir(); } else if (e.key === 'Escape') { e.preventDefault(); cancelNewDir(); } }}
        onblur={() => cancelNewDir()} />
    </form>
  {/snippet}
  {#snippet renameInput()}
    <form class="new-input-form" onsubmit={() => confirmRename()}>
      <input class="new-input" bind:this={renameInputEl} bind:value={renameName} autocomplete="off" spellcheck="false"
        placeholder="Rename"
        onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); confirmRename(); } else if (e.key === 'Escape') { e.preventDefault(); cancelRename(); } }}
        onblur={() => cancelRename()} />
    </form>
  {/snippet}
  {#snippet treeRenameInput()}
    <form class="new-input-form" onsubmit={() => confirmTreeRename()}>
      <input class="new-input" bind:this={treeRenameInputEl} bind:value={treeRenameName} autocomplete="off" spellcheck="false"
        placeholder="Rename"
        onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); confirmTreeRename(); } else if (e.key === 'Escape') { e.preventDefault(); cancelTreeRename(); } }}
        onblur={() => cancelTreeRename()} />
    </form>
  {/snippet}
  <header class="header" onpointerdown={onHeaderPointerDown} aria-roledescription="draggable" role="application">
    <span class="logo" aria-hidden="true">
      <img src="/filetoaster-directory.svg" alt="" />
    </span>
    <span class="title" title={`${title} — ${winProp.path}`}>{title}</span>
    <div class="window-controls">
      <button class="minimize" type="button" onclick={() => toggleMinimize(winProp.id)} aria-label="Minimize window">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M5 12h14" />
        </svg>
      </button>
      <button class="close" type="button" onclick={() => closeWindow(winProp.id)} aria-label="Close window">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </div>
  </header>

   <div class="toolbar">
     <button class="up" type="button" title="Up one directory" aria-label="Up one directory" disabled={isAtRoot} onclick={() => goUp()}>
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M12 19V5M5 12l7-7 7 7" />
       </svg>
     </button>
      <button class="tool reload" type="button" title="Reload" aria-label="Reload" onclick={() => reload()}>
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M21 12a9 9 0 1 1-3.2-6.9" />
         <path d="M21 4v5h-5" />
       </svg>
     </button>
      <button class="tool newfolder" type="button" title="New directory" aria-label="New directory" onclick={() => startNewDir()} disabled={editingNew}>
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M4 6a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6Z" />
         <path d="M12 11v6M9 14h6" />
       </svg>
     </button>
     <button
       class="tool rename"
       type="button"
       title="Rename selected"
       aria-label="Rename"
       disabled={selected.size === 0 || editingNew}
       onclick={startRename}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M3 17.25V21h3.75L17.89 9.64a1 1 0 0 0 0-1.41L15.77 6.11a1 1 0 0 0-1.41 0L3 15.82Z" />
         <path d="M17.5 4.5l2.5 2.5L19 9l-2.5-2.5Z" />
       </svg>
     </button>
     <button
       class="tool cut"
       class:cut-active={$cutMode}
       type="button"
       title="Cut selected to clipboard"
       aria-label="Cut"
       disabled={selected.size === 0}
       onclick={cutSelection}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <circle cx="6" cy="6" r="2.75" />
         <circle cx="6" cy="18" r="2.75" />
         <line x1="8.5" y1="8.5" x2="19" y2="19" />
         <line x1="8.5" y1="15.5" x2="19" y2="5" />
       </svg>
     </button>
     <button
       class="tool copy"
       type="button"
       title="Copy selected to clipboard"
       aria-label="Copy"
       disabled={selected.size === 0}
       onclick={copySelection}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M9 9h10v10H9Z" />
         <path d="M5 15V5a2 2 0 0 1 2-2h10" />
       </svg>
     </button>
     <button
       class="tool paste"
       type="button"
       title="Paste clipboard into this folder"
       aria-label="Paste"
       disabled={$clipboard.length === 0}
       onclick={paste}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M8 5h8v3H8Z" />
         <path d="M5 8h14v11a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2Z" />
       </svg>
     </button>
     <button
       class="tool delete"
       type="button"
       title="Delete selected"
       aria-label="Delete"
       disabled={selected.size === 0}
       onclick={startDelete}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M5 7h14" />
         <path d="M8 7V5a1 1 0 0 1 1-1h6a1 1 0 0 1 1 1v2" />
         <path d="M6 7l1 12h10l1-12" />
       </svg>
     </button>
     <button
       class="tool compress"
       type="button"
       title="Compress selected into an archive"
       aria-label="Compress"
       disabled={selected.size === 0}
       onclick={startCompress}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M4 7a2 2 0 0 1 2-2h8l2 2h4a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1Z" />
         <path d="M12 10v4m0 0-2-2m2 2 2-2" />
         <path d="M9 18h6" />
       </svg>
     </button>
     <button
       class="tool decompress"
       type="button"
       title="Extract selected archive"
       aria-label="Decompress"
       disabled={!canDecompress}
       onclick={startDecompress}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M4 7a2 2 0 0 1 2-2h8l2 2h4a1 1 0 0 1 1 1v11a1 1 0 0 1-1 1H6a1 1 0 0 1-1-1Z" />
         <path d="M12 14v-4m0 0-2 2m2-2 2 2" />
         <path d="M9 18h6" />
       </svg>
     </button>
     <button
       class="tool download"
       type="button"
       title="Download this file"
       aria-label="Download"
       disabled={!canDownload}
       onclick={confirmDownload}
     >
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M12 3v11" />
         <path d="M7 12l5 5 5-5" />
         <path d="M5 20v1h14v-1" />
       </svg>
     </button>
     <div class="upload-menu-wrap">
        <button class="tool upload" type="button" title="Upload files or directories to this folder" aria-label="Upload" aria-haspopup="menu" aria-expanded={uploadMenuOpen} onclick={openUploadMenu}>
         <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
           <path d="M12 19V5" />
           <path d="M6 9l6-6 6 6" />
           <path d="M4 15v4h16v-4" />
         </svg>
       </button>
       {#if uploadMenuOpen}
         <div class="upload-menu" role="menu" aria-label="Upload options">
           <button class="upload-menu-item" type="button" role="menuitem" onclick={() => { uploadMenuOpen = false; fileInput?.click(); }}>Upload files…</button>
            <button class="upload-menu-item" type="button" role="menuitem" onclick={() => { uploadMenuOpen = false; folderInput?.click(); }}>Upload directory…</button>
         </div>
       {/if}
     </div>
     <input
       type="file"
       multiple
       style="display:none"
       bind:this={fileInput}
       onchange={onPickFiles}
       aria-hidden="true"
     />
     <input
       type="file"
       multiple
       webkitdirectory
       style="display:none"
       bind:this={folderInput}
       onchange={onPickFolder}
       aria-hidden="true"
      />
       <button class="tool selectall" type="button" title="Select all items" aria-label="Select all" disabled={entries.length === 0} onclick={() => selectAll()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
          <path d="M3 3h18v18H3z" />
          <path d="M7 7l3 3 7-7" />
        </svg>
      </button>
      <button class="tool info" type="button" title="Information" aria-label="Information" disabled={selected.size === 0} onclick={() => openInfo()}>
       <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
         <path d="M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18Z" />
         <path d="M12 16v-4M12 8h.01" />
       </svg>
       {#if selected.size > 0}
         <span class="badge" aria-hidden="true">{selected.size}</span>
       {/if}
     </button>
   </div>

  <main class="body">
    <aside class="tree" aria-label="Folder tree" style="width:{treeWidth}px">
      <ul class="tree-root" data-frame={frame}>
        {#if win}
          {@render treeRow(win.rootPath, 0)}
        {/if}
        {#snippet treeRow(dir: string, depth: number)}
        {@const s = rowSnapshot(dir)}
        <li
          class="trow"
          class:active={win && win.path === dir}
          data-tree-active={win && win.path === dir ? dir : undefined}
        >
          <button
            type="button"
            class="tnode"
            title={dir}
            style="padding-left:{8 + depth * 12}px"
            onmousedown={(e) => onTreeRowMouseDown(e, dir)}
            onclick={(e) => onTreeRowClick(e, dir)}
            oncontextmenu={(e) => {
              e.preventDefault();
              e.stopPropagation();
              openContextMenu(e, 'folder', [{ path: dir, name: label(dir), type: 'dir' }], dir);
            }}
          >
            <span class="twisty" aria-hidden="true" onclick={(e) => { e.stopPropagation(); void tree.toggleExpand(winProp.id, dir).then(() => frame += 1).catch(() => frame += 1); frame += 1; }}>{arrow(dir)}</span>
             <span class="ticon" aria-hidden="true">
               <FileIcon type="folder" />
             </span>
             {#if editingTreeRename && treeRenamePath === dir}
               {@render treeRenameInput()}
             {:else}
               <span class="tname" title={label(dir)}>{label(dir)}</span>
             {/if}
          </button>
          {#if s.expanded}
            {#if s.loading}
              <span class="tchild t-loading">Loading…</span>
            {:else if s.error}
              <span class="tchild t-error">{s.error}</span>
            {:else}
              <ul class="tchildren">
                {#each s.kids as child (child.path)}
                  {@render treeRow(child.path, depth + 1)}
                {/each}
              </ul>
            {/if}
          {/if}
        </li>
      {/snippet}</ul>
    </aside>
    <div
      class="tree-resize"
      onpointerdown={onTreeResizePointerDown}
      aria-label="Resize folder tree"
      role="separator"
    ></div>

    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div class="content" bind:this={contentEl} onclick={clearSelectionIfBackground} oncontextmenu={onEmptyContextMenu}>
      {#if mainLoading && entries.length === 0}
        <div class="empty">Loading…</div>
      {:else if mainError}
        <div class="error">{mainError}</div>
      {:else if rows.length === 0}
        {#if editingNew}
          {@render newFolderInput()}
        {:else}
          <div class="empty">This folder is empty.</div>
        {/if}
      {:else}
        <table class="list">
          <thead>
            <tr>
              {#each SORT_KEYS as key (key)}
                <th class:active={sortKey === key} onclick={() => toggleSort(key)} role="button" tabindex="0"
                  onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') ? toggleSort(key) : undefined}>
                  <span class="thc">{headerLabel(key)}<span class="sort-arrow" aria-hidden="true">{sortKey === key ? (sortDir === 'asc' ? ' ▲' : ' ▼') : ''}</span></span>
                </th>
              {/each}
            </tr>
          </thead>
          <tbody>
            {#if editingNew}
              <tr class="row new-folder-row">
                <td class="name" colspan="4">
                  {@render newFolderInput()}
                </td>
              </tr>
            {/if}
            {#each rows as row (row.path)}
              {#if editingRename && row.path === renameTarget}
                <tr class="row renaming-row">
                  <td class="name" colspan="4">
                    {@render renameInput()}
                  </td>
                </tr>
              {:else}
                <tr
                  class="row"
                  class:selected={selected.has(row.path)}
                  class:dir={row.type === 'dir'}
                  data-row-path={row.path}
                  draggable={selected.has(row.path)}
                   onclick={(e) => onRowClick(e, row)}
                   ondblclick={() => (row.type === 'dir' && navigate(row.path))}
                   ondragstart={(e) => onRowDragStart(e, row)}
                   oncontextmenu={(e) => onRowContextMenu(e, row)}
                >
                  <td class="name">
                    <span class="fg" aria-hidden="true">
                      <FileIcon type={row.type === 'dir' ? 'folder' : 'doc'} />
                    </span>
                    <span class="rname">{row.name}</span>
                  </td>
                  <td class="size">{formatSize(row.size)}</td>
                  <td class="date">{formatDate(row.created)}</td>
                  <td class="date">{formatDate(row.modified)}</td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      {/if}
    </div>
  </main>

  <footer class="status">
    <span class="spath" aria-hidden="true">
      <svg viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
        <path d="M3 6a1 1 0 0 1 1-1h3l1.5 1.5H16a1 1 0 0 1 1 1V15a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V6Z" />
      </svg>
    </span>
    <span class="spath-val" title={winProp.path}>{winProp.path}</span>
    <button class="copy-path" type="button" title="Copy path to clipboard" aria-label="Copy path" onclick={copyPathToClipboard}>
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="14" height="14" aria-hidden="true"><path d="M9 9h10v10H9Z" /><path d="M5 15V5a2 2 0 0 1 2-2h10" /></svg>
    </button>
    {#if status}
      <span class="status-msg">{status}</span>
    {/if}
    <span class="count">{rows.length} item{rows.length === 1 ? '' : 's'}</span>
  </footer>

  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="resize" onpointerdown={onResizePointerDown} aria-label="Resize window"></div>

{#if infoOpen}
    <Portal>
      <InfoDialog items={infoItems ?? []} onClose={closeInfo} />
    </Portal>
  {/if}

  {#if deleteOpen}
    <Portal>
      <ConfirmDelete entries={deleteEntries} onClose={cancelDelete} onConfirm={confirmDelete} />
    </Portal>
  {/if}

  {#if moveOpen}
    <Portal>
      <ConfirmMove entries={$cutEntries} onClose={() => (moveOpen = false)} onConfirm={confirmMove} />
    </Portal>
  {/if}

  {#if dropChoiceOpen && pendingDrop}
  <Portal>
    <ConfirmDropMode
      entries={pendingDrop.items}
      dest={pendingDrop.dest}
      onClose={() => (dropChoiceOpen = false)}
      onChoose={(mode) => { executeDrop(pendingDrop!.items, pendingDrop!.dest, mode); dropChoiceOpen = false; }}
    />
  </Portal>
  {/if}

  {#if compressOpen}
    <Portal>
      <ConfirmCompress
        entries={selectionEntries()}
        onClose={cancelCompress}
        onConfirm={confirmCompress}
      />
    </Portal>
  {/if}

  {#if decompressOpen}
    <Portal>
      <ConfirmDecompress
        entries={decompressEntries}
        onClose={cancelDecompress}
        onConfirm={confirmDecompress}
      />
    </Portal>
  {/if}

  <ContextMenu
    open={menuOpen}
    x={menuX}
    y={menuY}
    kind={menuKind}
    selection={menuSelection}
    contextPath={menuContextPath}
    canPaste={$clipboard.length > 0}
    canSelectAll={entries.length > 0}
    canGoUp={!isAtRoot}
    onClose={closeContextMenu}
    onAction={onContextAction}
  />
</div>

<style>
  .window {
    position: absolute;
    display: flex;
    flex-direction: column;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 14px;
    box-shadow: var(--shadow-window);
    overflow: hidden;
    min-width: 360px;
    min-height: 260px;
  }
  .window.minimized {
    display: none;
  }
  .header {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 48px;
    padding: 0 10px 0 12px;
    background: var(--surface-soft);
    border-bottom: 1px solid var(--border);
    color: var(--text);
    cursor: grab;
    flex: none;
  }
  .header:active {
    cursor: grabbing;
  }
   .logo {
     display: grid;
     place-items: center;
     width: 36px;
     height: 36px;
     border-radius: 10px;
     background: linear-gradient(135deg, var(--accent), var(--accent-2));
     color: #fff;
   }
   .logo img {
     width: 27px;
     height: 27px;
   }
.title {
      font-size: 14px;
      font-weight: 600;
    }
    .window-controls {
      display: flex;
      align-items: center;
      gap: 6px;
      margin-left: auto;
    }
    .minimize {
      display: grid;
      place-items: center;
      width: 30px;
      height: 30px;
      background: transparent;
      border: none;
      border-radius: 8px;
      color: var(--text-muted);
      cursor: pointer;
    }
    .minimize:hover {
      background: var(--accent);
      color: #fff;
    }
    .minimize svg {
      width: 14px;
      height: 14px;
    }
    .close {
      display: grid;
      place-items: center;
      width: 30px;
      height: 30px;
      background: transparent;
      border: none;
      border-radius: 8px;
      color: var(--text-muted);
      cursor: pointer;
    }
  .close:hover {
    background: var(--alert);
    color: #fff;
  }
  .close svg {
    width: 14px;
    height: 14px;
  }
  .toolbar {
    display: flex;
    align-items: center;
    height: 44px;
    padding: 0 11px;
    background: var(--surface-soft);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .up {
    display: grid;
    place-items: center;
    height: 33px;
    width: 33px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .up:hover {
    background: var(--surface-inset);
    color: var(--text);
  }
  .up:focus-visible,
  .tool:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: -2px;
  }
  .up svg {
    width: 17.6px;
    height: 17.6px;
  }
  .tool {
    display: grid;
    place-items: center;
    height: 33px;
    width: 33px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
  }
  .tool:hover:not(:disabled) {
    background: var(--surface-inset);
    color: var(--text);
  }
  .tool:disabled {
    opacity: 0.4;
    cursor: default;
  }
  .tool svg {
    width: 17.6px;
    height: 17.6px;
  }
  .tool.cut.cut-active {
    color: var(--accent-strong);
    background: var(--surface-inset);
  }
  .tool.info {
    position: relative;
  }
  .badge {
    position: absolute;
    top: 2px;
    right: 2px;
    min-width: 15px;
    height: 15px;
    padding: 0 4px;
    display: grid;
    place-items: center;
    font-size: 10px;
    font-weight: 700;
    line-height: 1;
    color: #fff;
    background: var(--alert);
    border-radius: 9px;
  }
  .new-input-form {
    display: grid;
    place-items: center;
    width: 100%;
    padding: 6px 10px;
  }
  .new-folder-row,
  .renaming-row {
    background: var(--surface-soft);
  }
  .new-folder-row td,
  .renaming-row td {
    padding: 4px 10px;
  }
  .new-input {
    width: 100%;
    max-width: 320px;
    height: 28px;
    padding: 0 8px;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--accent-strong);
    border-radius: 7px;
    outline: none;
  }
  .body {
    flex: 1;
    display: flex;
    min-height: 0;
  }
  .tree {
    flex: none;
    width: 300px;
    border-right: 1px solid var(--border);
    background: var(--surface-soft);
    overflow: auto;
    padding: 8px 0;
  }
  .tree-resize {
    flex: none;
    width: 8px;
    margin-left: -4px;
    z-index: 5;
    cursor: ew-resize;
    display: flex;
    align-items: center;
    justify-content: center;
    background: transparent;
    border: none;
  }
  .tree-resize::after {
    content: '';
    width: 2px;
    height: 32px;
    border-radius: 2px;
    background: var(--border);
    transition: background 120ms ease, height 120ms ease;
  }
  .tree-resize:hover,
  .tree-resize:active {
    background: var(--surface-inset);
  }
  .tree-resize:hover::after,
  .tree-resize:active::after,
  .tree-resize:focus-visible::after {
    background: var(--accent-strong);
    height: 44px;
  }
  .tree-resize:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: -3px;
    border-radius: 2px;
  }
  .tree-root {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  /* Children live in a real nested list with no padding of its own; the
     indentation is applied to the row button instead. Nesting padded <li>s
     directly shrank every level (299 → 291 → … → 71px), so deep rows were only
     clickable across a narrow sliver and their hover/active highlight stopped
     early. */
  .tchildren {
    list-style: none;
    margin: 0;
    padding: 0;
  }
  .trow {
    margin: 0;
  }
  .tnode {
    display: flex;
    align-items: center;
    gap: 7px;
    width: 100%;
    background: transparent;
    border: none;
    color: var(--text);
    font: inherit;
    font-size: 13px;
    padding: 6px 10px;
    cursor: pointer;
    text-align: left;
    border-radius: 6px;
    transition: background 100ms ease;
  }
  .tnode:hover {
    background: var(--surface-inset);
  }
  .tnode:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: -2px;
  }
  .trow.active > .tnode {
    color: var(--accent-strong);
    font-weight: 700;
    background: var(--surface-inset);
    border-radius: 6px;
    box-shadow: inset 3px 0 0 var(--accent-strong);
  }
  .twisty {
    display: grid;
    place-items: center;
    flex: none;
    width: 26px;
    height: 24px;
    margin-top: -1px;
    font-size: 14.3px;
    line-height: 1;
    font-weight: 700;
    color: var(--text-muted);
    border-radius: 6px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
  }
  .twisty:hover {
    color: var(--text);
    background: var(--surface-inset);
  }
  .twisty:active {
    background: var(--border);
  }
   .ticon {
     flex: none;
     color: var(--accent-strong);
   }
   .tname {
    min-width: 60px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .tchild {
    font-size: 12px;
    padding: 4px 10px 4px 20px;
    white-space: nowrap;
  }
  .t-loading {
    color: var(--text-muted);
  }
  .t-error {
    color: var(--alert);
  }
  .content {
    flex: 1;
    min-width: 0;
    overflow: auto;
    position: relative;
  }
  .empty,
  .error {
    position: absolute;
    inset: 0;
    display: grid;
    place-items: center;
    color: var(--text-faint);
    font-size: 13px;
  }
  .error {
    color: var(--alert);
    padding: 14px;
    text-align: center;
  }
  .list {
    border-collapse: collapse;
    width: 100%;
    font-size: 13px;
  }
  thead th {
    position: sticky;
    top: 0;
    background: var(--surface-soft);
    border-bottom: 1px solid var(--border);
    padding: 0 8px;
    height: 30px;
    text-align: left;
    font-weight: 600;
    color: var(--text-muted);
    user-select: none;
    cursor: pointer;
    white-space: nowrap;
  }
  thead th.active {
    color: var(--accent-strong);
  }
  .sort-arrow {
    margin-left: 4px;
    font-size: 10px;
    color: var(--accent-strong);
  }
  .row {
    cursor: default;
  }
  .row td {
    padding: 7px 8px;
    border-bottom: 1px solid var(--surface-inset);
  }
  .row:hover td {
    background: var(--surface-soft);
  }
  .row.selected td {
    background: var(--accent-strong);
    color: #fff;
  }
  .row.selected .rname,
  .row.selected .size,
  .row.selected .date {
    color: #fff;
  }
  .name {
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
  }
  .fg {
    flex: none;
  }
  .rname {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .row.dir:not(.selected) .rname {
    color: var(--accent-strong);
    font-weight: 600;
  }
  .size {
    width: 1%;
    white-space: nowrap;
    text-align: right;
    color: var(--text-muted);
    min-width: 76px;
  }
  .date {
    width: 1%;
    white-space: nowrap;
    text-align: right;
    color: var(--text-muted);
    min-width: 156px;
  }
  .status {
    flex: none;
    display: flex;
    align-items: center;
    gap: 8px;
    height: 30px;
    padding: 0 12px;
    background: var(--surface-inset);
    border-top: 1px solid var(--border);
    color: var(--text-muted);
    font-size: 12px;
  }
  .spath {
    display: grid;
    place-items: center;
    color: var(--accent-strong);
  }
  .spath svg {
    width: 14px;
    height: 14px;
  }
  .spath-val {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 12px;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    flex: 1;
  }
  .copy-path {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
    flex: none;
  }
  .copy-path:hover {
    background: var(--surface);
    color: var(--accent-strong);
  }
  .copy-path:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: 2px;
  }
  .count {
    margin-left: auto;
    white-space: nowrap;
  }
  .upload-menu-wrap {
    position: relative;
  }
  .upload-menu {
    position: absolute;
    top: calc(100% + 4px);
    left: 0;
    z-index: 20;
    display: flex;
    flex-direction: column;
    min-width: 172px;
    padding: 4px;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    box-shadow: var(--shadow-window);
  }
  .upload-menu-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 8px 10px;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--text);
    font: inherit;
    font-size: 12px;
    cursor: pointer;
    white-space: nowrap;
  }
  .upload-menu-item:hover {
    background: var(--surface-soft);
  }
  .status-msg {
    margin-left: 8px;
    color: var(--accent-strong);
    font-size: 12px;
    white-space: nowrap;
  }
  .resize {
    position: absolute;
    right: 0;
    bottom: 0;
    width: 16px;
    height: 16px;
    cursor: nwse-resize;
  }
  .resize::after {
    content: '';
    position: absolute;
    right: 2px;
    bottom: 2px;
    width: 8px;
    height: 8px;
    border-right: 2px solid var(--text-faint);
    border-bottom: 2px solid var(--text-faint);
    border-radius: 0 0 3px 0;
    }
</style>
