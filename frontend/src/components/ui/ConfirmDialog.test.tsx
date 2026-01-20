import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor } from '../../test/test-utils';
import userEvent from '@testing-library/user-event';
import ConfirmDialog from './ConfirmDialog';

describe('ConfirmDialog', () => {
  const defaultProps = {
    isOpen: true,
    title: 'Confirm Action',
    message: 'Are you sure you want to proceed?',
    onConfirm: vi.fn(),
    onCancel: vi.fn(),
  };

  it('renders when open', () => {
    render(<ConfirmDialog {...defaultProps} />);

    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('Confirm Action')).toBeInTheDocument();
    expect(screen.getByText('Are you sure you want to proceed?')).toBeInTheDocument();
  });

  it('does not render when closed', () => {
    render(<ConfirmDialog {...defaultProps} isOpen={false} />);

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renders with default button labels', () => {
    render(<ConfirmDialog {...defaultProps} />);

    expect(screen.getByRole('button', { name: 'Confirm' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeInTheDocument();
  });

  it('renders with custom button labels', () => {
    render(
      <ConfirmDialog
        {...defaultProps}
        confirmLabel="Delete"
        cancelLabel="Keep"
      />
    );

    expect(screen.getByRole('button', { name: 'Delete' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Keep' })).toBeInTheDocument();
  });

  it('calls onConfirm when confirm button is clicked', async () => {
    const onConfirm = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onConfirm={onConfirm} />);

    await user.click(screen.getByRole('button', { name: 'Confirm' }));

    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel when cancel button is clicked', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} />);

    await user.click(screen.getByRole('button', { name: 'Cancel' }));

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel when close button is clicked', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} />);

    const closeButton = screen.getByRole('button', { name: /close dialog/i });
    await user.click(closeButton);

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('calls onCancel when Escape key is pressed', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} />);

    await user.keyboard('{Escape}');

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('does not call onCancel on Escape when loading', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} isLoading />);

    await user.keyboard('{Escape}');

    expect(onCancel).not.toHaveBeenCalled();
  });

  it('disables buttons when loading', () => {
    render(<ConfirmDialog {...defaultProps} isLoading />);

    expect(screen.getByRole('button', { name: /loading/i })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
  });

  it('has proper accessibility attributes', () => {
    render(<ConfirmDialog {...defaultProps} />);

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAttribute('aria-labelledby', 'confirm-dialog-title');
    expect(dialog).toHaveAttribute('aria-describedby', 'confirm-dialog-message');
  });

  it('focuses confirm button when opened', async () => {
    render(<ConfirmDialog {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus();
    });
  });

  it('renders danger variant with red styling', () => {
    render(<ConfirmDialog {...defaultProps} variant="danger" />);

    const confirmButton = screen.getByRole('button', { name: 'Confirm' });
    expect(confirmButton).toHaveClass('bg-red-500');
  });

  it('renders warning variant with amber styling', () => {
    render(<ConfirmDialog {...defaultProps} variant="warning" />);

    const confirmButton = screen.getByRole('button', { name: 'Confirm' });
    expect(confirmButton).toHaveClass('bg-amber-500');
  });

  it('renders info variant with blue styling', () => {
    render(<ConfirmDialog {...defaultProps} variant="info" />);

    const confirmButton = screen.getByRole('button', { name: 'Confirm' });
    expect(confirmButton).toHaveClass('bg-blue-500');
  });

  it('calls onCancel when clicking backdrop', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} />);

    // Click on the backdrop (the outer div with the dialog role)
    const backdrop = screen.getByRole('dialog');
    await user.click(backdrop);

    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  it('does not call onCancel when clicking dialog content', async () => {
    const onCancel = vi.fn();
    const user = userEvent.setup();

    render(<ConfirmDialog {...defaultProps} onCancel={onCancel} />);

    // Click on the title (inside the dialog content)
    await user.click(screen.getByText('Confirm Action'));

    expect(onCancel).not.toHaveBeenCalled();
  });
});
