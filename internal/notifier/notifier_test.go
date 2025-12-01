package notifier

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	_ "github.com/mattn/go-sqlite3"
)

// =============================================================================
// Test database helper
// =============================================================================

type testDB struct {
	DB   *sql.DB
	path string
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}

	// Create minimal schema needed for notifier tests
	schema := `
		CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			provider_type TEXT NOT NULL,
			config TEXT NOT NULL,
			events TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			throttle_seconds INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS notification_log (
			id INTEGER PRIMARY KEY,
			notification_id INTEGER,
			event_type TEXT,
			message TEXT,
			success INTEGER,
			error_message TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_version INTEGER NOT NULL,
			event_data TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Failed to create test schema: %v", err)
	}

	return &testDB{DB: db, path: dbPath}
}

func (tdb *testDB) Close() {
	tdb.DB.Close()
	os.Remove(tdb.path)
}

// =============================================================================
// Provider constant tests
// =============================================================================

func TestProviderConstants(t *testing.T) {
	// Verify provider constants exist and have expected values
	tests := []struct {
		name     string
		constant string
		expected string
	}{
		{"Discord", ProviderDiscord, "discord"},
		{"Pushover", ProviderPushover, "pushover"},
		{"Telegram", ProviderTelegram, "telegram"},
		{"Slack", ProviderSlack, "slack"},
		{"Email", ProviderEmail, "email"},
		{"Gotify", ProviderGotify, "gotify"},
		{"Ntfy", ProviderNtfy, "ntfy"},
		{"WhatsApp", ProviderWhatsApp, "whatsapp"},
		{"Signal", ProviderSignal, "signal"},
		{"Bark", ProviderBark, "bark"},
		{"GoogleChat", ProviderGoogleChat, "googlechat"},
		{"IFTTT", ProviderIFTTT, "ifttt"},
		{"Join", ProviderJoin, "join"},
		{"Mattermost", ProviderMattermost, "mattermost"},
		{"Matrix", ProviderMatrix, "matrix"},
		{"Pushbullet", ProviderPushbullet, "pushbullet"},
		{"Rocketchat", ProviderRocketchat, "rocketchat"},
		{"Teams", ProviderTeams, "teams"},
		{"Zulip", ProviderZulip, "zulip"},
		{"Generic", ProviderGeneric, "generic"},
		{"Custom", ProviderCustom, "custom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("Provider%s = %q, want %q", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

// =============================================================================
// GetEventGroups tests
// =============================================================================

func TestGetEventGroups(t *testing.T) {
	groups := GetEventGroups()

	if len(groups) == 0 {
		t.Error("Expected at least one event group")
	}

	// Verify expected groups exist
	groupNames := make(map[string]bool)
	for _, g := range groups {
		groupNames[g.Name] = true
	}

	expectedGroups := []string{
		"Scan Events",
		"Detection Events",
		"Remediation Events",
		"Verification Events",
		"Retry Events",
		"System Events",
	}

	for _, name := range expectedGroups {
		if !groupNames[name] {
			t.Errorf("Expected event group %q not found", name)
		}
	}
}

func TestGetEventGroups_ContainsScanEvents(t *testing.T) {
	groups := GetEventGroups()

	var scanGroup *EventGroup
	for i := range groups {
		if groups[i].Name == "Scan Events" {
			scanGroup = &groups[i]
			break
		}
	}

	if scanGroup == nil {
		t.Fatal("Scan Events group not found")
	}

	expectedEvents := []string{
		string(domain.ScanStarted),
		string(domain.ScanCompleted),
		string(domain.ScanFailed),
	}

	for _, expected := range expectedEvents {
		found := false
		for _, event := range scanGroup.Events {
			if event == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected event %q in Scan Events group", expected)
		}
	}
}

// =============================================================================
// Notifier constructor tests
// =============================================================================

func TestNewNotifier(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if n.db == nil {
		t.Error("Expected db to be set")
	}
	if n.eb == nil {
		t.Error("Expected eb to be set")
	}
	if n.configs == nil {
		t.Error("Expected configs map to be initialized")
	}
	if n.lastSent == nil {
		t.Error("Expected lastSent map to be initialized")
	}
	if n.stopChan == nil {
		t.Error("Expected stopChan to be initialized")
	}
	if n.reloadChan == nil {
		t.Error("Expected reloadChan to be initialized")
	}
}

// =============================================================================
// Config structure tests
// =============================================================================

func TestNotificationConfig_JSON(t *testing.T) {
	config := NotificationConfig{
		ID:              1,
		Name:            "Test Notification",
		ProviderType:    ProviderDiscord,
		Config:          json.RawMessage(`{"webhook_url":"https://discord.com/api/webhooks/test"}`),
		Events:          []string{string(domain.ScanCompleted), string(domain.CorruptionDetected)},
		Enabled:         true,
		ThrottleSeconds: 60,
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal config: %v", err)
	}

	var decoded NotificationConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal config: %v", err)
	}

	if decoded.ID != config.ID {
		t.Errorf("ID = %d, want %d", decoded.ID, config.ID)
	}
	if decoded.Name != config.Name {
		t.Errorf("Name = %q, want %q", decoded.Name, config.Name)
	}
	if decoded.ProviderType != config.ProviderType {
		t.Errorf("ProviderType = %q, want %q", decoded.ProviderType, config.ProviderType)
	}
	if len(decoded.Events) != len(config.Events) {
		t.Errorf("Events length = %d, want %d", len(decoded.Events), len(config.Events))
	}
}

func TestDiscordConfig_JSON(t *testing.T) {
	config := DiscordConfig{
		WebhookURL: "https://discord.com/api/webhooks/123456789/abcdef",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded DiscordConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.WebhookURL != config.WebhookURL {
		t.Errorf("WebhookURL = %q, want %q", decoded.WebhookURL, config.WebhookURL)
	}
}

func TestTelegramConfig_JSON(t *testing.T) {
	config := TelegramConfig{
		BotToken: "123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11",
		ChatID:   "-100123456789",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded TelegramConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.BotToken != config.BotToken {
		t.Errorf("BotToken = %q, want %q", decoded.BotToken, config.BotToken)
	}
	if decoded.ChatID != config.ChatID {
		t.Errorf("ChatID = %q, want %q", decoded.ChatID, config.ChatID)
	}
}

func TestPushoverConfig_JSON(t *testing.T) {
	config := PushoverConfig{
		UserKey:  "user123",
		AppToken: "app456",
		Priority: 1,
		Sound:    "pushover",
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded PushoverConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.Priority != config.Priority {
		t.Errorf("Priority = %d, want %d", decoded.Priority, config.Priority)
	}
}

func TestNtfyConfig_JSON(t *testing.T) {
	config := NtfyConfig{
		ServerURL: "https://ntfy.sh",
		Topic:     "healarr-alerts",
		Priority:  3,
	}

	data, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("Failed to marshal: %v", err)
	}

	var decoded NtfyConfig
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Failed to unmarshal: %v", err)
	}

	if decoded.Topic != config.Topic {
		t.Errorf("Topic = %q, want %q", decoded.Topic, config.Topic)
	}
}

// =============================================================================
// Notifier start/stop tests
// =============================================================================

func TestNotifier_StartStop(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop should not panic
	n.Stop()
}

func TestNotifier_ReloadConfigs(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	// Should not panic or block
	n.ReloadConfigs()

	// Calling multiple times should not block (buffered channel)
	n.ReloadConfigs()
	n.ReloadConfigs()
}

// =============================================================================
// LoadConfigs tests
// =============================================================================

func TestNotifier_LoadConfigs_Empty(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.loadConfigs(); err != nil {
		t.Fatalf("loadConfigs failed: %v", err)
	}

	if len(n.configs) != 0 {
		t.Errorf("Expected 0 configs, got %d", len(n.configs))
	}
}

func TestNotifier_LoadConfigs_WithData(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	// Insert test notification config
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Test Discord', 'discord', '{"webhook_url":"https://test.com"}', '["ScanCompleted"]', 1, 60)
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.loadConfigs(); err != nil {
		t.Fatalf("loadConfigs failed: %v", err)
	}

	if len(n.configs) != 1 {
		t.Errorf("Expected 1 config, got %d", len(n.configs))
	}

	config, ok := n.configs[1]
	if !ok {
		t.Fatal("Expected config with ID 1")
	}

	if config.Name != "Test Discord" {
		t.Errorf("Name = %q, want 'Test Discord'", config.Name)
	}
	if config.ProviderType != ProviderDiscord {
		t.Errorf("ProviderType = %q, want %q", config.ProviderType, ProviderDiscord)
	}
}

func TestNotifier_LoadConfigs_DisabledNotLoaded(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	// Insert disabled notification config
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Disabled', 'discord', '{}', '[]', 0, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.loadConfigs(); err != nil {
		t.Fatalf("loadConfigs failed: %v", err)
	}

	// Disabled config should not be loaded
	if len(n.configs) != 0 {
		t.Errorf("Expected 0 configs (disabled), got %d", len(n.configs))
	}
}

// =============================================================================
// SendSystemHealthDegraded tests
// =============================================================================

func TestNotifier_SendSystemHealthDegraded(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	// Should not panic even with no configs
	data := map[string]interface{}{
		"message":        "Test health degradation",
		"stuck_count":    5,
		"unhealthy_arrs": 2,
	}
	n.SendSystemHealthDegraded(data)
}
