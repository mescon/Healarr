import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act, fireEvent } from '../../test/test-utils';
import Toast from './Toast';

describe('Toast', () => {
  const mockOnClose = vi.fn();

  beforeEach(() => {
    mockOnClose.mockClear();
  });

  afterEach(() => {
    vi.clearAllTimers();
  });

  it('renders success toast with message', () => {
    render(
      <Toast
        id="test-1"
        type="success"
        message="Operation successful"
        onClose={mockOnClose}
      />
    );

    expect(screen.getByText('Operation successful')).toBeInTheDocument();
  });

  it('renders error toast with message', () => {
    render(
      <Toast
        id="test-2"
        type="error"
        message="Something went wrong"
        onClose={mockOnClose}
      />
    );

    expect(screen.getByText('Something went wrong')).toBeInTheDocument();
  });

  it('renders warning toast with message', () => {
    render(
      <Toast
        id="test-3"
        type="warning"
        message="Warning message"
        onClose={mockOnClose}
      />
    );

    expect(screen.getByText('Warning message')).toBeInTheDocument();
  });

  it('renders info toast with message', () => {
    render(
      <Toast
        id="test-4"
        type="info"
        message="Info message"
        onClose={mockOnClose}
      />
    );

    expect(screen.getByText('Info message')).toBeInTheDocument();
  });

  it('has accessible close button', () => {
    render(
      <Toast
        id="test-5"
        type="success"
        message="Test message"
        onClose={mockOnClose}
      />
    );

    const closeButton = screen.getByRole('button', { name: /close notification/i });
    expect(closeButton).toBeInTheDocument();
  });

  it('calls onClose when close button is clicked', () => {
    render(
      <Toast
        id="test-6"
        type="success"
        message="Test message"
        onClose={mockOnClose}
      />
    );

    const closeButton = screen.getByRole('button', { name: /close notification/i });
    fireEvent.click(closeButton);

    expect(mockOnClose).toHaveBeenCalledTimes(1);
  });

  it('auto-dismisses after duration', () => {
    vi.useFakeTimers();

    render(
      <Toast
        id="test-7"
        type="success"
        message="Auto dismiss test"
        onClose={mockOnClose}
        duration={5000}
      />
    );

    expect(mockOnClose).not.toHaveBeenCalled();

    // Fast-forward past the duration
    act(() => {
      vi.advanceTimersByTime(5100);
    });

    expect(mockOnClose).toHaveBeenCalled();

    vi.useRealTimers();
  });

  it('pauses auto-dismiss on mouse enter', () => {
    vi.useFakeTimers();

    render(
      <Toast
        id="test-8"
        type="success"
        message="Pause test"
        onClose={mockOnClose}
        duration={5000}
      />
    );

    // Advance time halfway
    act(() => {
      vi.advanceTimersByTime(2500);
    });

    // Hover over the toast (using fireEvent since we're in fake timers)
    const toastElement = screen.getByText('Pause test').closest('[class*="toast"]') ||
                         screen.getByText('Pause test').parentElement;
    if (toastElement) {
      fireEvent.mouseEnter(toastElement);
    }

    // Advance past what would be the original duration
    act(() => {
      vi.advanceTimersByTime(5000);
    });

    // Should NOT have been called because we're hovering (isPaused = true)
    expect(mockOnClose).not.toHaveBeenCalled();

    vi.useRealTimers();
  });
});
