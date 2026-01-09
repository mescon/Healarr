/**
 * Shared notification provider configurations.
 * Used by both the Config page and Setup Wizard to ensure
 * consistent provider options and form fields.
 */

// Provider field type definitions
export interface ProviderFieldOption {
    value: string;
    label: string;
}

export interface ProviderField {
    key: string;
    label: string;
    type: 'text' | 'password' | 'number' | 'email' | 'checkbox' | 'select' | 'textarea';
    placeholder?: string;
    options?: ProviderFieldOption[];
    numeric?: boolean; // For select fields where value should be converted to number
}

export interface ProviderConfig {
    label: string;
    icon: string;
    category: string;
    description?: string;
    fields: ProviderField[];
}

// Category definitions for organized dropdown display
export const PROVIDER_CATEGORIES = [
    { key: 'popular', label: 'Popular', emoji: '📱' },
    { key: 'push', label: 'Push Notifications', emoji: '🔔' },
    { key: 'messaging', label: 'Messaging', emoji: '💬' },
    { key: 'team', label: 'Team Collaboration', emoji: '👥' },
    { key: 'automation', label: 'Automation', emoji: '⚡' },
    { key: 'integration', label: 'Integration', emoji: '🌐' },
    { key: 'advanced', label: 'Advanced', emoji: '🔧' },
] as const;

export type ProviderCategory = typeof PROVIDER_CATEGORIES[number]['key'];

// Complete provider configurations - all 21 supported notification providers
export const PROVIDER_CONFIGS: Record<string, ProviderConfig> = {
    // Popular services
    discord: {
        label: 'Discord',
        icon: '🎮',
        category: 'popular',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://discord.com/api/webhooks/...' }
        ]
    },
    slack: {
        label: 'Slack',
        icon: '💬',
        category: 'popular',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/services/...' }
        ]
    },
    telegram: {
        label: 'Telegram',
        icon: '✈️',
        category: 'popular',
        fields: [
            { key: 'bot_token', label: 'Bot Token', type: 'text', placeholder: '123456789:ABC...' },
            { key: 'chat_id', label: 'Chat ID', type: 'text', placeholder: '-1001234567890 or @channel' }
        ]
    },
    email: {
        label: 'Email (SMTP)',
        icon: '📧',
        category: 'popular',
        fields: [
            { key: 'host', label: 'SMTP Host', type: 'text', placeholder: 'smtp.example.com' },
            { key: 'port', label: 'Port', type: 'number', placeholder: '587' },
            { key: 'username', label: 'Username', type: 'text', placeholder: 'Optional' },
            { key: 'password', label: 'Password', type: 'password', placeholder: 'Optional' },
            { key: 'from', label: 'From Address', type: 'email', placeholder: 'healarr@example.com' },
            { key: 'to', label: 'To Address', type: 'email', placeholder: 'you@example.com' },
            { key: 'tls', label: 'Use TLS', type: 'checkbox' }
        ]
    },
    // Push notification services
    pushover: {
        label: 'Pushover',
        icon: '📱',
        category: 'push',
        fields: [
            { key: 'user_key', label: 'User Key', type: 'text', placeholder: 'Your Pushover user key' },
            { key: 'app_token', label: 'App Token', type: 'text', placeholder: 'Your Pushover app token' },
            { key: 'priority', label: 'Priority', type: 'select', numeric: true, options: [
                { value: '-2', label: 'Lowest' },
                { value: '-1', label: 'Low' },
                { value: '0', label: 'Normal' },
                { value: '1', label: 'High' },
                { value: '2', label: 'Emergency' }
            ]},
            { key: 'sound', label: 'Sound', type: 'text', placeholder: 'pushover (optional)' }
        ]
    },
    gotify: {
        label: 'Gotify',
        icon: '🔔',
        category: 'push',
        fields: [
            { key: 'server_url', label: 'Server URL', type: 'text', placeholder: 'https://gotify.example.com' },
            { key: 'app_token', label: 'App Token', type: 'text', placeholder: 'Your Gotify app token' },
            { key: 'priority', label: 'Priority (1-10)', type: 'number', placeholder: '5' }
        ]
    },
    ntfy: {
        label: 'ntfy',
        icon: '📣',
        category: 'push',
        fields: [
            { key: 'server_url', label: 'Server URL (optional)', type: 'text', placeholder: 'https://ntfy.sh' },
            { key: 'topic', label: 'Topic', type: 'text', placeholder: 'healarr-alerts' },
            { key: 'priority', label: 'Priority (1-5)', type: 'number', placeholder: '3' }
        ]
    },
    pushbullet: {
        label: 'Pushbullet',
        icon: '📤',
        category: 'push',
        fields: [
            { key: 'api_token', label: 'Access Token', type: 'text', placeholder: 'Your Pushbullet access token' },
            { key: 'targets', label: 'Targets (optional)', type: 'text', placeholder: 'device/channel/email' }
        ]
    },
    bark: {
        label: 'Bark',
        icon: '🐕',
        category: 'push',
        fields: [
            { key: 'device_key', label: 'Device Key', type: 'text', placeholder: 'Your Bark device key' },
            { key: 'server_url', label: 'Server URL (optional)', type: 'text', placeholder: 'api.day.app' }
        ]
    },
    join: {
        label: 'Join',
        icon: '🔗',
        category: 'push',
        fields: [
            { key: 'api_key', label: 'API Key', type: 'text', placeholder: 'Your Join API key' },
            { key: 'devices', label: 'Devices', type: 'text', placeholder: 'device1,device2 or group.all' }
        ]
    },
    // Messaging apps
    whatsapp: {
        label: 'WhatsApp',
        icon: '💬',
        category: 'messaging',
        fields: [
            { key: 'phone', label: 'Phone Number', type: 'text', placeholder: '+1234567890 (with country code)' },
            { key: 'api_key', label: 'API Key', type: 'text', placeholder: 'Your CallMeBot API key' },
            { key: 'api_url', label: 'API URL (optional)', type: 'text', placeholder: 'https://api.callmebot.com/whatsapp.php' }
        ]
    },
    signal: {
        label: 'Signal',
        icon: '🔒',
        category: 'messaging',
        fields: [
            { key: 'number', label: 'Sender Number', type: 'text', placeholder: '+1234567890 (your registered Signal number)' },
            { key: 'recipient', label: 'Recipient', type: 'text', placeholder: '+1234567890 or group ID' },
            { key: 'api_url', label: 'Signal REST API URL', type: 'text', placeholder: 'http://localhost:8080' }
        ]
    },
    matrix: {
        label: 'Matrix',
        icon: '🔲',
        category: 'messaging',
        fields: [
            { key: 'home_server', label: 'Homeserver URL', type: 'text', placeholder: 'https://matrix.org' },
            { key: 'user', label: 'User ID', type: 'text', placeholder: '@user:matrix.org' },
            { key: 'password', label: 'Password/Token', type: 'password', placeholder: 'Password or access token' },
            { key: 'rooms', label: 'Room IDs', type: 'text', placeholder: '!roomid:server,#alias:server' }
        ]
    },
    // Team collaboration
    teams: {
        label: 'Microsoft Teams',
        icon: '👥',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://xxx.webhook.office.com/webhookb2/...' }
        ]
    },
    googlechat: {
        label: 'Google Chat',
        icon: '💭',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://chat.googleapis.com/v1/spaces/...' }
        ]
    },
    mattermost: {
        label: 'Mattermost',
        icon: '🟣',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://mattermost.example.com/hooks/xxx' },
            { key: 'channel', label: 'Channel (optional)', type: 'text', placeholder: 'town-square' },
            { key: 'username', label: 'Username (optional)', type: 'text', placeholder: 'Healarr' }
        ]
    },
    rocketchat: {
        label: 'Rocket.Chat',
        icon: '🚀',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://rocketchat.example.com/hooks/xxx' },
            { key: 'channel', label: 'Channel (optional)', type: 'text', placeholder: '#general' },
            { key: 'username', label: 'Username (optional)', type: 'text', placeholder: 'Healarr' }
        ]
    },
    zulip: {
        label: 'Zulip',
        icon: '💧',
        category: 'team',
        fields: [
            { key: 'host', label: 'Server Host', type: 'text', placeholder: 'yourorg.zulipchat.com' },
            { key: 'bot_email', label: 'Bot Email', type: 'text', placeholder: 'bot@yourorg.zulipchat.com' },
            { key: 'bot_key', label: 'Bot API Key', type: 'password', placeholder: 'Your bot API key' },
            { key: 'stream', label: 'Stream', type: 'text', placeholder: 'general' },
            { key: 'topic', label: 'Topic', type: 'text', placeholder: 'Healarr Alerts' }
        ]
    },
    // Automation & alerting
    ifttt: {
        label: 'IFTTT',
        icon: '⚡',
        category: 'automation',
        fields: [
            { key: 'webhook_key', label: 'Webhook Key', type: 'text', placeholder: 'Your IFTTT webhook key' },
            { key: 'event', label: 'Event Name', type: 'text', placeholder: 'healarr_alert' }
        ]
    },
    // Integrations (Generic Webhook)
    generic: {
        label: 'Generic Webhook',
        icon: '🌐',
        category: 'integration',
        description: 'Send notifications to any HTTP endpoint. Perfect for integrating with other tools in your media stack.',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://your-service.com/webhook' },
            { key: 'method', label: 'HTTP Method', type: 'select', options: [
                { value: 'POST', label: 'POST' },
                { value: 'GET', label: 'GET' },
                { value: 'PUT', label: 'PUT' }
            ]},
            { key: 'content_type', label: 'Content-Type', type: 'select', options: [
                { value: 'application/json', label: 'application/json' },
                { value: 'text/plain', label: 'text/plain' },
                { value: 'application/x-www-form-urlencoded', label: 'form-urlencoded' }
            ]},
            { key: 'template', label: 'Template', type: 'select', options: [
                { value: '', label: 'None (plain text body)' },
                { value: 'json', label: 'JSON (title + message)' }
            ]},
            { key: 'message_key', label: 'Message Key (JSON)', type: 'text', placeholder: 'message' },
            { key: 'title_key', label: 'Title Key (JSON)', type: 'text', placeholder: 'title' },
            { key: 'custom_headers', label: 'Custom Headers', type: 'textarea', placeholder: 'Authorization=Bearer xxx\nX-Custom-Header=value' },
            { key: 'extra_data', label: 'Extra JSON Data', type: 'textarea', placeholder: 'source=healarr\npriority=high' }
        ]
    },
    // Raw Shoutrrr URL
    custom: {
        label: 'Custom (Shoutrrr URL)',
        icon: '🔧',
        category: 'advanced',
        description: 'Provide a raw Shoutrrr URL for any supported service.',
        fields: [
            { key: 'url', label: 'Shoutrrr URL', type: 'text', placeholder: 'protocol://...' }
        ]
    }
};

// Helper function to get provider by key
export function getProviderConfig(providerKey: string): ProviderConfig | undefined {
    return PROVIDER_CONFIGS[providerKey];
}

// Helper function to get all providers in a category
export function getProvidersByCategory(categoryKey: string): Array<[string, ProviderConfig]> {
    return Object.entries(PROVIDER_CONFIGS).filter(
        ([, config]) => config.category === categoryKey
    );
}

// Helper function to get provider label with fallback
export function getProviderLabel(providerKey: string): string {
    return PROVIDER_CONFIGS[providerKey]?.label || providerKey;
}
