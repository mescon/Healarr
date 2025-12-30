package api

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	_ "modernc.org/sqlite"
)

func setupTestDBForWebSocket(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-ws-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	schema := `
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_data JSON NOT NULL,
			event_version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_id TEXT
		);
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func TestNewWebSocketHub(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	if hub == nil {
		t.Fatal("NewWebSocketHub should not return nil")
	}

	if hub.clients == nil {
		t.Error("clients map should be initialized")
	}

	if hub.broadcast == nil {
		t.Error("broadcast channel should be initialized")
	}

	if hub.register == nil {
		t.Error("register channel should be initialized")
	}

	if hub.unregister == nil {
		t.Error("unregister channel should be initialized")
	}

	if hub.eventBus != eb {
		t.Error("eventBus should be set correctly")
	}
}

func TestWebSocketHub_ClientCount_Empty(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	count := hub.ClientCount()
	if count != 0 {
		t.Errorf("ClientCount() = %d, want 0 for empty hub", count)
	}
}

func TestWebSocketHub_RegisterUnregister(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	// Give the hub's run goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Create a test WebSocket connection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Register with hub
		hub.register <- ws

		// Keep connection open
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				hub.unregister <- ws
				return
			}
		}
	}))
	defer server.Close()

	// Connect
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("ClientCount() = %d, want 1 after registration", hub.ClientCount())
	}

	// Close connection (triggers unregister on server side)
	ws.Close()

	// Wait for unregistration
	time.Sleep(100 * time.Millisecond)

	if hub.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d, want 0 after unregistration", hub.ClientCount())
	}
}

func TestWebSocketHub_Broadcast(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	// Give the hub's run goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		hub.register <- ws

		// Keep connection alive - read until closed
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				hub.unregister <- ws
				return
			}
		}
	}))
	defer server.Close()

	// Connect as client
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// Broadcast a message
	testMessage := map[string]interface{}{
		"type": "test",
		"data": "hello world",
	}
	hub.broadcast <- testMessage

	// Client reads the broadcast message
	received := make(chan map[string]interface{}, 1)
	go func() {
		var msg map[string]interface{}
		if err := ws.ReadJSON(&msg); err == nil {
			received <- msg
		}
	}()

	// Wait for message to be received
	select {
	case msg := <-received:
		if msg["type"] != "test" {
			t.Errorf("Received message type = %v, want 'test'", msg["type"])
		}
		if msg["data"] != "hello world" {
			t.Errorf("Received message data = %v, want 'hello world'", msg["data"])
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for broadcast message")
	}
}

func TestGetWebSocketUpgrader_WildcardCORS(t *testing.T) {
	// Set CORS to allow all origins
	os.Setenv("HEALARR_CORS_ORIGIN", "*")
	defer os.Unsetenv("HEALARR_CORS_ORIGIN")

	upgrader := getWebSocketUpgrader()

	// Create a request with any origin
	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "https://any-origin.example.com")

	if !upgrader.CheckOrigin(req) {
		t.Error("Wildcard CORS should allow any origin")
	}
}

func TestGetWebSocketUpgrader_SpecificOrigins(t *testing.T) {
	// Set specific allowed origins
	os.Setenv("HEALARR_CORS_ORIGIN", "https://allowed1.com,https://allowed2.com")
	defer os.Unsetenv("HEALARR_CORS_ORIGIN")

	upgrader := getWebSocketUpgrader()

	tests := []struct {
		origin  string
		allowed bool
	}{
		{"https://allowed1.com", true},
		{"https://allowed2.com", true},
		{"https://notallowed.com", false},
		{"", false}, // Empty origin not in allowed list
	}

	for _, tt := range tests {
		t.Run(tt.origin, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/ws", nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			result := upgrader.CheckOrigin(req)
			if result != tt.allowed {
				t.Errorf("CheckOrigin(%q) = %v, want %v", tt.origin, result, tt.allowed)
			}
		})
	}
}

func TestGetWebSocketUpgrader_NoCORS_SameOrigin(t *testing.T) {
	// Clear CORS setting
	os.Unsetenv("HEALARR_CORS_ORIGIN")

	upgrader := getWebSocketUpgrader()

	// Request without Origin header (same-origin)
	req1 := httptest.NewRequest("GET", "/ws", nil)
	req1.Host = "localhost:8080"
	if !upgrader.CheckOrigin(req1) {
		t.Error("Same-origin request (no Origin header) should be allowed")
	}

	// Request with matching host in Origin
	req2 := httptest.NewRequest("GET", "/ws", nil)
	req2.Host = "localhost:8080"
	req2.Header.Set("Origin", "http://localhost:8080")
	if !upgrader.CheckOrigin(req2) {
		t.Error("Same-origin request should be allowed")
	}
}

func TestWebSocketHub_EventBroadcast(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	// Give the hub's run goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Create a test WebSocket server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		hub.register <- ws

		// Keep connection alive
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				hub.unregister <- ws
				return
			}
		}
	}))
	defer server.Close()

	// Connect as client
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer ws.Close()

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	// Client reads messages in goroutine
	received := make(chan map[string]interface{}, 10)
	go func() {
		for {
			var msg map[string]interface{}
			if err := ws.ReadJSON(&msg); err != nil {
				return
			}
			received <- msg
		}
	}()

	// Publish an event through the event bus
	eb.Publish(domain.Event{
		EventType:     domain.ScanStarted,
		AggregateType: "scan",
		AggregateID:   "test-scan-1",
		EventData:     map[string]interface{}{"path": "/test/path"},
	})

	// Wait for event to be broadcast
	select {
	case msg := <-received:
		if msg["type"] != "event" {
			t.Errorf("Received message type = %v, want 'event'", msg["type"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Timeout waiting for event broadcast")
	}
}

func TestWebSocketHub_ConcurrentClients(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	// Give the hub's run goroutine time to start
	time.Sleep(10 * time.Millisecond)

	numClients := 5
	connections := make([]*websocket.Conn, 0, numClients)

	// Create multiple connections
	for i := 0; i < numClients; i++ {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			upgrader := websocket.Upgrader{
				CheckOrigin: func(r *http.Request) bool { return true },
			}
			ws, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			hub.register <- ws
		}))

		url := "ws" + strings.TrimPrefix(server.URL, "http")
		ws, _, err := websocket.DefaultDialer.Dial(url, nil)
		if err != nil {
			t.Fatalf("Failed to connect client %d: %v", i, err)
		}
		connections = append(connections, ws)
		// Clean up server after test
		defer server.Close()
	}

	// Wait for all registrations
	time.Sleep(100 * time.Millisecond)

	count := hub.ClientCount()
	if count != numClients {
		t.Errorf("ClientCount() = %d, want %d", count, numClients)
	}

	// Close all connections
	for _, ws := range connections {
		ws.Close()
	}
}

func TestWebSocketHub_HandleConnection(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/ws", func(c *gin.Context) {
		hub.HandleConnection(c)
	})

	// Create test server
	server := httptest.NewServer(r)
	defer server.Close()

	// Connect via WebSocket
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	ws, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v (resp=%v)", err, resp)
	}
	defer ws.Close()

	// Wait for registration and initial ping
	time.Sleep(50 * time.Millisecond)

	// Should have received initial ping
	var msg map[string]interface{}
	ws.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if err := ws.ReadJSON(&msg); err != nil {
		t.Fatalf("Failed to read initial message: %v", err)
	}

	if msg["type"] != "ping" {
		t.Errorf("First message type = %v, want 'ping'", msg["type"])
	}

	// Client should be registered
	if hub.ClientCount() != 1 {
		t.Errorf("ClientCount() = %d, want 1", hub.ClientCount())
	}
}

func TestWebSocketHub_MultipleUnregistersSafe(t *testing.T) {
	db, cleanup := setupTestDBForWebSocket(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	hub := NewWebSocketHub(eb)

	// Give the hub's run goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Channel to get the server-side websocket for unregistration
	serverWS := make(chan *websocket.Conn, 1)

	// Create a test connection
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		}
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.register <- ws
		serverWS <- ws

		// Keep alive until client closes
		for {
			_, _, err := ws.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	url := "ws" + strings.TrimPrefix(server.URL, "http")
	clientWS, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientWS.Close()

	// Get the server-side websocket
	ws := <-serverWS

	// Wait for registration
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Errorf("ClientCount() = %d, want 1 after registration", hub.ClientCount())
	}

	// Unregister multiple times (should not panic or cause issues)
	hub.unregister <- ws
	hub.unregister <- ws
	hub.unregister <- ws

	// Wait for unregistrations to be processed
	time.Sleep(100 * time.Millisecond)

	// Count should be 0
	if hub.ClientCount() != 0 {
		t.Errorf("ClientCount() = %d, want 0", hub.ClientCount())
	}
}

func TestGetWebSocketUpgrader_SpacesInOrigins(t *testing.T) {
	// Set CORS with spaces around origins
	os.Setenv("HEALARR_CORS_ORIGIN", " https://origin1.com , https://origin2.com ")
	defer os.Unsetenv("HEALARR_CORS_ORIGIN")

	upgrader := getWebSocketUpgrader()

	req := httptest.NewRequest("GET", "/ws", nil)
	req.Header.Set("Origin", "https://origin1.com")

	if !upgrader.CheckOrigin(req) {
		t.Error("Origin should be allowed after trimming spaces")
	}
}
