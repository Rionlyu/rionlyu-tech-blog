package httpserver_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	contentfiles "github.com/Rionlyu/rionlyu-tech-blog/content"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/config"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/contact"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
	bloghttp "github.com/Rionlyu/rionlyu-tech-blog/internal/httpserver"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/render"
	"github.com/Rionlyu/rionlyu-tech-blog/web"
)

const articlePath = "/posts/acknowledgement-durability-boundary"

func TestPublicRoutesAndMetadata(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, harnessOptions{})
	tests := []struct {
		path        string
		contentType string
		contains    string
	}{
		{path: "/", contentType: "text/html", contains: ">Writing</h1>"},
		{path: articlePath, contentType: "text/html", contains: "The acknowledgement is the durability boundary"},
		{path: "/projects", contentType: "text/html", contains: "kube-job-doctor"},
		{path: "/contact", contentType: "text/html", contains: ">Contact</h1>"},
		{path: "/rss.xml", contentType: "application/rss+xml", contains: "https://example.test" + articlePath},
		{path: "/sitemap.xml", contentType: "application/xml", contains: "https://example.test/projects"},
		{path: "/robots.txt", contentType: "text/plain", contains: "Sitemap: https://example.test/sitemap.xml"},
		{path: "/healthz", contentType: "text/plain", contains: "ok\n"},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			response := serve(harness.handler, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if !strings.HasPrefix(response.Header().Get("Content-Type"), test.contentType) {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			if !strings.Contains(response.Body.String(), test.contains) {
				t.Fatalf("body does not contain %q", test.contains)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("X-Request-ID is empty")
			}
		})
	}

	home := serve(harness.handler, httptest.NewRequest(http.MethodGet, "/", nil)).Body.String()
	if !strings.Contains(home, `rel="canonical" href="https://example.test/"`) || !strings.Contains(home, "titles.") || strings.Contains(home, "contact.") {
		t.Fatalf("home asset/metadata policy is wrong")
	}
	if count := strings.Count(home, `class="post-row"`); count < 3 {
		t.Fatalf("home post count = %d, want at least 3", count)
	}
	if strings.Contains(home, "tag-list") || strings.Contains(home, "BENIAMIN KMIEĆ / RIONLYU") || strings.Contains(home, "writing-heading") {
		t.Fatal("home contains removed tag or masthead UI")
	}
	projects := serve(harness.handler, httptest.NewRequest(http.MethodGet, "/projects", nil)).Body.String()
	if strings.Count(projects, `class="project-row"`) < 2 || strings.Contains(projects, "page-header") || strings.Contains(projects, "page-deck") || strings.Contains(projects, "project-index") {
		t.Fatal("projects page does not start directly with the project rows")
	}
	article := serve(harness.handler, httptest.NewRequest(http.MethodGet, articlePath, nil)).Body.String()
	if strings.Contains(article, "titles.") || strings.Contains(article, "contact.") || !strings.Contains(article, `"BlogPosting"`) {
		t.Fatalf("article script/structured-data policy is wrong")
	}
	contactPage := serve(harness.handler, httptest.NewRequest(http.MethodGet, "/contact", nil)).Body.String()
	if !strings.Contains(contactPage, "contact.") || strings.Contains(contactPage, "titles.") {
		t.Fatalf("contact script policy is wrong")
	}
}

func TestNotFoundAndStaticCaching(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, harnessOptions{})
	missing := serve(harness.handler, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "noindex, nofollow") {
		t.Fatalf("missing response = %d %s", missing.Code, missing.Body.String())
	}

	assetURL, ok := harness.renderer.AssetURL("site.css")
	if !ok {
		t.Fatal("site.css missing")
	}
	first := serve(harness.handler, httptest.NewRequest(http.MethodGet, assetURL, nil))
	if first.Code != http.StatusOK || first.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response = %d, cache = %q", first.Code, first.Header().Get("Cache-Control"))
	}
	request := httptest.NewRequest(http.MethodGet, assetURL, nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := serve(harness.handler, request)
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional asset status = %d", second.Code)
	}
	compressedRequest := httptest.NewRequest(http.MethodGet, assetURL, nil)
	compressedRequest.Header.Set("Accept-Encoding", "br, gzip")
	compressed := serve(harness.handler, compressedRequest)
	if compressed.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(compressed.Header().Get("Vary"), "Accept-Encoding") {
		t.Fatalf("compressed headers = %#v", compressed.Header())
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader() error = %v", err)
	}
	expanded, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(gzip) error = %v", err)
	}
	if !bytes.Contains(expanded, []byte(":root")) {
		t.Fatal("compressed stylesheet did not expand to CSS")
	}
}

func TestArticleCSPAndConditionalCaching(t *testing.T) {
	t.Parallel()
	harness := newHarness(t, harnessOptions{})
	first := serve(harness.handler, httptest.NewRequest(http.MethodGet, articlePath, nil))
	if first.Code != http.StatusOK {
		t.Fatalf("first article status = %d", first.Code)
	}
	if cache := first.Header().Get("Cache-Control"); cache != "public, max-age=0, must-revalidate" {
		t.Fatalf("article Cache-Control = %q", cache)
	}
	if edgeCache := first.Header().Get("Cloudflare-CDN-Cache-Control"); !strings.Contains(edgeCache, "max-age=60") || !strings.Contains(edgeCache, "stale-while-revalidate=300") || !strings.Contains(edgeCache, "stale-if-error=86400") {
		t.Fatalf("article Cloudflare-CDN-Cache-Control = %q", edgeCache)
	}
	match := regexp.MustCompile(`<script type="application/ld\+json">([^<]+)</script>`).FindSubmatch(first.Body.Bytes())
	if len(match) != 2 {
		t.Fatal("article JSON-LD script not found")
	}
	digest := sha256.Sum256(match[1])
	wantSource := "'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	if csp := first.Header().Get("Content-Security-Policy"); !strings.Contains(csp, wantSource) || strings.Contains(csp, "'unsafe-inline'") && strings.Contains(strings.Split(csp, "; style-src")[0], "'unsafe-inline'") {
		t.Fatalf("article CSP = %q, want script source %q", csp, wantSource)
	}

	request := httptest.NewRequest(http.MethodGet, articlePath, nil)
	request.Header.Set("If-None-Match", first.Header().Get("ETag"))
	second := serve(harness.handler, request)
	if second.Code != http.StatusNotModified {
		t.Fatalf("conditional article status = %d, ETag = %q", second.Code, second.Header().Get("ETag"))
	}
}

func TestCanonicalAndHTTPSRedirects(t *testing.T) {
	t.Parallel()
	development := newHarness(t, harnessOptions{})
	aliasRequest := httptest.NewRequest(http.MethodGet, "http://www.rionlyu.dev/projects?from=com", nil)
	aliasResponse := serve(development.handler, aliasRequest)
	if aliasResponse.Code != http.StatusPermanentRedirect || aliasResponse.Header().Get("Location") != "https://rionlyu.dev/projects?from=com" {
		t.Fatalf("alias redirect = %d %q", aliasResponse.Code, aliasResponse.Header().Get("Location"))
	}

	production := newHarness(t, harnessOptions{production: true})
	plainRequest := httptest.NewRequest(http.MethodGet, "http://example.test/projects", nil)
	plainRequest.RemoteAddr = "192.0.2.1:1234"
	plainResponse := serve(production.handler, plainRequest)
	if plainResponse.Code != http.StatusPermanentRedirect || plainResponse.Header().Get("Location") != "https://example.test/projects" {
		t.Fatalf("HTTPS redirect = %d %q", plainResponse.Code, plainResponse.Header().Get("Location"))
	}
	healthRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/healthz", nil)
	healthRequest.RemoteAddr = "127.0.0.1:1234"
	healthResponse := serve(production.handler, healthRequest)
	if healthResponse.Code != http.StatusOK || healthResponse.Body.String() != "ok\n" {
		t.Fatalf("internal health response = %d %q", healthResponse.Code, healthResponse.Body.String())
	}

	proxied := newHarness(t, harnessOptions{production: true, trustedProxy: true})
	proxyRequest := httptest.NewRequest(http.MethodGet, "http://example.test/projects", nil)
	proxyRequest.RemoteAddr = "127.0.0.1:4321"
	proxyRequest.Header.Set("X-Forwarded-Proto", "https")
	proxyResponse := serve(proxied.handler, proxyRequest)
	if proxyResponse.Code != http.StatusOK {
		t.Fatalf("trusted proxy response = %d", proxyResponse.Code)
	}
}

func TestContactSubmissionVariants(t *testing.T) {
	t.Parallel()
	t.Run("full page success without JavaScript", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		response := submit(t, harness.handler, form, cookie, false)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "<!doctype html>") || !strings.Contains(response.Body.String(), "data-contact-success") || !strings.Contains(response.Body.String(), ">Thank you.</h1>") || strings.Contains(response.Body.String(), ">Contact</h1>") || strings.Contains(response.Body.String(), ">Message sent.</h1>") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		if harness.sender.Calls() != 1 {
			t.Fatalf("sender calls = %d", harness.sender.Calls())
		}
	})

	t.Run("HTMX fragment success", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusOK || response.Header().Get("X-Contact-Fragment") != "1" || strings.Contains(response.Body.String(), "<!doctype html>") || !strings.Contains(response.Body.String(), "data-contact-success") || strings.Contains(response.Body.String(), ">Contact</h1>") {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
	})

	t.Run("validation failure", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		form.Set("email", "not-an-email")
		form.Set("message", "")
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `aria-invalid="true"`) || harness.sender.Calls() != 0 {
			t.Fatalf("response = %d, calls = %d, body = %s", response.Code, harness.sender.Calls(), response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `value="Beniamin"`) || strings.Contains(response.Body.String(), "not-an-email") {
			t.Fatal("response did not preserve only the safe name")
		}
	})

	t.Run("provider failure", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{senderError: errors.New("provider failed")})
		form, cookie := contactForm(t, harness.handler)
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusBadGateway || strings.Contains(response.Body.String(), "data-contact-success") || harness.sender.Calls() != 1 {
			t.Fatalf("response = %d, calls = %d", response.Code, harness.sender.Calls())
		}
	})

	t.Run("rate limiting", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{rateLimit: 1})
		form, cookie := contactForm(t, harness.handler)
		if first := submit(t, harness.handler, form, cookie, true); first.Code != http.StatusOK {
			t.Fatalf("first status = %d", first.Code)
		}
		second := submit(t, harness.handler, form, cookie, true)
		if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" || harness.sender.Calls() != 1 {
			t.Fatalf("second = %d, calls = %d", second.Code, harness.sender.Calls())
		}
	})

	t.Run("global rate limiting", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{globalRateLimit: 1})
		firstForm, firstCookie := contactForm(t, harness.handler)
		if first := submitFrom(t, harness.handler, firstForm, firstCookie, true, "192.0.2.10:1234"); first.Code != http.StatusOK {
			t.Fatalf("first status = %d", first.Code)
		}
		secondForm, secondCookie := contactForm(t, harness.handler)
		second := submitFrom(t, harness.handler, secondForm, secondCookie, true, "192.0.2.11:1234")
		if second.Code != http.StatusTooManyRequests || second.Header().Get("Retry-After") == "" || harness.sender.Calls() != 1 {
			t.Fatalf("second = %d, calls = %d", second.Code, harness.sender.Calls())
		}
	})

	t.Run("CSRF failure", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		form.Set("_csrf", "invalid")
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusForbidden || len(response.Result().Cookies()) == 0 || harness.sender.Calls() != 0 {
			t.Fatalf("response = %d, cookies = %d, calls = %d", response.Code, len(response.Result().Cookies()), harness.sender.Calls())
		}
	})

	t.Run("honeypot never reports real success", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		form.Set("website", "https://spam.example")
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusNoContent || strings.Contains(response.Body.String(), "data-contact-success") || harness.sender.Calls() != 0 {
			t.Fatalf("response = %d, calls = %d", response.Code, harness.sender.Calls())
		}
	})

	t.Run("unknown field is rejected", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		form, cookie := contactForm(t, harness.handler)
		form.Set("admin", "true")
		response := submit(t, harness.handler, form, cookie, true)
		if response.Code != http.StatusBadRequest || harness.sender.Calls() != 0 {
			t.Fatalf("response = %d, calls = %d", response.Code, harness.sender.Calls())
		}
	})

	t.Run("oversized request", func(t *testing.T) {
		harness := newHarness(t, harnessOptions{})
		_, cookie := contactForm(t, harness.handler)
		request := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(strings.Repeat("a", 70<<10)))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.AddCookie(cookie)
		response := serve(harness.handler, request)
		if response.Code != http.StatusRequestEntityTooLarge || harness.sender.Calls() != 0 {
			t.Fatalf("response = %d, calls = %d", response.Code, harness.sender.Calls())
		}
	})
}

type harnessOptions struct {
	production      bool
	trustedProxy    bool
	rateLimit       int
	globalRateLimit int
	senderError     error
}

type harness struct {
	handler  http.Handler
	renderer *render.Renderer
	sender   *stubSender
}

func newHarness(t *testing.T, options harnessOptions) harness {
	t.Helper()
	store, err := content.Load(contentfiles.Files)
	if err != nil {
		t.Fatalf("content.Load() error = %v", err)
	}
	renderer, err := render.New(web.Files)
	if err != nil {
		t.Fatalf("render.New() error = %v", err)
	}
	origin, _ := url.Parse("https://example.test")
	environment := "test"
	if options.production {
		environment = config.Production
	}
	rateLimit := options.rateLimit
	if rateLimit == 0 {
		rateLimit = 5
	}
	globalRateLimit := options.globalRateLimit
	if globalRateLimit == 0 {
		globalRateLimit = 100
	}
	cfg := config.Config{
		Environment:         environment,
		Origin:              origin,
		ContactProvider:     "webhook",
		ContactTimeout:      time.Second,
		ContactRateLimit:    rateLimit,
		ContactGlobalLimit:  globalRateLimit,
		ContactRateWindow:   time.Hour,
		ContactMaxBody:      64 << 10,
		MaxInFlightRequests: 64,
		CSRFSecret:          []byte("0123456789abcdef0123456789abcdef"),
	}
	if options.trustedProxy {
		cfg.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	}
	sender := &stubSender{err: options.senderError}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	server, err := bloghttp.New(bloghttp.Dependencies{
		Config:        cfg,
		Content:       store,
		Renderer:      renderer,
		Sender:        sender,
		Limiter:       contact.NewLimiter(rateLimit, time.Hour, time.Now),
		GlobalLimiter: contact.NewLimiter(globalRateLimit, time.Hour, time.Now),
		Logger:        logger,
		Now:           func() time.Time { return time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("httpserver.New() error = %v", err)
	}
	return harness{handler: server.Handler(), renderer: renderer, sender: sender}
}

type stubSender struct {
	mu    sync.Mutex
	calls int
	err   error
}

func (s *stubSender) Send(context.Context, contact.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return s.err
}

func (s *stubSender) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

var csrfPattern = regexp.MustCompile(`name="_csrf" value="([^"]+)"`)

func contactForm(t *testing.T, handler http.Handler) (url.Values, *http.Cookie) {
	t.Helper()
	response := serve(handler, httptest.NewRequest(http.MethodGet, "/contact", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("GET /contact status = %d", response.Code)
	}
	match := csrfPattern.FindStringSubmatch(response.Body.String())
	if len(match) != 2 {
		t.Fatal("CSRF input not found")
	}
	var csrfCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "rionlyu_csrf" {
			csrfCookie = cookie
		}
	}
	if csrfCookie == nil {
		t.Fatal("CSRF cookie not found")
	}
	return url.Values{
		"_csrf":   {match[1]},
		"name":    {"Beniamin"},
		"email":   {"hello@example.test"},
		"message": {"Hello from the test."},
		"website": {""},
	}, csrfCookie
}

func submit(t *testing.T, handler http.Handler, form url.Values, cookie *http.Cookie, htmx bool) *httptest.ResponseRecorder {
	return submitFrom(t, handler, form, cookie, htmx, "192.0.2.10:1234")
}

func submitFrom(t *testing.T, handler http.Handler, form url.Values, cookie *http.Cookie, htmx bool, remoteAddress string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/contact", strings.NewReader(form.Encode()))
	request.RemoteAddr = remoteAddress
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		request.Header.Set("HX-Request", "true")
	}
	request.AddCookie(cookie)
	return serve(handler, request)
}

func serve(handler http.Handler, request *http.Request) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
