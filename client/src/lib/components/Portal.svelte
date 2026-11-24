<script lang="ts">
  import { onMount, onDestroy } from 'svelte';

  // Renders its children into a div moved to document.body. Doing so escapes the
  // stacking context of the window that owns the component, so content with a
  // high z-index (dialogs, menus) paints above every window regardless of the
  // z-order the windows were created in.
  // `passthrough` keeps the fixed, full-viewport host from intercepting clicks
  // for purely informational content (e.g. a toast). Interactive portals
  // (dialogs, menus) leave it false so their backdrop still captures input.
  let { children, passthrough = false } = $props();
  let host: HTMLDivElement | null = null;
  onMount(() => {
    if (host) document.body.appendChild(host);
  });
  onDestroy(() => {
    if (host && host.parentNode) host.parentNode.removeChild(host);
  });
</script>

<div bind:this={host} class="window-portal" class:passthrough>{@render children()}</div>

<style>
  :global(.window-portal) {
    position: fixed;
    inset: 0;
    z-index: 1000000;
  }
  :global(.window-portal.passthrough) {
    pointer-events: none;
  }
</style>
