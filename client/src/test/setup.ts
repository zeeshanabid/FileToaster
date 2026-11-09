// Register testing-library jest-dom matchers (toBeInTheDocument, etc.) and
// polyfill the small slice of the DOM the tests touch.
import '@testing-library/jest-dom/vitest';
if (typeof (globalThis as any).CSS === 'undefined') {
	(globalThis as any).CSS = {
		escape: (value: string) => value.replace(/[^a-zA-Z0-9-_]/g, (ch) => '\\' + ch)
	};
}
if (
	typeof (globalThis as any).Element !== 'undefined' &&
	typeof (globalThis as any).Element.prototype.scrollIntoView === 'undefined'
) {
	(globalThis as any).Element.prototype.scrollIntoView = () => {};
}
