package contact_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/contact"
)

func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input contact.Message
		field string
	}{
		{name: "valid", input: contact.Message{Name: " Beniamin ", Email: "hello@example.test", Body: " Hello. "}},
		{name: "missing name", input: contact.Message{Email: "hello@example.test", Body: "Hello."}, field: "name"},
		{name: "display name email", input: contact.Message{Name: "B", Email: "Ben <hello@example.test>", Body: "Hello."}, field: "email"},
		{name: "missing at sign", input: contact.Message{Name: "B", Email: "localhost", Body: "Hello."}, field: "email"},
		{name: "control in name", input: contact.Message{Name: "B\nC", Email: "hello@example.test", Body: "Hello."}, field: "name"},
		{name: "oversized message", input: contact.Message{Name: "B", Email: "hello@example.test", Body: strings.Repeat("a", contact.MaxMessageRunes+1)}, field: "message"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			message, fieldErrors := contact.Validate(test.input)
			if test.field == "" {
				if fieldErrors.Any() {
					t.Fatalf("Validate() errors = %#v", fieldErrors)
				}
				if message.Name != "Beniamin" || message.Body != "Hello." {
					t.Fatalf("Validate() message = %#v", message)
				}
				return
			}
			if _, ok := fieldErrors[test.field]; !ok {
				t.Fatalf("Validate() errors = %#v, want %s", fieldErrors, test.field)
			}
		})
	}
}

func TestLimiter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	limiter := contact.NewLimiter(2, time.Minute, func() time.Time { return now })
	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("first attempt rejected")
	}
	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("second attempt rejected")
	}
	if allowed, retry := limiter.Allow("client"); allowed || retry != time.Minute {
		t.Fatalf("third attempt = (%v, %v)", allowed, retry)
	}
	now = now.Add(time.Minute)
	if allowed, _ := limiter.Allow("client"); !allowed {
		t.Fatal("attempt after window rejected")
	}
}

func TestWebhookSender(t *testing.T) {
	t.Parallel()
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"message":"Hello"`) {
			t.Errorf("body = %s", body)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	sender := contact.NewWebhookSender(server.Client(), endpoint, "secret")
	if err := sender.Send(context.Background(), contact.Message{Name: "B", Email: "b@example.test", Body: "Hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if authorization != "Bearer secret" {
		t.Fatalf("Authorization = %q", authorization)
	}
}
