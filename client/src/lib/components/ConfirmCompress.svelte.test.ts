import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { fireEvent, screen, within } from '@testing-library/dom';
import ConfirmCompress from './ConfirmCompress.svelte';

const instances: unknown[] = [];
function mountComp(props: unknown): HTMLElement {
	const target = document.createElement('div');
	document.body.appendChild(target);
	instances.push(mount(ConfirmCompress as never, { target, props } as never));
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
	return screen.getByTestId('compress-dialog') as HTMLElement;
}

describe('ConfirmCompress', () => {
	const onClose = vi.fn();
	const onConfirm = vi.fn();
	const entries = [
		entry('/root/notes.txt', 'notes.txt', 'file'),
		entry('/root/documents', 'documents', 'dir')
	];

	beforeEach(() => {
		onClose.mockClear();
		onConfirm.mockClear();
	});
	afterEach(() => {
		unmountAll();
	});

	it('renders the heading with the root-level selected-item count and default format', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText(/Create a \.zip archive of 2 files in the current folder/)).toBeInTheDocument();
	});

	it('lists each selected name and type', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText('notes.txt')).toBeInTheDocument();
		expect(within(dialog()).getByText('documents')).toBeInTheDocument();
		expect(within(dialog()).getByText('file')).toBeInTheDocument();
		expect(within(dialog()).getByText('directory')).toBeInTheDocument();
	});

	it('exposes the three archive formats', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByLabelText('ZIP archive (.zip)')).toBeInTheDocument();
		expect(within(dialog()).getByLabelText('GNU tar archive, gzip (.tar.gz)')).toBeInTheDocument();
		expect(within(dialog()).getByLabelText('GNU tar archive (.tar)')).toBeInTheDocument();
	});

	it('calls onConfirm with zip when Compress is pressed without choosing a format', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Compress' }));
		expect(onConfirm).toHaveBeenCalledTimes(1);
		expect(onConfirm).toHaveBeenCalledWith('zip');
		expect(onClose).not.toHaveBeenCalled();
	});

	it('passes the chosen format to onConfirm', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByLabelText('GNU tar archive, gzip (.tar.gz)'));
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Compress' }));
		expect(onConfirm).toHaveBeenCalledTimes(1);
		expect(onConfirm).toHaveBeenCalledWith('targz');
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

	it('disables Compress when the selection is empty', () => {
		mountComp({ entries: [], onClose, onConfirm });
		expect(within(dialog()).getByText(/Create a \.zip archive of 0 files in the current folder/)).toBeInTheDocument();
		expect(within(dialog()).getByRole('button', { name: 'Compress' })).toBeDisabled();
	});
});
