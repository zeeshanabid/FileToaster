import adapter from '@sveltejs/adapter-static';

// Plain CSS only (no PostCSS/SCSS), so no language preprocessor is needed.
// vite compiles `<style>` and `.css` natively; vitePreprocess() is omitted
// because it is incompatible with vite 6's preprocessCSS in the vitest path.
/** @type {import('@sveltejs/kit').Config} */
const config = {
	kit: {
		adapter: adapter({
			// Output goes here at build time; embedded into the Go binary.
			pages: 'frontend-build'
		})
	}
};

export default config;
