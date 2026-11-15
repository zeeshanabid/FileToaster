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

<div class="taskbar">
  <button class="launcher" type="button" title="FileToaster">
    <img
      class="launcher-logo"
      src="/filetoaster-logo.svg"
      alt="FileToaster"
    />
  </button>

  <div class="windows">
    {#each $windows as win (win.id)}
      <WindowButton window={win} active={!win.minimized} />
    {/each}
  </div>

  <div class="spacer"></div>
  <div class="account">
    <RunningTaskIcon />
    <button type="button" class="account-btn" title="Change password" aria-label="Change password" onclick={onChangePassword}>
      <span class="account-mark" aria-hidden="true">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
          <rect x="5" y="11" width="14" height="9" rx="2" />
          <path d="M8 11V8a4 4 0 0 1 8 0v3" />
        </svg>
      </span>
    </button>
    <button type="button" class="account-btn" title="Log out" aria-label="Log out" onclick={handleLogout}>
      <span class="account-mark" aria-hidden="true">
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
    left: 0;
    right: 0;
     bottom: 12px;
     height: 56px;
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 0 14px;
    background: var(--taskbar-bg);
    backdrop-filter: blur(16px) saturate(160%);
    -webkit-backdrop-filter: blur(16px) saturate(160%);
    border: 1px solid var(--taskbar-border);
    border-top: 1px solid var(--taskbar-border);
    border-radius: 16px;
    z-index: 40;
    box-shadow: 0 12px 30px rgba(0, 0, 0, 0.35);
  }
  .launcher {
    display: inline-flex;
    align-items: center;
    padding: 6px 6px;
    background: #fff;
    border: 1px solid rgba(113, 56, 245, 0.15);
    border-radius: 11px;
    cursor: pointer;
    box-shadow: 0 2px 10px rgba(113, 56, 245, 0.16);
  }
.launcher-logo {
  display: block;
  height: 37px;
  width: auto;
}
  .windows {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 1;
    min-width: 0;
    height: 100%;
    overflow: hidden;
  }
  .spacer {
    flex: 0 0 auto;
  }
  .account {
    display: flex;
    align-items: center;
    gap: 6px;
    flex: 0 0 auto;
  }
  .account-btn {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 38px;
    height: 38px;
    background: rgba(255, 255, 255, 0.07);
    border: 1px solid rgba(255, 255, 255, 0.12);
    border-radius: 10px;
    color: var(--taskbar-text);
    cursor: pointer;
  }
  .account-btn:hover {
    background: var(--taskbar-hover);
  }
  .account-btn[aria-label='Log out']:hover {
    color: var(--alert);
    border-color: rgba(251, 113, 133, 0.35);
  }
  .account-mark {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 19px;
    height: 19px;
  }
  .account-mark svg {
    width: 18px;
    height: 18px;
  }
</style>
