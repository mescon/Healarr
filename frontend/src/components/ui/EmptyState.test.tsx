import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '../../test/test-utils';
import userEvent from '@testing-library/user-event';
import { Search } from 'lucide-react';
import EmptyState from './EmptyState';

describe('EmptyState', () => {
  it('renders with title only', () => {
    render(<EmptyState title="No items found" />);

    expect(screen.getByText('No items found')).toBeInTheDocument();
  });

  it('renders with title and description', () => {
    render(
      <EmptyState
        title="No items found"
        description="Try adjusting your search criteria"
      />
    );

    expect(screen.getByText('No items found')).toBeInTheDocument();
    expect(screen.getByText('Try adjusting your search criteria')).toBeInTheDocument();
  });

  it('renders with custom icon', () => {
    render(
      <EmptyState
        title="No search results"
        icon={Search}
      />
    );

    expect(screen.getByText('No search results')).toBeInTheDocument();
    // Icon should be rendered (we can't easily test for the specific icon)
  });

  it('renders action button when provided', () => {
    const onClick = vi.fn();
    render(
      <EmptyState
        title="No items"
        action={{ label: 'Add Item', onClick }}
      />
    );

    expect(screen.getByRole('button', { name: 'Add Item' })).toBeInTheDocument();
  });

  it('calls action onClick when button is clicked', async () => {
    const onClick = vi.fn();
    const user = userEvent.setup();

    render(
      <EmptyState
        title="No items"
        action={{ label: 'Add Item', onClick }}
      />
    );

    await user.click(screen.getByRole('button', { name: 'Add Item' }));

    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('does not render action button when not provided', () => {
    render(<EmptyState title="No items" />);

    expect(screen.queryByRole('button')).not.toBeInTheDocument();
  });

  it('renders compact variant differently', () => {
    const { container: regularContainer } = render(
      <EmptyState title="Regular" />
    );

    const { container: compactContainer } = render(
      <EmptyState title="Compact" compact />
    );

    // Compact variant should have different structure (no icon)
    expect(regularContainer.querySelector('.py-12')).toBeInTheDocument();
    expect(compactContainer.querySelector('.py-12')).not.toBeInTheDocument();
    expect(compactContainer.querySelector('.p-8')).toBeInTheDocument();
  });

  it('renders compact variant with description', () => {
    render(
      <EmptyState
        title="Compact Title"
        description="Compact description"
        compact
      />
    );

    expect(screen.getByText('Compact Title')).toBeInTheDocument();
    expect(screen.getByText('Compact description')).toBeInTheDocument();
  });

  it('hides icon with aria-hidden', () => {
    const { container } = render(<EmptyState title="Test" />);

    const icon = container.querySelector('svg[aria-hidden="true"]');
    expect(icon).toBeInTheDocument();
  });

  it('renders heading as h3 element', () => {
    render(<EmptyState title="Heading Test" />);

    expect(screen.getByRole('heading', { level: 3 })).toHaveTextContent('Heading Test');
  });
});
