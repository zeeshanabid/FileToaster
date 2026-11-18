<script lang="ts">
  import { displayName, focusWindow, toggleMinimize, type AppWindow } from '$lib/store/windows';

  let { window: win, active }: { window: AppWindow; active: boolean } = $props();
  let label = $derived(displayName(win));
  let isRoot = $derived(win.path === win.rootPath);
</script>

<button
  class="window-button"
  class:active={active}
  class:minimized={win.minimized}
  title={win.minimized ? `${label} (minimized)` : label}
  onclick={() => (win.minimized ? toggleMinimize(win.id) : focusWindow(win.id))}
>
  <span class="icon" aria-hidden="true">
    <img src="/filetoaster-directory.svg" alt="" class="icon-img" />
  </span>
  <span class="indicator" aria-hidden="true"></span>
  <span class="name">{label}</span>
</button>

<style>
  .window-button {
    display: inline-flex;
    align-items: center;
    gap: 9px;
    max-width: 180px;
    height: 38px;
    padding: 0 13px;
    background: rgba(255, 255, 255, 0.06);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 10px;
    color: var(--taskbar-text);
    font-size: 12.5px;
    font-weight: 500;
    cursor: pointer;
    transition: all 150ms ease;
    box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.03);
  }
  .window-button:hover {
    background: rgba(255, 255, 255, 0.14);
    border-color: rgba(255, 255, 255, 0.18);
    box-shadow:
      0 2px 10px rgba(0, 0, 0, 0.2),
      inset 0 1px 0 rgba(255, 255, 255, 0.08);
  }
  .window-button:active {
    transform: scale(0.98);
  }
  .window-button:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 2px;
  }
  .window-button.active {
    background: var(--taskbar-active);
    border-color: rgba(129, 140, 248, 0.55);
    color: #fff;
    box-shadow:
      0 2px 12px rgba(129, 140, 248, 0.25),
      inset 0 1px 0 rgba(255, 255, 255, 0.1);
  }
  .window-button.minimized {
    opacity: 0.55;
    border-color: rgba(255, 255, 255, 0.08);
    background: rgba(255, 255, 255, 0.04);
  }
  .window-button.minimized:hover {
    opacity: 0.8;
    background: rgba(255, 255, 255, 0.1);
    border-color: rgba(255, 255, 255, 0.15);
  }
  .icon {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    flex: none;
    color: currentColor;
    opacity: 0.9;
  }
  .icon-img {
    width: 16px;
    height: 16px;
  }
  .indicator {
    width: 6px;
    height: 6px;
    border-radius: 50%;
    background: var(--ok);
    flex: none;
    opacity: 0.8;
    box-shadow: 0 0 6px var(--ok);
    transition: all 150ms ease;
  }
  .window-button.active .indicator {
    background: var(--accent-2);
    box-shadow: 0 0 8px var(--accent-2);
    opacity: 1;
  }
  .window-button.minimized .indicator {
    background: var(--warn);
    box-shadow: 0 0 6px var(--warn);
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    max-width: 130px;
  }
</style>