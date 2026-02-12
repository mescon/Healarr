/* eslint-disable react-refresh/only-export-components -- test utility file, not subject to HMR */
import React, { type ReactElement } from 'react';
import { render, type RenderOptions } from '@testing-library/react';
import { BrowserRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ToastProvider } from '../contexts/ToastContext';
import { ThemeProvider } from '../contexts/ThemeContext';

/**
 * Create a fresh QueryClient for each test to prevent state leaking between tests.
 * This is important for test isolation.
 */
export function createTestQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        // Disable retries in tests for faster failures
        retry: false,
        // Disable garbage collection timeout in tests
        gcTime: Infinity,
        // Don't refetch on window focus during tests
        refetchOnWindowFocus: false,
      },
      mutations: {
        retry: false,
      },
    },
  });
}

interface AllProvidersProps {
  children: React.ReactNode;
}

/**
 * Wrapper component that provides all necessary context providers for testing.
 * This mimics the provider structure in App.tsx.
 */
function AllProviders({ children }: AllProvidersProps) {
  const queryClient = createTestQueryClient();

  return (
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <ThemeProvider>
          <ToastProvider>
            {children}
          </ToastProvider>
        </ThemeProvider>
      </BrowserRouter>
    </QueryClientProvider>
  );
}

/**
 * Custom render function that wraps components with all providers.
 * Use this instead of @testing-library/react's render.
 *
 * @example
 * import { render, screen } from '@/test/test-utils';
 *
 * test('renders component', () => {
 *   render(<MyComponent />);
 *   expect(screen.getByText('Hello')).toBeInTheDocument();
 * });
 */
function customRender(
  ui: ReactElement,
  options?: Omit<RenderOptions, 'wrapper'>
) {
  return render(ui, { wrapper: AllProviders, ...options });
}

/**
 * Create a render function with a custom QueryClient.
 * Useful when you need to inspect query state or pre-populate cache.
 *
 * @example
 * const queryClient = createTestQueryClient();
 * const { render } = createRenderWithQueryClient(queryClient);
 * render(<MyComponent />);
 * expect(queryClient.getQueryState(['key'])).toBeDefined();
 */
export function createRenderWithQueryClient(queryClient: QueryClient) {
  function CustomWrapper({ children }: AllProvidersProps) {
    return (
      <QueryClientProvider client={queryClient}>
        <BrowserRouter>
          <ThemeProvider>
            <ToastProvider>
              {children}
            </ToastProvider>
          </ThemeProvider>
        </BrowserRouter>
      </QueryClientProvider>
    );
  }

  return {
    render: (ui: ReactElement, options?: Omit<RenderOptions, 'wrapper'>) =>
      render(ui, { wrapper: CustomWrapper, ...options }),
    queryClient,
  };
}

// Re-export everything from @testing-library/react
export * from '@testing-library/react';
export { default as userEvent } from '@testing-library/user-event';

// Override render with our custom version
export { customRender as render };
