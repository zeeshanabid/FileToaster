import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { fireEvent, screen, within } from '@testing-library/dom';
import ConfirmDecompress from './ConfirmDecompress.svelte';

const instances: unknown[] = [];
function mountComp(props: unknown): HTMLElement {
	const target = document.createElement('div');
	document.body.appendChild(target);
	instances.push(mount(ConfirmDecompress as never, { target, props } as never));
	return target;
}
function unmountAll() {
	while (instances.length) unmount(instances.pop() as never);
	document.querySelectorAll('body > div').forEach((el) => el.remove());
}

function entry(path: string, name: string, type: 'file' | 'dir') {
	return { path, name, type };
}

function dialog(): HTMLElement {
	return screen.getByTestId('decompress-dialog') as HTMLElement;
}

describe('ConfirmDecompress', () => {
	const onClose = vi.fn();
	const onConfirm = vi.fn();
	const entries = [entry('/root/myfiles.zip', 'myfiles.zip', 'file')];

	beforeEach(() => {
		onClose.mockClear();
		onConfirm.mockClear();
	});
	afterEach(() => {
		unmountAll();
	});

	it('renders the header and the archive name', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(dialog().querySelector('.dlg-title')?.textContent).toBe('Decompress');
		expect(within(dialog()).getByText('myfiles.zip')).toBeInTheDocument();
	});

	it('exposes exactly two options: extract here and extract in the stem', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByLabelText('Extract here')).toBeInTheDocument();
		expect(within(dialog()).getByLabelText('Extract in \u201Cmyfiles\u201D')).toBeInTheDocument();
	});

	it('lists the selected name and type', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText('myfiles.zip')).toBeInTheDocument();
		expect(within(dialog()).getByText('file')).toBeInTheDocument();
	});

	it('fires onConfirm with an empty into for “Extract here” (the default)', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByLabelText('Extract here'));
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Extract' }));
		expect(onConfirm).toHaveBeenCalledTimes(1);
		expect(onConfirm).toHaveBeenCalledWith({ into: '' });
		expect(onClose).not.toHaveBeenCalled();
	});

	it('fires onConfirm with the stem for “Extract in …”', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(dialog().querySelector('input[value="myfiles"]') as HTMLElement);
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Extract' }));
		expect(onConfirm).toHaveBeenCalledTimes(1);
		expect(onConfirm).toHaveBeenCalledWith({ into: 'myfiles' });
	});

	it('calls onClose when Cancel is pressed', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Cancel' }));
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(onConfirm).not.toHaveBeenCalled();
	});

	it('closes on the close button', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByLabelText('Close'));
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it('closes on Escape', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.keyDown(dialog(), { key: 'Escape' });
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it('closes when the backdrop is clicked', () => {
		mountComp({ entries, onClose, onConfirm });
		const overlay = screen.getByRole('presentation') as HTMLElement;
		overlay.dispatchEvent(new MouseEvent('mousedown', { bubbles: true, cancelable: true }));
		expect(onClose).toHaveBeenCalledTimes(1);
	});
});
