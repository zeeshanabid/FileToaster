import { sveltekit } from '@sveltejs/kit/vite';
import { defineConfig, type UserConfig } from 'vite';

// Vitest reads this file at runtime, so the `test` field lives here. It is not
// part of Vite's `UserConfig`, and pulling `defineConfig` from `vitest/config`
// drags in Vitest's bundled Vite 5 types, which conflict with the Vite 6 plugin
// SvelteKit uses — so we keep Vite's own defineConfig and carry `test` explicitly.
type VitestUserConfig = UserConfig & {
  test: {
    environment: string;
    setupFiles: string[];
    include: string[];
  };
};

const viteConfig: VitestUserConfig = {
  plugins: [sveltekit()],
  resolve: {
    // Ensure the bare `svelte` import resolves to the client build (`browser`
    // condition) during tests; vitest otherwise resolves it to the server
    // build, whose `mount` throws.
    conditions: ['browser']
  },
  test: {
    environment: 'jsdom',
    setupFiles: ['./src/test/setup.ts'],
    include: ['src/**/*.{test,spec}.{js,ts}']
  }
};

export default defineConfig(viteConfig);
