package notifier

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/containrrr/shoutrrr"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

// notifierQueryTimeout is the maximum time for database queries in notifier.
const notifierQueryTimeout = 10 * time.Second

// Provider types
const (
	ProviderDiscord    = "discord"
	ProviderPushover   = "pushover"
	ProviderTelegram   = "telegram"
	ProviderSlack      = "slack"
	ProviderEmail      = "email"
	ProviderGotify     = "gotify"
	ProviderNtfy       = "ntfy"
	ProviderWhatsApp   = "whatsapp"
	ProviderSignal     = "signal"
	ProviderBark       = "bark"
	ProviderGoogleChat = "googlechat"
	ProviderIFTTT      = "ifttt"
	ProviderJoin       = "join"
	ProviderMattermost = "mattermost"
	ProviderMatrix     = "matrix"
	ProviderPushbullet = "pushbullet"
	ProviderRocketchat = "rocketchat"
	ProviderTeams      = "teams"
	ProviderZulip      = "zulip"
	ProviderGeneric    = "generic"
	ProviderCustom     = "custom"
)

// NotificationConfig represents a notification provider configuration
type NotificationConfig struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	ProviderType    string          `json:"provider_type"`
	Config          json.RawMessage `json:"config"`
	Events          []string        `json:"events"`
	Enabled         bool            `json:"enabled"`
	ThrottleSeconds int             `json:"throttle_seconds"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// Provider-specific config structures
type DiscordConfig struct {
	WebhookURL string `json:"webhook_url"`
}

type PushoverConfig struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
	Priority int    `json:"priority"` // -2 to 2
	Sound    string `json:"sound"`
}

type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
}

type EmailConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
	TLS      bool   `json:"tls"`
}

type GotifyConfig struct {
	ServerURL string `json:"server_url"`
	AppToken  string `json:"app_token"`
	Priority  int    `json:"priority"` // 1-10
}

type NtfyConfig struct {
	ServerURL string `json:"server_url"` // Default: https://ntfy.sh
	Topic     string `json:"topic"`
	Priority  int    `json:"priority"` // 1-5
}

type CustomConfig struct {
	URL string `json:"url"` // Raw shoutrrr URL
}

type WhatsAppConfig struct {
	Phone  string `json:"phone"`   // Phone number with country code (e.g., +1234567890)
	APIURL string `json:"api_url"` // WhatsApp API URL (e.g., CallMeBot or custom)
	APIKey string `json:"api_key"` // API key for the service
}

type SignalConfig struct {
	Number    string `json:"number"`    // Your registered Signal number (+1234567890)
	Recipient string `json:"recipient"` // Recipient number or group ID
	APIURL    string `json:"api_url"`   // Signal REST API URL (e.g., signal-cli-rest-api)
}

type BarkConfig struct {
	DeviceKey string `json:"device_key"` // Bark device key
	ServerURL string `json:"server_url"` // Bark server URL (optional, default: api.day.app)
}

type GoogleChatConfig struct {
	WebhookURL string `json:"webhook_url"` // Google Chat webhook URL
}

type IFTTTConfig struct {
	WebhookKey string `json:"webhook_key"` // IFTTT webhook key
	Event      string `json:"event"`       // IFTTT event name
}

type JoinConfig struct {
	APIKey  string `json:"api_key"` // Join API key
	Devices string `json:"devices"` // Device IDs (comma-separated) or "group.all"
}

type MattermostConfig struct {
	WebhookURL string `json:"webhook_url"` // Mattermost incoming webhook URL
	Channel    string `json:"channel"`     // Channel (optional)
	Username   string `json:"username"`    // Bot username (optional)
}

type MatrixConfig struct {
	HomeServer string `json:"home_server"` // Matrix homeserver URL
	User       string `json:"user"`        // Matrix user ID (e.g., @user:matrix.org)
	Password   string `json:"password"`    // Matrix password or access token
	Rooms      string `json:"rooms"`       // Room IDs (comma-separated)
}

type PushbulletConfig struct {
	APIToken string `json:"api_token"` // Pushbullet access token
	Targets  string `json:"targets"`   // Device/channel/email targets (optional)
}

type RocketchatConfig struct {
	WebhookURL string `json:"webhook_url"` // Rocketchat incoming webhook URL
	Channel    string `json:"channel"`     // Channel (optional)
	Username   string `json:"username"`    // Bot username (optional)
}

type TeamsConfig struct {
	WebhookURL string `json:"webhook_url"` // Microsoft Teams webhook URL
}

type ZulipConfig struct {
	BotEmail string `json:"bot_email"` // Zulip bot email
	BotKey   string `json:"bot_key"`   // Zulip bot API key
	Host     string `json:"host"`      // Zulip server host
	Stream   string `json:"stream"`    // Zulip stream name
	Topic    string `json:"topic"`     // Zulip topic name
}

type GenericConfig struct {
	WebhookURL    string `json:"webhook_url"`    // Target URL
	Method        string `json:"method"`         // HTTP method (POST, GET, etc.)
	ContentType   string `json:"content_type"`   // Content-Type header
	Template      string `json:"template"`       // Template (empty, json, or custom)
	MessageKey    string `json:"message_key"`    // JSON key for message (default: message)
	TitleKey      string `json:"title_key"`      // JSON key for title (default: title)
	CustomHeaders string `json:"custom_headers"` // Custom headers (key=value, one per line)
	ExtraData     string `json:"extra_data"`     // Extra JSON data ($key=value, one per line)
}

// EventInfo contains details about a single event type
type EventInfo struct {
	Name        string `json:"name"`        // Event type name (e.g., "ScanStarted")
	Label       string `json:"label"`       // Friendly display name (e.g., "Scan Started")
	Description string `json:"description"` // Tooltip description explaining when this event triggers
}

// EventGroup organizes related events for UI display
type EventGroup struct {
	Name   string      `json:"name"`
	Events []EventInfo `json:"events"`
}

// GetEventGroups returns all available event groups with labels and descriptions
func GetEventGroups() []EventGroup {
	return []EventGroup{
		{
			Name: "Scan Events",
			Events: []EventInfo{
				{string(domain.ScanStarted), "Scan Started", "When a scan begins on a configured media path"},
				{string(domain.ScanCompleted), "Scan Completed", "When a scan finishes with results"},
				{string(domain.ScanFailed), "Scan Failed", "When a scan encounters an error and cannot continue"},
			},
		},
		{
			Name: "Detection Events",
			Events: []EventInfo{
				{string(domain.CorruptionDetected), "Corruption Detected", "When a file fails health check during scanning"},
			},
		},
		{
			Name: "Remediation Events",
			Events: []EventInfo{
				{string(domain.RemediationQueued), "Remediation Queued", "When a corrupt file is queued for automatic repair"},
				{string(domain.DeletionStarted), "File Deletion Started", "When the corrupt file is about to be deleted"},
				{string(domain.DeletionCompleted), "File Deleted", "When the corrupt file has been successfully removed"},
				{string(domain.DeletionFailed), "Deletion Failed", "When the file could not be deleted (check permissions)"},
				{string(domain.SearchStarted), "Search Triggered", "When *arr is asked to find a replacement"},
				{string(domain.SearchCompleted), "Replacement Found", "When *arr finds and grabs a replacement download"},
				{string(domain.SearchFailed), "Search Failed", "When *arr search encounters an error"},
			},
		},
		{
			Name: "Verification Events",
			Events: []EventInfo{
				{string(domain.VerificationStarted), "Verification Started", "When checking if the new download is healthy"},
				{string(domain.VerificationSuccess), "Successfully Repaired", "When the replacement file passes health checks"},
				{string(domain.VerificationFailed), "Replacement Corrupt", "When the new download is also corrupt"},
				{string(domain.DownloadTimeout), "Download Timeout", "When the replacement download takes too long"},
			},
		},
		{
			Name: "Manual Intervention Required",
			Events: []EventInfo{
				{string(domain.ImportBlocked), "Import Blocked", "When *arr blocks import (quality/cutoff issues)"},
				{string(domain.ManuallyRemoved), "Manually Removed", "When user removes item from *arr queue"},
				{string(domain.DownloadIgnored), "Download Ignored", "When download was skipped or ignored by *arr"},
				{string(domain.SearchExhausted), "No Replacement Found", "When indexers have no candidates after retries"},
			},
		},
		{
			Name: "Retry Events",
			Events: []EventInfo{
				{string(domain.RetryScheduled), "Retry Scheduled", "When a manual retry is triggered for an item"},
				{string(domain.MaxRetriesReached), "Max Retries", "When remediation has failed too many times"},
			},
		},
		{
			Name: "System Events",
			Events: []EventInfo{
				{string(domain.SystemHealthDegraded), "System Health Degraded", "When system health checks detect issues"},
			},
		},
	}
}

// Notifier handles sending notifications based on events
type Notifier struct {
	db         *sql.DB
	eb         *eventbus.EventBus
	configs    map[int64]*NotificationConfig
	lastSent   map[int64]time.Time // Per-provider throttling
	mu         sync.RWMutex
	stopChan   chan struct{}
	reloadChan chan struct{}
}

// NewNotifier creates a new notifier service
func NewNotifier(db *sql.DB, eb *eventbus.EventBus) *Notifier {
	n := &Notifier{
		db:         db,
		eb:         eb,
		configs:    make(map[int64]*NotificationConfig),
		lastSent:   make(map[int64]time.Time),
		stopChan:   make(chan struct{}),
		reloadChan: make(chan struct{}, 1),
	}
	return n
}

// Start begins listening for events
func (n *Notifier) Start() error {
	// Load configs from database
	if err := n.loadConfigs(); err != nil {
		return fmt.Errorf("failed to load notification configs: %w", err)
	}

	// Subscribe to all notification-eligible events
	events := n.getAllEvents()
	for _, event := range events {
		eventType := domain.EventType(event) // Capture for closure
		n.eb.Subscribe(eventType, func(ev domain.Event) {
			// Ensure aggregate_id is included in data for proper event correlation
			data := ev.EventData
			if data == nil {
				data = make(map[string]interface{})
			}
			if ev.AggregateID != "" {
				data["aggregate_id"] = ev.AggregateID
			}
			n.handleEvent(string(eventType), data)
		})
	}

	// Start background goroutine for config reloading and log cleanup
	go n.backgroundWorker()

	logger.Infof("Notifier started with %d configurations", len(n.configs))
	return nil
}

// Stop stops the notifier
func (n *Notifier) Stop() {
	close(n.stopChan)
}

// SendSystemHealthDegraded sends a notification when system health is degraded
func (n *Notifier) SendSystemHealthDegraded(data map[string]interface{}) {
	n.handleEvent(string(domain.SystemHealthDegraded), data)
}

// ReloadConfigs triggers a config reload
func (n *Notifier) ReloadConfigs() {
	select {
	case n.reloadChan <- struct{}{}:
	default:
		// Already a reload pending
	}
}

func (n *Notifier) backgroundWorker() {
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-n.stopChan:
			return
		case <-n.reloadChan:
			if err := n.loadConfigs(); err != nil {
				logger.Errorf("Failed to reload notification configs: %v", err)
			} else {
				logger.Infof("Notification configs reloaded: %d active", len(n.configs))
			}
		case <-cleanupTicker.C:
			n.cleanupOldLogs()
		}
	}
}

func (n *Notifier) loadConfigs() error {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	rows, err := n.db.QueryContext(ctx, `
		SELECT id, name, provider_type, config, events, enabled, throttle_seconds, created_at, updated_at
		FROM notifications
		WHERE enabled = 1
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	configs := make(map[int64]*NotificationConfig)
	for rows.Next() {
		var cfg NotificationConfig
		var configJSON string
		var eventsJSON string
		if err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.ProviderType, &configJSON, &eventsJSON, &cfg.Enabled, &cfg.ThrottleSeconds, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return err
		}
		// Decrypt config if encrypted
		decryptedConfig, err := crypto.Decrypt(configJSON)
		if err != nil {
			logger.Errorf("Failed to decrypt config for notification %d: %v", cfg.ID, err)
			continue
		}
		cfg.Config = json.RawMessage(decryptedConfig)
		if err := json.Unmarshal([]byte(eventsJSON), &cfg.Events); err != nil {
			logger.Errorf("Failed to parse events for notification %d: %v", cfg.ID, err)
			continue
		}
		configs[cfg.ID] = &cfg
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating notification configs: %w", err)
	}

	n.mu.Lock()
	n.configs = configs
	n.mu.Unlock()
	return nil
}

func (n *Notifier) getAllEvents() []string {
	events := []string{}
	for _, group := range GetEventGroups() {
		for _, eventInfo := range group.Events {
			events = append(events, eventInfo.Name)
		}
	}
	return events
}

func (n *Notifier) handleEvent(eventType string, data map[string]interface{}) {
	n.mu.RLock()
	defer n.mu.RUnlock()

	for _, cfg := range n.configs {
		if !n.shouldNotify(cfg, eventType) {
			continue
		}
		// Check throttle
		if !n.canSend(cfg.ID, cfg.ThrottleSeconds) {
			logger.Debugf("Throttled notification %d for event %s", cfg.ID, eventType)
			continue
		}
		// Send notification asynchronously
		go n.sendNotification(cfg, eventType, data)
	}
}

func (n *Notifier) shouldNotify(cfg *NotificationConfig, eventType string) bool {
	for _, e := range cfg.Events {
		if e == eventType {
			return true
		}
	}
	return false
}

func (n *Notifier) canSend(configID int64, throttleSeconds int) bool {
	n.mu.RLock()
	lastSent, exists := n.lastSent[configID]
	n.mu.RUnlock()

	if !exists {
		return true
	}
	return time.Since(lastSent) >= time.Duration(throttleSeconds)*time.Second
}

func (n *Notifier) sendNotification(cfg *NotificationConfig, eventType string, data map[string]interface{}) {
	var err error
	var message string

	// Use custom sender for generic webhooks (richer payload)
	if cfg.ProviderType == ProviderGeneric {
		err = n.sendGenericWebhook(cfg, eventType, data)
		message = fmt.Sprintf("[Generic Webhook] %s", eventType)
	} else {
		// Build shoutrrr URL for other providers
		shoutrrrURL, buildErr := n.buildShoutrrrURL(cfg)
		if buildErr != nil {
			logger.Errorf("Failed to build shoutrrr URL for notification %d: %v", cfg.ID, buildErr)
			n.logNotification(cfg.ID, eventType, "", "failed", buildErr.Error())
			return
		}

		// Format message
		message = n.formatMessage(eventType, data)

		// Send via shoutrrr
		err = shoutrrr.Send(shoutrrrURL, message)
	}

	// Update last sent time
	n.mu.Lock()
	n.lastSent[cfg.ID] = time.Now()
	n.mu.Unlock()

	// Log result and publish to EventBus for timeline
	aggregateID := n.extractAggregateID(data)
	providerLabel := n.getProviderLabel(cfg.ProviderType)

	if err != nil {
		logger.Errorf("Failed to send notification %d: %v", cfg.ID, err)
		n.logNotification(cfg.ID, eventType, message, "failed", err.Error())
		// Publish NotificationFailed event if we have an aggregate ID
		if aggregateID != "" {
			if pubErr := n.eb.Publish(domain.Event{
				AggregateType: "corruption",
				AggregateID:   aggregateID,
				EventType:     domain.NotificationFailed,
				EventData: map[string]interface{}{
					"provider":      providerLabel,
					"trigger_event": eventType,
					"error":         err.Error(),
				},
			}); pubErr != nil {
				logger.Errorf("Failed to publish NotificationFailed event: %v", pubErr)
			}
		}
	} else {
		logger.Debugf("Sent notification %d for event %s", cfg.ID, eventType)
		n.logNotification(cfg.ID, eventType, message, "sent", "")
		// Publish NotificationSent event if we have an aggregate ID
		if aggregateID != "" {
			if pubErr := n.eb.Publish(domain.Event{
				AggregateType: "corruption",
				AggregateID:   aggregateID,
				EventType:     domain.NotificationSent,
				EventData: map[string]interface{}{
					"provider":      providerLabel,
					"trigger_event": eventType,
				},
			}); pubErr != nil {
				logger.Debugf("Failed to publish NotificationSent event: %v", pubErr)
			}
		}
	}
}

// extractAggregateID gets the corruption aggregate ID from event data
func (n *Notifier) extractAggregateID(data map[string]interface{}) string {
	// Try to get aggregate_id directly (passed from event subscription)
	if id, ok := data["aggregate_id"].(string); ok && id != "" {
		return id
	}
	// Try corruption_id (backup)
	if id, ok := data["corruption_id"].(string); ok && id != "" {
		return id
	}
	// Note: We no longer fall back to file_path - it's not a valid aggregate ID
	// Aggregate IDs must be UUIDs to properly correlate events
	return ""
}

// getProviderLabel returns a human-readable label for the provider type
func (n *Notifier) getProviderLabel(providerType string) string {
	switch providerType {
	case ProviderDiscord:
		return "Discord"
	case ProviderPushover:
		return "Pushover"
	case ProviderTelegram:
		return "Telegram"
	case ProviderSlack:
		return "Slack"
	case ProviderEmail:
		return "Email"
	case ProviderGotify:
		return "Gotify"
	case ProviderNtfy:
		return "ntfy"
	case ProviderWhatsApp:
		return "WhatsApp"
	case ProviderSignal:
		return "Signal"
	case ProviderBark:
		return "Bark"
	case ProviderGoogleChat:
		return "Google Chat"
	case ProviderIFTTT:
		return "IFTTT"
	case ProviderJoin:
		return "Join"
	case ProviderMattermost:
		return "Mattermost"
	case ProviderMatrix:
		return "Matrix"
	case ProviderPushbullet:
		return "Pushbullet"
	case ProviderRocketchat:
		return "Rocket.Chat"
	case ProviderTeams:
		return "Microsoft Teams"
	case ProviderZulip:
		return "Zulip"
	case ProviderGeneric:
		return "Generic Webhook"
	case ProviderCustom:
		return "Custom (Shoutrrr URL)"
	default:
		return providerType
	}
}

func (n *Notifier) buildShoutrrrURL(cfg *NotificationConfig) (string, error) {
	builder, ok := urlBuilders[cfg.ProviderType]
	if !ok {
		return "", fmt.Errorf("unknown provider type: %s", cfg.ProviderType)
	}
	return builder.BuildURL(cfg.Config)
}

func convertDiscordWebhook(webhookURL string) (string, error) {
	webhookURL = strings.TrimSpace(webhookURL)
	// Discord webhook URL: https://discord.com/api/webhooks/{id}/{token}
	// or https://discordapp.com/api/webhooks/{id}/{token}
	// Extract ID and token from URL
	parts := strings.Split(webhookURL, "/webhooks/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Discord webhook URL format")
	}
	idToken := strings.Split(parts[1], "/")
	if len(idToken) < 2 {
		return "", fmt.Errorf("invalid Discord webhook URL format")
	}
	id := idToken[0]
	token := strings.Split(idToken[1], "?")[0] // Remove query params if any
	return fmt.Sprintf("discord://%s@%s", token, id), nil
}

func convertSlackWebhook(webhookURL string) (string, error) {
	webhookURL = strings.TrimSpace(webhookURL)
	// Slack webhook URL format: hooks.slack.com/services/{workspace}/{channel}/{token}
	parts := strings.Split(webhookURL, "/services/")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid Slack webhook URL format")
	}
	tokens := strings.Split(parts[1], "/")
	if len(tokens) != 3 {
		return "", fmt.Errorf("invalid Slack webhook URL format: expected 3 tokens")
	}
	return fmt.Sprintf("slack://hook:%s-%s-%s@webhook", tokens[0], tokens[1], tokens[2]), nil
}

func (n *Notifier) formatMessage(eventType string, data map[string]interface{}) string {
	// Extract common fields
	filePath, _ := data["file_path"].(string)
	fileName := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		fileName = filePath[idx+1:]
	}
	corruptionType, _ := data["corruption_type"].(string)
	scanPath, _ := data["path"].(string)
	healthy, _ := data["healthy_files"].(int)
	corrupt, _ := data["corrupt_files"].(int)
	total, _ := data["total_files"].(int)
	retryCount, _ := data["retry_count"].(int)
	maxRetries, _ := data["max_retries"].(int)
	errorMsg, _ := data["error"].(string)

	switch eventType {
	case string(domain.ScanStarted):
		return fmt.Sprintf("🔍 Scan started: %s", scanPath)
	case string(domain.ScanCompleted):
		return fmt.Sprintf("✅ Scan complete: %s\n📊 %d/%d healthy, %d corrupt", scanPath, healthy, total, corrupt)
	case string(domain.ScanFailed):
		return fmt.Sprintf("❌ Scan failed: %s\n⚠️ %s", scanPath, errorMsg)
	case string(domain.CorruptionDetected):
		msg := fmt.Sprintf("🔴 Corruption detected: %s", fileName)
		if corruptionType != "" {
			msg += fmt.Sprintf("\n📋 Type: %s", corruptionType)
		}
		return msg
	case string(domain.RemediationQueued):
		return fmt.Sprintf("🔧 Remediation queued: %s", fileName)
	case string(domain.DeletionStarted):
		return fmt.Sprintf("🗑️ Deletion started: %s", fileName)
	case string(domain.DeletionCompleted):
		return fmt.Sprintf("✅ File deleted for re-download: %s", fileName)
	case string(domain.DeletionFailed):
		return fmt.Sprintf("❌ Deletion failed: %s\n⚠️ %s", fileName, errorMsg)
	case string(domain.SearchStarted):
		return fmt.Sprintf("🔎 Search triggered in *arr: %s", fileName)
	case string(domain.SearchCompleted):
		return fmt.Sprintf("✅ Search completed: %s", fileName)
	case string(domain.SearchFailed):
		return fmt.Sprintf("❌ Search failed: %s\n⚠️ %s", fileName, errorMsg)
	case string(domain.VerificationStarted):
		return fmt.Sprintf("🔬 Verification started: %s", fileName)
	case string(domain.VerificationSuccess):
		return fmt.Sprintf("✅ File verified healthy: %s", fileName)
	case string(domain.VerificationFailed):
		return fmt.Sprintf("❌ Verification failed: %s\n⚠️ %s", fileName, errorMsg)
	case string(domain.DownloadTimeout):
		return fmt.Sprintf("⏰ Download timeout: %s", fileName)
	case string(domain.ImportBlocked):
		return fmt.Sprintf("🚫 Import blocked in *arr: %s\n⚠️ %s\n👉 Manual intervention required in Sonarr/Radarr", fileName, errorMsg)
	case string(domain.ManuallyRemoved):
		return fmt.Sprintf("🗑️ Download manually removed: %s\n👉 Item was removed from *arr queue without being imported", fileName)
	case string(domain.DownloadIgnored):
		return fmt.Sprintf("⏸️ Download ignored by user: %s\n👉 User marked download as ignored in *arr - remediation stopped", fileName)
	case string(domain.RetryScheduled):
		return fmt.Sprintf("🔄 Retry scheduled (%d/%d): %s", retryCount, maxRetries, fileName)
	case string(domain.MaxRetriesReached):
		return fmt.Sprintf("⚠️ Max retries exhausted (%d): %s", maxRetries, fileName)
	case string(domain.SearchExhausted):
		reason, _ := data["reason"].(string)
		attempts, _ := data["attempts"].(int)
		if attempts == 0 {
			// Try float64 (JSON numbers)
			if f, ok := data["attempts"].(float64); ok {
				attempts = int(f)
			}
		}
		msg := fmt.Sprintf("🔍 No replacement found: %s", fileName)
		if attempts > 0 {
			msg += fmt.Sprintf("\n📊 Attempts: %d", attempts)
		}
		if reason != "" {
			msg += fmt.Sprintf("\n📋 Reason: %s", reason)
		}
		msg += "\n👉 Check your indexers or manually search in Sonarr/Radarr"
		return msg
	default:
		return fmt.Sprintf("📢 Event: %s", eventType)
	}
}

// GenericWebhookPayload is the rich JSON payload sent to generic webhooks
type GenericWebhookPayload struct {
	Title     string                 `json:"title"`
	Message   string                 `json:"message"`
	Event     string                 `json:"event"`
	Timestamp string                 `json:"timestamp"`
	Source    string                 `json:"source"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// sendGenericWebhook sends a rich JSON payload directly to a webhook URL
func (n *Notifier) sendGenericWebhook(cfg *NotificationConfig, eventType string, data map[string]interface{}) error {
	var c GenericConfig
	if err := json.Unmarshal(cfg.Config, &c); err != nil {
		return fmt.Errorf("invalid generic config: %w", err)
	}

	// Ensure URL has scheme
	targetURL := c.WebhookURL
	if !strings.HasPrefix(targetURL, "http") {
		targetURL = "https://" + targetURL
	}

	// Extract common fields for the message
	filePath, _ := data["file_path"].(string)
	fileName := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		fileName = filePath[idx+1:]
	}
	corruptionType, _ := data["corruption_type"].(string)
	scanPath, _ := data["path"].(string)
	errorMsg, _ := data["error"].(string)

	// Build structured data payload
	structuredData := make(map[string]interface{})
	if filePath != "" {
		structuredData["file_path"] = filePath
		structuredData["file_name"] = fileName
	}
	if corruptionType != "" {
		structuredData["corruption_type"] = corruptionType
	}
	if scanPath != "" {
		structuredData["scan_path"] = scanPath
	}
	if errorMsg != "" {
		structuredData["error"] = errorMsg
	}
	// Include numeric fields
	if v, ok := data["healthy_files"]; ok {
		structuredData["healthy_files"] = v
	}
	if v, ok := data["corrupt_files"]; ok {
		structuredData["corrupt_files"] = v
	}
	if v, ok := data["total_files"]; ok {
		structuredData["total_files"] = v
	}
	if v, ok := data["retry_count"]; ok {
		structuredData["retry_count"] = v
	}
	if v, ok := data["max_retries"]; ok {
		structuredData["max_retries"] = v
	}

	// Build payload
	payload := GenericWebhookPayload{
		Title:     n.formatTitle(eventType, fileName),
		Message:   n.formatMessage(eventType, data),
		Event:     eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "healarr",
		Data:      structuredData,
	}

	// Parse extra data from config and add to payload.Data
	if c.ExtraData != "" {
		for _, line := range strings.Split(c.ExtraData, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				payload.Data[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
			}
		}
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create request
	method := c.Method
	if method == "" {
		method = "POST"
	}
	req, err := http.NewRequest(method, targetURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set content type
	contentType := c.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "Healarr/1.0")

	// Parse and add custom headers
	if c.CustomHeaders != "" {
		for _, line := range strings.Split(c.CustomHeaders, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				req.Header.Set(strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]))
			}
		}
	}

	// Send request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(body))
	}

	logger.Debugf("Generic webhook sent successfully to %s (status: %d)", targetURL, resp.StatusCode)
	return nil
}

// formatTitle creates a short title for the event
func (n *Notifier) formatTitle(eventType string, fileName string) string {
	switch eventType {
	case string(domain.ScanStarted):
		return "🔍 Scan Started"
	case string(domain.ScanCompleted):
		return "✅ Scan Complete"
	case string(domain.ScanFailed):
		return "❌ Scan Failed"
	case string(domain.CorruptionDetected):
		if fileName != "" {
			return fmt.Sprintf("🔴 Corruption detected: %s", fileName)
		}
		return "🔴 Corruption Detected"
	case string(domain.RemediationQueued):
		return "🔧 Remediation Queued"
	case string(domain.DeletionStarted):
		return "🗑️ Deletion Started"
	case string(domain.DeletionCompleted):
		return "✅ File Deleted"
	case string(domain.DeletionFailed):
		return "❌ Deletion Failed"
	case string(domain.SearchStarted):
		return "🔎 Search Triggered"
	case string(domain.SearchCompleted):
		return "✅ Search Complete"
	case string(domain.SearchFailed):
		return "❌ Search Failed"
	case string(domain.VerificationStarted):
		return "🔬 Verification Started"
	case string(domain.VerificationSuccess):
		return "✅ Verification Success"
	case string(domain.VerificationFailed):
		return "❌ Verification Failed"
	case string(domain.DownloadTimeout):
		return "⏰ Download Timeout"
	case string(domain.ImportBlocked):
		return "🚫 Import Blocked - Manual Action Required"
	case string(domain.ManuallyRemoved):
		return "🗑️ Download Manually Removed"
	case string(domain.DownloadIgnored):
		return "⏸️ Download Ignored by User"
	case string(domain.RetryScheduled):
		return "🔄 Retry Scheduled"
	case string(domain.MaxRetriesReached):
		return "⚠️ Max Retries Reached"
	default:
		return fmt.Sprintf("📢 %s", eventType)
	}
}

func (n *Notifier) logNotification(notificationID int64, eventType, message, status, errorMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	_, err := n.db.ExecContext(ctx, `
		INSERT INTO notification_log (notification_id, event_type, message, status, error)
		VALUES (?, ?, ?, ?, ?)
	`, notificationID, eventType, message, status, errorMsg)
	if err != nil {
		logger.Errorf("Failed to log notification: %v", err)
	}
}

func (n *Notifier) cleanupOldLogs() {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	// Delete logs older than 7 days
	result, err := n.db.ExecContext(ctx, `
		DELETE FROM notification_log
		WHERE sent_at < datetime('now', '-7 days')
	`)
	if err != nil {
		logger.Errorf("Failed to cleanup notification logs: %v", err)
		return
	}
	rows, _ := result.RowsAffected()
	if rows > 0 {
		logger.Infof("Cleaned up %d old notification log entries", rows)
	}

	// Also limit to 100 entries per notification
	_, err = n.db.ExecContext(ctx, `
		DELETE FROM notification_log
		WHERE id NOT IN (
			SELECT id FROM notification_log
			ORDER BY sent_at DESC
			LIMIT 100
		)
	`)
	if err != nil {
		logger.Errorf("Failed to limit notification logs: %v", err)
	}
}

// SendTestNotification sends a test notification to verify configuration
func (n *Notifier) SendTestNotification(cfg *NotificationConfig) error {
	shoutrrrURL, err := n.buildShoutrrrURL(cfg)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	message := "🧪 Healarr Test Notification\n✅ Your notification configuration is working correctly!"

	if err := shoutrrr.Send(shoutrrrURL, message); err != nil {
		return fmt.Errorf("failed to send: %w", err)
	}

	return nil
}

// GetAllConfigs returns all notification configurations (for API)
func (n *Notifier) GetAllConfigs() ([]*NotificationConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	rows, err := n.db.QueryContext(ctx, `
		SELECT id, name, provider_type, config, events, enabled, throttle_seconds, created_at, updated_at
		FROM notifications
		ORDER BY name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make([]*NotificationConfig, 0)
	for rows.Next() {
		var cfg NotificationConfig
		var configJSON string
		var eventsJSON string
		if err := rows.Scan(&cfg.ID, &cfg.Name, &cfg.ProviderType, &configJSON, &eventsJSON, &cfg.Enabled, &cfg.ThrottleSeconds, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
			return nil, err
		}
		// Decrypt config
		decryptedConfig, err := crypto.Decrypt(configJSON)
		if err != nil {
			logger.Errorf("Failed to decrypt config for notification %d: %v", cfg.ID, err)
			continue
		}
		cfg.Config = json.RawMessage(decryptedConfig)
		if err := json.Unmarshal([]byte(eventsJSON), &cfg.Events); err != nil {
			cfg.Events = []string{}
		}
		configs = append(configs, &cfg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating notification configs: %w", err)
	}

	return configs, nil
}

// GetConfig returns a specific notification configuration
func (n *Notifier) GetConfig(id int64) (*NotificationConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	var cfg NotificationConfig
	var configJSON string
	var eventsJSON string

	err := n.db.QueryRowContext(ctx, `
		SELECT id, name, provider_type, config, events, enabled, throttle_seconds, created_at, updated_at
		FROM notifications
		WHERE id = ?
	`, id).Scan(&cfg.ID, &cfg.Name, &cfg.ProviderType, &configJSON, &eventsJSON, &cfg.Enabled, &cfg.ThrottleSeconds, &cfg.CreatedAt, &cfg.UpdatedAt)
	if err != nil {
		return nil, err
	}

	// Decrypt config
	decryptedConfig, err := crypto.Decrypt(configJSON)
	if err != nil {
		logger.Errorf("Failed to decrypt config for notification %d: %v", id, err)
		return nil, fmt.Errorf("failed to decrypt config: %w", err)
	}
	cfg.Config = json.RawMessage(decryptedConfig)
	if err := json.Unmarshal([]byte(eventsJSON), &cfg.Events); err != nil {
		cfg.Events = []string{}
	}

	return &cfg, nil
}

// CreateConfig creates a new notification configuration
func (n *Notifier) CreateConfig(cfg *NotificationConfig) (int64, error) {
	eventsJSON, err := json.Marshal(cfg.Events)
	if err != nil {
		return 0, err
	}

	// Encrypt config before storage
	encryptedConfig, err := crypto.Encrypt(string(cfg.Config))
	if err != nil {
		return 0, fmt.Errorf("failed to encrypt config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	result, err := n.db.ExecContext(ctx, `
		INSERT INTO notifications (name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (?, ?, ?, ?, ?, ?)
	`, cfg.Name, cfg.ProviderType, encryptedConfig, string(eventsJSON), cfg.Enabled, cfg.ThrottleSeconds)
	if err != nil {
		return 0, err
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}

	n.ReloadConfigs()
	return id, nil
}

// UpdateConfig updates an existing notification configuration
func (n *Notifier) UpdateConfig(cfg *NotificationConfig) error {
	eventsJSON, err := json.Marshal(cfg.Events)
	if err != nil {
		return err
	}

	// Encrypt config before storage
	encryptedConfig, err := crypto.Encrypt(string(cfg.Config))
	if err != nil {
		return fmt.Errorf("failed to encrypt config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	_, err = n.db.ExecContext(ctx, `
		UPDATE notifications
		SET name = ?, provider_type = ?, config = ?, events = ?, enabled = ?, throttle_seconds = ?, updated_at = datetime('now')
		WHERE id = ?
	`, cfg.Name, cfg.ProviderType, encryptedConfig, string(eventsJSON), cfg.Enabled, cfg.ThrottleSeconds, cfg.ID)
	if err != nil {
		return err
	}

	n.ReloadConfigs()
	return nil
}

// DeleteConfig deletes a notification configuration
func (n *Notifier) DeleteConfig(id int64) error {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	_, err := n.db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		return err
	}

	// Also delete related logs
	if _, logErr := n.db.ExecContext(ctx, `DELETE FROM notification_log WHERE notification_id = ?`, id); logErr != nil {
		logger.Warnf("Failed to cleanup notification logs for id=%d: %v", id, logErr)
	}

	// Clean up lastSent map to prevent memory leak
	n.mu.Lock()
	delete(n.lastSent, id)
	n.mu.Unlock()

	n.ReloadConfigs()
	return nil
}

// GetNotificationLog returns recent notification log entries
func (n *Notifier) GetNotificationLog(notificationID int64, limit int) ([]NotificationLogEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	query := `
		SELECT id, notification_id, event_type, message, status, error, sent_at
		FROM notification_log
	`
	args := []interface{}{}

	if notificationID > 0 {
		query += ` WHERE notification_id = ?`
		args = append(args, notificationID)
	}

	query += ` ORDER BY sent_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := n.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]NotificationLogEntry, 0)
	for rows.Next() {
		var entry NotificationLogEntry
		var errorMsg sql.NullString
		if err := rows.Scan(&entry.ID, &entry.NotificationID, &entry.EventType, &entry.Message, &entry.Status, &errorMsg, &entry.SentAt); err != nil {
			return nil, err
		}
		entry.Error = errorMsg.String
		entries = append(entries, entry)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating notification log: %w", err)
	}

	return entries, nil
}

// NotificationLogEntry represents a notification log entry
type NotificationLogEntry struct {
	ID             int64  `json:"id"`
	NotificationID int64  `json:"notification_id"`
	EventType      string `json:"event_type"`
	Message        string `json:"message"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	SentAt         string `json:"sent_at"`
}
