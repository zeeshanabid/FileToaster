import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
import { fireEvent, screen, within, waitFor } from '@testing-library/dom';
import ConfirmTruncate from './ConfirmTruncate.svelte';

const instances: unknown[] = [];
function mountComp(props: unknown): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instances.push(mount(ConfirmTruncate as never, { target, props } as never));
  return target;
}
function unmountAll() {
  while (instances.length) unmount(instances.pop() as never);
  document.querySelectorAll('body > div').forEach((el) => el.remove());
}

describe('ConfirmTruncate', () => {
  const onCancel = vi.fn();
  const onConfirm = vi.fn();

  beforeEach(() => {
    onCancel.mockClear();
    onConfirm.mockClear();
  });
  afterEach(() => {
    unmountAll();
  });

  it('renders the heading with the file count', async () => {
    mountComp({ fileCount: 3, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByText('Truncate Files')).toBeInTheDocument());
    expect(screen.getByText('Target size for 3 files:')).toBeInTheDocument();
  });

  it('renders singular heading for a single file', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByText('Target size for 1 file:')).toBeInTheDocument());
  });

  it('shows all preset size options in the dropdown', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => {
      const select = screen.getByRole('combobox') as HTMLSelectElement;
      const options = select.options;
      expect(options.length).toBe(10); // 9 presets + custom
      expect(options[0].textContent).toBe('0 MB');
      expect(options[1].textContent).toBe('4 MB');
      expect(options[8].textContent).toBe('1 GB');
      expect(options[9].textContent).toBe('Custom size (MB)...');
    });
  });

  it('selects 0 MB by default', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => {
      const select = screen.getByRole('combobox') as HTMLSelectElement;
      expect(select.value).toBe('0 MB');
    });
  });

  it('shows custom size input when Custom is selected', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: 'custom' } });
    await waitFor(() => expect(screen.getByLabelText('Enter size in MB:')).toBeInTheDocument());
    expect(screen.getByPlaceholderText('e.g., 100')).toBeInTheDocument();
  });

  it('calls onConfirm with correct size when preset is selected', async () => {
    mountComp({ fileCount: 2, onCancel, onConfirm });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    // Default is 0 MB
    expect(onConfirm).toHaveBeenCalledWith(0);
  });

  it('calls onConfirm with correct size for 4 MB preset', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: '4 MB' } });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    expect(onConfirm).toHaveBeenCalledWith(4 * 1024 * 1024);
  });

  it('calls onConfirm with correct size for 1 GB preset', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: '1 GB' } });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    expect(onConfirm).toHaveBeenCalledWith(1024 * 1024 * 1024);
  });

  it('calls onConfirm with custom size in bytes', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: 'custom' } });
    await waitFor(() => expect(screen.getByLabelText('Enter size in MB:')).toBeInTheDocument());
    const input = screen.getByLabelText('Enter size in MB:') as HTMLInputElement;
    fireEvent.input(input, { target: { value: '100' } });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    expect(onConfirm).toHaveBeenCalledWith(100 * 1024 * 1024);
  });

  it('shows error for invalid custom size', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: 'custom' } });
    await waitFor(() => expect(screen.getByLabelText('Enter size in MB:')).toBeInTheDocument());
    const input = screen.getByLabelText('Enter size in MB:') as HTMLInputElement;
    fireEvent.input(input, { target: { value: '-5' } });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    await waitFor(() => expect(screen.getByText('Please enter a valid size in MB (non-negative number)')).toBeInTheDocument());
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('shows error for non-numeric custom size', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    const select = screen.getByRole('combobox') as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe('0 MB'));
    fireEvent.change(select, { target: { value: 'custom' } });
    await waitFor(() => expect(screen.getByLabelText('Enter size in MB:')).toBeInTheDocument());
    const input = screen.getByLabelText('Enter size in MB:') as HTMLInputElement;
    fireEvent.input(input, { target: { value: 'abc' } });
    const confirmBtn = screen.getByRole('button', { name: 'Truncate' });
    fireEvent.click(confirmBtn);
    await waitFor(() => expect(screen.getByText('Please enter a valid size in MB (non-negative number)')).toBeInTheDocument());
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('calls onCancel when Cancel is pressed', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument());
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(onCancel).toHaveBeenCalledTimes(1);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it('closes on the close button', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByLabelText('Close')).toBeInTheDocument());
    fireEvent.click(screen.getByLabelText('Close'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('closes on Escape', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByRole('dialog')).toBeInTheDocument());
    const dialogEl = screen.getByRole('dialog');
    fireEvent.keyDown(dialogEl, { key: 'Escape' });
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('closes when the backdrop is clicked', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByRole('button', { name: 'Close modal' })).toBeInTheDocument());
    const overlay = screen.getByRole('button', { name: 'Close modal' }) as HTMLElement;
    fireEvent.click(overlay);
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('shows info note about truncate behavior', async () => {
    mountComp({ fileCount: 1, onCancel, onConfirm });
    await waitFor(() => expect(screen.getByText(/Note:/)).toBeInTheDocument());
    expect(screen.getByText(/truncate the selected files to the specified size/)).toBeInTheDocument();
    expect(screen.getByText(/extended with zeros/)).toBeInTheDocument();
    expect(screen.getByText(/only works on files, not directories/)).toBeInTheDocument();
  });
});