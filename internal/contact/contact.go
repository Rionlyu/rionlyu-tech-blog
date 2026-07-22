// Package contact owns contact-message validation, delivery boundaries, and rate policy.
package contact

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	MaxNameRunes    = 100
	MaxEmailRunes   = 254
	MaxMessageRunes = 5000
)

var ErrUnavailable = errors.New("contact delivery is unavailable")

// Message is the narrow provider-neutral contact payload.
type Message struct {
	Name  string `json:"name"`
	Email string `json:"email"`
	Body  string `json:"message"`
}

// FieldErrors maps stable field names to safe user-facing validation messages.
type FieldErrors map[string]string

func (e FieldErrors) Any() bool {
	return len(e) > 0
}

// Validate normalizes a message and returns all field failures at once.
func Validate(input Message) (Message, FieldErrors) {
	message := Message{
		Name:  strings.TrimSpace(input.Name),
		Email: strings.TrimSpace(input.Email),
		Body:  strings.TrimSpace(input.Body),
	}
	errorsByField := make(FieldErrors)

	switch {
	case message.Name == "":
		errorsByField["name"] = "Enter your name."
	case !utf8.ValidString(message.Name) || utf8.RuneCountInString(message.Name) > MaxNameRunes:
		errorsByField["name"] = "Keep your name under 100 characters."
	case hasDisallowedControl(message.Name, false):
		errorsByField["name"] = "Your name contains an unsupported character."
	}

	switch {
	case message.Email == "":
		errorsByField["email"] = "Enter your email address."
	case !utf8.ValidString(message.Email) || utf8.RuneCountInString(message.Email) > MaxEmailRunes:
		errorsByField["email"] = "Enter a valid email address."
	default:
		address, err := mail.ParseAddress(message.Email)
		if err != nil || address.Address != message.Email || address.Name != "" || !strings.Contains(address.Address, "@") {
			errorsByField["email"] = "Enter a valid email address."
		}
	}

	switch {
	case message.Body == "":
		errorsByField["message"] = "Write a short message."
	case !utf8.ValidString(message.Body) || utf8.RuneCountInString(message.Body) > MaxMessageRunes:
		errorsByField["message"] = "Keep your message under 5,000 characters."
	case hasDisallowedControl(message.Body, true):
		errorsByField["message"] = "Your message contains an unsupported character."
	}

	return message, errorsByField
}

func hasDisallowedControl(value string, allowWhitespace bool) bool {
	for _, character := range value {
		if !unicode.IsControl(character) {
			continue
		}
		if allowWhitespace && (character == '\n' || character == '\r' || character == '\t') {
			continue
		}
		return true
	}
	return false
}

// Sender is the real external boundary. Success means the configured provider accepted the message.
type Sender interface {
	Send(context.Context, Message) error
}

type WebhookSender struct {
	client   *http.Client
	endpoint *url.URL
	token    string
}

func NewWebhookSender(client *http.Client, endpoint *url.URL, token string) *WebhookSender {
	return &WebhookSender{client: client, endpoint: endpoint, token: token}
}

func (s *WebhookSender) Send(ctx context.Context, message Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode contact payload: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "rionlyu.dev-contact/1")
	if s.token != "" {
		request.Header.Set("Authorization", "Bearer "+s.token)
	}

	response, err := s.client.Do(request)
	if err != nil {
		return fmt.Errorf("send provider request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("provider rejected contact with status %d", response.StatusCode)
	}
	return nil
}

// DevNullSender is an explicit development-only provider that accepts and discards messages.
type DevNullSender struct{}

func (DevNullSender) Send(context.Context, Message) error { return nil }

type DisabledSender struct{}

func (DisabledSender) Send(context.Context, Message) error { return ErrUnavailable }

type bucket struct {
	start time.Time
	count int
}

const maxLimiterBuckets = 4096

// Limiter is a small in-memory fixed-window limiter suitable for one blog process.
type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	now     func() time.Time
	buckets map[string]bucket
	calls   uint64
}

func NewLimiter(limit int, window time.Duration, now func() time.Time) *Limiter {
	if now == nil {
		now = time.Now
	}
	return &Limiter{limit: limit, window: window, now: now, buckets: make(map[string]bucket)}
}

// Allow records one attempt and reports how long remains when the key is blocked.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	current, exists := l.buckets[key]
	if !exists && len(l.buckets) >= maxLimiterBuckets {
		l.pruneExpired(now)
		if len(l.buckets) >= maxLimiterBuckets {
			return false, l.retryUntilCapacity(now)
		}
	}
	if !exists || now.Sub(current.start) >= l.window {
		l.buckets[key] = bucket{start: now, count: 1}
		l.prune(now)
		return true, 0
	}
	if current.count >= l.limit {
		return false, max(time.Second, l.window-now.Sub(current.start))
	}
	current.count++
	l.buckets[key] = current
	l.prune(now)
	return true, 0
}

func (l *Limiter) prune(now time.Time) {
	l.calls++
	if l.calls%128 != 0 {
		return
	}
	l.pruneExpired(now)
}

func (l *Limiter) pruneExpired(now time.Time) {
	for key, entry := range l.buckets {
		if now.Sub(entry.start) >= l.window {
			delete(l.buckets, key)
		}
	}
}

func (l *Limiter) retryUntilCapacity(now time.Time) time.Duration {
	retry := l.window
	for _, entry := range l.buckets {
		remaining := l.window - now.Sub(entry.start)
		if remaining < retry {
			retry = remaining
		}
	}
	return max(time.Second, retry)
}
