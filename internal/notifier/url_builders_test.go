package notifier

import (
	"encoding/json"
	"testing"
)

// =============================================================================
// URLBuilder Tests
// =============================================================================

func TestDiscordBuilder_BuildURL(t *testing.T) {
	builder := &discordBuilder{}

	t.Run("builds valid Discord URL", func(t *testing.T) {
		config := json.RawMessage(`{"webhook_url":"https://discord.com/api/webhooks/123456/abcdef"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		// Format: discord://token@id
		expected := "discord://abcdef@123456"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("returns error for invalid JSON", func(t *testing.T) {
		config := json.RawMessage(`{invalid}`)
		_, err := builder.BuildURL(config)
		if err == nil {
			t.Error("Expected error for invalid JSON")
		}
	})

	t.Run("returns error for empty webhook URL", func(t *testing.T) {
		config := json.RawMessage(`{"webhook_url":""}`)
		_, err := builder.BuildURL(config)
		if err == nil {
			t.Error("Expected error for empty webhook URL")
		}
	})
}

func TestPushoverBuilder_BuildURL(t *testing.T) {
	builder := &pushoverBuilder{}

	t.Run("builds valid Pushover URL", func(t *testing.T) {
		// Uses app_token field (not api_token)
		config := json.RawMessage(`{"app_token":"token123","user_key":"user456"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		// Format includes trailing slash
		expected := "pushover://shoutrrr:token123@user456/"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("builds URL with empty app_token", func(t *testing.T) {
		// Implementation doesn't validate missing tokens, just produces empty URL parts
		config := json.RawMessage(`{"user_key":"user456"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "pushover://shoutrrr:@user456/"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})
}

func TestSlackBuilder_BuildURL(t *testing.T) {
	builder := &slackBuilder{}

	t.Run("builds valid Slack URL", func(t *testing.T) {
		config := json.RawMessage(`{"webhook_url":"https://hooks.slack.com/services/T123/B456/abc"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "slack://hook:T123-B456-abc@webhook"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("returns error for invalid Slack URL", func(t *testing.T) {
		config := json.RawMessage(`{"webhook_url":"https://invalid.com/webhook"}`)
		_, err := builder.BuildURL(config)
		if err == nil {
			t.Error("Expected error for invalid Slack URL")
		}
	})
}

func TestTelegramBuilder_BuildURL(t *testing.T) {
	builder := &telegramBuilder{}

	t.Run("builds valid Telegram URL", func(t *testing.T) {
		config := json.RawMessage(`{"bot_token":"123456:ABCDEF","chat_id":"@mychannel"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "telegram://123456:ABCDEF@telegram?chats=@mychannel"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("builds URL with empty bot_token", func(t *testing.T) {
		// Implementation doesn't validate missing tokens
		config := json.RawMessage(`{"chat_id":"@channel"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "telegram://@telegram?chats=@channel"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})
}

func TestEmailBuilder_BuildURL(t *testing.T) {
	builder := &emailBuilder{}

	t.Run("builds valid Email URL with auth", func(t *testing.T) {
		config := json.RawMessage(`{"smtp_host":"smtp.example.com","smtp_port":587,"username":"user","password":"pass","from":"sender@example.com","to":"recipient@example.com"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if url == "" {
			t.Error("Expected non-empty URL")
		}
	})

	t.Run("builds valid Email URL without auth", func(t *testing.T) {
		config := json.RawMessage(`{"smtp_host":"smtp.example.com","smtp_port":25,"from":"sender@example.com","to":"recipient@example.com"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if url == "" {
			t.Error("Expected non-empty URL")
		}
	})
}

func TestGotifyBuilder_BuildURL(t *testing.T) {
	builder := &gotifyBuilder{}

	t.Run("builds valid Gotify URL", func(t *testing.T) {
		config := json.RawMessage(`{"server_url":"https://gotify.example.com","app_token":"token123"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "gotify://gotify.example.com/token123"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})
}

func TestNtfyBuilder_BuildURL(t *testing.T) {
	builder := &ntfyBuilder{}

	t.Run("builds valid ntfy URL with default server", func(t *testing.T) {
		config := json.RawMessage(`{"topic":"mytopic"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "ntfy://ntfy.sh/mytopic"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("builds valid ntfy URL with custom server", func(t *testing.T) {
		config := json.RawMessage(`{"server_url":"https://my.ntfy.server","topic":"mytopic"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		expected := "ntfy://my.ntfy.server/mytopic"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})

	t.Run("builds ntfy URL ignoring authentication", func(t *testing.T) {
		// Note: The current implementation doesn't include auth in the URL
		config := json.RawMessage(`{"server_url":"https://my.ntfy.server","topic":"mytopic","username":"user","password":"pass"}`)
		url, err := builder.BuildURL(config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		// Implementation doesn't include auth credentials in URL
		expected := "ntfy://my.ntfy.server/mytopic"
		if url != expected {
			t.Errorf("Expected %q, got %q", expected, url)
		}
	})
}

func TestUrlBuilders_MapCompleteness(t *testing.T) {
	// Verify all providers have builders
	expectedProviders := []string{
		ProviderDiscord,
		ProviderPushover,
		ProviderSlack,
		ProviderTelegram,
		ProviderEmail,
		ProviderGotify,
		ProviderNtfy,
		ProviderMattermost,
		ProviderRocketchat,
		ProviderPushbullet,
		ProviderJoin,
		ProviderIFTTT,
		ProviderMatrix,
		ProviderZulip,
		ProviderTeams,
		ProviderGeneric,
		ProviderBark,
		ProviderWhatsApp,
		ProviderSignal,
		ProviderGoogleChat,
		ProviderCustom,
	}

	for _, provider := range expectedProviders {
		t.Run(provider, func(t *testing.T) {
			if _, ok := urlBuilders[provider]; !ok {
				t.Errorf("Missing URL builder for provider: %s", provider)
			}
		})
	}
}

// =============================================================================
// Benchmarks
// =============================================================================

func BenchmarkDiscordBuilder_BuildURL(b *testing.B) {
	builder := &discordBuilder{}
	config := json.RawMessage(`{"webhook_url":"https://discord.com/api/webhooks/123456789012345678/abcdefghijklmnopqrstuvwxyz1234567890ABCDEFGHIJKLMNOP"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkPushoverBuilder_BuildURL(b *testing.B) {
	builder := &pushoverBuilder{}
	config := json.RawMessage(`{"api_token":"azGDORePK8gMaC0QOYAMyEEuzJnyUI","user_key":"uQiRzpo4DXghDmr9QzzfQu27cmVRs"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkSlackBuilder_BuildURL(b *testing.B) {
	builder := &slackBuilder{}
	config := json.RawMessage(`{"webhook_url":"https://hooks.slack.com/services/TABC/BDEF/testtoken"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkTelegramBuilder_BuildURL(b *testing.B) {
	builder := &telegramBuilder{}
	config := json.RawMessage(`{"bot_token":"123456789:ABCdefGhIJKlmNoPQRsTUVwxyZ","chat_id":"@mychannel"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkEmailBuilder_BuildURL(b *testing.B) {
	builder := &emailBuilder{}
	config := json.RawMessage(`{"smtp_host":"smtp.gmail.com","smtp_port":587,"username":"user@gmail.com","password":"password123","from":"sender@gmail.com","to":"recipient@example.com"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkNtfyBuilder_BuildURL(b *testing.B) {
	builder := &ntfyBuilder{}
	config := json.RawMessage(`{"server_url":"https://ntfy.example.com","topic":"alerts","username":"admin","password":"secret123"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = builder.BuildURL(config)
	}
}

func BenchmarkUrlBuilderLookup(b *testing.B) {
	providers := []string{
		ProviderDiscord,
		ProviderSlack,
		ProviderTelegram,
		ProviderNtfy,
		ProviderEmail,
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, provider := range providers {
			_ = urlBuilders[provider]
		}
	}
}
