// Package config parses and validates process configuration from the environment.
package config

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	Development = "development"
	Production  = "production"
)

// Config contains the explicit runtime and HTTP policy used by the composition root.
type Config struct {
	Environment         string
	Port                int
	Origin              *url.URL
	ContactProvider     string
	ContactWebhookURL   *url.URL
	ContactWebhookToken string
	ContactTimeout      time.Duration
	ContactRateLimit    int
	ContactGlobalLimit  int
	ContactRateWindow   time.Duration
	ContactMaxBody      int64
	MaxInFlightRequests int
	CSRFSecret          []byte
	TrustedProxies      []netip.Prefix
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
}

func (c Config) IsProduction() bool {
	return c.Environment == Production
}

// Load reads configuration through lookup so it remains deterministic in tests.
func Load(lookup func(string) (string, bool)) (Config, error) {
	environment := valueOr(lookup, "ENVIRONMENT", Development)
	if environment != Development && environment != Production && environment != "test" {
		return Config{}, fmt.Errorf("ENVIRONMENT must be %q, %q, or %q", Development, Production, "test")
	}

	port, err := positiveInt(valueOr(lookup, "PORT", "8080"), "PORT")
	if err != nil || port > 65535 {
		if err == nil {
			err = errors.New("must be at most 65535")
		}
		return Config{}, fmt.Errorf("PORT: %w", err)
	}
	originText := valueOr(lookup, "CANONICAL_ORIGIN", fmt.Sprintf("http://localhost:%d", port))
	origin, err := parseOrigin(originText)
	if err != nil {
		return Config{}, fmt.Errorf("CANONICAL_ORIGIN: %w", err)
	}
	if environment == Production && origin.Scheme != "https" {
		return Config{}, errors.New("CANONICAL_ORIGIN must use https in production")
	}

	provider := strings.ToLower(valueOr(lookup, "CONTACT_PROVIDER", "devnull"))
	if provider != "devnull" && provider != "webhook" && provider != "disabled" {
		return Config{}, errors.New("CONTACT_PROVIDER must be devnull, webhook, or disabled")
	}
	if environment == Production && provider != "webhook" {
		return Config{}, errors.New("CONTACT_PROVIDER must be webhook in production")
	}

	var webhookURL *url.URL
	if provider == "webhook" {
		rawURL := valueOr(lookup, "CONTACT_WEBHOOK_URL", "")
		if rawURL == "" {
			return Config{}, errors.New("CONTACT_WEBHOOK_URL is required for the webhook provider")
		}
		webhookURL, err = parseEndpoint(rawURL)
		if err != nil {
			return Config{}, fmt.Errorf("CONTACT_WEBHOOK_URL: %w", err)
		}
		if environment == Production && webhookURL.Scheme != "https" {
			return Config{}, errors.New("CONTACT_WEBHOOK_URL must use https in production")
		}
	}

	contactTimeout, err := positiveDuration(valueOr(lookup, "CONTACT_DELIVERY_TIMEOUT", "8s"), "CONTACT_DELIVERY_TIMEOUT")
	if err != nil {
		return Config{}, err
	}
	rateLimit, err := positiveInt(valueOr(lookup, "CONTACT_RATE_LIMIT", "5"), "CONTACT_RATE_LIMIT")
	if err != nil {
		return Config{}, err
	}
	globalRateLimit, err := positiveInt(valueOr(lookup, "CONTACT_GLOBAL_RATE_LIMIT", "100"), "CONTACT_GLOBAL_RATE_LIMIT")
	if err != nil {
		return Config{}, err
	}
	rateWindow, err := positiveDuration(valueOr(lookup, "CONTACT_RATE_WINDOW", "1h"), "CONTACT_RATE_WINDOW")
	if err != nil {
		return Config{}, err
	}
	maxInFlight, err := positiveInt(valueOr(lookup, "MAX_IN_FLIGHT_REQUESTS", "64"), "MAX_IN_FLIGHT_REQUESTS")
	if err != nil {
		return Config{}, err
	}
	if maxInFlight > 4096 {
		return Config{}, errors.New("MAX_IN_FLIGHT_REQUESTS must be at most 4096")
	}

	secret := []byte(valueOr(lookup, "CSRF_SECRET", ""))
	if len(secret) == 0 && environment != Production {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return Config{}, fmt.Errorf("generate development CSRF secret: %w", err)
		}
	}
	if len(secret) < 32 {
		return Config{}, errors.New("CSRF_SECRET must contain at least 32 bytes")
	}

	trustedProxies, err := parsePrefixes(valueOr(lookup, "TRUSTED_PROXY_CIDRS", ""))
	if err != nil {
		return Config{}, fmt.Errorf("TRUSTED_PROXY_CIDRS: %w", err)
	}

	return Config{
		Environment:         environment,
		Port:                port,
		Origin:              origin,
		ContactProvider:     provider,
		ContactWebhookURL:   webhookURL,
		ContactWebhookToken: valueOr(lookup, "CONTACT_WEBHOOK_TOKEN", ""),
		ContactTimeout:      contactTimeout,
		ContactRateLimit:    rateLimit,
		ContactGlobalLimit:  globalRateLimit,
		ContactRateWindow:   rateWindow,
		ContactMaxBody:      64 << 10,
		MaxInFlightRequests: maxInFlight,
		CSRFSecret:          secret,
		TrustedProxies:      trustedProxies,
		ReadHeaderTimeout:   5 * time.Second,
		ReadTimeout:         10 * time.Second,
		WriteTimeout:        15 * time.Second,
		IdleTimeout:         60 * time.Second,
		ShutdownTimeout:     10 * time.Second,
	}, nil
}

func valueOr(lookup func(string) (string, bool), key, fallback string) string {
	value, ok := lookup(key)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func positiveInt(raw, name string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return value, nil
}

func positiveDuration(raw, name string) (time.Duration, error) {
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration", name)
	}
	return value, nil
}

func parseOrigin(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("must contain only a scheme and host")
	}
	parsed.Path = ""
	return parsed, nil
}

func parseEndpoint(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, errors.New("must be an absolute http or https URL")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("must not contain user information or a fragment")
	}
	return parsed, nil
}

func parsePrefixes(raw string) ([]netip.Prefix, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	prefixes := make([]netip.Prefix, 0, len(parts))
	for _, part := range parts {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(part))
		if err != nil {
			return nil, fmt.Errorf("parse %q: %w", part, err)
		}
		prefixes = append(prefixes, prefix.Masked())
	}
	return prefixes, nil
}
