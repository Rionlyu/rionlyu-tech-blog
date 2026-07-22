package httpserver

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

const overloadPage = `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Please try again shortly</title>
<style>body{max-width:42rem;margin:15vh auto;padding:0 1.5rem;background:#ece4d7;color:#0a0a0a;font:18px/1.55 "Helvetica Neue",Helvetica,Arial,sans-serif}h1{font-size:clamp(2rem,6vw,3.5rem);line-height:1;margin:.4em 0}p{max-width:34rem}</style>
<main><p>rionlyu.com</p><h1>Just a moment.</h1><p>The site is handling more traffic than usual. Please try again in a few seconds.</p></main>
</html>`

type contextKey string

const requestIDKey contextKey = "request-id"

func (s *Server) requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.randomString(12)
		if err != nil {
			id = "fallback-" + strconv.FormatUint(s.sequence.Add(1), 10)
		}
		w.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func requestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.ErrorContext(r.Context(), "request panic",
					"request_id", requestIDFrom(r.Context()),
					"error", fmt.Sprint(recovered),
				)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.status != 0 {
		return
	}
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.WriteHeader(http.StatusOK)
	}
	written, err := r.ResponseWriter.Write(data)
	r.bytes += written
	return written, err
}

func (r *responseRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		recorder := &responseRecorder{ResponseWriter: w}
		next.ServeHTTP(recorder, r)
		status := recorder.status
		if status == 0 {
			status = http.StatusOK
		}
		s.logger.InfoContext(r.Context(), "http request",
			"request_id", requestIDFrom(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", status,
			"bytes", recorder.bytes,
			"latency_ms", time.Since(started).Milliseconds(),
		)
	})
}

func (s *Server) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(nil))
		if s.config.IsProduction() {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=()")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// overload bounds active application work without making requests wait in an
// unbounded queue. Health checks bypass the gate so an orchestrator can still
// distinguish a busy process from a dead one.
func (s *Server) overload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case s.inFlight <- struct{}{}:
			defer func() { <-s.inFlight }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "5")
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) && strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusServiceUnavailable)
				if r.Method == http.MethodGet {
					_, _ = w.Write([]byte(overloadPage))
				}
				return
			}
			http.Error(w, "Service temporarily busy. Please try again shortly.", http.StatusServiceUnavailable)
		}
	})
}

func (s *Server) contentSecurityPolicy(inlineScript []byte) string {
	csp := "default-src 'self'; base-uri 'none'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; img-src 'self' data:; object-src 'none'; script-src 'self'"
	if len(inlineScript) > 0 {
		digest := sha256.Sum256(inlineScript)
		csp += " 'sha256-" + base64.StdEncoding.EncodeToString(digest[:]) + "'"
	}
	csp += "; style-src 'self' 'unsafe-inline'"
	if s.config.IsProduction() {
		csp += "; upgrade-insecure-requests"
	}
	return csp
}

func (s *Server) redirects(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHost(r.Host)
		if host == "www."+strings.ToLower(s.config.Origin.Hostname()) {
			target := *s.config.Origin
			target.Path = r.URL.Path
			target.RawPath = r.URL.RawPath
			target.RawQuery = r.URL.RawQuery
			http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
			return
		}
		// Container orchestrators need a private HTTP liveness check before public
		// DNS and TLS are available. No application data is served on this route.
		if s.config.IsProduction() && r.URL.Path != "/healthz" && !s.isSecureRequest(r) {
			target := *s.config.Origin
			target.Path = r.URL.Path
			target.RawPath = r.URL.RawPath
			target.RawQuery = r.URL.RawQuery
			http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func requestHost(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.ToLower(strings.Trim(host, "[]"))
	}
	return strings.ToLower(strings.Trim(hostport, "[]"))
}

func (s *Server) isSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, ok := remoteAddress(r.RemoteAddr)
	if !ok || !s.isTrustedProxy(peer) {
		return false
	}
	proto := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0])
	return strings.EqualFold(proto, "https")
}

func (s *Server) clientAddress(r *http.Request) netip.Addr {
	peer, ok := remoteAddress(r.RemoteAddr)
	if !ok {
		return netip.Addr{}
	}
	if !s.isTrustedProxy(peer) {
		return peer
	}
	rawChain := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	chain := make([]netip.Addr, 0, len(rawChain))
	for _, raw := range rawChain {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil {
			return peer
		}
		chain = append(chain, address.Unmap())
	}
	current := peer
	for index := len(chain) - 1; index >= 0; index-- {
		if !s.isTrustedProxy(current) {
			return current
		}
		current = chain[index]
	}
	return current
}

func remoteAddress(remote string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func (s *Server) isTrustedProxy(address netip.Addr) bool {
	for _, prefix := range s.config.TrustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}

func (s *Server) rateKey(r *http.Request) string {
	address := s.clientAddress(r)
	hash := sha256.New()
	_, _ = hash.Write(s.ipSalt[:])
	_, _ = hash.Write([]byte(address.String()))
	return base64.RawURLEncoding.EncodeToString(hash.Sum(nil))
}
