<script lang="ts">
  import Portal from './Portal.svelte';

  // Preset sizes in bytes
  const PRESET_SIZES: { label: string; value: number }[] = [
    { label: '0 MB', value: 0 },
    { label: '4 MB', value: 4 * 1024 * 1024 },
    { label: '8 MB', value: 8 * 1024 * 1024 },
    { label: '16 MB', value: 16 * 1024 * 1024 },
    { label: '32 MB', value: 32 * 1024 * 1024 },
    { label: '64 MB', value: 64 * 1024 * 1024 },
    { label: '256 MB', value: 256 * 1024 * 1024 },
    { label: '512 MB', value: 512 * 1024 * 1024 },
    { label: '1 GB', value: 1024 * 1024 * 1024 }
  ];

  let {
    fileCount = 0,
    onConfirm,
    onCancel
  }: {
    fileCount: number;
    onConfirm: (size: number) => void;
    onCancel: () => void;
  } = $props();

  let selectedSize = $state<string>('0 MB');
  let customSizeMB = $state<string>('0');
  let customSizeError = $state<string | null>(null);
  let customInputEl: HTMLInputElement | null = $state(null);

  function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 MB';
    const mb = bytes / (1024 * 1024);
    if (mb >= 1024) {
      return `${(mb / 1024).toFixed(mb % 1024 === 0 ? 0 : 1)} GB`;
    }
    return `${mb.toFixed(mb % 1 === 0 ? 0 : 1)} MB`;
  }

  function close() {
    onCancel();
  }

  function handleConfirm() {
    let sizeBytes: number;
    if (selectedSize === 'custom') {
      const mb = parseFloat(customSizeMB);
      if (isNaN(mb) || mb < 0) {
        customSizeError = 'Please enter a valid size in MB (non-negative number)';
        return;
      }
      sizeBytes = Math.round(mb * 1024 * 1024);
    } else {
      const preset = PRESET_SIZES.find(p => p.label === selectedSize);
      sizeBytes = preset?.value ?? 0;
    }
    customSizeError = null;
    onConfirm(sizeBytes);
  }

  function onModalKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      handleConfirm();
    } else if (e.key === 'Escape') {
      close();
    }
  }

  function onCustomSelectChange() {
    if (selectedSize === 'custom') {
      // Focus the custom input when custom is selected
      setTimeout(() => customInputEl?.focus(), 0);
    }
  }

  // Return focus to the dialog on open so keyboard users stay trapped until
  // they close it.
  let dialog: HTMLDivElement | null = $state(null);
  import { onMount } from 'svelte';
  onMount(() => dialog?.focus());
</script>

<div
  class="modal-backdrop"
  role="button"
  tabindex="0"
  aria-label="Close modal"
  onclick={close}
  onkeydown={(e) => e.key === 'Escape' && close()}
>
  <div
      class="modal"
      role="dialog"
      aria-modal="true"
      aria-labelledby="truncate-title"
      tabindex="-1"
      onclick={(e) => e.stopPropagation()}
      onkeydown={(e) => {}}
      data-testid="truncate-dialog"
      bind:this={dialog}
    >
      <header class="modal-header">
        <h2 id="truncate-title">Truncate Files</h2>
        <button class="modal-close" type="button" aria-label="Close" onclick={(e) => { e.stopPropagation(); close(); }}>
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="18" height="18" aria-hidden="true"><path d="M7 7l10 10M17 7L7 17" /></svg>
        </button>
      </header>
      <div class="modal-body">
        <label for="truncate-size-select" class="modal-label">
          Target size for {fileCount} file{fileCount !== 1 ? 's' : ''}:
        </label>
        <select
          id="truncate-size-select"
          class="modal-select"
          bind:value={selectedSize}
          onchange={onCustomSelectChange}
        >
          {#each PRESET_SIZES as preset}
            <option value={preset.label}>{preset.label}</option>
          {/each}
          <option value="custom">Custom size (MB)...</option>
        </select>
        {#if selectedSize === 'custom'}
          <div class="custom-size-input">
            <label for="custom-size-input" class="modal-label">Enter size in MB:</label>
            <input
              id="custom-size-input"
              class="modal-input"
              bind:this={customInputEl}
              bind:value={customSizeMB}
              type="number"
              step="1"
              min="0"
              placeholder="e.g., 100"
              onkeydown={onModalKeydown}
            />
            {#if customSizeError}
              <div class="modal-error">{customSizeError}</div>
            {/if}
          </div>
        {/if}
        <div class="modal-info">
          <strong>Note:</strong> This operation will truncate the selected files to the specified size.
          If the target size is larger than the current file size, the file will be extended with zeros.
          If smaller, the file will be truncated. This operation only works on files, not directories.
        </div>
      </div>
      <footer class="modal-footer">
        <button class="modal-btn cancel" type="button" onclick={(e) => { e.stopPropagation(); close(); }}>Cancel</button>
        <button class="modal-btn confirm danger" type="button" onclick={handleConfirm}>Truncate</button>
      </footer>
    </div>
  </div>

<style>
  .modal-backdrop {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.5);
    backdrop-filter: blur(4px);
    -webkit-backdrop-filter: blur(4px);
    z-index: 100;
    display: flex;
    align-items: center;
    justify-content: center;
    animation: fadeIn 120ms ease;
  }
  @keyframes fadeIn {
    from { opacity: 0; }
    to { opacity: 1; }
  }
  .modal {
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: var(--shadow-window);
    width: 100%;
    max-width: 480px;
    margin: 24px;
    overflow: hidden;
    animation: slideUp 150ms ease;
  }
  @keyframes slideUp {
    from { transform: translateY(16px); opacity: 0; }
    to { transform: translateY(0); opacity: 1; }
  }
  .modal-header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 16px 20px;
    border-bottom: 1px solid var(--border);
  }
  .modal-header h2 {
    margin: 0;
    font-size: 15px;
    font-weight: 600;
    color: var(--text);
  }
  .modal-close {
    display: grid;
    place-items: center;
    width: 28px;
    height: 28px;
    background: transparent;
    border: none;
    border-radius: 6px;
    color: var(--text-muted);
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
  }
  .modal-close:hover {
    background: var(--surface-soft);
    color: var(--text);
  }
  .modal-body {
    padding: 20px;
    gap: 16px;
    display: flex;
    flex-direction: column;
  }
  .modal-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-muted);
  }
  .modal-select {
    width: 100%;
    height: 36px;
    padding: 0 12px;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    background: var(--surface-inset);
    border: 1px solid var(--border);
    border-radius: 8px;
    outline: none;
    transition: border-color 100ms ease, box-shadow 100ms ease;
    appearance: none;
    background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='%23888' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M6 9l6 6 6-6'/%3E%3C/svg%3E");
    background-repeat: no-repeat;
    background-position: right 12px center;
    padding-right: 40px;
  }
  .modal-select:focus {
    border-color: var(--accent-strong);
    box-shadow: 0 0 0 3px var(--accent-strong-transparent);
  }
  .custom-size-input {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding-top: 8px;
  }
  .modal-input {
    width: 100%;
    height: 36px;
    padding: 0 12px;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    background: var(--surface-inset);
    border: 1px solid var(--border);
    border-radius: 8px;
    outline: none;
    transition: border-color 100ms ease, box-shadow 100ms ease;
  }
  .modal-input:focus {
    border-color: var(--accent-strong);
    box-shadow: 0 0 0 3px var(--accent-strong-transparent);
  }
  .modal-error {
    color: var(--alert);
    font-size: 12px;
    margin-top: -8px;
  }
  .modal-info {
    font-size: 12px;
    color: var(--text-muted);
    line-height: 1.5;
    padding: 12px;
    background: var(--surface-soft);
    border-radius: 8px;
    border: 1px solid var(--border);
  }
  .modal-info strong {
    color: var(--text);
  }
  .modal-footer {
    display: flex;
    justify-content: flex-end;
    gap: 12px;
    padding: 16px 20px;
    border-top: 1px solid var(--border);
  }
  .modal-btn {
    height: 32px;
    padding: 0 16px;
    font: inherit;
    font-size: 13px;
    font-weight: 500;
    border-radius: 8px;
    cursor: pointer;
    transition: background 100ms ease, color 100ms ease;
  }
  .modal-btn.cancel {
    background: #f1f5f9;
    border: 1px solid #94a3b8;
    color: #0f172a;
  }
  .modal-btn.cancel:hover {
    background: #e2e8f0;
    border-color: #818cf8;
    color: #0f172a;
    box-shadow: 0 1px 4px rgba(99, 102, 241, 0.15);
  }
  .modal-btn.cancel:active {
    transform: scale(0.98);
    background: #cbd5e1;
  }
  .modal-btn.confirm {
    background: var(--accent-strong);
    border: none;
    color: #fff;
  }
  .modal-btn.confirm:hover {
    background: var(--accent);
  }
  .modal-btn.confirm.danger {
    background: var(--alert);
  }
  .modal-btn.confirm.danger:hover {
    background: var(--alert-strong);
  }
  .modal-btn:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: 2px;
  }
</style>