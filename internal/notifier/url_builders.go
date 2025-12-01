package notifier

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// URLBuilder defines the interface for provider-specific URL building
type URLBuilder interface {
	BuildURL(config json.RawMessage) (string, error)
}

// urlBuilders maps provider types to their URL builders
var urlBuilders = map[string]URLBuilder{
	ProviderDiscord:    &discordBuilder{},
	ProviderPushover:   &pushoverBuilder{},
	ProviderTelegram:   &telegramBuilder{},
	ProviderSlack:      &slackBuilder{},
	ProviderEmail:      &emailBuilder{},
	ProviderGotify:     &gotifyBuilder{},
	ProviderNtfy:       &ntfyBuilder{},
	ProviderWhatsApp:   &whatsAppBuilder{},
	ProviderSignal:     &signalBuilder{},
	ProviderBark:       &barkBuilder{},
	ProviderGoogleChat: &googleChatBuilder{},
	ProviderIFTTT:      &iftttBuilder{},
	ProviderJoin:       &joinBuilder{},
	ProviderMattermost: &mattermostBuilder{},
	ProviderMatrix:     &matrixBuilder{},
	ProviderPushbullet: &pushbulletBuilder{},
	ProviderRocketchat: &rocketchatBuilder{},
	ProviderTeams:      &teamsBuilder{},
	ProviderZulip:      &zulipBuilder{},
	ProviderGeneric:    &genericBuilder{},
	ProviderCustom:     &customBuilder{},
}

// discordBuilder builds Discord shoutrrr URLs
type discordBuilder struct{}

func (b *discordBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c DiscordConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return convertDiscordWebhook(c.WebhookURL)
}

// pushoverBuilder builds Pushover shoutrrr URLs
type pushoverBuilder struct{}

func (b *pushoverBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c PushoverConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	u := fmt.Sprintf("pushover://shoutrrr:%s@%s/", c.AppToken, c.UserKey)
	params := url.Values{}
	if c.Priority != 0 {
		params.Set("priority", fmt.Sprintf("%d", c.Priority))
	}
	if c.Sound != "" {
		params.Set("sound", c.Sound)
	}
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u, nil
}

// telegramBuilder builds Telegram shoutrrr URLs
type telegramBuilder struct{}

func (b *telegramBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c TelegramConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return fmt.Sprintf("telegram://%s@telegram?chats=%s", c.BotToken, c.ChatID), nil
}

// slackBuilder builds Slack shoutrrr URLs
type slackBuilder struct{}

func (b *slackBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c SlackConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return convertSlackWebhook(c.WebhookURL)
}

// emailBuilder builds Email shoutrrr URLs
type emailBuilder struct{}

func (b *emailBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c EmailConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	auth := ""
	if c.Username != "" {
		auth = url.QueryEscape(c.Username)
		if c.Password != "" {
			auth += ":" + url.QueryEscape(c.Password)
		}
		auth += "@"
	}
	scheme := "smtp"
	if c.TLS {
		scheme = "smtps"
	}
	return fmt.Sprintf("%s://%s%s:%d/?from=%s&to=%s",
		scheme, auth, c.Host, c.Port,
		url.QueryEscape(c.From), url.QueryEscape(c.To)), nil
}

// gotifyBuilder builds Gotify shoutrrr URLs
type gotifyBuilder struct{}

func (b *gotifyBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c GotifyConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	serverURL := strings.TrimPrefix(c.ServerURL, "https://")
	serverURL = strings.TrimPrefix(serverURL, "http://")
	u := fmt.Sprintf("gotify://%s/%s", serverURL, c.AppToken)
	if c.Priority > 0 {
		u += fmt.Sprintf("?priority=%d", c.Priority)
	}
	return u, nil
}

// ntfyBuilder builds Ntfy shoutrrr URLs
type ntfyBuilder struct{}

func (b *ntfyBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c NtfyConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	serverURL := c.ServerURL
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	serverURL = strings.TrimPrefix(serverURL, "https://")
	serverURL = strings.TrimPrefix(serverURL, "http://")
	u := fmt.Sprintf("ntfy://%s/%s", serverURL, c.Topic)
	if c.Priority > 0 {
		u += fmt.Sprintf("?priority=%d", c.Priority)
	}
	return u, nil
}

// whatsAppBuilder builds WhatsApp shoutrrr URLs
type whatsAppBuilder struct{}

func (b *whatsAppBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c WhatsAppConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	apiURL := c.APIURL
	if apiURL == "" {
		apiURL = "https://api.callmebot.com/whatsapp.php"
	}
	apiURL = strings.TrimPrefix(apiURL, "https://")
	apiURL = strings.TrimPrefix(apiURL, "http://")
	return fmt.Sprintf("generic+https://%s?phone=%s&apikey=%s", apiURL, url.QueryEscape(c.Phone), url.QueryEscape(c.APIKey)), nil
}

// signalBuilder builds Signal shoutrrr URLs
type signalBuilder struct{}

func (b *signalBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c SignalConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	apiURL := c.APIURL
	if apiURL == "" {
		return "", fmt.Errorf("signal API URL is required (e.g., http://localhost:8080)")
	}
	apiURL = strings.TrimSuffix(apiURL, "/")
	apiURL = strings.TrimPrefix(apiURL, "https://")
	apiURL = strings.TrimPrefix(apiURL, "http://")
	return fmt.Sprintf("generic+http://%s/v2/send?number=%s&recipients=%s", apiURL, url.QueryEscape(c.Number), url.QueryEscape(c.Recipient)), nil
}

// barkBuilder builds Bark shoutrrr URLs
type barkBuilder struct{}

func (b *barkBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c BarkConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	serverURL := c.ServerURL
	if serverURL == "" {
		serverURL = "api.day.app"
	}
	serverURL = strings.TrimPrefix(serverURL, "https://")
	serverURL = strings.TrimPrefix(serverURL, "http://")
	return fmt.Sprintf("bark://%s@%s", c.DeviceKey, serverURL), nil
}

// googleChatBuilder builds Google Chat shoutrrr URLs
type googleChatBuilder struct{}

func (b *googleChatBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c GoogleChatConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	u, err := url.Parse(c.WebhookURL)
	if err != nil {
		return "", fmt.Errorf("invalid Google Chat webhook URL: %w", err)
	}
	return fmt.Sprintf("googlechat://%s%s?%s", u.Host, u.Path, u.RawQuery), nil
}

// iftttBuilder builds IFTTT shoutrrr URLs
type iftttBuilder struct{}

func (b *iftttBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c IFTTTConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return fmt.Sprintf("ifttt://%s/?events=%s", c.WebhookKey, c.Event), nil
}

// joinBuilder builds Join shoutrrr URLs
type joinBuilder struct{}

func (b *joinBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c JoinConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return fmt.Sprintf("join://shoutrrr:%s@join/?devices=%s", c.APIKey, c.Devices), nil
}

// mattermostBuilder builds Mattermost shoutrrr URLs
type mattermostBuilder struct{}

func (b *mattermostBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c MattermostConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	u, err := url.Parse(c.WebhookURL)
	if err != nil {
		return "", fmt.Errorf("invalid Mattermost webhook URL: %w", err)
	}
	token := strings.TrimPrefix(u.Path, "/hooks/")
	result := fmt.Sprintf("mattermost://%s/%s", u.Host, token)
	if c.Channel != "" {
		result += "/" + c.Channel
	}
	return result, nil
}

// matrixBuilder builds Matrix shoutrrr URLs
type matrixBuilder struct{}

func (b *matrixBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c MatrixConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	host := strings.TrimPrefix(c.HomeServer, "https://")
	host = strings.TrimPrefix(host, "http://")
	result := fmt.Sprintf("matrix://%s:%s@%s", url.QueryEscape(c.User), url.QueryEscape(c.Password), host)
	if c.Rooms != "" {
		result += "/?rooms=" + url.QueryEscape(c.Rooms)
	}
	return result, nil
}

// pushbulletBuilder builds Pushbullet shoutrrr URLs
type pushbulletBuilder struct{}

func (b *pushbulletBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c PushbulletConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	result := fmt.Sprintf("pushbullet://%s", c.APIToken)
	if c.Targets != "" {
		result += "/" + c.Targets
	}
	return result, nil
}

// rocketchatBuilder builds Rocketchat shoutrrr URLs
type rocketchatBuilder struct{}

func (b *rocketchatBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c RocketchatConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	u, err := url.Parse(c.WebhookURL)
	if err != nil {
		return "", fmt.Errorf("invalid Rocketchat webhook URL: %w", err)
	}
	token := strings.TrimPrefix(u.Path, "/hooks/")
	result := fmt.Sprintf("rocketchat://%s/%s", u.Host, token)
	if c.Channel != "" {
		result += "/" + c.Channel
	}
	return result, nil
}

// teamsBuilder builds Microsoft Teams shoutrrr URLs
type teamsBuilder struct{}

func (b *teamsBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c TeamsConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	u, err := url.Parse(c.WebhookURL)
	if err != nil {
		return "", fmt.Errorf("invalid Teams webhook URL: %w", err)
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/webhookb2/"), "/")
	if len(parts) < 4 {
		return "", fmt.Errorf("invalid Teams webhook URL format")
	}
	groupTenant := strings.Split(parts[0], "@")
	if len(groupTenant) != 2 {
		return "", fmt.Errorf("invalid Teams webhook URL format: missing group@tenant")
	}
	return fmt.Sprintf("teams://%s@%s/%s/%s?host=%s", groupTenant[0], groupTenant[1], parts[2], parts[3], u.Host), nil
}

// zulipBuilder builds Zulip shoutrrr URLs
type zulipBuilder struct{}

func (b *zulipBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c ZulipConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	host := strings.TrimPrefix(c.Host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return fmt.Sprintf("zulip://%s:%s@%s/?stream=%s&topic=%s",
		url.QueryEscape(c.BotEmail), url.QueryEscape(c.BotKey), host,
		url.QueryEscape(c.Stream), url.QueryEscape(c.Topic)), nil
}

// genericBuilder builds Generic webhook shoutrrr URLs
type genericBuilder struct{}

func (b *genericBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c GenericConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	targetURL := c.WebhookURL
	if !strings.HasPrefix(targetURL, "http") {
		targetURL = "https://" + targetURL
	}

	params := url.Values{}
	if c.Template != "" {
		params.Set("template", c.Template)
	}
	if c.MessageKey != "" && c.MessageKey != "message" {
		params.Set("messageKey", c.MessageKey)
	}
	if c.TitleKey != "" && c.TitleKey != "title" {
		params.Set("titleKey", c.TitleKey)
	}
	if c.ContentType != "" && c.ContentType != "application/json" {
		params.Set("contenttype", c.ContentType)
	}
	if c.Method != "" && c.Method != "POST" {
		params.Set("requestmethod", c.Method)
	}

	// Parse custom headers (@key=value format)
	if c.CustomHeaders != "" {
		for _, line := range strings.Split(c.CustomHeaders, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				params.Set("@"+parts[0], parts[1])
			}
		}
	}

	// Parse extra data ($key=value format)
	if c.ExtraData != "" {
		for _, line := range strings.Split(c.ExtraData, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				params.Set("$"+parts[0], parts[1])
			}
		}
	}

	result := "generic+" + targetURL
	if len(params) > 0 {
		// Need to use generic:// format for params
		u, _ := url.Parse(targetURL)
		host := u.Host + u.Path
		result = "generic://" + host + "?" + params.Encode()
	}
	return result, nil
}

// customBuilder handles Custom shoutrrr URLs (user provides raw URL)
type customBuilder struct{}

func (b *customBuilder) BuildURL(config json.RawMessage) (string, error) {
	var c CustomConfig
	if err := json.Unmarshal(config, &c); err != nil {
		return "", err
	}
	return c.URL, nil
}
