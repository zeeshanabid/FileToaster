<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { windows } from '$lib/store/windows';
  import { logout } from '$lib/store/auth';
  import { initTasks, shutdownTasks } from '$lib/store/tasks';
  import WindowButton from './WindowButton.svelte';
  import RunningTaskIcon from './RunningTaskIcon.svelte';

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
    z-index: 40;
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
</style>