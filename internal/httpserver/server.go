// Package httpserver owns routing, middleware, handlers, and response policy.
package httpserver

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/config"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/contact"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/render"
)

type Dependencies struct {
	Config   config.Config
	Content  *content.Store
	Renderer *render.Renderer
	Sender   contact.Sender
	Limiter  *contact.Limiter
	Logger   *slog.Logger
	Now      func() time.Time
	Random   io.Reader
}

type Server struct {
	config   config.Config
	content  *content.Store
	renderer *render.Renderer
	sender   contact.Sender
	limiter  *contact.Limiter
	logger   *slog.Logger
	now      func() time.Time
	random   io.Reader
	site     content.Site
	ipSalt   [32]byte
	sequence atomic.Uint64
	handler  http.Handler
}

func New(dependencies Dependencies) (*Server, error) {
	if dependencies.Config.Origin == nil {
		return nil, fmt.Errorf("construct HTTP server: canonical origin is required")
	}
	if dependencies.Content == nil || dependencies.Renderer == nil || dependencies.Sender == nil || dependencies.Limiter == nil {
		return nil, fmt.Errorf("construct HTTP server: content, renderer, sender, and limiter are required")
	}
	if dependencies.Logger == nil {
		dependencies.Logger = slog.Default()
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.Random == nil {
		dependencies.Random = rand.Reader
	}
	server := &Server{
		config:   dependencies.Config,
		content:  dependencies.Content,
		renderer: dependencies.Renderer,
		sender:   dependencies.Sender,
		limiter:  dependencies.Limiter,
		logger:   dependencies.Logger,
		now:      dependencies.Now,
		random:   dependencies.Random,
		site:     dependencies.Content.Site(),
	}
	if _, err := io.ReadFull(server.random, server.ipSalt[:]); err != nil {
		return nil, fmt.Errorf("construct HTTP server: generate rate-limit salt: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", server.home)
	mux.HandleFunc("GET /posts/{slug}", server.article)
	mux.HandleFunc("GET /projects", server.projects)
	mux.HandleFunc("GET /contact", server.contactForm)
	mux.HandleFunc("POST /contact", server.submitContact)
	mux.HandleFunc("GET /rss.xml", server.rss)
	mux.HandleFunc("GET /sitemap.xml", server.sitemap)
	mux.HandleFunc("GET /robots.txt", server.robots)
	mux.HandleFunc("GET /healthz", server.health)
	mux.HandleFunc("GET /assets/{file}", server.staticAsset)
	mux.HandleFunc("GET /", server.notFound)

	server.handler = server.requestID(server.recoverPanics(server.logging(server.security(server.redirects(mux)))))
	return server, nil
}

func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	copy := s.site.Pages.Home
	meta := s.pageMeta(r, copy.Title, copy.Description, "/", "website", "writing", []string{"titles.js"})
	data, err := s.renderer.Home(render.HomePage{
		Chrome: s.chrome(meta),
		Posts:  s.content.Posts(),
	})
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	s.writeCacheable(w, r, http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) article(w http.ResponseWriter, r *http.Request) {
	post, ok := s.content.Post(r.PathValue("slug"))
	if !ok {
		s.notFound(w, r)
		return
	}
	path := "/posts/" + post.Slug
	description := post.Summary
	if description == "" {
		description = s.site.Pages.Home.Description
	}
	meta := s.pageMeta(r, post.Title+" — "+s.site.Identity.Wordmark, description, path, "article", "writing", nil)
	meta.Published = post.Date.Format(time.RFC3339)
	if !post.Updated.IsZero() {
		meta.Updated = post.Updated.Format(time.RFC3339)
	}
	structured, err := json.Marshal(map[string]any{
		"@context":         "https://schema.org",
		"@type":            "BlogPosting",
		"headline":         post.Title,
		"description":      description,
		"datePublished":    post.Date.Format("2006-01-02"),
		"dateModified":     dateModified(post),
		"wordCount":        post.WordCount,
		"mainEntityOfPage": meta.Canonical,
		"author": map[string]any{
			"@type": "Person",
			"name":  s.site.Identity.Name,
			"url":   s.site.Identity.GitHub,
		},
	})
	if err != nil {
		s.renderFailure(w, r, fmt.Errorf("encode article structured data: %w", err))
		return
	}
	meta.StructuredData = template.JS(structured) // json.Marshal provides the script-safe serialization.
	w.Header().Set("Content-Security-Policy", s.contentSecurityPolicy(structured))
	older, hasOlder, newer, hasNewer := s.content.Neighbors(post.Slug)
	data, err := s.renderer.Article(render.ArticlePage{
		Chrome:   s.chrome(meta),
		Post:     post,
		Older:    older,
		Newer:    newer,
		HasOlder: hasOlder,
		HasNewer: hasNewer,
	})
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	s.writeCacheable(w, r, http.StatusOK, "text/html; charset=utf-8", data)
}

func dateModified(post content.Post) string {
	if !post.Updated.IsZero() {
		return post.Updated.Format("2006-01-02")
	}
	return post.Date.Format("2006-01-02")
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	copy := s.site.Pages.Projects
	meta := s.pageMeta(r, copy.Title, copy.Description, "/projects", "website", "projects", nil)
	data, err := s.renderer.Projects(render.ProjectsPage{Chrome: s.chrome(meta)})
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	s.writeCacheable(w, r, http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) contactForm(w http.ResponseWriter, r *http.Request) {
	token, err := s.ensureCSRF(w, r)
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	form := render.ContactForm{CSRFToken: token, Available: s.config.ContactProvider != "disabled", Errors: map[string]string{}}
	if !form.Available {
		form.GlobalError = s.site.Pages.Contact.Unavailable
	}
	s.renderContact(w, r, http.StatusOK, form, false)
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	copy := s.site.Pages.NotFound
	meta := s.pageMeta(r, copy.Title, copy.Description, r.URL.Path, "website", "", nil)
	meta.NoIndex = true
	data, err := s.renderer.Simple(render.SimplePage{
		Chrome:    s.chrome(meta),
		Heading:   copy.Heading,
		Body:      copy.Body,
		LinkLabel: copy.LinkLabel,
	})
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	s.writeHTML(w, http.StatusNotFound, data, "no-store")
}

func (s *Server) renderFailure(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "render request", "request_id", requestIDFrom(r.Context()), "error", err)
	copy := s.site.Pages.Error
	meta := s.pageMeta(r, copy.Title, copy.Description, r.URL.Path, "website", "", nil)
	meta.NoIndex = true
	data, renderErr := s.renderer.Simple(render.SimplePage{
		Chrome:  s.chrome(meta),
		Heading: copy.Heading,
		Body:    copy.Body,
	})
	if renderErr != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	s.writeHTML(w, http.StatusInternalServerError, data, "no-store")
}

func (s *Server) pageMeta(_ *http.Request, title, description, path, ogType, section string, scripts []string) render.PageMeta {
	return render.PageMeta{
		Title:       title,
		Description: description,
		Canonical:   s.absolute(path),
		RSS:         s.absolute("/rss.xml"),
		OGType:      ogType,
		Section:     section,
		Scripts:     scripts,
	}
}

func (s *Server) chrome(meta render.PageMeta) render.Chrome {
	return render.Chrome{Meta: meta, Site: s.site, Year: s.now().Year()}
}

func (s *Server) absolute(path string) string {
	origin := *s.config.Origin
	origin.Path = path
	origin.RawPath = ""
	origin.RawQuery = ""
	origin.Fragment = ""
	return origin.String()
}

func (s *Server) writeCacheable(w http.ResponseWriter, r *http.Request, status int, contentType string, data []byte) {
	representation := data
	if len(data) >= 1024 && acceptsGzip(r.Header.Get("Accept-Encoding")) {
		if compressed, err := gzipBytes(data); err == nil {
			representation = compressed
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Add("Vary", "Accept-Encoding")
		}
	}
	digest := sha256.Sum256(representation)
	etag := `"` + base64.RawURLEncoding.EncodeToString(digest[:]) + `"`
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("If-None-Match")), []byte(etag)) == 1 {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write(representation)
}

func (s *Server) writeHTML(w http.ResponseWriter, status int, data []byte, cacheControl string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", cacheControl)
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func (s *Server) staticAsset(w http.ResponseWriter, r *http.Request) {
	asset, ok := s.renderer.AssetByFile(r.PathValue("file"))
	if !ok {
		s.notFound(w, r)
		return
	}
	data := asset.Data
	etag := asset.ETag
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		data = asset.GzipData
		etag = asset.GzipETag
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Add("Vary", "Accept-Encoding")
	}
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func acceptsGzip(header string) bool {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if parts[0] != "gzip" && parts[0] != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, found := strings.Cut(strings.TrimSpace(parameter), "=")
			if found && name == "q" {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil {
					quality = 0
				} else {
					quality = parsed
				}
			}
		}
		return quality > 0
	}
	return false
}

func gzipBytes(data []byte) ([]byte, error) {
	var buffer bytes.Buffer
	writer, err := gzip.NewWriterLevel(&buffer, gzip.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := writer.Write(data); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) renderContact(w http.ResponseWriter, r *http.Request, status int, form render.ContactForm, fragment bool) {
	data, err := s.contactBytes(r, form, fragment)
	if err != nil {
		s.renderFailure(w, r, err)
		return
	}
	s.writeContact(w, status, data, fragment)
}

func (s *Server) contactBytes(r *http.Request, form render.ContactForm, fragment bool) ([]byte, error) {
	copy := s.site.Pages.Contact
	meta := s.pageMeta(r, copy.Title, copy.Description, "/contact", "website", "contact", []string{"contact.js"})
	page := render.ContactPage{Chrome: s.chrome(meta), Form: form}
	if fragment {
		return s.renderer.ContactFragment(page)
	}
	return s.renderer.Contact(page)
}

func (s *Server) writeContact(w http.ResponseWriter, status int, data []byte, fragment bool) {
	if fragment {
		w.Header().Set("X-Contact-Fragment", "1")
	}
	s.writeHTML(w, status, data, "no-store")
}

func isHTMX(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}
