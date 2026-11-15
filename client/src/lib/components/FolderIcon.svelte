<script lang="ts">
  import type { Root } from '$lib/roots';

  let { root, onopen }: { root: Root; onopen: (root: Root) => void } = $props();
</script>

<button class="tile" type="button" onclick={() => onopen(root)}>
  <span class="glyph" aria-hidden="true">
    <img class="glyph-logo" src="/filetoaster-directory.svg" alt="" />
  </span>
  <span class="label">{root.name}</span>
</button>

<style>
  .tile {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 10px;
    width: 100px;
    padding: 18px 8px 12px;
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.09);
    border-radius: 16px;
    color: var(--taskbar-text);
    cursor: pointer;
  }
  .tile:hover,
  .tile:focus-visible {
    background: rgba(255, 255, 255, 0.11);
    border-color: rgba(255, 255, 255, 0.2);
  }
  .glyph {
    display: grid;
    place-items: center;
    width: 54px;
    height: 54px;
    border-radius: 15px;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
    box-shadow: var(--shadow-glyph);
  }
  .glyph-logo {
    width: 43.2px;
    height: 43.2px;
  }
  .label {
    font-size: 13px;
    font-weight: 500;
    text-align: center;
    text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
  }
</style>
