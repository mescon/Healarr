import { setupServer } from 'msw/node';
import { handlers } from './handlers';

// Create the MSW server with the default handlers
export const server = setupServer(...handlers);
