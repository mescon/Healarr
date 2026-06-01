import { z } from 'zod';
import { absolutePathErrorMessage, isAbsolutePath } from '../paths';

/**
 * Validation schema for scan path configuration.
 * Used when adding or editing media scan paths.
 */
export const scanPathSchema = z.object({
  local_path: z
    .string()
    .min(1, 'Path is required')
    .refine(isAbsolutePath, { message: absolutePathErrorMessage }),
  arr_path: z
    .string()
    .min(1, 'Arr path is required')
    .refine(isAbsolutePath, { message: absolutePathErrorMessage }),
  arr_instance_id: z
    .number()
    .nullable()
    .refine((id) => id === null || id > 0, {
      message: 'Select an Arr instance',
    }),
  enabled: z.boolean(),
  auto_remediate: z.boolean(),
  dry_run: z.boolean().optional().default(false),
  detection_method: z
    .enum(['zero_byte', 'ffprobe', 'mediainfo', 'handbrake'])
    .optional()
    .default('ffprobe'),
  detection_mode: z.enum(['quick', 'thorough']).optional().default('quick'),
  detection_args: z.string().optional(),
  max_retries: z
    .number()
    .min(0, 'Cannot be negative')
    .max(10, 'Maximum 10 retries allowed')
    .optional()
    .default(3),
  verification_timeout_hours: z
    .number()
    .min(1, 'Must be at least 1 hour')
    .max(168, 'Maximum 168 hours (7 days)')
    .nullable()
    .optional(),

  // Per-path overrides for the global scan tunables. null = "inherit the
  // global" (the existing behaviour); a numeric/string value wins for this
  // specific path. Bounds mirror the catalog entries on the backend
  // (internal/repository/tunables.go).
  thorough_duration_seconds: z
    .number()
    .min(0, 'Cannot be negative')
    .max(86400, 'Maximum 24 hours')
    .nullable()
    .optional(),
  thorough_timeout_seconds: z
    .number()
    .min(30, 'Must be at least 30 seconds')
    .max(21600, 'Maximum 6 hours')
    .nullable()
    .optional(),
  hwaccel: z
    .enum(['auto', 'off', 'cuda', 'vaapi', 'qsv', 'videotoolbox', 'vdpau', 'drm'])
    .nullable()
    .optional(),
});

/**
 * Type inferred from the schema for use in components.
 */
export type ScanPathInput = z.infer<typeof scanPathSchema>;

/**
 * Schema for partial updates (all fields optional).
 */
export const scanPathPartialSchema = scanPathSchema.partial();
export type ScanPathPartialInput = z.infer<typeof scanPathPartialSchema>;
