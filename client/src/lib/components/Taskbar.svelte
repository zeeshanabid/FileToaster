<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { windows } from '$lib/store/windows';
  import { logout } from '$lib/store/auth';
  import { initTasks, shutdownTasks } from '$lib/store/tasks';
  import WindowButton from './WindowButton.svelte';
  import RunningTaskIcon from './RunningTaskIcon.svelte';
  import Portal from './Portal.svelte';
  import { openWindow } from '$lib/store/windows';
  import { isWithin } from '$lib/fs/paths';

  // Callback to open the change-password dialog. The dialog itself is owned by
  // the page root so it renders outside this taskbar's stacking context.
  let { onChangePassword }: { onChangePassword: () => void } = $props();

  // The taskbar only renders while authenticated, so (re)start the task engine
  // (snapshot + SSE) on mount and tear it down on logout.
  onMount(() => initTasks());
  onDestroy(() => shutdownTasks());

  // Log the user out of the current session; the page re-renders the login form.
  async function handleLogout() {
    await logout();
  }

  // Open path modal state
  let openPathModal = $state(false);
  let openPathInput = $state('');
  let openPathError = $state<string | null>(null);
  let openPathInputEl: HTMLInputElement | null = $state(null);

  // Roots are needed for validation - fetch them
  let roots = $state<Array<{ name: string; path: string }>>([]);
  let rootsLoaded = $state(false);

  onMount(async () => {
    try {
      const res = await fetch('/api/fs/roots', {
        method: 'POST',
        credentials: 'include',
      });
      if (res.ok) {
        const body = await res.json();
        if (body.allowed_paths) {
          roots = body.allowed_paths;
        }
      }
    } catch {
      // Ignore - roots will be empty
    } finally {
      rootsLoaded = true;
    }
  });

  function openOpenPathModal() {
    openPathInput = '';
    openPathError = null;
    openPathModal = true;
  }

  function closeOpenPathModal() {
    openPathModal = false;
    openPathInput = '';
    openPathError = null;
  }

  async function confirmOpenPath() {
    const path = openPathInput.trim();
    if (!path) {
      openPathError = 'Please enter a path.';
      return;
    }

    // Client-side validation: check if path is within any allowed root
    const isAllowed = roots.some((root) => isWithin(path, root.path));
    if (!isAllowed) {
      openPathError = 'The path is outside the allowed folders.';
      return;
    }

    // Server-side validation
    try {
      const res = await fetch('/api/fs/validate-path', {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ path }),
      });
      if (!res.ok) {
        const data = await res.json().catch(() => ({}));
        openPathError = data.error ?? 'Invalid path or outside allowed folders.';
        return;
      }
    } catch {
      openPathError = 'Failed to validate path with server.';
      return;
    }

    // Path is valid, open new window
    closeOpenPathModal();
    // Find the root this path belongs to
    const root = roots.find((r) => isWithin(path, r.path));
    if (root) {
      openWindow({ name: root.name, path, rootPath: root.path });
    } else {
      // Fallback: use the first root
      openWindow({ name: roots[0]?.name ?? 'Files', path, rootPath: roots[0]?.path ?? path });
    }
  }

  function onModalKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      confirmOpenPath();
    } else if (e.key === 'Escape') {
      closeOpenPathModal();
    }
  }

  function onInputFocus() {
    openPathInputEl?.focus();
  }

  // Focus the input when the modal opens
  $effect(() => {
    if (openPathModal) {
      onInputFocus();
    }
  });
</script>

<div class="taskbar" role="toolbar" aria-label="FileToaster taskbar">
  <button class="launcher" type="button" title="FileToaster" aria-label="FileToaster menu">
    <img class="launcher-logo" src="/filetoaster-logo.svg" alt="FileToaster" />
  </button>

  <div class="divider" aria-hidden="true"></div>

  <div class="windows" role="group" aria-label="Open windows">
    {#each $windows as win (win.id)}
      <WindowButton window={win} active={!win.minimized} />
    {/each}
    {#if $windows.length === 0}
      <span class="windows-empty" aria-hidden="true">No windows open</span>
    {/if}
  </div>

  <div class="system" role="group" aria-label="System controls">
    <RunningTaskIcon />
    <div class="divider" aria-hidden="true"></div>
    <button type="button" class="system-btn" title="Open path in new window" aria-label="Open path in new window" onclick={openOpenPathModal}>
      <span class="system-mark" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
          <path d="M3 7v10a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V9a2 2 0 0 0-2-2H7l-2-2H3a2 2 0 0 0-2 2z" />
          <path d="M17 4v4h4" />
          <path d="M21 3v5h-5" />
          <path d="M14 14l7-7" />
        </svg>
      </span>
    </button>
    <div class="divider" aria-hidden="true"></div>
    <button type="button" class="system-btn" title="Change password" aria-label="Change password" onclick={onChangePassword}>
      <span class="system-mark" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
          <rect x="5" y="11" width="14" height="9" rx="2" />
          <path d="M8 11V8a4 4 0 0 1 8 0v3" />
        </svg>
      </span>
    </button>
    <button type="button" class="system-btn" title="Log out" aria-label="Log out" onclick={handleLogout}>
      <span class="system-mark" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
          <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
          <path d="M16 17l5-5-5-5" />
          <path d="M21 12H9" />
        </svg>
      </span>
    </button>
  </div>
</div>

{#if openPathModal}
  <Portal>
    <div 
      class="modal-backdrop" 
      role="button" 
      tabindex="0" 
      aria-label="Close modal" 
      onclick={closeOpenPathModal} 
      onkeydown={(e) => e.key === 'Escape' && closeOpenPathModal()}
    >
      <div 
        class="modal" 
        role="dialog" 
        aria-modal="true" 
        aria-labelledby="open-path-title" 
        tabindex="-1"
        onclick={(e) => e.stopPropagation()}
        onkeydown={(e) => e.key === 'Escape' && closeOpenPathModal()}
      >
        <header class="modal-header">
          <h2 id="open-path-title">Open Path in New Window</h2>
          <button class="modal-close" type="button" aria-label="Close" onclick={closeOpenPathModal}>
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="18" height="18" aria-hidden="true"><path d="M7 7l10 10M17 7L7 17" /></svg>
          </button>
        </header>
        <div class="modal-body">
          <label for="open-path-input" class="modal-label">Enter an absolute path within the allowed folders:</label>
          <input
            id="open-path-input"
            class="modal-input"
            bind:this={openPathInputEl}
            bind:value={openPathInput}
            type="text"
            placeholder="/path/to/directory"
            onkeydown={onModalKeydown}
            onfocus={onInputFocus}
            autocomplete="off"
            spellcheck="false"
          />
          {#if openPathError}
            <div class="modal-error">{openPathError}</div>
          {/if}
        </div>
        <footer class="modal-footer">
          <button class="modal-btn cancel" type="button" onclick={closeOpenPathModal}>Cancel</button>
          <button class="modal-btn confirm" type="button" onclick={confirmOpenPath}>Open</button>
        </footer>
      </div>
    </div>
  </Portal>
{/if}

<style>
  .taskbar {
    position: absolute;
    left: 12px;
    right: 12px;
    bottom: 12px;
    height: 56px;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 0 16px;
    background: var(--taskbar-bg);
    backdrop-filter: blur(20px) saturate(180%);
    -webkit-backdrop-filter: blur(20px) saturate(180%);
    border: 1px solid var(--taskbar-border);
    border-top: 1px solid var(--taskbar-border);
    border-radius: 18px;
    z-index: 1000;
    box-shadow:
      0 4px 24px rgba(0, 0, 0, 0.3),
      0 1px 3px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.06);
  }

  /* Launcher / Brand area */
  .launcher {
    display: inline-flex;
    align-items: center;
    padding: 6px 6px;
    background: #fff;
    border: 1px solid rgba(113, 56, 245, 0.15);
    border-radius: 11px;
    cursor: pointer;
    box-shadow: 0 2px 10px rgba(113, 56, 245, 0.16);
    flex-shrink: 0;
  }
  .launcher-logo {
    display: block;
    height: 37px;
    width: auto;
  }

  /* Divider */
  .divider {
    width: 1px;
    height: 28px;
    background: linear-gradient(180deg, transparent, var(--taskbar-border), transparent);
    flex: none;
  }

  /* Windows area - takes all available space */
  .windows {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1 1 auto;
    min-width: 0;
    height: 100%;
    overflow: hidden;
  }
  .windows-empty {
    display: inline-flex;
    align-items: center;
    height: 100%;
    padding: 0 12px;
    color: var(--taskbar-muted);
    font-size: 12px;
    font-weight: 500;
  }

  /* System controls area */
  .system {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    flex: 0 0 auto;
  }
  .system-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 38px;
    height: 38px;
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px;
    color: var(--taskbar-text);
    cursor: pointer;
    transition: all 150ms ease;
  }
  .system-btn:hover {
    background: var(--taskbar-hover);
    border-color: rgba(255, 255, 255, 0.18);
  }
  .system-btn[aria-label='Log out']:hover {
    color: var(--alert);
    border-color: rgba(251, 113, 133, 0.35);
    background: rgba(251, 113, 133, 0.1);
  }
  .system-btn:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .system-mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 20px;
    height: 20px;
  }
  .system-mark svg {
    width: 18px;
    height: 18px;
  }

  /* Open Path Modal */
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
    gap: 12px;
    display: flex;
    flex-direction: column;
  }
  .modal-label {
    font-size: 13px;
    font-weight: 500;
    color: var(--text-muted);
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
  .modal-btn:focus-visible {
    outline: 2px solid var(--accent-strong);
    outline-offset: 2px;
  }
</style>