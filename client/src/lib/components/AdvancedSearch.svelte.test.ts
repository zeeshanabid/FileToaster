import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { mount, unmount, tick } from 'svelte';
import { fireEvent, screen, within, waitFor } from '@testing-library/dom';
import userEvent from '@testing-library/user-event';
import AdvancedSearch from './AdvancedSearch.svelte';
import { defaultAdvancedFilters, type AdvancedFilters } from '$lib/store/search';

const instances: unknown[] = [];
function mountComp(props: unknown): HTMLElement {
  const target = document.createElement('div');
  document.body.appendChild(target);
  instances.push(mount(AdvancedSearch as never, { target, props } as never));
  return target;
}
function unmountAll() {
  while (instances.length) unmount(instances.pop() as never);
  document.querySelectorAll('body > div').forEach((el) => el.remove());
}
function dialog(): HTMLElement {
  return screen.getByTestId('advanced-search-dialog') as HTMLElement;
}

describe('AdvancedSearch', () => {
  const onClose = vi.fn();
  const onApply = vi.fn<(f: AdvancedFilters) => void>();

  beforeEach(() => {
    onClose.mockClear();
    onApply.mockClear();
  });
  afterEach(() => unmountAll());

  function render(filters: AdvancedFilters = defaultAdvancedFilters()): void {
    mountComp({ filters, onClose, onApply });
  }

  it('renders the section headings', () => {
    render();
    const d = dialog();
    expect(within(d).getByText('File types')).toBeInTheDocument();
    expect(within(d).getByText('File extensions')).toBeInTheDocument();
    expect(within(d).getByText('Created')).toBeInTheDocument();
    expect(within(d).getByText('Modified')).toBeInTheDocument();
    expect(within(d).getByText('Size')).toBeInTheDocument();
  });

  it('includes the catalogue MIME options', () => {
    render();
    expect(within(dialog()).getByLabelText('Images')).toBeInTheDocument();
    expect(within(dialog()).getByLabelText('Videos')).toBeInTheDocument();
  });

  it('applies a selected MIME type', async () => {
    const user = userEvent.setup();
    render();
    await user.click(within(dialog()).getByLabelText('Images'));
    await user.click(within(dialog()).getByText('Apply'));
    expect(onApply).toHaveBeenCalledTimes(1);
    expect(onApply.mock.calls[0][0].mimeTypes).toContain('image/*');
  });

  it('adds a custom MIME type via the Add button', async () => {
    const user = userEvent.setup();
    render();
    const input = within(dialog()).getByLabelText('Custom MIME type') as HTMLInputElement;
    await user.type(input, 'image/png');
    await user.click(within(dialog()).getByLabelText('Add MIME type'));
    await user.click(within(dialog()).getByText('Apply'));
    expect(onApply.mock.calls[0][0].mimeTypes).toContain('image/png');
  });

  it('adds a normalized extension on Enter', async () => {
    const user = userEvent.setup();
    render();
    const input = within(dialog()).getByLabelText('Custom extension') as HTMLInputElement;
    await user.type(input, '.TXT{enter}');
    await user.click(within(dialog()).getByText('Apply'));
    expect(onApply.mock.calls[0][0].extensions).toContain('txt');
  });

  it('reveals the custom date inputs only for the custom preset', async () => {
    render();
    expect(within(dialog()).queryByLabelText('Created after')).not.toBeInTheDocument();
    fireEvent.change(within(dialog()).getByLabelText('Created date filter'), { target: { value: 'custom' } });
    await tick();
    expect(within(dialog()).getByLabelText('Created after')).toBeInTheDocument();
    expect(within(dialog()).getByLabelText('Created before')).toBeInTheDocument();
  });

  it('carries a size range and unit into the applied filters', async () => {
    const user = userEvent.setup();
    render();
    const min = within(dialog()).getByLabelText('Minimum size');
    const max = within(dialog()).getByLabelText('Maximum size');
    fireEvent.input(min, { target: { value: '2' } });
    fireEvent.input(max, { target: { value: '8' } });
    await user.selectOptions(within(dialog()).getByLabelText('Size unit'), 'MB');
    await user.click(within(dialog()).getByText('Apply'));
    const applied = onApply.mock.calls[0][0];
    expect(applied.minSize).toBe(2);
    expect(applied.maxSize).toBe(8);
    expect(applied.sizeUnit).toBe('MB');
  });

  it('reset restores the defaults', async () => {
    const user = userEvent.setup();
    render({ ...defaultAdvancedFilters(), mimeTypes: ['image/*'], minSize: 5 });
    expect(within(dialog()).getByText('image/*')).toBeInTheDocument();
    await user.click(within(dialog()).getByText('Reset'));
    await tick();
    expect(within(dialog()).queryByText('image/*')).not.toBeInTheDocument();
    // A rebuilt default has no MIME selections.
    const checked = within(dialog())
      .queryAllByRole('checkbox')
      .filter((c) => (c as HTMLInputElement).checked);
    expect(checked.length).toBe(0);
  });

  it('cancel and escape call onClose', async () => {
    render();
    fireEvent.keyDown(dialog(), { key: 'Escape' });
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    onClose.mockClear();
    fireEvent.click(within(dialog()).getByText('Cancel'));
    expect(onClose).toHaveBeenCalled();
  });
});
