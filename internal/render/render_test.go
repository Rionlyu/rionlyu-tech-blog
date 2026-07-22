package render_test

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"testing"

	contentfiles "github.com/Rionlyu/rionlyu-tech-blog/content"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
	"github.com/Rionlyu/rionlyu-tech-blog/internal/render"
	"github.com/Rionlyu/rionlyu-tech-blog/web"
)

func TestCompletePagesMatchGoldenFiles(t *testing.T) {
	t.Parallel()
	store, err := content.Load(contentfiles.Files)
	if err != nil {
		t.Fatalf("content.Load() error = %v", err)
	}
	renderer, err := render.New(web.Files)
	if err != nil {
		t.Fatalf("render.New() error = %v", err)
	}
	chrome := render.Chrome{
		Meta: render.PageMeta{
			Title:       "Writing — Rionlyu",
			Description: "Technical writing.",
			Canonical:   "https://example.test/",
			RSS:         "https://example.test/rss.xml",
			OGType:      "website",
			Section:     "writing",
			Scripts:     []string{"titles.js"},
		},
		Site: store.Site(),
		Year: 2026,
	}
	home, err := renderer.Home(render.HomePage{Chrome: chrome, Posts: store.Posts()})
	if err != nil {
		t.Fatalf("Home() error = %v", err)
	}
	assertGolden(t, "home.html", home)

	post := store.Posts()[0]
	articleChrome := chrome
	articleChrome.Meta = render.PageMeta{
		Title:          post.Title + " — Rionlyu",
		Description:    post.Summary,
		Canonical:      "https://example.test/posts/" + post.Slug,
		RSS:            "https://example.test/rss.xml",
		OGType:         "article",
		Section:        "writing",
		Published:      post.Date.Format("2006-01-02T15:04:05Z07:00"),
		StructuredData: template.JS(`{"@type":"BlogPosting"}`),
	}
	article, err := renderer.Article(render.ArticlePage{Chrome: articleChrome, Post: post})
	if err != nil {
		t.Fatalf("Article() error = %v", err)
	}
	assertGolden(t, "article.html", article)
}

func assertGolden(t *testing.T, name string, actual []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll() error = %v", err)
		}
		if err := os.WriteFile(path, actual, 0o644); err != nil {
			t.Fatalf("WriteFile() error = %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v; run with UPDATE_GOLDEN=1", path, err)
	}
	if !bytes.Equal(actual, want) {
		t.Fatalf("rendered %s differs from golden file; run with UPDATE_GOLDEN=1 after review", name)
	}
}
