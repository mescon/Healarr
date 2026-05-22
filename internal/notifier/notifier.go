package notifier

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
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
	"github.com/mescon/Healarr/internal/network"
	"github.com/mescon/Healarr/internal/safego"
)

// notifierQueryTimeout is the maximum time for database queries in notifier.
const notifierQueryTimeout = 10 * time.Second

// logFmtDecryptFailed is the log format for config decryption failures.
const logFmtDecryptFailed = "failed to decrypt config for notification %d: %v"

// Message detail format strings used across notification formatters.
const (
	msgFmtReason = "\n📋 Reason: %s"
	msgFmtDetail = "\n📋 %s"
)

// notificationColumns is the SQL column list for notification queries.
const notificationColumns = `id, name, provider_type, config, events, enabled, throttle_seconds, created_at, updated_at`

// ProviderType is the typed enum of supported notification provider kinds.
// Same template as integration.ArrType (Phase 2.1.a): typed string + Scan
// + Value + ParseProviderType for boundary validation. Closes T2 (Provider
// drift) on the notifier side.
type ProviderType string

// Provider types — all known notification providers.
const (
	ProviderDiscord    ProviderType = "discord"
	ProviderPushover   ProviderType = "pushover"
	ProviderTelegram   ProviderType = "telegram"
	ProviderSlack      ProviderType = "slack"
	ProviderEmail      ProviderType = "email"
	ProviderGotify     ProviderType = "gotify"
	ProviderNtfy       ProviderType = "ntfy"
	ProviderWhatsApp   ProviderType = "whatsapp"
	ProviderSignal     ProviderType = "signal"
	ProviderBark       ProviderType = "bark"
	ProviderGoogleChat ProviderType = "googlechat"
	ProviderIFTTT      ProviderType = "ifttt"
	ProviderJoin       ProviderType = "join"
	ProviderMattermost ProviderType = "mattermost"
	ProviderMatrix     ProviderType = "matrix"
	ProviderPushbullet ProviderType = "pushbullet"
	ProviderRocketchat ProviderType = "rocketchat"
	ProviderTeams      ProviderType = "teams"
	ProviderZulip      ProviderType = "zulip"
	ProviderGeneric    ProviderType = "generic"
	ProviderCustom     ProviderType = "custom"
)

// validProviderTypes is the authoritative set for boundary validation.
var validProviderTypes = map[ProviderType]bool{
	ProviderDiscord: true, ProviderPushover: true, ProviderTelegram: true,
	ProviderSlack: true, ProviderEmail: true, ProviderGotify: true,
	ProviderNtfy: true, ProviderWhatsApp: true, ProviderSignal: true,
	ProviderBark: true, ProviderGoogleChat: true, ProviderIFTTT: true,
	ProviderJoin: true, ProviderMattermost: true, ProviderMatrix: true,
	ProviderPushbullet: true, ProviderRocketchat: true, ProviderTeams: true,
	ProviderZulip: true, ProviderGeneric: true, ProviderCustom: true,
}

// ParseProviderType validates and converts a raw string to ProviderType.
// Use at API write boundaries (create/update notification handlers).
func ParseProviderType(s string) (ProviderType, error) {
	p := ProviderType(s)
	if !validProviderTypes[p] {
		return "", fmt.Errorf("unknown provider_type %q", s)
	}
	return p, nil
}

// Scan implements sql.Scanner so NotificationConfig.ProviderType can be
// passed directly to rows.Scan. Unknown DB values produce errors at scan
// time rather than silently propagating to downstream switches.
func (p *ProviderType) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("ProviderType: cannot scan NULL")
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("ProviderType: expected string DB value, got %T", value)
	}
	parsed, err := ParseProviderType(s)
	if err != nil {
		return fmt.Errorf("ProviderType.Scan: %w", err)
	}
	*p = parsed
	return nil
}

// Value implements driver.Valuer for symmetric DB writes.
func (p ProviderType) Value() (driver.Value, error) {
	return string(p), nil
}

// NotificationConfig represents a notification provider configuration.
//
// ProviderType is the typed ProviderType enum — the compiler rejects
// misspelled or unknown values at every comparison site, and the DB
// row scan validates at read time via ProviderType.Scan.
type NotificationConfig struct {
	ID              int64           `json:"id"`
	Name            string          `json:"name"`
	ProviderType    ProviderType    `json:"provider_type"`
	Config          json.RawMessage `json:"config"`
	Events          []string        `json:"events"`
	Enabled         bool            `json:"enabled"`
	ThrottleSeconds int             `json:"throttle_seconds"`
	CreatedAt       string          `json:"created_at"`
	UpdatedAt       string          `json:"updated_at"`
}

// DiscordConfig holds Discord webhook notification settings.
type DiscordConfig struct {
	WebhookURL string `json:"webhook_url"`
}

// PushoverConfig holds Pushover notification settings.
type PushoverConfig struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
	Priority int    `json:"priority"` // -2 to 2
	Sound    string `json:"sound"`
}

// TelegramConfig holds configuration for Telegram notifications.
type TelegramConfig struct {
	BotToken string `json:"bot_token"`
	ChatID   string `json:"chat_id"`
}

// SlackConfig holds configuration for Slack notifications.
type SlackConfig struct {
	WebhookURL string `json:"webhook_url"`
}

// EmailConfig holds configuration for email notifications.
type EmailConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
	TLS      bool   `json:"tls"`
}

// GotifyConfig holds configuration for Gotify notifications.
type GotifyConfig struct {
	ServerURL string `json:"server_url"`
	AppToken  string `json:"app_token"`
	Priority  int    `json:"priority"` // 1-10
}

// NtfyConfig holds configuration for ntfy notifications.
type NtfyConfig struct {
	ServerURL string `json:"server_url"` // Default: https://ntfy.sh
	Topic     string `json:"topic"`
	Priority  int    `json:"priority"` // 1-5
}

// CustomConfig holds configuration for custom shoutrrr URL notifications.
type CustomConfig struct {
	URL string `json:"url"` // Raw shoutrrr URL
}

// WhatsAppConfig holds configuration for WhatsApp notifications.
type WhatsAppConfig struct {
	Phone  string `json:"phone"`   // Phone number with country code (e.g., +1234567890)
	APIURL string `json:"api_url"` // WhatsApp API URL (e.g., CallMeBot or custom)
	APIKey string `json:"api_key"` // API key for the service
}

// SignalConfig holds configuration for Signal notifications.
type SignalConfig struct {
	Number    string `json:"number"`    // Your registered Signal number (+1234567890)
	Recipient string `json:"recipient"` // Recipient number or group ID
	APIURL    string `json:"api_url"`   // Signal REST API URL (e.g., signal-cli-rest-api)
}

// BarkConfig holds configuration for Bark notifications.
type BarkConfig struct {
	DeviceKey string `json:"device_key"` // Bark device key
	ServerURL string `json:"server_url"` // Bark server URL (optional, default: api.day.app)
}

// GoogleChatConfig holds configuration for Google Chat notifications.
type GoogleChatConfig struct {
	WebhookURL string `json:"webhook_url"` // Google Chat webhook URL
}

// IFTTTConfig holds configuration for IFTTT notifications.
type IFTTTConfig struct {
	WebhookKey string `json:"webhook_key"` // IFTTT webhook key
	Event      string `json:"event"`       // IFTTT event name
}

// JoinConfig holds configuration for Join notifications.
type JoinConfig struct {
	APIKey  string `json:"api_key"` // Join API key
	Devices string `json:"devices"` // Device IDs (comma-separated) or "group.all"
}

// MattermostConfig holds configuration for Mattermost notifications.
type MattermostConfig struct {
	WebhookURL string `json:"webhook_url"` // Mattermost incoming webhook URL
	Channel    string `json:"channel"`     // Channel (optional)
	Username   string `json:"username"`    // Bot username (optional)
}

// MatrixConfig holds configuration for Matrix notifications.
type MatrixConfig struct {
	HomeServer string `json:"home_server"` // Matrix homeserver URL
	User       string `json:"user"`        // Matrix user ID (e.g., @user:matrix.org)
	Password   string `json:"password"`    // Matrix password or access token
	Rooms      string `json:"rooms"`       // Room IDs (comma-separated)
}

// PushbulletConfig holds configuration for Pushbullet notifications.
type PushbulletConfig struct {
	APIToken string `json:"api_token"` // Pushbullet access token
	Targets  string `json:"targets"`   // Device/channel/email targets (optional)
}

// RocketchatConfig holds configuration for Rocket.Chat notifications.
type RocketchatConfig struct {
	WebhookURL string `json:"webhook_url"` // Rocketchat incoming webhook URL
	Channel    string `json:"channel"`     // Channel (optional)
	Username   string `json:"username"`    // Bot username (optional)
}

// TeamsConfig holds configuration for Microsoft Teams notifications.
type TeamsConfig struct {
	WebhookURL string `json:"webhook_url"` // Microsoft Teams webhook URL
}

// ZulipConfig holds configuration for Zulip notifications.
type ZulipConfig struct {
	BotEmail string `json:"bot_email"` // Zulip bot email
	BotKey   string `json:"bot_key"`   // Zulip bot API key
	Host     string `json:"host"`      // Zulip server host
	Stream   string `json:"stream"`    // Zulip stream name
	Topic    string `json:"topic"`     // Zulip topic name
}

// GenericConfig holds configuration for generic webhook notifications.
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
				{string(domain.DownloadFailed), "Download Failed", "When the download fails (no seeders, tracker issues)"},
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
			Name: "User Actions",
			Events: []EventInfo{
				{string(domain.CorruptionIgnored), "Corruption Ignored", "When a user ignores a detected corruption"},
			},
		},
		{
			Name: "System Events",
			Events: []EventInfo{
				{string(domain.SystemHealthDegraded), "System Health Degraded", "When system health checks detect issues"},
				{string(domain.InstanceUnhealthy), "Arr Instance Unhealthy", "When an *arr instance becomes unreachable"},
				{string(domain.InstanceHealthy), "Arr Instance Healthy", "When an *arr instance recovers"},
				{string(domain.StuckRemediation), "Stuck Remediation", "When a remediation has been stuck for too long"},
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
	wg         sync.WaitGroup // Tracks background goroutines for clean shutdown
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
	n.wg.Add(1)
	safego.Run("notifier-background-worker", func() {
		defer n.wg.Done()
		n.backgroundWorker()
	})

	logger.Infof("Notifier started with %d configurations", len(n.configs))
	return nil
}

// Stop stops the notifier and waits for background goroutines to exit
func (n *Notifier) Stop() {
	close(n.stopChan)
	n.wg.Wait()
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

// scanNotificationRow scans a notification config from a database row and decrypts/parses JSON fields.
func (n *Notifier) scanNotificationRow(scanner interface {
	Scan(dest ...interface{}) error
}) (*NotificationConfig, error) {
	var cfg NotificationConfig
	var configJSON, eventsJSON string
	if err := scanner.Scan(&cfg.ID, &cfg.Name, &cfg.ProviderType, &configJSON, &eventsJSON, &cfg.Enabled, &cfg.ThrottleSeconds, &cfg.CreatedAt, &cfg.UpdatedAt); err != nil {
		return nil, err
	}

	decrypted, err := crypto.Decrypt(configJSON)
	if err != nil {
		return nil, fmt.Errorf(logFmtDecryptFailed, cfg.ID, err)
	}
	if err := json.Unmarshal([]byte(decrypted), &cfg.Config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config for notification %d: %w", cfg.ID, err)
	}
	// Previously this silently set cfg.Events to []string{} on unmarshal
	// failure, which loaded the notification config with zero subscribed
	// events — it would silently never fire. Returning an error lets the
	// caller skip the config and log loudly so the operator can fix the
	// corrupt row rather than wonder why notifications stopped working.
	if err := json.Unmarshal([]byte(eventsJSON), &cfg.Events); err != nil {
		return nil, fmt.Errorf("failed to unmarshal events for notification %d: %w", cfg.ID, err)
	}
	return &cfg, nil
}

func (n *Notifier) loadConfigs() error {
	ctx, cancel := context.WithTimeout(context.Background(), notifierQueryTimeout)
	defer cancel()

	rows, err := n.db.QueryContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications WHERE enabled = 1`)
	if err != nil {
		return err
	}
	defer rows.Close()

	configs := make(map[int64]*NotificationConfig)
	for rows.Next() {
		cfg, err := n.scanNotificationRow(rows)
		if err != nil {
			logger.Errorf("Failed to scan notification row: %v", err)
			continue
		}
		configs[cfg.ID] = cfg
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
		n.wg.Add(1)
		go func(cfg *NotificationConfig) {
			defer n.wg.Done()
			n.sendNotification(cfg, eventType, data)
		}(cfg)
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

// canSend checks if a notification can be sent based on throttle.
// Caller must hold n.mu.RLock().
func (n *Notifier) canSend(configID int64, throttleSeconds int) bool {
	lastSent, exists := n.lastSent[configID]
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

	// Log result and publish to EventBus for timeline
	aggregateID := n.extractAggregateID(data)
	providerLabel := n.getProviderLabel(cfg.ProviderType)

	if err != nil {
		// Do NOT update lastSent on failure. Otherwise, in a burst (e.g. many
		// CorruptionDetected events firing at once) the first failed send would
		// throttle every subsequent send in the window and the user would
		// receive zero alerts despite events occurring. Failed sends leave the
		// throttle window open so the next event can try again.
		logger.Errorf("Failed to send notification %d: %v", cfg.ID, err)
		n.logNotification(cfg.ID, eventType, message, "failed", err.Error())
		n.publishNotificationEvent(aggregateID, domain.NotificationFailed, providerLabel, eventType, err.Error())
		return
	}

	// Send succeeded — arm the throttle window.
	n.mu.Lock()
	n.lastSent[cfg.ID] = time.Now()
	n.mu.Unlock()

	logger.Debugf("Sent notification %d for event %s", cfg.ID, eventType)
	n.logNotification(cfg.ID, eventType, message, "sent", "")
	n.publishNotificationEvent(aggregateID, domain.NotificationSent, providerLabel, eventType, "")
}

// publishNotificationEvent publishes notification success/failure events to the event bus
func (n *Notifier) publishNotificationEvent(aggregateID string, eventType domain.EventType, provider, triggerEvent, errMsg string) {
	if aggregateID == "" {
		return
	}

	eventData := map[string]interface{}{
		"provider":      provider,
		"trigger_event": triggerEvent,
	}
	if errMsg != "" {
		eventData["error"] = errMsg
	}

	if err := n.eb.Publish(domain.Event{
		AggregateType: "corruption",
		AggregateID:   aggregateID,
		EventType:     eventType,
		EventData:     eventData,
	}); err != nil {
		if eventType == domain.NotificationFailed {
			logger.Errorf("Failed to publish %s event: %v", eventType, err)
		} else {
			logger.Debugf("Failed to publish %s event: %v", eventType, err)
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
	// Aggregate IDs must be UUIDs to correlate events; file_path is not valid here.
	return ""
}

// providerLabels maps provider types to human-readable labels
var providerLabels = map[ProviderType]string{
	ProviderDiscord:    "Discord",
	ProviderPushover:   "Pushover",
	ProviderTelegram:   "Telegram",
	ProviderSlack:      "Slack",
	ProviderEmail:      "Email",
	ProviderGotify:     "Gotify",
	ProviderNtfy:       "ntfy",
	ProviderWhatsApp:   "WhatsApp",
	ProviderSignal:     "Signal",
	ProviderBark:       "Bark",
	ProviderGoogleChat: "Google Chat",
	ProviderIFTTT:      "IFTTT",
	ProviderJoin:       "Join",
	ProviderMattermost: "Mattermost",
	ProviderMatrix:     "Matrix",
	ProviderPushbullet: "Pushbullet",
	ProviderRocketchat: "Rocket.Chat",
	ProviderTeams:      "Microsoft Teams",
	ProviderZulip:      "Zulip",
	ProviderGeneric:    "Generic Webhook",
	ProviderCustom:     "Custom (Shoutrrr URL)",
}

// getProviderLabel returns a human-readable label for the provider type.
// Falls back to the raw provider string if not found in the labels map
// (defensive — should not happen since ParseProviderType validates input,
// but Scan-rejected types or future additions could conceivably land here).
func (n *Notifier) getProviderLabel(providerType ProviderType) string {
	if label, ok := providerLabels[providerType]; ok {
		return label
	}
	return string(providerType)
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

// messageContext holds extracted data for message formatting
type messageContext struct {
	FilePath       string
	FileName       string
	CorruptionType string
	ScanPath       string
	Healthy        int
	Corrupt        int
	Total          int
	RetryCount     int
	MaxRetries     int
	ErrorMsg       string
	Reason         string
	Attempts       int
}

// extractMessageContext extracts common fields from event data
func extractMessageContext(data map[string]interface{}) messageContext {
	filePath, _ := data["file_path"].(string)
	fileName := filePath
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		fileName = filePath[idx+1:]
	}

	ctx := messageContext{
		FilePath: filePath,
		FileName: fileName,
	}
	ctx.CorruptionType, _ = data["corruption_type"].(string)
	ctx.ScanPath, _ = data["path"].(string)
	ctx.Healthy = extractInt(data, "healthy_files")
	ctx.Corrupt = extractInt(data, "corrupt_files")
	ctx.Total = extractInt(data, "total_files")
	ctx.RetryCount = extractInt(data, "retry_count")
	ctx.MaxRetries = extractInt(data, "max_retries")
	ctx.Attempts = extractInt(data, "attempts")
	ctx.ErrorMsg, _ = data["error"].(string)
	ctx.Reason, _ = data["reason"].(string)

	return ctx
}

// extractInt extracts an int from a map, handling both int and float64 (from JSON).
func extractInt(data map[string]interface{}, key string) int {
	if v, ok := data[key].(int); ok {
		return v
	}
	if v, ok := data[key].(float64); ok {
		return int(v)
	}
	return 0
}

// messageFormatter is a function type for formatting event messages
type messageFormatter func(ctx messageContext) string

// messageFormatters maps event types to their message formatters
var messageFormatters = map[string]messageFormatter{
	string(domain.ScanStarted):          fmtScanStarted,
	string(domain.ScanCompleted):        fmtScanCompleted,
	string(domain.ScanFailed):           fmtScanFailed,
	string(domain.CorruptionDetected):   fmtCorruptionDetected,
	string(domain.RemediationQueued):    fmtRemediationQueued,
	string(domain.DeletionStarted):      fmtDeletionStarted,
	string(domain.DeletionCompleted):    fmtDeletionCompleted,
	string(domain.DeletionFailed):       fmtDeletionFailed,
	string(domain.SearchStarted):        fmtSearchStarted,
	string(domain.SearchCompleted):      fmtSearchCompleted,
	string(domain.SearchFailed):         fmtSearchFailed,
	string(domain.VerificationStarted):  fmtVerificationStarted,
	string(domain.VerificationSuccess):  fmtVerificationSuccess,
	string(domain.VerificationFailed):   fmtVerificationFailed,
	string(domain.DownloadTimeout):      fmtDownloadTimeout,
	string(domain.ImportBlocked):        fmtImportBlocked,
	string(domain.ManuallyRemoved):      fmtManuallyRemoved,
	string(domain.DownloadIgnored):      fmtDownloadIgnored,
	string(domain.RetryScheduled):       fmtRetryScheduled,
	string(domain.MaxRetriesReached):    fmtMaxRetriesReached,
	string(domain.SearchExhausted):      fmtSearchExhausted,
	string(domain.DownloadFailed):       fmtDownloadFailed,
	string(domain.SystemHealthDegraded): fmtSystemHealthDegraded,
	string(domain.InstanceUnhealthy):    fmtInstanceUnhealthy,
	string(domain.InstanceHealthy):      fmtInstanceHealthy,
	string(domain.StuckRemediation):     fmtStuckRemediation,
	string(domain.CorruptionIgnored):    fmtCorruptionIgnored,
}

func fmtScanStarted(ctx messageContext) string {
	return fmt.Sprintf("🔍 Scan started: %s", ctx.ScanPath)
}

func fmtScanCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ Scan complete: %s\n📊 %d/%d healthy, %d corrupt", ctx.ScanPath, ctx.Healthy, ctx.Total, ctx.Corrupt)
}

func fmtScanFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Scan failed: %s\n⚠️ %s", ctx.ScanPath, ctx.ErrorMsg)
}

func fmtCorruptionDetected(ctx messageContext) string {
	msg := fmt.Sprintf("🔴 Corruption detected: %s", ctx.FileName)
	if ctx.CorruptionType != "" {
		msg += fmt.Sprintf("\n📋 Type: %s", ctx.CorruptionType)
	}
	return msg
}

func fmtRemediationQueued(ctx messageContext) string {
	return fmt.Sprintf("🔧 Remediation queued: %s", ctx.FileName)
}

func fmtDeletionStarted(ctx messageContext) string {
	return fmt.Sprintf("🗑️ Deletion started: %s", ctx.FileName)
}

func fmtDeletionCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ File deleted for re-download: %s", ctx.FileName)
}

func fmtDeletionFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Deletion failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtSearchStarted(ctx messageContext) string {
	return fmt.Sprintf("🔎 Search triggered in *arr: %s", ctx.FileName)
}

func fmtSearchCompleted(ctx messageContext) string {
	return fmt.Sprintf("✅ Search completed: %s", ctx.FileName)
}

func fmtSearchFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Search failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtVerificationStarted(ctx messageContext) string {
	return fmt.Sprintf("🔬 Verification started: %s", ctx.FileName)
}

func fmtVerificationSuccess(ctx messageContext) string {
	return fmt.Sprintf("✅ File verified healthy: %s", ctx.FileName)
}

func fmtVerificationFailed(ctx messageContext) string {
	return fmt.Sprintf("❌ Verification failed: %s\n⚠️ %s", ctx.FileName, ctx.ErrorMsg)
}

func fmtDownloadTimeout(ctx messageContext) string {
	return fmt.Sprintf("⏰ Download timeout: %s", ctx.FileName)
}

func fmtImportBlocked(ctx messageContext) string {
	return fmt.Sprintf("🚫 Import blocked in *arr: %s\n⚠️ %s\n👉 Manual intervention required in Sonarr/Radarr", ctx.FileName, ctx.ErrorMsg)
}

func fmtManuallyRemoved(ctx messageContext) string {
	return fmt.Sprintf("🗑️ Download manually removed: %s\n👉 Item was removed from *arr queue without being imported", ctx.FileName)
}

func fmtDownloadIgnored(ctx messageContext) string {
	return fmt.Sprintf("⏸️ Download ignored by user: %s\n👉 User marked download as ignored in *arr - remediation stopped", ctx.FileName)
}

func fmtRetryScheduled(ctx messageContext) string {
	return fmt.Sprintf("🔄 Retry scheduled (%d/%d): %s", ctx.RetryCount, ctx.MaxRetries, ctx.FileName)
}

func fmtMaxRetriesReached(ctx messageContext) string {
	return fmt.Sprintf("⚠️ Max retries exhausted (%d): %s", ctx.MaxRetries, ctx.FileName)
}

func fmtSearchExhausted(ctx messageContext) string {
	msg := fmt.Sprintf("🔍 No replacement found: %s", ctx.FileName)
	if ctx.Attempts > 0 {
		msg += fmt.Sprintf("\n📊 Attempts: %d", ctx.Attempts)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	msg += "\n👉 Check your indexers or manually search in Sonarr/Radarr"
	return msg
}

func fmtDownloadFailed(ctx messageContext) string {
	msg := fmt.Sprintf("❌ Download failed: %s", ctx.FileName)
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf("\n⚠️ %s", ctx.ErrorMsg)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	return msg
}

func fmtSystemHealthDegraded(ctx messageContext) string {
	msg := "⚠️ System health degraded"
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.ErrorMsg)
	}
	return msg
}

func fmtInstanceUnhealthy(ctx messageContext) string {
	msg := "🔴 Arr instance unreachable"
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	if ctx.ErrorMsg != "" {
		msg += fmt.Sprintf("\n⚠️ %s", ctx.ErrorMsg)
	}
	return msg
}

func fmtInstanceHealthy(ctx messageContext) string {
	msg := "🟢 Arr instance recovered"
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	return msg
}

func fmtStuckRemediation(ctx messageContext) string {
	msg := "⏰ Stuck remediation detected"
	if ctx.FilePath != "" {
		msg += fmt.Sprintf(": %s", ctx.FileName)
	}
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtDetail, ctx.Reason)
	}
	return msg + "\n👉 Remediation has shown no progress - manual check recommended"
}

func fmtCorruptionIgnored(ctx messageContext) string {
	msg := fmt.Sprintf("🙈 Corruption ignored: %s", ctx.FileName)
	if ctx.Reason != "" {
		msg += fmt.Sprintf(msgFmtReason, ctx.Reason)
	}
	return msg
}

func (n *Notifier) formatMessage(eventType string, data map[string]interface{}) string {
	ctx := extractMessageContext(data)
	if formatter, ok := messageFormatters[eventType]; ok {
		return formatter(ctx)
	}
	return fmt.Sprintf("📢 Event: %s", eventType)
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
// ensureHTTPScheme adds https:// prefix if URL doesn't have a scheme.
func ensureHTTPScheme(url string) string {
	if strings.HasPrefix(url, "http") {
		return url
	}
	return "https://" + url
}

// parseKeyValuePairs parses newline-separated "key=value" pairs into a map.
func parseKeyValuePairs(input string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			result[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return result
}

// extractWebhookData extracts and structures data fields for webhook payload.
func extractWebhookData(data map[string]interface{}) map[string]interface{} {
	structuredData := make(map[string]interface{})

	// String fields with transformation
	if filePath, ok := data["file_path"].(string); ok && filePath != "" {
		structuredData["file_path"] = filePath
		fileName := filePath
		if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
			fileName = filePath[idx+1:]
		}
		structuredData["file_name"] = fileName
	}

	// Simple string fields
	stringFields := []string{"corruption_type", "error"}
	for _, field := range stringFields {
		if v, ok := data[field].(string); ok && v != "" {
			structuredData[field] = v
		}
	}
	// Rename "path" to "scan_path"
	if v, ok := data["path"].(string); ok && v != "" {
		structuredData["scan_path"] = v
	}

	// Pass-through numeric fields
	numericFields := []string{"healthy_files", "corrupt_files", "total_files", "retry_count", "max_retries"}
	for _, field := range numericFields {
		if v, ok := data[field]; ok {
			structuredData[field] = v
		}
	}

	return structuredData
}

// getFileName extracts the file name from a path in the data map.
func getFileName(data map[string]interface{}) string {
	filePath, _ := data["file_path"].(string)
	if idx := strings.LastIndex(filePath, "/"); idx >= 0 {
		return filePath[idx+1:]
	}
	return filePath
}

func (n *Notifier) sendGenericWebhook(cfg *NotificationConfig, eventType string, data map[string]interface{}) error {
	var c GenericConfig
	if err := json.Unmarshal(cfg.Config, &c); err != nil {
		return fmt.Errorf("invalid generic config: %w", err)
	}

	targetURL := ensureHTTPScheme(c.WebhookURL)
	if err := network.ValidateDestination(targetURL); err != nil {
		return fmt.Errorf("webhook destination refused: %w", err)
	}
	structuredData := extractWebhookData(data)

	// Add extra data from config
	for k, v := range parseKeyValuePairs(c.ExtraData) {
		structuredData[k] = v
	}

	payload := GenericWebhookPayload{
		Title:     n.formatTitle(eventType, getFileName(data)),
		Message:   n.formatMessage(eventType, data),
		Event:     eventType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Source:    "healarr",
		Data:      structuredData,
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	method := c.Method
	if method == "" {
		method = "POST"
	}
	req, err := http.NewRequest(method, targetURL, bytes.NewReader(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	contentType := c.ContentType
	if contentType == "" {
		contentType = "application/json"
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "Healarr/1.0")

	for k, v := range parseKeyValuePairs(c.CustomHeaders) {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("webhook returned %d: %s", resp.StatusCode, string(body))
	}

	logger.Debugf("Generic webhook sent successfully to %s (status: %d)", targetURL, resp.StatusCode)
	return nil
}

// formatTitle creates a short title for the event
// eventTitles maps event types to short titles
var eventTitles = map[string]string{
	string(domain.ScanStarted):          "🔍 Scan Started",
	string(domain.ScanCompleted):        "✅ Scan Complete",
	string(domain.ScanFailed):           "❌ Scan Failed",
	string(domain.RemediationQueued):    "🔧 Remediation Queued",
	string(domain.DeletionStarted):      "🗑️ Deletion Started",
	string(domain.DeletionCompleted):    "✅ File Deleted",
	string(domain.DeletionFailed):       "❌ Deletion Failed",
	string(domain.SearchStarted):        "🔎 Search Triggered",
	string(domain.SearchCompleted):      "✅ Search Complete",
	string(domain.SearchFailed):         "❌ Search Failed",
	string(domain.VerificationStarted):  "🔬 Verification Started",
	string(domain.VerificationSuccess):  "✅ Verification Success",
	string(domain.VerificationFailed):   "❌ Verification Failed",
	string(domain.DownloadTimeout):      "⏰ Download Timeout",
	string(domain.ImportBlocked):        "🚫 Import Blocked - Manual Action Required",
	string(domain.ManuallyRemoved):      "🗑️ Download Manually Removed",
	string(domain.DownloadIgnored):      "⏸️ Download Ignored by User",
	string(domain.RetryScheduled):       "🔄 Retry Scheduled",
	string(domain.MaxRetriesReached):    "⚠️ Max Retries Reached",
	string(domain.SearchExhausted):      "🔍 No Replacement Found",
	string(domain.DownloadFailed):       "❌ Download Failed",
	string(domain.SystemHealthDegraded): "⚠️ System Health Degraded",
	string(domain.InstanceUnhealthy):    "🔴 Arr Instance Unreachable",
	string(domain.InstanceHealthy):      "🟢 Arr Instance Recovered",
	string(domain.StuckRemediation):     "⏰ Stuck Remediation Detected",
	string(domain.CorruptionIgnored):    "🙈 Corruption Ignored by User",
}

func (n *Notifier) formatTitle(eventType, fileName string) string {
	// Special case: CorruptionDetected includes filename
	if eventType == string(domain.CorruptionDetected) {
		if fileName != "" {
			return fmt.Sprintf("🔴 Corruption detected: %s", fileName)
		}
		return "🔴 Corruption Detected"
	}

	if title, ok := eventTitles[eventType]; ok {
		return title
	}
	return fmt.Sprintf("📢 %s", eventType)
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

	rows, err := n.db.QueryContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	configs := make([]*NotificationConfig, 0)
	for rows.Next() {
		cfg, err := n.scanNotificationRow(rows)
		if err != nil {
			logger.Errorf("Failed to scan notification row: %v", err)
			continue
		}
		configs = append(configs, cfg)
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

	row := n.db.QueryRowContext(ctx,
		`SELECT `+notificationColumns+` FROM notifications WHERE id = ?`, id)
	return n.scanNotificationRow(row)
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
