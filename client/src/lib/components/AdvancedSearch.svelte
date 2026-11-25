<script lang="ts">
  import { onMount } from 'svelte';
  import {
    DATE_PRESETS,
    SEARCH_SIZE_UNITS,
    MIME_TYPE_OPTIONS,
    formatDatePreset,
    normalizeExtension,
    type DatePreset,
    type SizeUnit
  } from '$lib/fs/format';
  import { defaultAdvancedFilters, type AdvancedFilters } from '$lib/store/search';

  let {
    filters,
    onApply,
    onClose
  }: {
    filters: AdvancedFilters;
    onApply: (filters: AdvancedFilters) => void;
    onClose: () => void;
  } = $props();

  // Local editable copy so Cancel leaves the caller's filters untouched.
  // svelte-ignore state_referenced_locally
  let draft = $state<AdvancedFilters>({
    ...filters,
    mimeTypes: [...filters.mimeTypes],
    extensions: [...filters.extensions]
  });

  let customMime = $state('');
  let extInput = $state('');

  let dialog: HTMLDivElement | null = $state(null);
  onMount(() => dialog?.focus());

  // The catalogue grouped by its display group, preserving catalogue order.
  const mimeGroups = $derived.by(() => {
    const groups: Array<{ name: string; options: typeof MIME_TYPE_OPTIONS }> = [];
    for (const opt of MIME_TYPE_OPTIONS) {
      const found = groups.find((g) => g.name === opt.group);
      if (found) found.options.push(opt);
      else groups.push({ name: opt.group, options: [opt] });
    }
    return groups;
  });

  function hasMime(value: string): boolean {
    return draft.mimeTypes.includes(value);
  }
  function toggleMime(value: string): void {
    const set = new Set(draft.mimeTypes);
    if (set.has(value)) set.delete(value);
    else set.add(value);
    draft.mimeTypes = [...set];
  }
  function addCustomMime(): void {
    const value = customMime.trim().toLowerCase();
    if (!value) return;
    if (!draft.mimeTypes.includes(value)) draft.mimeTypes = [...draft.mimeTypes, value];
    customMime = '';
  }
  function removeMime(value: string): void {
    draft.mimeTypes = draft.mimeTypes.filter((m) => m !== value);
  }

  function addExtension(): void {
    const value = normalizeExtension(extInput);
    if (!value) return;
    if (!draft.extensions.includes(value)) draft.extensions = [...draft.extensions, value];
    extInput = '';
  }
  function onExtKey(e: KeyboardEvent): void {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      addExtension();
    }
  }
  function removeExtension(value: string): void {
    draft.extensions = draft.extensions.filter((x) => x !== value);
  }

  function reset(): void {
    draft = defaultAdvancedFilters();
    customMime = '';
    extInput = '';
  }

  function onBackdrop(e: MouseEvent): void {
    if (e.target === e.currentTarget) onClose();
  }
  function onKey(e: KeyboardEvent): void {
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
    aria-label="Advanced search"
    data-testid="advanced-search-dialog"
    bind:this={dialog}
    tabindex="0"
  >
    <header class="dlg-header">
      <span class="dlg-title">Advanced search</span>
      <button class="dlg-close" type="button" aria-label="Close" onclick={() => onClose()}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.9" stroke-linecap="round" width="100%" height="100%">
          <path d="M7 7l10 10M17 7L7 17" />
        </svg>
      </button>
    </header>

    <div class="dlg-body">
      <fieldset class="field">
        <legend>File types</legend>
        {#each mimeGroups as group (group.name)}
          <div class="mime-group">
            <span class="mime-group-name">{group.name}</span>
            <div class="mime-options">
              {#each group.options as opt (opt.value)}
                <label class="check">
                  <input
                    type="checkbox"
                    checked={hasMime(opt.value)}
                    onchange={() => toggleMime(opt.value)}
                  />
                  <span>{opt.label}</span>
                </label>
              {/each}
            </div>
          </div>
        {/each}
        <div class="tag-add">
          <input
            class="text-input"
            type="text"
            bind:value={customMime}
            placeholder="Add a MIME type (e.g. image/png)"
            aria-label="Custom MIME type"
            onkeydown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                addCustomMime();
              }
            }}
          />
          <button class="mini-btn" type="button" aria-label="Add MIME type" onclick={addCustomMime}>Add</button>
        </div>
        {#if draft.mimeTypes.length > 0}
          <div class="tags">
            {#each draft.mimeTypes as mime (mime)}
              <span class="tag">
                {mime}
                <button class="tag-remove" type="button" aria-label={`Remove ${mime}`} onclick={() => removeMime(mime)}>×</button>
              </span>
            {/each}
          </div>
        {/if}
      </fieldset>

      <fieldset class="field">
        <legend>File extensions</legend>
        <div class="tag-add">
          <input
            class="text-input"
            type="text"
            bind:value={extInput}
            placeholder="Add an extension (e.g. txt)"
            aria-label="Custom extension"
            onkeydown={onExtKey}
          />
          <button class="mini-btn" type="button" aria-label="Add extension" onclick={addExtension}>Add</button>
        </div>
        {#if draft.extensions.length > 0}
          <div class="tags">
            {#each draft.extensions as ext (ext)}
              <span class="tag">
                .{ext}
                <button class="tag-remove" type="button" aria-label={`Remove ${ext}`} onclick={() => removeExtension(ext)}>×</button>
              </span>
            {/each}
          </div>
        {/if}
      </fieldset>

      <fieldset class="field">
        <legend>Created</legend>
        <select class="select-input" bind:value={draft.createdPreset} aria-label="Created date filter">
          {#each DATE_PRESETS as preset (preset)}
            <option value={preset}>{formatDatePreset(preset)}</option>
          {/each}
        </select>
        {#if draft.createdPreset === 'custom'}
          <div class="range-row">
            <input class="text-input" type="datetime-local" bind:value={draft.createdAfter} aria-label="Created after" />
            <input class="text-input" type="datetime-local" bind:value={draft.createdBefore} aria-label="Created before" />
          </div>
        {/if}
      </fieldset>

      <fieldset class="field">
        <legend>Modified</legend>
        <select class="select-input" bind:value={draft.modifiedPreset} aria-label="Modified date filter">
          {#each DATE_PRESETS as preset (preset)}
            <option value={preset}>{formatDatePreset(preset)}</option>
          {/each}
        </select>
        {#if draft.modifiedPreset === 'custom'}
          <div class="range-row">
            <input class="text-input" type="datetime-local" bind:value={draft.modifiedAfter} aria-label="Modified after" />
            <input class="text-input" type="datetime-local" bind:value={draft.modifiedBefore} aria-label="Modified before" />
          </div>
        {/if}
      </fieldset>

      <fieldset class="field">
        <legend>Size</legend>
        <div class="range-row">
          <input class="text-input" type="number" min="0" bind:value={draft.minSize} placeholder="Min" aria-label="Minimum size" />
          <input class="text-input" type="number" min="0" bind:value={draft.maxSize} placeholder="Max" aria-label="Maximum size" />
          <select class="select-input unit" bind:value={draft.sizeUnit} aria-label="Size unit">
            {#each SEARCH_SIZE_UNITS as unit (unit)}
              <option value={unit}>{unit}</option>
            {/each}
          </select>
        </div>
      </fieldset>
    </div>

    <footer class="dlg-footer">
      <button class="dlg-reset" type="button" onclick={reset}>Reset</button>
      <button class="dlg-cancel" type="button" onclick={() => onClose()}>Cancel</button>
      <button class="dlg-apply" type="button" onclick={() => onApply(draft)}>Apply</button>
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
    max-width: 620px;
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
  .dlg-body {
    flex: 1;
    min-height: 0;
    overflow: auto;
    padding: 8px 16px 14px;
  }
  .field {
    margin: 10px 0;
    padding: 4px 0 10px;
    border: none;
    border-bottom: 1px solid var(--border);
  }
  .field legend {
    padding: 0 0 6px;
    font-size: 12px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }
  .mime-group {
    margin-bottom: 8px;
  }
  .mime-group-name {
    display: block;
    font-size: 12px;
    color: var(--text-muted);
    margin-bottom: 4px;
  }
  .mime-options {
    display: flex;
    flex-wrap: wrap;
    gap: 6px 14px;
  }
  .check {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 13px;
    color: var(--text);
    cursor: pointer;
  }
  .tag-add {
    display: flex;
    gap: 6px;
    margin-top: 8px;
  }
  .text-input,
  .select-input {
    height: 30px;
    padding: 0 8px;
    font: inherit;
    font-size: 13px;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 7px;
    outline: none;
    min-width: 0;
  }
  .text-input:focus,
  .select-input:focus {
    border-color: var(--accent-strong);
  }
  .text-input {
    flex: 1;
  }
  .select-input {
    width: 100%;
  }
  .select-input.unit {
    width: auto;
    flex: none;
  }
  .range-row {
    display: flex;
    gap: 8px;
    margin-top: 8px;
  }
  .mini-btn {
    height: 30px;
    padding: 0 12px;
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    color: var(--text);
    background: var(--surface-soft);
    border: 1px solid var(--border);
    border-radius: 7px;
    cursor: pointer;
    flex: none;
  }
  .mini-btn:hover {
    color: var(--accent-strong);
    border-color: var(--accent-strong);
  }
  .tags {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin-top: 8px;
  }
  .tag {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    padding: 3px 6px 3px 8px;
    font-size: 12px;
    color: var(--text);
    background: var(--surface-inset);
    border: 1px solid var(--border);
    border-radius: 999px;
  }
  .tag-remove {
    display: grid;
    place-items: center;
    width: 16px;
    height: 16px;
    padding: 0;
    background: transparent;
    border: none;
    color: var(--text-muted);
    font-size: 14px;
    line-height: 1;
    cursor: pointer;
    border-radius: 50%;
  }
  .tag-remove:hover {
    background: var(--alert);
    color: #fff;
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
  .dlg-reset {
    margin-right: auto;
  }
  .dlg-reset,
  .dlg-cancel,
  .dlg-apply {
    height: 32px;
    padding: 0 18px;
    font: inherit;
    font-size: 13px;
    font-weight: 600;
    border-radius: 8px;
    cursor: pointer;
  }
  .dlg-reset,
  .dlg-cancel {
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
  }
  .dlg-reset:hover,
  .dlg-cancel:hover {
    color: var(--accent-strong);
    border-color: var(--accent-strong);
  }
  .dlg-apply {
    color: #fff;
    background: var(--accent);
    border: 1px solid var(--accent);
  }
  .dlg-apply:hover {
    background: var(--accent-strong);
    border-color: var(--accent-strong);
  }
</style>
