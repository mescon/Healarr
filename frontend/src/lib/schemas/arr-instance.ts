import { z } from 'zod';

/**
 * Validation schema for *arr instance configuration.
 * Used when adding or editing Sonarr/Radarr/Whisparr instances.
 */
export const arrInstanceSchema = z.object({
  name: z
    .string()
    .min(1, 'Name is required')
    .max(100, 'Name must be 100 characters or less'),
  type: z.enum(['sonarr', 'radarr', 'whisparr-v2', 'whisparr-v3'], {
    message: 'Please select an instance type',
  }),
  url: z
    .string()
    .min(1, 'URL is required')
    .url('Must be a valid URL (e.g., http://localhost:8989)'),
  api_key: z
    .string()
    .min(32, 'API key must be at least 32 characters')
    .max(64, 'API key must be 64 characters or less'),
  enabled: z.boolean(),
});

/**
 * Type inferred from the schema for use in components.
 */
export type ArrInstanceInput = z.infer<typeof arrInstanceSchema>;

/**
 * Schema for partial updates (all fields optional).
 */
export const arrInstancePartialSchema = arrInstanceSchema.partial();
export type ArrInstancePartialInput = z.infer<typeof arrInstancePartialSchema>;
