<script lang="ts">
  import { onMount } from 'svelte';
  import FileIcon from './FileIcon.svelte';
  import type { FileInfo } from '$lib/fs/api';
  import { formatSize, formatDate } from '$lib/fs/format';

  let { items, onClose }: { items: FileInfo[]; onClose: () => void } = $props();

  let totalSize = $derived(items.reduce((sum, it) => sum + it.size, 0));
  let rootFiles = $derived(items.filter((it) => it.type === 'file').length);
  let rootDirs = $derived(items.filter((it) => it.type === 'dir').length);
  let totalFiles = $derived(items.reduce((sum, it) => sum + it.fileCount, 0));
  let totalDirs = $derived(items.reduce((sum, it) => sum + it.dirCount, 0));

  // Return focus to the dialog on open so keyboard users stay trapped until
  // they close it.
  let dialog: HTMLDivElement | null = $state(null);
  onMount(() => dialog?.focus());

  function onBackdrop(e: MouseEvent) {
    if (e.target === e.currentTarget) onClose();
  }
  function onKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="overlay" role="presentation" onmousedown={onBackdrop} onkeydown={onKey} tabindex="-1">
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-label="Information"
    data-testid="info-dialog"
    bind:this={dialog}
    tabindex="0"
  >
    <header class="dlg-header">
      <span class="dlg-title">Information</span>
      <button class="dlg-close" type="button" aria-label="Close" onclick={() => onClose()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </header>

    <div class="summary">
      <div class="stat">
        <span class="stat-val">{items.length}</span>
        <span class="stat-label">Selected ({rootFiles} files, {rootDirs} directories)</span>
      </div>
      <div class="stat">
        <span class="stat-val">{totalFiles}</span>
        <span class="stat-label">Total files</span>
      </div>
      <div class="stat">
        <span class="stat-val">{totalDirs}</span>
        <span class="stat-label">Total directories</span>
      </div>
      <div class="stat">
        <span class="stat-val">{formatSize(totalSize)}</span>
        <span class="stat-label">Total size</span>
      </div>
    </div>

    <div class="items">
      {#if items.length === 0}
        <div class="empty">No information available.</div>
      {:else}
        <table class="list">
          <thead>
            <tr>
              <th>Name</th>
              <th>Size</th>
              <th>Permissions</th>
              <th>Created</th>
              <th>Updated</th>
            </tr>
          </thead>
          <tbody>
            {#each items as item (item.path)}
              <tr class="row">
                <td class="name">
                  <span class="fg" aria-hidden="true">
                    <FileIcon type={item.type === 'dir' ? 'folder' : 'doc'} />
                  </span>
                  <span class="rname" title={item.path}>{item.name}</span>
                </td>
                <td class="size">{formatSize(item.size)}</td>
                <td class="perms" title={item.permsOctal}>
                  <span class="perm">{item.perms}</span>
                  <span class="perm-oct">{item.permsOctal}</span>
                </td>
                <td class="date">{formatDate(item.created)}</td>
                <td class="date">{formatDate(item.modified)}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>

    <footer class="dlg-footer">
      <button class="dlg-ok" type="button" onclick={() => onClose()}>Close</button>
    </footer>
  </div>
</div>

<style>
  .overlay {
    position: fixed;
    inset: 0;
    display: grid;
    place-items: center;
    background: rgba(11, 17, 32, 0.5);
    backdrop-filter: blur(2px);
    z-index: 1000;
    padding: 24px;
  }
  .dialog {
    display: flex;
    flex-direction: column;
    width: 100%;
    max-width: 720px;
    max-height: 100%;
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 14px;
    box-shadow: var(--shadow-popover);
    overflow: hidden;
    outline: none;
  }
  .dlg-header {
    display: flex;
    align-items: center;
    gap: 10px;
    height: 48px;
    padding: 0 12px;
    background: var(--surface-soft);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .dlg-title {
    font-size: 14px;
    font-weight: 600;
    color: var(--text);
  }
  .dlg-close {
    display: grid;
    place-items: center;
    margin-left: auto;
    width: 30px;
    height: 30px;
    background: transparent;
    border: none;
    border-radius: 8px;
    color: var(--text-muted);
    cursor: pointer;
  }
  .dlg-close:hover {
    background: var(--alert);
    color: #fff;
  }
  .dlg-close svg {
    width: 14px;
    height: 14px;
  }
  .summary {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 1px;
    background: var(--border);
    border-bottom: 1px solid var(--border);
    flex: none;
  }
  .stat {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    padding: 12px 8px;
    background: var(--surface);
    text-align: center;
  }
  .stat-val {
    font-size: 18px;
    font-weight: 700;
    color: var(--accent-strong);
  }
  .stat-label {
    font-size: 11px;
    color: var(--text-muted);
    line-height: 1.2;
  }
  .items {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }
  .empty {
    display: grid;
    place-items: center;
    height: 120px;
    color: var(--text-faint);
    font-size: 13px;
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
    padding: 6px 10px;
    text-align: left;
    font-weight: 600;
    color: var(--text-muted);
    white-space: nowrap;
  }
  thead th:first-child {
    border-top-left-radius: 8px;
  }
  thead th:last-child {
    border-top-right-radius: 8px;
  }
  .row td {
    padding: 7px 10px;
    border-bottom: 1px solid var(--surface-inset);
    vertical-align: middle;
  }
  .row:hover td {
    background: var(--surface-soft);
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
  .size,
  .date {
    width: 1%;
    white-space: nowrap;
    text-align: right;
    color: var(--text-muted);
  }
  .date {
    min-width: 156px;
  }
  .perms {
    text-align: left;
    min-width: 120px;
  }
  .perm {
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    color: var(--text);
  }
  .perm-oct {
    display: block;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 11px;
    color: var(--text-faint);
  }
  .dlg-footer {
    display: flex;
    justify-content: flex-end;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
    flex: none;
  }
  .dlg-ok {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: #fff;
    background: var(--accent-strong);
    border: 1px solid var(--accent-strong);
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-ok:hover {
    background: var(--accent);
    border-color: var(--accent);
    box-shadow: 0 2px 8px rgba(var(--accent-rgb), 0.3);
  }
  .dlg-ok:active {
    transform: scale(0.98);
  }
  @media (max-width: 560px) {
    .summary {
      grid-template-columns: repeat(2, 1fr);
    }
  }
</style>
