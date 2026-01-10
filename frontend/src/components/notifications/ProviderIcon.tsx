/**
 * ProviderIcon - Displays the appropriate icon for a notification provider.
 * Uses SVG icons when available, falls back to emojis.
 */
import clsx from 'clsx';

// Provider icon paths map - relative paths, will be prefixed with BASE_URL
const PROVIDER_ICON_FILES: Record<string, string> = {
    discord: 'icons/notifications/discord.svg',
    slack: 'icons/notifications/slack.svg',
    telegram: 'icons/notifications/telegram.svg',
    pushover: 'icons/notifications/pushover.svg',
    gotify: 'icons/notifications/gotify.svg',
    ntfy: 'icons/notifications/ntfy.svg',
    pushbullet: 'icons/notifications/pushbullet.svg',
    bark: 'icons/notifications/bark.png',
    whatsapp: 'icons/notifications/whatsapp.svg',
    signal: 'icons/notifications/signal.svg',
    matrix: 'icons/notifications/matrix.svg',
    teams: 'icons/notifications/teams.svg',
    googlechat: 'icons/notifications/google-chat.svg',
    mattermost: 'icons/notifications/mattermost.svg',
    rocketchat: 'icons/notifications/rocketchat.svg',
    zulip: 'icons/notifications/zulip.svg',
    ifttt: 'icons/notifications/ifttt-dark.svg',
    generic: 'icons/notifications/webhook.svg',
};

// Build full paths with BASE_URL prefix for subdirectory support
export const PROVIDER_ICON_PATHS: Record<string, string> = Object.fromEntries(
    Object.entries(PROVIDER_ICON_FILES).map(([key, path]) => [
        key,
        `${import.meta.env.BASE_URL}${path}`
    ])
);

// Fallback emojis for providers without SVG icons
export const PROVIDER_EMOJI_FALLBACK: Record<string, string> = {
    email: '📧',
    join: '🔗',
    custom: '🔧',
};

interface ProviderIconProps {
    provider: string;
    className?: string;
}

export function ProviderIcon({ provider, className = "w-5 h-5" }: ProviderIconProps) {
    const iconPath = PROVIDER_ICON_PATHS[provider];
    if (iconPath) {
        // Matrix logo is white, so invert it on light mode to make it visible
        const needsInvert = provider === 'matrix';
        return (
            <img
                src={iconPath}
                alt={provider}
                className={clsx(className, needsInvert && "invert dark:invert-0")}
            />
        );
    }
    return <span className="text-lg">{PROVIDER_EMOJI_FALLBACK[provider] || '📢'}</span>;
}

export default ProviderIcon;
