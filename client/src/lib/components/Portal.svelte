<script lang="ts">
  import { onMount } from 'svelte';

  // Renders its children into a div moved to document.body. Doing so escapes the
  // stacking context of the window that owns the component, so content with a
  // high z-index (dialogs, menus) paints above every window regardless of the
  // z-order the windows were created in.
  let { children } = $props();
  let host: HTMLDivElement | null = null;
  onMount(() => {
    if (host) document.body.appendChild(host);
  });
</script>

<div bind:this={host} class="window-portal">{@render children()}</div>

<style>
  :global(.window-portal) {
    position: fixed;
    inset: 0;
    z-index: 1000000;
  }
</style>
