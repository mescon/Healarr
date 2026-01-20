/**
 * Centralized validation schemas using Zod.
 *
 * These schemas provide:
 * - Runtime validation for form inputs
 * - TypeScript type inference
 * - User-friendly error messages
 *
 * @example
 * import { arrInstanceSchema } from '@/lib/schemas';
 *
 * const result = arrInstanceSchema.safeParse(formData);
 * if (!result.success) {
 *   // Handle validation errors
 *   console.log(result.error.flatten().fieldErrors);
 * }
 */

export * from './arr-instance';
export * from './scan-path';
export * from './notification';
