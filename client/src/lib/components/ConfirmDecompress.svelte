<script lang="ts">
  import { onMount } from 'svelte';
  import FileIcon from './FileIcon.svelte';
  import { archiveStem } from '$lib/fs/archive';

  let {
    entries,
    onClose,
    onConfirm
  }: {
    entries: Array<{ path: string; name: string; type: 'file' | 'dir' }>;
    onClose: () => void;
    onConfirm: (choice: { into: string }) => void;
  } = $props();

  let selectedCount = $derived(entries.length);
  let singular = $derived(selectedCount === 1 ? '' : 's');

  // The extraction destination: "" extracts here; the archive stem extracts
  // into a sibling folder named after the archive. "Extract here" is default.
  let into = $state('');

  // The single archive being extracted and the folder "Extract in …" uses: its
  // stem (myfiles.zip → myfiles).
  const entry = $derived(entries[0] ?? null);
  const name = $derived(entry?.name ?? '');
  const stem = $derived(entry ? archiveStem(entry.name) : name);

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
    aria-label="Decompress"
    data-testid="decompress-dialog"
    bind:this={dialog}
    tabindex="0"
  >
    <header class="dlg-header">
      <span class="dlg-title">Decompress</span>
      <button class="dlg-close" type="button" aria-label="Close" onclick={() => onClose()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </header>

    <p class="warning">
      Extract {selectedCount} file{singular} into the current folder: straight here or into a folder named “{stem}”.
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
                    <FileIcon type={item.type === 'dir' ? 'folder' : 'sheet'} />
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

    <fieldset class="options">
      <legend class="options-legend">Destination</legend>
      <label class="option-row">
        <input type="radio" name="into" value="" bind:group={into} />
        <span>Extract here</span>
      </label>
      <label class="option-row">
        <input type="radio" name="into" value={stem} bind:group={into} />
        <span>Extract in “{stem}”</span>
      </label>
    </fieldset>

    <footer class="dlg-footer">
      <button class="dlg-cancel" type="button" onclick={() => onClose()}>Cancel</button>
      <button class="dlg-decompress" type="button" disabled={selectedCount === 0} onclick={() => onConfirm({ into })}>Extract</button>
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
  .options {
    margin: 0;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .options-legend {
    padding: 0 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
  }
  .option-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
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
    color: #0f172a;
    background: #f1f5f9;
    border: 1px solid #94a3b8;
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-cancel:hover {
    color: #0f172a;
    border-color: #818cf8;
    background: #e2e8f0;
    box-shadow: 0 1px 4px rgba(99, 102, 241, 0.15);
  }
  .dlg-cancel:active {
    transform: scale(0.98);
    background: #cbd5e1;
  }
  .dlg-decompress {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    color: #fff;
    background: var(--accent);
    border: 1px solid var(--accent);
    border-radius: 8px;
    cursor: pointer;
    transition: background-color 0.15s ease, border-color 0.15s ease, box-shadow 0.15s ease;
  }
  .dlg-decompress:hover {
    background: var(--accent-strong);
    border-color: var(--accent-strong);
    box-shadow: 0 2px 8px rgba(var(--accent-rgb), 0.3);
  }
  .dlg-decompress:active {
    transform: scale(0.98);
  }
  .dlg-decompress:disabled {
    opacity: 0.5;
    cursor: not-allowed;
    transform: none;
  }
  @media (max-width: 560px) {
    .warning {
      font-size: 13px;
    }
  }
</style>
