package config_test

import (
	"testing"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/config"
)

func TestLoadDefaultsForDevelopment(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(mapLookup(nil))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Port != 8080 || cfg.Origin.String() != "http://localhost:8080" || cfg.ContactProvider != "devnull" || cfg.ContactRateLimit != 5 || cfg.ContactGlobalLimit != 100 || cfg.MaxInFlightRequests != 64 {
		t.Fatalf("Load() = %#v", cfg)
	}
	if len(cfg.CSRFSecret) != 32 {
		t.Fatalf("development CSRF secret length = %d", len(cfg.CSRFSecret))
	}
}

func TestLoadRejectsInvalidMaximumInFlightRequests(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"0", "4097"} {
		if _, err := config.Load(mapLookup(map[string]string{"MAX_IN_FLIGHT_REQUESTS": value})); err == nil {
			t.Fatalf("Load(MAX_IN_FLIGHT_REQUESTS=%s) error = nil, want error", value)
		}
	}
}

func TestLoadProductionValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		values map[string]string
	}{
		{name: "requires HTTPS origin", values: map[string]string{"CANONICAL_ORIGIN": "http://rionlyu.com", "CONTACT_PROVIDER": "webhook", "CONTACT_WEBHOOK_URL": "https://contact.example", "CSRF_SECRET": longSecret}},
		{name: "requires webhook", values: map[string]string{"CANONICAL_ORIGIN": "https://rionlyu.com", "CONTACT_PROVIDER": "devnull", "CSRF_SECRET": longSecret}},
		{name: "requires webhook URL", values: map[string]string{"CANONICAL_ORIGIN": "https://rionlyu.com", "CONTACT_PROVIDER": "webhook", "CSRF_SECRET": longSecret}},
		{name: "requires CSRF secret", values: map[string]string{"CANONICAL_ORIGIN": "https://rionlyu.com", "CONTACT_PROVIDER": "webhook", "CONTACT_WEBHOOK_URL": "https://contact.example"}},
		{name: "rejects HTTP webhook", values: map[string]string{"CANONICAL_ORIGIN": "https://rionlyu.com", "CONTACT_PROVIDER": "webhook", "CONTACT_WEBHOOK_URL": "http://contact.example", "CSRF_SECRET": longSecret}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			test.values["ENVIRONMENT"] = "production"
			if _, err := config.Load(mapLookup(test.values)); err == nil {
				t.Fatal("Load() error = nil, want error")
			}
		})
	}
}

func TestLoadProduction(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load(mapLookup(map[string]string{
		"ENVIRONMENT":         "production",
		"CANONICAL_ORIGIN":    "https://rionlyu.com",
		"CONTACT_PROVIDER":    "webhook",
		"CONTACT_WEBHOOK_URL": "https://contact.example/",
		"CSRF_SECRET":         longSecret,
		"TRUSTED_PROXY_CIDRS": "10.0.0.0/8, 2001:db8::/32",
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.IsProduction() || len(cfg.TrustedProxies) != 2 {
		t.Fatalf("Load() = %#v", cfg)
	}
}

const longSecret = "0123456789abcdef0123456789abcdef"

func mapLookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
