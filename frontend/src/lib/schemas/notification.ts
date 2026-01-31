import { z } from 'zod';

/**
 * Supported notification provider types.
 */
export const notificationProviderTypes = [
  'discord',
  'slack',
  'telegram',
  'email',
  'webhook',
  'gotify',
  'ntfy',
  'pushover',
] as const;

/**
 * Base notification schema with common fields.
 */
const notificationBaseSchema = z.object({
  name: z
    .string()
    .min(1, 'Name is required')
    .max(100, 'Name must be 100 characters or less'),
  provider_type: z.enum(notificationProviderTypes, {
    message: 'Please select a notification provider',
  }),
  events: z
    .array(z.string())
    .min(1, 'Select at least one event to notify on'),
  enabled: z.boolean(),
  throttle_seconds: z
    .number()
    .min(0, 'Cannot be negative')
    .max(86400, 'Maximum 24 hours (86400 seconds)')
    .optional()
    .default(0),
});

/**
 * Provider-specific config schemas.
 * These validate the nested `config` object based on provider type.
 */
/**
 * Provider-specific config schemas.
 * Field names MUST match the `key` values in notificationProviders.ts PROVIDER_CONFIGS.
 */

const webhookConfigSchema = z.object({
  url: z.string().url('Must be a valid webhook URL'),
});

const discordConfigSchema = z.object({
  webhook_url: z.string().url('Must be a valid Discord webhook URL'),
});

const slackConfigSchema = z.object({
  webhook_url: z.string().url('Must be a valid Slack webhook URL'),
});

const telegramConfigSchema = z.object({
  bot_token: z.string().min(1, 'Bot token is required'),
  chat_id: z.string().min(1, 'Chat ID is required'),
});

const emailConfigSchema = z.object({
  host: z.string().min(1, 'SMTP host is required'),
  port: z.string().or(z.number()).optional(),
  username: z.string().optional(),
  password: z.string().optional(),
  from: z.string().email('Must be a valid email address'),
  to: z.string().min(1, 'At least one recipient is required'),
  tls: z.boolean().optional(),
});

const gotifyConfigSchema = z.object({
  server_url: z.string().url('Must be a valid Gotify server URL'),
  app_token: z.string().min(1, 'Application token is required'),
  priority: z.number().optional(),
});

const ntfyConfigSchema = z.object({
  server_url: z.string().optional(),
  topic: z.string().min(1, 'Topic is required'),
  priority: z.number().optional(),
});

const pushoverConfigSchema = z.object({
  user_key: z.string().min(1, 'User key is required'),
  app_token: z.string().min(1, 'API token is required'),
  priority: z.number().optional(),
  sound: z.string().optional(),
});

/**
 * Map of provider types to their config schemas.
 */
export const providerConfigSchemas = {
  discord: discordConfigSchema,
  slack: slackConfigSchema,
  telegram: telegramConfigSchema,
  email: emailConfigSchema,
  webhook: webhookConfigSchema,
  gotify: gotifyConfigSchema,
  ntfy: ntfyConfigSchema,
  pushover: pushoverConfigSchema,
} as const;

/**
 * Full notification schema with config validation.
 * Note: Config validation is done separately based on provider_type.
 */
export const notificationSchema = notificationBaseSchema.extend({
  config: z.record(z.string(), z.unknown()).default({}),
});

/**
 * Type inferred from the schema for use in components.
 */
export type NotificationInput = z.infer<typeof notificationSchema>;

/**
 * Result type from safeParse for validation functions.
 */
type SafeParseResult<T> = { success: true; data: T } | { success: false; error: z.ZodError };

/**
 * Validate notification config based on provider type.
 * Call this separately after base validation.
 */
export function validateNotificationConfig(
  providerType: string,
  config: Record<string, unknown>
): SafeParseResult<unknown> {
  const schema = providerConfigSchemas[providerType as keyof typeof providerConfigSchemas];
  if (!schema) {
    // Unknown provider, skip config validation
    return { success: true, data: config };
  }
  return schema.safeParse(config);
}
