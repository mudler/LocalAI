package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const WebSocketTicketProtocolPrefix = "localai.ticket."
const WebSocketTicketTTL = 30 * time.Second
const contextKeyWebSocketTicket = "auth_websocket_ticket"
const contextKeyTicketCredential = "auth_ticket_credential"

type ticketCredential struct{ name, value string }

// WebSocketTickets is application-local: tickets cannot move between frontends.
// Only hashes of random tickets are retained. Credentials are retained briefly
// so the normal authentication path can recheck revocation at redemption.
type WebSocketTickets struct {
	mu      sync.Mutex
	entries map[[32]byte]webSocketTicket
	now     func() time.Time
}
type webSocketTicket struct {
	path, origin, owner string
	credentials         http.Header
	expires             time.Time
	timer               *time.Timer
}
type WebSocketTicketResponse struct {
	Ticket    string    `json:"ticket"`
	ExpiresAt time.Time `json:"expires_at"`
}

func NewWebSocketTickets() *WebSocketTickets {
	return &WebSocketTickets{entries: make(map[[32]byte]webSocketTicket), now: time.Now}
}
func ticketOwner(c echo.Context) string {
	if user := GetUser(c); user != nil {
		return user.ID
	}
	return ""
}
func ValidWebSocketOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.ContainsAny(origin, "\r\n ,")
}
func (s *WebSocketTickets) expire(now time.Time) {
	for hash, ticket := range s.entries {
		if !now.Before(ticket.expires) {
			if ticket.timer != nil {
				ticket.timer.Stop()
			}
			delete(s.entries, hash)
		}
	}
}

// Issue must be called only after the endpoint authorizes the target resource.
func (s *WebSocketTickets) Issue(c echo.Context, path, origin string) (WebSocketTicketResponse, error) {
	if !ValidWebSocketOrigin(origin) {
		return WebSocketTicketResponse{}, echo.NewHTTPError(400, "a valid HTTP(S) browser origin is required")
	}
	if requestOrigin := c.Request().Header.Get("Origin"); requestOrigin != "" && requestOrigin != origin {
		return WebSocketTicketResponse{}, echo.NewHTTPError(400, "ticket origin must match the requesting browser origin")
	}
	credentials := make(http.Header)
	if credential, ok := c.Get(contextKeyTicketCredential).(ticketCredential); ok {
		credentials.Set(credential.name, credential.value)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.expire(now)
	owner := ticketOwner(c)
	count := 0
	for _, ticket := range s.entries {
		if ticket.owner == owner {
			count++
		}
	}
	if len(s.entries) >= 4096 || count >= 32 {
		return WebSocketTicketResponse{}, echo.NewHTTPError(429, "too many outstanding WebSocket tickets")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return WebSocketTicketResponse{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	expires := now.Add(WebSocketTicketTTL)
	hash := sha256.Sum256([]byte(token))
	ticket := webSocketTicket{path: path, origin: origin, owner: owner, credentials: credentials, expires: expires}
	// Bound credential lifetime even when no subsequent requests arrive.
	ticket.timer = time.AfterFunc(WebSocketTicketTTL, func() { s.mu.Lock(); delete(s.entries, hash); s.mu.Unlock() })
	s.entries[hash] = ticket
	return WebSocketTicketResponse{Ticket: token, ExpiresAt: expires}, nil
}
func (s *WebSocketTickets) consume(token, path, origin string) (webSocketTicket, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expire(s.now())
	hash := sha256.Sum256([]byte(token))
	ticket, ok := s.entries[hash]
	if !ok || ticket.path != path || ticket.origin != origin {
		return webSocketTicket{}, false
	}
	delete(s.entries, hash)
	if ticket.timer != nil {
		ticket.timer.Stop()
	}
	return ticket, true
}

// RevokePath releases outstanding tickets when the target resource closes.
func (s *WebSocketTickets) RevokePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for hash, ticket := range s.entries {
		if ticket.path == path {
			if ticket.timer != nil {
				ticket.timer.Stop()
			}
			delete(s.entries, hash)
		}
	}
}

func WebSocketTicketAuthenticated(c echo.Context) bool {
	ok, _ := c.Get(contextKeyWebSocketTicket).(bool)
	return ok
}

// WithWebSocketTickets authenticates a scoped ticket through the normal auth
// middleware, retaining feature, model and ownership checks on every upgrade.
func WithWebSocketTickets(store *WebSocketTickets, authenticate echo.MiddlewareFunc) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		ordinary := authenticate(next)
		return func(c echo.Context) error {
			var token string
			protocols := []string{}
			ticketCount := 0
			for _, protocol := range websocket.Subprotocols(c.Request()) {
				if strings.HasPrefix(protocol, WebSocketTicketProtocolPrefix) {
					ticketCount++
					token = strings.TrimPrefix(protocol, WebSocketTicketProtocolPrefix)
				} else {
					protocols = append(protocols, protocol)
				}
			}
			if ticketCount == 0 {
				return ordinary(c)
			}
			// Remove the credential before any subsequent request logging or upgrade.
			c.Request().Header.Del("Sec-WebSocket-Protocol")
			if len(protocols) > 0 {
				c.Request().Header.Set("Sec-WebSocket-Protocol", strings.Join(protocols, ", "))
			}
			if ticketCount != 1 || len(token) != 43 || c.Request().Method != http.MethodGet || !websocket.IsWebSocketUpgrade(c.Request()) {
				return echo.NewHTTPError(401, "invalid WebSocket ticket")
			}
			ticket, ok := store.consume(token, c.Request().URL.Path, c.Request().Header.Get("Origin"))
			if !ok {
				return echo.NewHTTPError(401, "invalid or expired WebSocket ticket")
			}
			original := c.Request()
			request := original.Clone(original.Context())
			for _, name := range []string{"Authorization", "X-Api-Key", "Xi-Api-Key", "Cookie"} {
				request.Header.Del(name)
				for _, value := range ticket.credentials.Values(name) {
					request.Header.Add(name, value)
				}
			}
			owner := ticket.owner
			ticket.credentials = nil
			c.SetRequest(request)
			defer c.SetRequest(original)
			return authenticate(func(c echo.Context) error {
				if ticketOwner(c) != owner {
					return echo.NewHTTPError(401, "WebSocket ticket authentication is no longer valid")
				}
				// Do not retain the original credential for the WebSocket lifetime.
				for _, name := range []string{"Authorization", "X-Api-Key", "Xi-Api-Key", "Cookie"} {
					request.Header.Del(name)
				}
				c.Set(contextKeyTicketCredential, nil)
				c.SetRequest(original)
				c.Set(contextKeyWebSocketTicket, true)
				return next(c)
			})(c)
		}
	}
}
