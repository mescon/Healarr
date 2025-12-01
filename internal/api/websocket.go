package api

import (
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

// getWebSocketUpgrader returns an upgrader with origin validation
// based on HEALARR_CORS_ORIGIN environment variable
func getWebSocketUpgrader() websocket.Upgrader {
	corsOrigins := os.Getenv("HEALARR_CORS_ORIGIN")
	allowedOrigins := make(map[string]bool)
	if corsOrigins != "" && corsOrigins != "*" {
		for _, origin := range strings.Split(corsOrigins, ",") {
			allowedOrigins[strings.TrimSpace(origin)] = true
		}
	}

	return websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			// If CORS is set to "*", allow all origins
			if corsOrigins == "*" {
				return true
			}
			// If no CORS origins configured, only allow same-origin
			if corsOrigins == "" {
				// Same-origin check: origin should match host
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // No origin header = same-origin request
				}
				// Parse origin and compare host
				host := r.Host
				return strings.Contains(origin, host)
			}
			// Check against allowed origins
			origin := r.Header.Get("Origin")
			return allowedOrigins[origin]
		},
	}
}

var upgrader = getWebSocketUpgrader()

type WebSocketHub struct {
	clients    map[*websocket.Conn]bool
	broadcast  chan interface{}
	register   chan *websocket.Conn
	unregister chan *websocket.Conn
	mu         sync.Mutex
	eventBus   *eventbus.EventBus
}

func NewWebSocketHub(eventBus *eventbus.EventBus) *WebSocketHub {
	h := &WebSocketHub{
		broadcast:  make(chan interface{}),
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		clients:    make(map[*websocket.Conn]bool),
		eventBus:   eventBus,
	}

	// Subscribe to all events
	types := []domain.EventType{
		domain.CorruptionDetected,
		domain.DeletionCompleted,
		domain.VerificationSuccess,
		domain.VerificationFailed,
		domain.RetryScheduled,
		domain.ScanStarted,
		domain.ScanCompleted,
		domain.ScanFailed,
		domain.ScanProgress,
	}

	for _, t := range types {
		eventBus.Subscribe(t, func(e domain.Event) {
			h.broadcast <- map[string]interface{}{
				"type": "event",
				"data": e,
			}
		})
	}

	// Subscribe to logs
	logCh := logger.Subscribe()
	go func() {
		for entry := range logCh {
			h.broadcast <- map[string]interface{}{
				"type": "log",
				"data": entry,
			}
		}
	}()

	go h.run()
	return h
}

func (h *WebSocketHub) run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			logger.Debugf("WebSocket client connected (Total: %d)", len(h.clients))
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				if err := client.Close(); err != nil {
					logger.Debugf("WebSocket close error: %v", err)
				}
				logger.Debugf("WebSocket client disconnected")
			}
			h.mu.Unlock()

		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				err := client.WriteJSON(message)
				if err != nil {
					logger.Errorf("WebSocket error: %v", err)
					if closeErr := client.Close(); closeErr != nil {
						logger.Debugf("WebSocket close error during broadcast: %v", closeErr)
					}
					delete(h.clients, client)
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *WebSocketHub) HandleConnection(c *gin.Context) {
	ws, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Errorf("Failed to upgrade to WebSocket: %v", err)
		return
	}
	h.register <- ws

	// Send initial ping to verify connection (safe before ping goroutine starts)
	h.mu.Lock()
	if err := ws.WriteJSON(gin.H{"type": "ping", "timestamp": time.Now()}); err != nil {
		logger.Debugf("Failed to send initial ping: %v", err)
	}
	h.mu.Unlock()

	// Set up ping/pong to keep connection alive
	const (
		pongWait   = 60 * time.Second
		pingPeriod = (pongWait * 9) / 10
	)

	if err := ws.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		logger.Debugf("Failed to set initial read deadline: %v", err)
	}
	ws.SetPongHandler(func(string) error {
		// SetReadDeadline error is returned to the pong handler caller
		return ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	// Send pings periodically
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	go func() {
		for range ticker.C {
			h.mu.Lock()
			_, exists := h.clients[ws]
			if !exists {
				h.mu.Unlock()
				return // Client disconnected, stop sending pings
			}
			// Write ping while holding mutex to prevent concurrent writes with broadcast
			err := ws.WriteMessage(websocket.PingMessage, nil)
			h.mu.Unlock()
			if err != nil {
				logger.Errorf("WebSocket ping error: %v", err)
				h.unregister <- ws
				return
			}
		}
	}()

	// Keep connection alive by reading messages (pings/pongs/close)
	// This loop blocks until the connection is closed or an error occurs.
	// The defer function will handle cleanup.
	defer func() {
		h.unregister <- ws // Unregister client when HandleConnection exits
		logger.Debugf("WebSocket client handler exited")
	}()

	for {
		// ReadMessage blocks until a message is received or an error occurs.
		// We don't necessarily care about the content of the message here,
		// as the pong handler updates the read deadline.
		// This loop primarily keeps the connection open and allows the pong handler to work.
		_, _, err := ws.ReadMessage()
		if err != nil {
			break
		}
	}
}

// ClientCount returns the number of connected WebSocket clients
func (h *WebSocketHub) ClientCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}
