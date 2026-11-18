<script lang="ts">
  import { onMount } from 'svelte';
  import { KEYBOARD_SHORTCUTS, formatShortcut, isMacPlatform } from '$lib/keyboard/shortcuts';
  import Portal from './Portal.svelte';

  // The help overlay is opened with the "?" key from anywhere in the app (not
  // just the focused window), so the listener lives on `window`. Typing "?" in
  // an input must keep the character instead of opening the overlay.
  let open = $state(false);
  let panelEl: HTMLElement | null = $state(null);
  // macOS calls Alt "Option" and shows modifier glyphs; resolve this on mount
  // (the overlay itself only renders after a key press, so there is no flash).
  let isMac = $state(false);

  function isEditable(target: EventTarget | null): boolean {
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

  function onGlobalKeydown(e: KeyboardEvent) {
    if (e.key !== '?' || e.ctrlKey || e.altKey || e.metaKey) return;
    if (isEditable(e.target)) return;
    e.preventDefault();
    open = true;
  }

  onMount(() => {
    isMac = isMacPlatform();
    window.addEventListener('keydown', onGlobalKeydown);
    return () => window.removeEventListener('keydown', onGlobalKeydown);
  });

  // Move focus into the overlay so the background is inaccessible and every
  // key is routed through this layer (which swallows the window shortcuts).
  $effect(() => {
    if (open) panelEl?.focus();
  });

  function close() {
    open = false;
  }

  function onOverlayKeydown(e: KeyboardEvent) {
    // The overlay is modal: nothing below it should react to keys while it is
    // open. Escape closes the overlay itself.
    e.stopPropagation();
    if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  }
</script>

{#if open}
  <Portal>
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
    <div
      class="shortcuts-backdrop"
      data-testid="shortcuts-backdrop"
      onclick={close}
      onkeydown={onOverlayKeydown}
    >
      <div
        class="shortcuts-panel"
        data-testid="shortcuts-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        tabindex="-1"
        bind:this={panelEl}
        onclick={(e) => e.stopPropagation()}
      >
        <header class="shortcuts-head">
          <h2>Keyboard shortcuts</h2>
          <span class="shortcuts-close-hint">Press ESC or click outside to close</span>
        </header>
        <ul class="shortcuts-list">
          {#each KEYBOARD_SHORTCUTS as shortcut (shortcut.modifiers.join('+') + shortcut.key)}
            <li class="shortcut">
              <span class="shortcut-keys">{formatShortcut(shortcut, isMac)}</span>
              <span class="shortcut-desc">{shortcut.description}</span>
            </li>
          {/each}
        </ul>
      </div>
    </div>
  </Portal>
{/if}

<style>
  .shortcuts-backdrop {
    position: fixed;
    inset: 0;
    z-index: 1000000;
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 32px;
    background: rgba(8, 11, 22, 0.55);
    backdrop-filter: blur(10px) saturate(120%);
    -webkit-backdrop-filter: blur(10px) saturate(120%);
    animation: shortcuts-fade-in 120ms ease;
  }
  @keyframes shortcuts-fade-in {
    from {
      opacity: 0;
    }
    to {
      opacity: 1;
    }
  }
  .shortcuts-panel {
    width: min(640px, 100%);
    max-height: min(80vh, 720px);
    overflow: auto;
    padding: 26px 30px 30px;
    background: rgba(15, 23, 42, 0.72);
    border: 1px solid rgba(255, 255, 255, 0.16);
    border-radius: 18px;
    box-shadow: 0 32px 90px rgba(0, 0, 0, 0.55);
    color: #e2e8f0;
    font-family: ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif;
    backdrop-filter: blur(22px) saturate(150%);
    -webkit-backdrop-filter: blur(22px) saturate(150%);
    outline: none;
    animation: shortcuts-pop-in 150ms ease-out;
  }
  @keyframes shortcuts-pop-in {
    from {
      opacity: 0;
      transform: translateY(10px) scale(0.98);
    }
    to {
      opacity: 1;
      transform: translateY(0) scale(1);
    }
  }
  .shortcuts-head {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    gap: 16px;
    margin-bottom: 18px;
    padding-bottom: 14px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.14);
  }
  .shortcuts-head h2 {
    margin: 0;
    font-size: 18px;
    font-weight: 700;
    letter-spacing: 0.01em;
  }
  .shortcuts-close-hint {
    font-size: 12px;
    color: #94a3b8;
    white-space: nowrap;
  }
  .shortcuts-list {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 4px;
  }
  .shortcut {
    display: grid;
    grid-template-columns: minmax(220px, auto) 1fr;
    align-items: center;
    column-gap: 26px;
    padding: 9px 10px;
    border-radius: 8px;
  }
  .shortcut:nth-child(odd) {
    background: rgba(255, 255, 255, 0.05);
  }
  .shortcut-keys {
    color: #a5b4fc;
    font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
    font-size: 13.5px;
    font-weight: 700;
    letter-spacing: 0.04em;
    white-space: nowrap;
  }
  .shortcut-desc {
    color: #e2e8f0;
    font-size: 13.5px;
    line-height: 1.4;
  }
</style>