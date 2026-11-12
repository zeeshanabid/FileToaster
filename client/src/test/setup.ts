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

// jsdom's Blob lacks arrayBuffer(); polyfill it so folder-drop file cloning
// (which reads File data into an independent ArrayBuffer) can be tested.
if (typeof Blob.prototype.arrayBuffer === 'undefined') {
	Blob.prototype.arrayBuffer = function arrayBuffer(): Promise<ArrayBuffer> {
		const reader = new FileReader();
		return new Promise<ArrayBuffer>((resolve, reject) => {
			reader.onload = () => resolve(reader.result as ArrayBuffer);
			reader.onerror = () => reject(reader.error);
			reader.readAsArrayBuffer(this);
		});
	};
}
