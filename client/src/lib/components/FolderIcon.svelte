<script lang="ts">
	import type { Root } from '$lib/roots';

	let { root, onopen }: { root: Root; onopen: (root: Root) => void } = $props();
</script>

<button class="tile" type="button" onclick={() => onopen(root)}>
	<span class="glyph" aria-hidden="true">
		<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" width="100%" height="100%">
			<path d="M3 7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v2H3V7Z" />
			<path d="M4 12h16a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1v-5a1 1 0 0 1 1-1Z" />
		</svg>
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
	.glyph svg {
		width: 28px;
		height: 28px;
		color: #fff;
	}
	.label {
		font-size: 13px;
		font-weight: 500;
		text-align: center;
		text-shadow: 0 1px 2px rgba(0, 0, 0, 0.55);
	}
</style>
