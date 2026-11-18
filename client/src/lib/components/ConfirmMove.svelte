<script lang="ts">
  import { onMount } from 'svelte';
  import FileIcon from './FileIcon.svelte';

  let {
    entries,
    onClose,
    onConfirm
  }: {
    entries: Array<{ path: string; name: string; type: 'file' | 'dir' }>;
    onClose: () => void;
    onConfirm: () => void;
  } = $props();

  let selectedCount = $derived(entries.length);
  let singular = $derived(selectedCount === 1 ? '' : 's');

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
    aria-label="Move"
    data-testid="move-dialog"
    bind:this={dialog}
    tabindex="0"
  >
    <header class="dlg-header">
      <span class="dlg-title">Move</span>
      <button class="dlg-close" type="button" aria-label="Close" onclick={() => onClose()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </header>

    <p class="warning">
      Are you sure you want to move {selectedCount} file{singular} and directories?
    </p>

    <p class="notice">
      If an item with the same name already exists at the destination, it will be overwritten and its current contents replaced.
    </p>

    <div class="items">
      {#if entries.length === 0}
        <div class="empty">Nothing selected.</div>
      {:else}
        <table class="list">
          <thead>
            <tr>
              <th>Name</th>
              <th>Type</th>
            </tr>
          </thead>
          <tbody>
            {#each entries as item (item.path)}
              <tr class="row">
                <td class="name">
                  <span class="fg" aria-hidden="true">
                    <FileIcon type={item.type === 'dir' ? 'folder' : 'doc'} />
                  </span>
                  <span class="rname" title={item.path}>{item.name}</span>
                </td>
                <td class="kind">{item.type === 'dir' ? 'directory' : 'file'}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {/if}
    </div>

    <footer class="dlg-footer">
      <button class="dlg-cancel" type="button" onclick={() => onClose()}>Cancel</button>
      <button class="dlg-move" type="button" disabled={selectedCount === 0} onclick={() => onConfirm()}>Move</button>
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
    max-width: 560px;
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
  .warning {
    flex: none;
    margin: 0;
    padding: 14px 16px;
    font-size: 14px;
    line-height: 1.5;
    color: var(--text);
    border-bottom: 1px solid var(--border);
  }
  .notice {
    flex: none;
    margin: 0;
    padding: 10px 16px;
    font-size: 12px;
    line-height: 1.45;
    color: var(--text-muted);
    background: var(--surface-inset);
    border-bottom: 1px solid var(--border);
  }
  .items {
    flex: 1;
    min-height: 0;
    overflow: auto;
  }
  .empty {
    display: grid;
    place-items: center;
    height: 100px;
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
    padding: 6px 12px;
    text-align: left;
    font-weight: 600;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .row td {
    padding: 7px 12px;
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
  .kind {
    width: 1%;
    white-space: nowrap;
    text-align: right;
    color: var(--text-muted);
    text-transform: capitalize;
  }
  .dlg-footer {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    padding: 10px 12px;
    border-top: 1px solid var(--border);
    background: var(--surface-soft);
    flex: none;
  }
  .dlg-cancel {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-cancel:hover {
    color: var(--accent-strong);
    border-color: var(--accent-strong);
    background: var(--surface-soft);
    box-shadow: 0 2px 8px rgba(0, 0, 0, 0.15);
  }
  .dlg-cancel:active {
    transform: scale(0.98);
  }
  .dlg-move {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: #fff;
    background: var(--alert);
    border: 1px solid var(--alert);
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-move:hover {
    background: var(--alert-strong);
    border-color: var(--alert-strong);
    box-shadow: 0 2px 8px rgba(220, 53, 69, 0.3);
  }
  .dlg-move:active {
    transform: scale(0.98);
  }
  .dlg-move:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    transform: none;
  }
  @media (max-width: 560px) {
    .warning,
    .notice {
      font-size: 13px;
    }
  }
</style>
