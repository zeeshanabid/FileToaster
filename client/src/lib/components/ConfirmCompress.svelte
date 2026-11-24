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
    onConfirm: (format: string, compressionLevel: number) => void;
  } = $props();

  let selectedCount = $derived(entries.length);
  let singular = $derived(selectedCount === 1 ? '' : 's');

  // The archive format. The three the server accepts are presented explicitly;
  // ZIP defaults as the common case.
  const formats = [
    { value: 'zip', label: 'ZIP archive (.zip)' },
    { value: 'targz', label: 'GNU tar archive, gzip (.tar.gz)' },
    { value: 'tar', label: 'GNU tar archive (.tar)' }
  ] as const;
  let format = $state<'zip' | 'targz' | 'tar'>('zip');
  let compressionLevel = $state(6); // default: balanced (0-9)

  // Progress percentage for the slider filled portion (0-9 maps to 0-100%)
  let compressionProgress = $derived((compressionLevel / 9) * 100);

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

  // Whether the current format supports compression level (zip, targz)
// tar format shows the slider but disabled since it has no compression
  let isSliderDisabled = $derived(format === 'tar');
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="overlay" role="presentation" onmousedown={onBackdrop} onkeydown={onKey} tabindex="-1">
  <div
    class="dialog"
    role="dialog"
    aria-modal="true"
    aria-label="Compress"
    data-testid="compress-dialog"
    bind:this={dialog}
    tabindex="0"
  >
    <header class="dlg-header">
      <span class="dlg-title">Compress</span>
      <button class="dlg-close" type="button" aria-label="Close" onclick={() => onClose()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </header>

    <p class="warning">
      Create a {format === 'zip' ? '.zip' : format === 'targz' ? '.tar.gz' : '.tar'} archive of {selectedCount} file{singular} in the current folder.
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

    <fieldset class="formats">
      <legend class="formats-legend">Format</legend>
      {#each formats as f (f.value)}
        <label class="format-row">
          <input type="radio" bind:group={format} value={f.value} />
          <span>{f.label}</span>
        </label>
      {/each}
    </fieldset>

    <fieldset class="compression">
      <legend class="compression-legend">Compression level</legend>
      <div class="compression-slider" class:disabled={isSliderDisabled}>
        <span class="compression-label">Fast</span>
        <input
          type="range"
          min="0"
          max="9"
          bind:value={compressionLevel}
          class="compression-range"
          aria-label="Compression level"
          style="--progress: {compressionProgress}%"
          disabled={isSliderDisabled}
        />
        <span class="compression-label">Best</span>
      </div>
    </fieldset>

    <footer class="dlg-footer">
      <button class="dlg-cancel" type="button" onclick={() => onClose()}>Cancel</button>
      <button class="dlg-compress" type="button" disabled={selectedCount === 0} onclick={() => onConfirm(format, compressionLevel)}>Compress</button>
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
  .formats {
    margin: 0;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .formats-legend {
    padding: 0 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: capitalize;
  }
  .format-row {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-top: 8px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .compression {
    margin: 0;
    padding: 12px 16px;
    border-bottom: 1px solid var(--border);
  }
  .compression-legend {
    padding: 0 6px;
    font-size: 12px;
    font-weight: 600;
    color: var(--text-muted);
    text-transform: capitalize;
  }
  .compression-slider {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 8px;
  }
  .compression-label {
    font-size: 12px;
    color: var(--text-muted);
    white-space: nowrap;
  }
  .compression-range {
    flex: 1;
    height: 6px;
    -webkit-appearance: none;
    appearance: none;
    background: var(--border);
    border-radius: 3px;
    outline: none;
    cursor: pointer;
    /* Filled portion (progress) - using CSS custom property */
    background: linear-gradient(var(--accent), var(--accent)) 0 0 / var(--progress, 0%) 100% no-repeat var(--border);
  }
  .compression-range::-webkit-slider-runnable-track {
    height: 6px;
    background: transparent;
    border-radius: 3px;
    border: none;
  }
  .compression-range::-webkit-slider-thumb {
    -webkit-appearance: none;
    appearance: none;
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: var(--accent);
    border: 2px solid var(--surface);
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
    cursor: pointer;
    transition: transform 0.1s ease, box-shadow 0.1s ease, background-color 0.15s ease;
    /* Extend the filled portion visually */
    margin-top: -6px;
  }
  .compression-range::-webkit-slider-thumb:hover {
    transform: scale(1.15);
    box-shadow: 0 3px 8px rgba(var(--accent-rgb), 0.4);
    background: var(--accent-strong);
  }
  .compression-range::-moz-range-thumb {
    width: 18px;
    height: 18px;
    border-radius: 50%;
    background: var(--accent);
    border: 2px solid var(--surface);
    box-shadow: 0 2px 4px rgba(0, 0, 0, 0.2);
    cursor: pointer;
    transition: transform 0.1s ease, box-shadow 0.1s ease, background-color 0.15s ease;
  }
  .compression-range::-moz-range-thumb:hover {
    transform: scale(1.15);
    box-shadow: 0 3px 8px rgba(var(--accent-rgb), 0.4);
    background: var(--accent-strong);
  }
  .compression-range::-moz-range-track {
    height: 6px;
    background: var(--border);
    border-radius: 3px;
    border: none;
  }
  .compression-range::-moz-range-progress {
    height: 6px;
    background: var(--accent);
    border-radius: 3px;
    border: none;
  }
  .compression-range:focus-visible::-webkit-slider-thumb {
    box-shadow: 0 0 0 3px rgba(var(--accent-rgb), 0.3), 0 2px 4px rgba(0, 0, 0, 0.2);
  }
  .compression-range:focus-visible::-moz-range-thumb {
    box-shadow: 0 0 0 3px rgba(var(--accent-rgb), 0.3), 0 2px 4px rgba(0, 0, 0, 0.2);
  }
  .compression-range:disabled {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .compression-range:disabled::-webkit-slider-thumb {
    background: var(--text-faint);
    box-shadow: none;
    transform: none;
  }
  .compression-range:disabled::-moz-range-thumb {
    background: var(--text-faint);
    box-shadow: none;
    transform: none;
  }
  .compression-range:disabled::-moz-range-progress {
    background: var(--border);
  }
  .compression-slider.disabled .compression-label {
    color: var(--text-faint);
  }
  .compression-slider.disabled .compression-range {
    opacity: 0.5;
    cursor: not-allowed;
  }
  .compression-slider.disabled .compression-range::-webkit-slider-thumb {
    background: var(--text-faint);
    box-shadow: none;
    transform: none;
  }
  .compression-slider.disabled .compression-range::-moz-range-thumb {
    background: var(--text-faint);
    box-shadow: none;
    transform: none;
  }
  .compression-slider.disabled .compression-range::-moz-range-progress {
    background: var(--border);
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
  .dlg-compress {
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
  .dlg-compress:hover {
    background: var(--accent-strong);
    border-color: var(--accent-strong);
    box-shadow: 0 2px 8px rgba(var(--accent-rgb), 0.3);
  }
  .dlg-compress:active {
    transform: scale(0.98);
  }
  .dlg-compress:disabled {
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
