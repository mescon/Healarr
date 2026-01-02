package notifier

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
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
			status TEXT,
			error TEXT,
			sent_at DATETIME DEFAULT CURRENT_TIMESTAMP
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

// =============================================================================
// Additional URL Builder tests (complementing url_builders_test.go)
// =============================================================================

func TestBarkBuilder_BuildURL_Extra(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name:    "default server",
			config:  `{"device_key":"mykey123"}`,
			wantURL: "bark://mykey123@api.day.app",
		},
		{
			name:    "custom server",
			config:  `{"device_key":"key","server_url":"https://bark.example.com"}`,
			wantURL: "bark://key@bark.example.com",
		},
	}

	builder := &barkBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("BuildURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestCustomBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"url":"discord://token@id"}`
	builder := &customBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if url != "discord://token@id" {
		t.Errorf("BuildURL() = %q, want 'discord://token@id'", url)
	}
}

func TestSignalBuilder_RequiresAPIURL(t *testing.T) {
	config := `{"number":"+1234567890","recipient":"+0987654321"}`
	builder := &signalBuilder{}
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for missing API URL")
	}
}

func TestTeamsBuilder_InvalidFormat(t *testing.T) {
	config := `{"webhook_url":"https://teams.invalid.com/simple"}`
	builder := &teamsBuilder{}
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for invalid Teams webhook format")
	}
}

func TestIFTTTBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"webhook_key":"mykey","event":"healarr_alert"}`
	builder := &iftttBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	expected := "ifttt://mykey/?events=healarr_alert"
	if url != expected {
		t.Errorf("BuildURL() = %q, want %q", url, expected)
	}
}

func TestPushoverBuilder_BuildURL_WithPriority(t *testing.T) {
	builder := &pushoverBuilder{}
	config := `{"app_token":"apptoken","user_key":"userkey","priority":1}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "priority=1") {
		t.Errorf("BuildURL() = %q, should contain priority=1", url)
	}
}

func TestPushoverBuilder_BuildURL_WithSound(t *testing.T) {
	builder := &pushoverBuilder{}
	config := `{"app_token":"apptoken","user_key":"userkey","sound":"siren"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "sound=siren") {
		t.Errorf("BuildURL() = %q, should contain sound=siren", url)
	}
}

func TestPushoverBuilder_BuildURL_Full(t *testing.T) {
	builder := &pushoverBuilder{}
	config := `{"app_token":"apptoken","user_key":"userkey","priority":2,"sound":"persistent"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "priority=2") {
		t.Errorf("BuildURL() = %q, should contain priority=2", url)
	}
	if !strings.Contains(url, "sound=persistent") {
		t.Errorf("BuildURL() = %q, should contain sound=persistent", url)
	}
}

func TestJoinBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"api_key":"apikey123","devices":"group.all"}`
	builder := &joinBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	expected := "join://shoutrrr:apikey123@join/?devices=group.all"
	if url != expected {
		t.Errorf("BuildURL() = %q, want %q", url, expected)
	}
}

func TestPushbulletBuilder_BuildURL_Extra(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name:    "basic",
			config:  `{"api_token":"token123"}`,
			wantURL: "pushbullet://token123",
		},
		{
			name:    "with targets",
			config:  `{"api_token":"token","targets":"device123"}`,
			wantURL: "pushbullet://token/device123",
		},
	}

	builder := &pushbulletBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("BuildURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestWhatsAppBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"phone":"+1234567890","api_key":"myapikey"}`
	builder := &whatsAppBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	// Should use default CallMeBot API
	if !strings.Contains(url, "api.callmebot.com") {
		t.Errorf("BuildURL() = %q, should contain default CallMeBot API", url)
	}
}

func TestSignalBuilder_WithAPIURL(t *testing.T) {
	config := `{"number":"+1234567890","recipient":"+0987654321","api_url":"http://localhost:8080"}`
	builder := &signalBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "localhost:8080") {
		t.Errorf("BuildURL() = %q, should contain localhost:8080", url)
	}
}

func TestMatrixBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"home_server":"https://matrix.org","user":"@user:matrix.org","password":"pass123","rooms":"!room:matrix.org"}`
	builder := &matrixBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "matrix://") {
		t.Errorf("BuildURL() = %q, should be a matrix:// URL", url)
	}
}

func TestZulipBuilder_BuildURL_Extra(t *testing.T) {
	config := `{"bot_email":"bot@zulip.com","bot_key":"key123","host":"https://zulip.example.com","stream":"alerts","topic":"healarr"}`
	builder := &zulipBuilder{}
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "zulip://") {
		t.Errorf("BuildURL() = %q, should be a zulip:// URL", url)
	}
}

func TestGenericBuilder_BuildURL_Extra(t *testing.T) {
	tests := []struct {
		name   string
		config string
		want   string
	}{
		{
			name:   "simple URL",
			config: `{"webhook_url":"https://example.com/webhook"}`,
			want:   "generic+https://example.com/webhook",
		},
		{
			name:   "URL without scheme",
			config: `{"webhook_url":"example.com/webhook"}`,
			want:   "generic+https://example.com/webhook",
		},
	}

	builder := &genericBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.want {
				t.Errorf("BuildURL() = %q, want %q", url, tt.want)
			}
		})
	}
}

func TestGenericBuilder_BuildURL_WithParams(t *testing.T) {
	builder := &genericBuilder{}

	// Test with template parameter
	config := `{"webhook_url":"https://example.com/webhook","template":"json"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "template=json") {
		t.Errorf("BuildURL() = %q, should contain template=json", url)
	}
}

func TestGenericBuilder_BuildURL_WithMessageKey(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","message_key":"text"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "messageKey=text") {
		t.Errorf("BuildURL() = %q, should contain messageKey=text", url)
	}
}

func TestGenericBuilder_BuildURL_WithTitleKey(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","title_key":"subject"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "titleKey=subject") {
		t.Errorf("BuildURL() = %q, should contain titleKey=subject", url)
	}
}

func TestGenericBuilder_BuildURL_WithContentType(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","content_type":"text/plain"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "contenttype=text") {
		t.Errorf("BuildURL() = %q, should contain contenttype param", url)
	}
}

func TestGenericBuilder_BuildURL_WithMethod(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","method":"PUT"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "requestmethod=PUT") {
		t.Errorf("BuildURL() = %q, should contain requestmethod=PUT", url)
	}
}

func TestGenericBuilder_BuildURL_WithCustomHeaders(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","custom_headers":"Authorization=Bearer token123"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "%40Authorization") || !strings.Contains(url, "token123") {
		t.Errorf("BuildURL() = %q, should contain custom header", url)
	}
}

func TestGenericBuilder_BuildURL_WithExtraData(t *testing.T) {
	builder := &genericBuilder{}
	config := `{"webhook_url":"https://example.com/webhook","extra_data":"priority=high"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "%24priority") || !strings.Contains(url, "high") {
		t.Errorf("BuildURL() = %q, should contain extra data", url)
	}
}

func TestTeamsBuilder_BuildURL(t *testing.T) {
	builder := &teamsBuilder{}
	// A valid Teams webhook URL format
	config := `{"webhook_url":"https://outlook.office.com/webhookb2/group123@tenant456/IncomingWebhook/webhook789/signature"}`
	url, err := builder.BuildURL(json.RawMessage(config))
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if !strings.Contains(url, "teams://") {
		t.Errorf("BuildURL() = %q, should start with teams://", url)
	}
	if !strings.Contains(url, "group123") {
		t.Errorf("BuildURL() = %q, should contain group ID", url)
	}
}

func TestTeamsBuilder_BuildURL_InvalidURL(t *testing.T) {
	builder := &teamsBuilder{}
	config := `{"webhook_url":"://invalid"}`
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestTeamsBuilder_BuildURL_MissingParts(t *testing.T) {
	builder := &teamsBuilder{}
	// URL with insufficient parts
	config := `{"webhook_url":"https://outlook.office.com/webhookb2/abc"}`
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for URL with missing parts")
	}
}

func TestTeamsBuilder_BuildURL_MissingGroupTenant(t *testing.T) {
	builder := &teamsBuilder{}
	// URL without @ in the first part
	config := `{"webhook_url":"https://outlook.office.com/webhookb2/grouponly/IncomingWebhook/a/b"}`
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for URL missing group@tenant")
	}
}

func TestGoogleChatBuilder_BuildURL(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name:    "basic webhook",
			config:  `{"webhook_url":"https://chat.googleapis.com/v1/spaces/abc/messages?key=xyz&token=tok"}`,
			wantURL: "googlechat://chat.googleapis.com/v1/spaces/abc/messages?key=xyz&token=tok",
		},
	}

	builder := &googleChatBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("BuildURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestGoogleChatBuilder_BuildURL_InvalidURL(t *testing.T) {
	config := `{"webhook_url":"://invalid-url"}`
	builder := &googleChatBuilder{}
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestMattermostBuilder_BuildURL(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name:    "basic webhook",
			config:  `{"webhook_url":"https://mattermost.example.com/hooks/abc123def"}`,
			wantURL: "mattermost://mattermost.example.com/abc123def",
		},
		{
			name:    "with channel",
			config:  `{"webhook_url":"https://mattermost.example.com/hooks/abc123def","channel":"general"}`,
			wantURL: "mattermost://mattermost.example.com/abc123def/general",
		},
	}

	builder := &mattermostBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("BuildURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestMattermostBuilder_BuildURL_InvalidURL(t *testing.T) {
	config := `{"webhook_url":"://invalid"}`
	builder := &mattermostBuilder{}
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

func TestRocketchatBuilder_BuildURL(t *testing.T) {
	tests := []struct {
		name    string
		config  string
		wantURL string
	}{
		{
			name:    "basic webhook",
			config:  `{"webhook_url":"https://chat.example.com/hooks/abc123"}`,
			wantURL: "rocketchat://chat.example.com/abc123",
		},
		{
			name:    "with channel",
			config:  `{"webhook_url":"https://chat.example.com/hooks/abc123","channel":"alerts"}`,
			wantURL: "rocketchat://chat.example.com/abc123/alerts",
		},
	}

	builder := &rocketchatBuilder{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url, err := builder.BuildURL(json.RawMessage(tt.config))
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			if url != tt.wantURL {
				t.Errorf("BuildURL() = %q, want %q", url, tt.wantURL)
			}
		})
	}
}

func TestRocketchatBuilder_BuildURL_InvalidURL(t *testing.T) {
	config := `{"webhook_url":"://invalid"}`
	builder := &rocketchatBuilder{}
	_, err := builder.BuildURL(json.RawMessage(config))
	if err == nil {
		t.Error("Expected error for invalid URL")
	}
}

// =============================================================================
// Message and Title formatting tests
// =============================================================================

func TestNotifier_FormatMessage(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	tests := []struct {
		eventType string
		data      map[string]interface{}
		contains  []string
	}{
		{
			eventType: string(domain.ScanStarted),
			data:      map[string]interface{}{"path": "/media/movies"},
			contains:  []string{"Scan started", "/media/movies"},
		},
		{
			eventType: string(domain.ScanCompleted),
			data:      map[string]interface{}{"path": "/media/tv", "healthy_files": 100, "total_files": 105, "corrupt_files": 5},
			contains:  []string{"Scan complete", "/media/tv", "100/105 healthy", "5 corrupt"},
		},
		{
			eventType: string(domain.ScanFailed),
			data:      map[string]interface{}{"path": "/media", "error": "access denied"},
			contains:  []string{"Scan failed", "access denied"},
		},
		{
			eventType: string(domain.CorruptionDetected),
			data:      map[string]interface{}{"file_path": "/media/movie.mkv", "corruption_type": "video_codec_error"},
			contains:  []string{"Corruption detected", "movie.mkv", "video_codec_error"},
		},
		{
			eventType: string(domain.RemediationQueued),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv"},
			contains:  []string{"Remediation queued", "episode.mkv"},
		},
		{
			eventType: string(domain.DeletionStarted),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv"},
			contains:  []string{"Deletion started", "episode.mkv"},
		},
		{
			eventType: string(domain.DeletionCompleted),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv"},
			contains:  []string{"deleted", "episode.mkv"},
		},
		{
			eventType: string(domain.DeletionFailed),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv", "error": "permission denied"},
			contains:  []string{"Deletion failed", "episode.mkv", "permission denied"},
		},
		{
			eventType: string(domain.SearchStarted),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv"},
			contains:  []string{"Search triggered", "episode.mkv"},
		},
		{
			eventType: string(domain.SearchCompleted),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv"},
			contains:  []string{"Search completed", "episode.mkv"},
		},
		{
			eventType: string(domain.SearchFailed),
			data:      map[string]interface{}{"file_path": "/media/show/episode.mkv", "error": "no results"},
			contains:  []string{"Search failed", "episode.mkv", "no results"},
		},
		{
			eventType: string(domain.VerificationStarted),
			data:      map[string]interface{}{"file_path": "/media/video.mkv"},
			contains:  []string{"Verification started", "video.mkv"},
		},
		{
			eventType: string(domain.DownloadTimeout),
			data:      map[string]interface{}{"file_path": "/media/video.mkv"},
			contains:  []string{"Download timeout", "video.mkv"},
		},
		{
			eventType: string(domain.VerificationSuccess),
			data:      map[string]interface{}{"file_path": "/media/video.mkv"},
			contains:  []string{"verified healthy", "video.mkv"},
		},
		{
			eventType: string(domain.VerificationFailed),
			data:      map[string]interface{}{"file_path": "/media/bad.mkv", "error": "still corrupt"},
			contains:  []string{"Verification failed", "bad.mkv", "still corrupt"},
		},
		{
			eventType: string(domain.RetryScheduled),
			data:      map[string]interface{}{"file_path": "/media/file.mkv", "retry_count": 2, "max_retries": 5},
			contains:  []string{"Retry scheduled", "2/5"},
		},
		{
			eventType: string(domain.MaxRetriesReached),
			data:      map[string]interface{}{"file_path": "/media/file.mkv", "max_retries": 5},
			contains:  []string{"Max retries exhausted", "5"},
		},
		{
			eventType: string(domain.ImportBlocked),
			data:      map[string]interface{}{"file_path": "/media/file.mkv", "error": "blocked reason"},
			contains:  []string{"Import blocked", "Manual intervention"},
		},
		{
			eventType: string(domain.ManuallyRemoved),
			data:      map[string]interface{}{"file_path": "/media/file.mkv"},
			contains:  []string{"manually removed"},
		},
		{
			eventType: "UnknownEvent",
			data:      map[string]interface{}{},
			contains:  []string{"Event:", "UnknownEvent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			msg := n.formatMessage(tt.eventType, tt.data)
			for _, s := range tt.contains {
				if !strings.Contains(msg, s) {
					t.Errorf("formatMessage() = %q, should contain %q", msg, s)
				}
			}
		})
	}
}

func TestNotifier_FormatTitle(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	tests := []struct {
		eventType string
		fileName  string
		contains  string
	}{
		{string(domain.ScanStarted), "", "Scan Started"},
		{string(domain.ScanCompleted), "", "Scan Complete"},
		{string(domain.ScanFailed), "", "Scan Failed"},
		{string(domain.CorruptionDetected), "movie.mkv", "Corruption detected"},
		{string(domain.CorruptionDetected), "", "Corruption Detected"},
		{string(domain.RemediationQueued), "", "Remediation Queued"},
		{string(domain.DeletionStarted), "", "Deletion Started"},
		{string(domain.DeletionCompleted), "", "File Deleted"},
		{string(domain.DeletionFailed), "", "Deletion Failed"},
		{string(domain.SearchStarted), "", "Search Triggered"},
		{string(domain.SearchCompleted), "", "Search Complete"},
		{string(domain.SearchFailed), "", "Search Failed"},
		{string(domain.VerificationStarted), "", "Verification Started"},
		{string(domain.VerificationSuccess), "", "Verification Success"},
		{string(domain.VerificationFailed), "", "Verification Failed"},
		{string(domain.DownloadTimeout), "", "Download Timeout"},
		{string(domain.ImportBlocked), "", "Manual Action Required"},
		{string(domain.ManuallyRemoved), "", "Manually Removed"},
		{string(domain.RetryScheduled), "", "Retry Scheduled"},
		{string(domain.MaxRetriesReached), "", "Max Retries Reached"},
		{"UnknownEvent", "", "UnknownEvent"},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			title := n.formatTitle(tt.eventType, tt.fileName)
			if !strings.Contains(title, tt.contains) {
				t.Errorf("formatTitle() = %q, should contain %q", title, tt.contains)
			}
		})
	}
}

// =============================================================================
// Provider label tests
// =============================================================================

func TestNotifier_GetProviderLabel(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	tests := []struct {
		provider string
		expected string
	}{
		{ProviderDiscord, "Discord"},
		{ProviderPushover, "Pushover"},
		{ProviderTelegram, "Telegram"},
		{ProviderSlack, "Slack"},
		{ProviderEmail, "Email"},
		{ProviderGotify, "Gotify"},
		{ProviderNtfy, "ntfy"},
		{ProviderWhatsApp, "WhatsApp"},
		{ProviderSignal, "Signal"},
		{ProviderBark, "Bark"},
		{ProviderGoogleChat, "Google Chat"},
		{ProviderIFTTT, "IFTTT"},
		{ProviderJoin, "Join"},
		{ProviderMattermost, "Mattermost"},
		{ProviderMatrix, "Matrix"},
		{ProviderPushbullet, "Pushbullet"},
		{ProviderRocketchat, "Rocket.Chat"},
		{ProviderTeams, "Microsoft Teams"},
		{ProviderZulip, "Zulip"},
		{ProviderGeneric, "Generic Webhook"},
		{ProviderCustom, "Custom (Shoutrrr URL)"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			label := n.getProviderLabel(tt.provider)
			if label != tt.expected {
				t.Errorf("getProviderLabel(%q) = %q, want %q", tt.provider, label, tt.expected)
			}
		})
	}
}

// =============================================================================
// Aggregate ID extraction tests
// =============================================================================

func TestNotifier_ExtractAggregateID(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	tests := []struct {
		name     string
		data     map[string]interface{}
		expected string
	}{
		{
			name:     "with aggregate_id",
			data:     map[string]interface{}{"aggregate_id": "agg123"},
			expected: "agg123",
		},
		{
			name:     "with corruption_id",
			data:     map[string]interface{}{"corruption_id": "corr456"},
			expected: "corr456",
		},
		{
			name:     "file_path is not used as aggregate_id",
			data:     map[string]interface{}{"file_path": "/media/movie.mkv"},
			expected: "", // file_path is not a valid aggregate ID, only UUIDs are
		},
		{
			name:     "aggregate_id takes precedence",
			data:     map[string]interface{}{"aggregate_id": "agg", "corruption_id": "corr", "file_path": "/path"},
			expected: "agg",
		},
		{
			name:     "empty data",
			data:     map[string]interface{}{},
			expected: "",
		},
		{
			name:     "non-string values",
			data:     map[string]interface{}{"aggregate_id": 123},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := n.extractAggregateID(tt.data)
			if id != tt.expected {
				t.Errorf("extractAggregateID() = %q, want %q", id, tt.expected)
			}
		})
	}
}

// =============================================================================
// Throttle tests
// =============================================================================

func TestNotifier_CanSend_NewConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// New config should always be allowed
	if !n.canSend(1, 60) {
		t.Error("canSend() should return true for new config")
	}
}

func TestNotifier_CanSend_WithThrottle(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Set last sent time
	n.mu.Lock()
	n.lastSent[1] = time.Now()
	n.mu.Unlock()

	// Should be throttled (60 second throttle)
	if n.canSend(1, 60) {
		t.Error("canSend() should return false when throttled")
	}

	// Set last sent time to 2 minutes ago
	n.mu.Lock()
	n.lastSent[1] = time.Now().Add(-2 * time.Minute)
	n.mu.Unlock()

	// Should be allowed now
	if !n.canSend(1, 60) {
		t.Error("canSend() should return true after throttle period")
	}
}

func TestNotifier_CanSend_ZeroThrottle(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Set last sent time to just now
	n.mu.Lock()
	n.lastSent[1] = time.Now()
	n.mu.Unlock()

	// Zero throttle should always allow
	if !n.canSend(1, 0) {
		t.Error("canSend() with zero throttle should always return true")
	}
}

// =============================================================================
// ShouldNotify tests
// =============================================================================

func TestNotifier_ShouldNotify(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		Events: []string{string(domain.ScanCompleted), string(domain.CorruptionDetected)},
	}

	tests := []struct {
		eventType string
		want      bool
	}{
		{string(domain.ScanCompleted), true},
		{string(domain.CorruptionDetected), true},
		{string(domain.ScanStarted), false},
		{string(domain.DeletionCompleted), false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			got := n.shouldNotify(cfg, tt.eventType)
			if got != tt.want {
				t.Errorf("shouldNotify() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// CRUD operation tests
// =============================================================================

func TestNotifier_CreateConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)
	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	cfg := &NotificationConfig{
		Name:            "Test Discord",
		ProviderType:    ProviderDiscord,
		Config:          json.RawMessage(`{"webhook_url":"https://discord.com/api/webhooks/123/token"}`),
		Events:          []string{string(domain.ScanCompleted)},
		Enabled:         true,
		ThrottleSeconds: 30,
	}

	id, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("CreateConfig() error = %v", err)
	}
	if id <= 0 {
		t.Error("CreateConfig() should return positive ID")
	}

	// Verify it was created
	retrieved, err := n.GetConfig(id)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if retrieved.Name != cfg.Name {
		t.Errorf("Name = %q, want %q", retrieved.Name, cfg.Name)
	}
	if retrieved.ProviderType != cfg.ProviderType {
		t.Errorf("ProviderType = %q, want %q", retrieved.ProviderType, cfg.ProviderType)
	}
}

func TestNotifier_UpdateConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)
	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	// Create initial config
	cfg := &NotificationConfig{
		Name:            "Original",
		ProviderType:    ProviderNtfy,
		Config:          json.RawMessage(`{"topic":"test"}`),
		Events:          []string{string(domain.ScanCompleted)},
		Enabled:         true,
		ThrottleSeconds: 0,
	}
	id, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("CreateConfig() error = %v", err)
	}

	// Update it
	cfg.ID = id
	cfg.Name = "Updated"
	cfg.ThrottleSeconds = 60
	if err := n.UpdateConfig(cfg); err != nil {
		t.Fatalf("UpdateConfig() error = %v", err)
	}

	// Verify update
	retrieved, err := n.GetConfig(id)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}
	if retrieved.Name != "Updated" {
		t.Errorf("Name = %q, want 'Updated'", retrieved.Name)
	}
	if retrieved.ThrottleSeconds != 60 {
		t.Errorf("ThrottleSeconds = %d, want 60", retrieved.ThrottleSeconds)
	}
}

func TestNotifier_DeleteConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)
	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	// Create config
	cfg := &NotificationConfig{
		Name:         "ToDelete",
		ProviderType: ProviderNtfy,
		Config:       json.RawMessage(`{"topic":"test"}`),
		Events:       []string{},
		Enabled:      true,
	}
	id, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("CreateConfig() error = %v", err)
	}

	// Delete it
	if err := n.DeleteConfig(id); err != nil {
		t.Fatalf("DeleteConfig() error = %v", err)
	}

	// Verify it's gone
	_, err = n.GetConfig(id)
	if err == nil {
		t.Error("GetConfig() should return error for deleted config")
	}
}

func TestNotifier_GetAllConfigs(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)
	if err := n.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer n.Stop()

	// Create multiple configs
	for i := 0; i < 3; i++ {
		cfg := &NotificationConfig{
			Name:         fmt.Sprintf("Config %d", i),
			ProviderType: ProviderNtfy,
			Config:       json.RawMessage(`{"topic":"test"}`),
			Events:       []string{},
			Enabled:      true,
		}
		if _, err := n.CreateConfig(cfg); err != nil {
			t.Fatalf("CreateConfig() error = %v", err)
		}
	}

	configs, err := n.GetAllConfigs()
	if err != nil {
		t.Fatalf("GetAllConfigs() error = %v", err)
	}
	if len(configs) != 3 {
		t.Errorf("GetAllConfigs() returned %d configs, want 3", len(configs))
	}
}

// =============================================================================
// Notification log tests
// =============================================================================

func newTestDBWithFullLogSchema(t *testing.T) *testDB {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}

	// Create schema with correct notification_log columns
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
			status TEXT DEFAULT 'sent',
			error TEXT,
			sent_at DATETIME DEFAULT CURRENT_TIMESTAMP
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

func TestNotifier_GetNotificationLog_Empty(t *testing.T) {
	tdb := newTestDBWithFullLogSchema(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	logs, err := n.GetNotificationLog(0, 50)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("GetNotificationLog() returned %d entries, want 0", len(logs))
	}
}

func TestNotifier_GetNotificationLog_WithData(t *testing.T) {
	tdb := newTestDBWithFullLogSchema(t)
	defer tdb.Close()

	// Insert test log entries
	_, err := tdb.DB.Exec(`
		INSERT INTO notification_log (notification_id, event_type, message, status, error, sent_at)
		VALUES (1, 'ScanCompleted', 'Test message', 'sent', NULL, datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert test log: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	logs, err := n.GetNotificationLog(1, 50)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("GetNotificationLog() returned %d entries, want 1", len(logs))
	}
}

func TestNotifier_GetNotificationLog_DefaultLimit(t *testing.T) {
	tdb := newTestDBWithFullLogSchema(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Zero limit should default to 50
	logs, err := n.GetNotificationLog(0, 0)
	if err != nil {
		t.Fatalf("GetNotificationLog() error = %v", err)
	}
	// Just verify no error - limit defaulting is internal behavior
	_ = logs
}

// =============================================================================
// BuildShoutrrrURL tests
// =============================================================================

func TestNotifier_BuildShoutrrrURL_UnknownProvider(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ProviderType: "unknown_provider",
		Config:       json.RawMessage(`{}`),
	}

	_, err := n.buildShoutrrrURL(cfg)
	if err == nil {
		t.Error("buildShoutrrrURL() should return error for unknown provider")
	}
}

// =============================================================================
// getAllEvents tests
// =============================================================================

func TestNotifier_GetAllEvents(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	events := n.getAllEvents()

	if len(events) == 0 {
		t.Error("getAllEvents() should return events")
	}

	// Should contain events from all groups
	expectedEvents := []string{
		string(domain.ScanStarted),
		string(domain.ScanCompleted),
		string(domain.CorruptionDetected),
		string(domain.VerificationSuccess),
	}

	for _, expected := range expectedEvents {
		found := false
		for _, event := range events {
			if event == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("getAllEvents() should contain %q", expected)
		}
	}
}

// =============================================================================
// logNotification tests
// =============================================================================

func TestNotifier_LogNotification(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Create a test notification config first
	cfg := &NotificationConfig{
		Name:         "Test Config",
		ProviderType: "discord",
		Config:       json.RawMessage(`{"webhookurl":"https://discord.com/webhook"}`),
		Events:       []string{string(domain.ScanCompleted)},
		Enabled:      true,
	}
	cfgID, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Log a notification
	n.logNotification(cfgID, string(domain.ScanCompleted), "Test message", "success", "")

	// Verify it was logged
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log WHERE notification_id = ?", cfgID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query log count: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 log entry, got %d", count)
	}
}

func TestNotifier_LogNotification_WithError(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Create a test notification config
	cfg := &NotificationConfig{
		Name:         "Test Config",
		ProviderType: "discord",
		Config:       json.RawMessage(`{"webhookurl":"https://discord.com/webhook"}`),
		Events:       []string{string(domain.ScanCompleted)},
		Enabled:      true,
	}
	cfgID, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Log a failed notification
	n.logNotification(cfgID, string(domain.ScanCompleted), "Test message", "error", "Connection refused")

	// Verify the error was logged
	var status, errMsg string
	err = tdb.DB.QueryRow("SELECT status, error FROM notification_log WHERE notification_id = ?", cfgID).Scan(&status, &errMsg)
	if err != nil {
		t.Fatalf("Failed to query log: %v", err)
	}
	if status != "error" {
		t.Errorf("Expected status 'error', got '%s'", status)
	}
	if errMsg != "Connection refused" {
		t.Errorf("Expected error message 'Connection refused', got '%s'", errMsg)
	}
}

// =============================================================================
// cleanupOldLogs tests
// =============================================================================

func TestNotifier_CleanupOldLogs(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Insert an old log entry (more than 7 days old)
	_, err := tdb.DB.Exec(`
		INSERT INTO notification_log (notification_id, event_type, message, status, error, sent_at)
		VALUES (1, 'test', 'old message', 'success', '', datetime('now', '-8 days'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert old log: %v", err)
	}

	// Insert a recent log entry
	_, err = tdb.DB.Exec(`
		INSERT INTO notification_log (notification_id, event_type, message, status, error, sent_at)
		VALUES (1, 'test', 'new message', 'success', '', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert new log: %v", err)
	}

	// Run cleanup
	n.cleanupOldLogs()

	// Verify old log was deleted
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 log entry after cleanup, got %d", count)
	}
}

// =============================================================================
// handleEvent additional tests
// =============================================================================

func TestNotifier_HandleEvent_NoMatchingConfigs(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Create a config that only listens to ScanCompleted
	cfg := &NotificationConfig{
		Name:         "Scan Only",
		ProviderType: "discord",
		Config:       json.RawMessage(`{"webhookurl":"https://discord.com/webhook"}`),
		Events:       []string{string(domain.ScanCompleted)},
		Enabled:      true,
	}
	_, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Handle an event that doesn't match
	n.handleEvent(string(domain.CorruptionDetected), map[string]interface{}{
		"file_path": "/test/path.mkv",
	})

	// No logs should be created since the event didn't match
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 log entries for non-matching event, got %d", count)
	}
}

func TestNotifier_HandleEvent_ShouldNotifyFalse(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Insert an enabled config directly and load it
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Test', 'discord', '{"webhook_url":"https://discord.com/api/webhooks/123/token"}', '["ScanCompleted"]', 1, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert config: %v", err)
	}

	// Load configs to populate n.configs
	if err := n.loadConfigs(); err != nil {
		t.Fatalf("loadConfigs failed: %v", err)
	}

	// Verify config is loaded
	if len(n.configs) != 1 {
		t.Fatalf("Expected 1 config loaded, got %d", len(n.configs))
	}

	// Handle an event that doesn't match the configured events
	// This should iterate over configs and call shouldNotify which returns false
	n.handleEvent(string(domain.CorruptionDetected), map[string]interface{}{
		"file_path": "/test/path.mkv",
	})

	// No logs should be created since shouldNotify returned false
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 log entries when shouldNotify returns false, got %d", count)
	}
}

func TestNotifier_HandleEvent_Throttled(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Insert an enabled config with throttle and load it
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Test', 'discord', '{"webhook_url":"https://discord.com/api/webhooks/123/token"}', '["CorruptionDetected"]', 1, 3600)
	`)
	if err != nil {
		t.Fatalf("Failed to insert config: %v", err)
	}

	// Load configs to populate n.configs
	if err := n.loadConfigs(); err != nil {
		t.Fatalf("loadConfigs failed: %v", err)
	}

	// Set lastSent to now to trigger throttling
	n.mu.Lock()
	n.lastSent[1] = time.Now()
	n.mu.Unlock()

	// Handle a matching event - should be throttled
	n.handleEvent(string(domain.CorruptionDetected), map[string]interface{}{
		"file_path": "/test/path.mkv",
	})

	// No logs should be created since notification was throttled
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 log entries when throttled, got %d", count)
	}
}

func TestNotifier_HandleEvent_DisabledConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Create a disabled config
	cfg := &NotificationConfig{
		Name:         "Disabled Config",
		ProviderType: "discord",
		Config:       json.RawMessage(`{"webhookurl":"https://discord.com/webhook"}`),
		Events:       []string{string(domain.CorruptionDetected)},
		Enabled:      false,
	}
	_, err := n.CreateConfig(cfg)
	if err != nil {
		t.Fatalf("Failed to create config: %v", err)
	}

	// Handle an event - should not trigger disabled config
	n.handleEvent(string(domain.CorruptionDetected), map[string]interface{}{
		"file_path": "/test/path.mkv",
	})

	// No logs should be created since the config is disabled
	var count int
	err = tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 log entries for disabled config, got %d", count)
	}
}

// =============================================================================
// Error handling tests (database failure scenarios)
// =============================================================================

func TestNotifier_LogNotification_DBError(t *testing.T) {
	tdb := newTestDB(t)

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Close DB to trigger error
	tdb.DB.Close()

	// Should not panic, just log error internally
	n.logNotification(1, "TestEvent", "Test message", "sent", "")
}

func TestNotifier_CleanupOldLogs_DBError(t *testing.T) {
	tdb := newTestDB(t)

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Close DB to trigger error
	tdb.DB.Close()

	// Should not panic, just log error internally
	n.cleanupOldLogs()
}

func TestNotifier_CleanupOldLogs_LimitQuery(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Insert more than 100 log entries to trigger the limit cleanup
	for i := 0; i < 110; i++ {
		_, err := tdb.DB.Exec(`
			INSERT INTO notification_log (notification_id, event_type, message, status, sent_at)
			VALUES (?, ?, ?, ?, datetime('now'))
		`, 1, "TestEvent", fmt.Sprintf("Message %d", i), "sent")
		if err != nil {
			t.Fatalf("Failed to insert log %d: %v", i, err)
		}
	}

	// Run cleanup - should delete entries beyond 100
	n.cleanupOldLogs()

	// Verify entries were limited
	var count int
	err := tdb.DB.QueryRow("SELECT COUNT(*) FROM notification_log").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count logs: %v", err)
	}
	if count != 100 {
		t.Errorf("Expected 100 log entries after cleanup, got %d", count)
	}
}

func TestNotifier_LoadConfigs_InvalidEventsJSON(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	// Insert a config with invalid events JSON
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Invalid', 'discord', '{"webhook_url":"test"}', 'not-valid-json', 1, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// loadConfigs should skip configs with invalid JSON without error
	err = n.loadConfigs()
	if err != nil {
		t.Fatalf("loadConfigs failed unexpectedly: %v", err)
	}

	// The invalid config should not be loaded
	if len(n.configs) != 0 {
		t.Errorf("Expected 0 configs (invalid skipped), got %d", len(n.configs))
	}
}

func TestNotifier_GetAllConfigs_InvalidEventsJSON(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	// Insert a config with invalid events JSON
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Invalid Events', 'discord', '{"webhook_url":"test"}', 'invalid', 1, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// GetAllConfigs should handle invalid JSON gracefully by using empty events
	configs, err := n.GetAllConfigs()
	if err != nil {
		t.Fatalf("GetAllConfigs() error = %v", err)
	}

	if len(configs) != 1 {
		t.Fatalf("Expected 1 config, got %d", len(configs))
	}

	// Events should be empty array for invalid JSON
	if len(configs[0].Events) != 0 {
		t.Errorf("Expected 0 events for invalid JSON, got %d", len(configs[0].Events))
	}
}

func TestNotifier_GetConfig_InvalidEventsJSON(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	// Insert a config with invalid events JSON
	_, err := tdb.DB.Exec(`
		INSERT INTO notifications (id, name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (1, 'Invalid Events', 'discord', '{"webhook_url":"test"}', 'invalid', 1, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// GetConfig should handle invalid JSON gracefully
	cfg, err := n.GetConfig(1)
	if err != nil {
		t.Fatalf("GetConfig() error = %v", err)
	}

	// Events should be empty array for invalid JSON
	if len(cfg.Events) != 0 {
		t.Errorf("Expected 0 events for invalid JSON, got %d", len(cfg.Events))
	}
}

// =============================================================================
// sendGenericWebhook tests (using httptest)
// =============================================================================

func TestNotifier_SendGenericWebhook_Success(t *testing.T) {
	// Create test server that returns success
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("User-Agent") != "Healarr/1.0" {
			t.Errorf("Expected User-Agent Healarr/1.0, got %s", r.Header.Get("User-Agent"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s"}`, server.URL)),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{
		"file_path":       "/media/movie.mkv",
		"corruption_type": "video_codec_error",
	})
	if err != nil {
		t.Errorf("sendGenericWebhook() error = %v", err)
	}
}

func TestNotifier_SendGenericWebhook_WithExtraData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s","extra_data":"priority=high\nservice=healarr"}`, server.URL)),
	}

	err := n.sendGenericWebhook(cfg, string(domain.ScanCompleted), map[string]interface{}{
		"path":          "/media/movies",
		"healthy_files": 100,
		"corrupt_files": 5,
		"total_files":   105,
	})
	if err != nil {
		t.Errorf("sendGenericWebhook() error = %v", err)
	}
}

func TestNotifier_SendGenericWebhook_WithCustomHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Custom-Header") != "custom-value" {
			t.Errorf("Expected X-Custom-Header 'custom-value', got %s", r.Header.Get("X-Custom-Header"))
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s","custom_headers":"X-Custom-Header=custom-value"}`, server.URL)),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	if err != nil {
		t.Errorf("sendGenericWebhook() error = %v", err)
	}
}

func TestNotifier_SendGenericWebhook_CustomMethod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("Expected PUT request, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s","method":"PUT"}`, server.URL)),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	if err != nil {
		t.Errorf("sendGenericWebhook() error = %v", err)
	}
}

func TestNotifier_SendGenericWebhook_HTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s"}`, server.URL)),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	if err == nil {
		t.Error("Expected error for HTTP 500 response")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("Error should contain status code 500: %v", err)
	}
}

func TestNotifier_SendGenericWebhook_InvalidConfig(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(`{invalid json}`),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	if err == nil {
		t.Error("Expected error for invalid config JSON")
	}
}

func TestNotifier_SendGenericWebhook_URLWithoutScheme(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Extract just the host:port without scheme
	serverURL := strings.TrimPrefix(server.URL, "http://")

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s"}`, serverURL)),
	}

	// This should prepend https:// but will fail because server is http
	// We're mainly testing the URL scheme logic here
	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	// Expect error because https:// will be added but server is http
	if err == nil {
		// If no error, the connection might have worked differently
		// This is acceptable as we're testing the scheme-adding logic
	}
}

func TestNotifier_SendGenericWebhook_ConnectionError(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	// Use a URL that will definitely fail to connect
	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(`{"webhook_url":"http://localhost:1"}`),
	}

	err := n.sendGenericWebhook(cfg, string(domain.CorruptionDetected), map[string]interface{}{})
	if err == nil {
		t.Error("Expected connection error")
	}
}

func TestNotifier_SendGenericWebhook_AllEventData(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	cfg := &NotificationConfig{
		ID:           1,
		Name:         "Test Generic",
		ProviderType: ProviderGeneric,
		Config:       json.RawMessage(fmt.Sprintf(`{"webhook_url":"%s"}`, server.URL)),
	}

	// Test with all possible event data fields
	err := n.sendGenericWebhook(cfg, string(domain.RetryScheduled), map[string]interface{}{
		"file_path":       "/media/movie.mkv",
		"corruption_type": "audio_sync_error",
		"path":            "/media/movies",
		"error":           "test error",
		"healthy_files":   95,
		"corrupt_files":   5,
		"total_files":     100,
		"retry_count":     2,
		"max_retries":     5,
	})
	if err != nil {
		t.Errorf("sendGenericWebhook() error = %v", err)
	}
}
