import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { fireEvent, screen, within } from '@testing-library/dom';
import ConfirmMove from './ConfirmMove.svelte';

const instances: unknown[] = [];
function mountComp(props: unknown): HTMLElement {
	const target = document.createElement('div');
	document.body.appendChild(target);
	instances.push(mount(ConfirmMove as never, { target, props } as never));
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
	return screen.getByTestId('move-dialog') as HTMLElement;
}

describe('ConfirmMove', () => {
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

	it('renders the heading with the root-level selected-item count (plural)', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText('Are you sure you want to move 2 files and directories?')).toBeInTheDocument();
	});

	it('renders the singular heading for a single selection', () => {
		mountComp({ entries: [entries[0]], onClose, onConfirm });
		expect(within(dialog()).getByText('Are you sure you want to move 1 file and directories?')).toBeInTheDocument();
	});

	it('shows the overwrite notice', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText(/already exists at the destination, it will be overwritten/)).toBeInTheDocument();
	});

	it('lists each selected name and type', () => {
		mountComp({ entries, onClose, onConfirm });
		expect(within(dialog()).getByText('notes.txt')).toBeInTheDocument();
		expect(within(dialog()).getByText('documents')).toBeInTheDocument();
		expect(within(dialog()).getByText('file')).toBeInTheDocument();
		expect(within(dialog()).getByText('directory')).toBeInTheDocument();
	});

	it('calls onConfirm when Move is pressed', () => {
		mountComp({ entries, onClose, onConfirm });
		fireEvent.click(within(dialog()).getByRole('button', { name: 'Move' }));
		expect(onConfirm).toHaveBeenCalledTimes(1);
		expect(onClose).not.toHaveBeenCalled();
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

	it('disables Move and shows a zero count when the selection is empty', () => {
		mountComp({ entries: [], onClose, onConfirm });
		expect(within(dialog()).getByText('Are you sure you want to move 0 files and directories?')).toBeInTheDocument();
		expect(within(dialog()).getByRole('button', { name: 'Move' })).toBeDisabled();
	});
});
