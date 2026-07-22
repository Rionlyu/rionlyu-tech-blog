package content_test

import (
	"testing"
	"testing/fstest"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
)

const validSite = `
identity: {name: Beniamin, wordmark: Rionlyu, github: https://example.test/profile}
navigation: {writing: Writing, projects: Projects, contact: Contact, github: GitHub}
pages:
  home: {title: Writing, description: Posts, heading: Writing}
  projects: {title: Projects, description: Projects, heading: Projects, source_label: Source}
  contact: {title: Contact, description: Contact, heading: Hello, introduction: Intro, name_label: Name, email_label: Email, message_label: Message, send_label: Send, success_heading: Sent, success_body: Thanks, success_announcement: Sent, unavailable: Unavailable, form_expired: Expired, request_too_large: Large, invalid_submission: Invalid, rate_limited: Limited, delivery_failed: Failed}
  not_found: {title: Missing, description: Missing, heading: Missing, body: Missing, link_label: Home}
  error: {title: Error, description: Error, heading: Error, body: Error}
footer: {contact: Contact, rss: RSS, github: GitHub}
labels: {skip_content: Skip, writing_home: Home, primary_navigation: Primary, published_writing: Published writing, adjacent_articles: Adjacent, honeypot: Website, feed_title: Feed, published: Published, updated: Updated, minute_read: min read, older_post: Older, newer_post: Newer, heading_link: Copy link}
projects:
  - {name: spoold, url: https://example.test/spoold, summary: Durable delivery.}
`

func TestLoadSortsAndSanitizesPosts(t *testing.T) {
	t.Parallel()
	files := fstest.MapFS{
		"site.yaml": &fstest.MapFile{Data: []byte(validSite)},
		"posts/older.md": &fstest.MapFile{Data: []byte(`---
title: Older
slug: older
date: 2026-01-01
---
## A heading

Hello <script>alert(1)</script> [safe](https://example.test).
`)},
		"posts/newer.md": &fstest.MapFile{Data: []byte(`---
title: Newer
slug: newer
date: 2026-02-01
---
Newer body.
`)},
	}
	store, err := content.Load(files)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	posts := store.Posts()
	if len(posts) != 2 || posts[0].Slug != "newer" || posts[1].Slug != "older" {
		t.Fatalf("posts order = %#v", posts)
	}
	older, ok := store.Post("older")
	if !ok {
		t.Fatal("older post not found")
	}
	html := string(older.HTML)
	if contains(html, "<script") || !contains(html, `class="heading-anchor"`) {
		t.Fatalf("sanitized HTML = %q", html)
	}
}

func TestLoadRejectsInvalidContent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		post string
	}{
		{name: "missing front matter", post: "Body only"},
		{name: "invalid slug", post: "---\ntitle: Bad\nslug: Bad Slug\ndate: 2026-01-01\n---\nBody"},
		{name: "updated before publication", post: "---\ntitle: Bad\nslug: bad\ndate: 2026-02-01\nupdated: 2026-01-01\n---\nBody"},
		{name: "tags are unsupported", post: "---\ntitle: Bad\nslug: bad\ndate: 2026-01-01\ntags: [Go]\n---\nBody"},
		{name: "empty body", post: "---\ntitle: Bad\nslug: bad\ndate: 2026-01-01\n---\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := content.Load(fstest.MapFS{
				"site.yaml":    &fstest.MapFile{Data: []byte(validSite)},
				"posts/bad.md": &fstest.MapFile{Data: []byte(test.post)},
			})
			if err == nil {
				t.Fatal("Load() error = nil, want error")
			}
		})
	}
}

func TestLoadRejectsDuplicateSlugsIncludingDrafts(t *testing.T) {
	t.Parallel()
	_, err := content.Load(fstest.MapFS{
		"site.yaml":  &fstest.MapFile{Data: []byte(validSite)},
		"posts/a.md": &fstest.MapFile{Data: []byte("---\ntitle: A\nslug: same\ndate: 2026-01-01\ndraft: true\n---\nA")},
		"posts/b.md": &fstest.MapFile{Data: []byte("---\ntitle: B\nslug: same\ndate: 2026-01-02\n---\nB")},
	})
	if err == nil {
		t.Fatal("Load() error = nil, want duplicate slug error")
	}
}

func contains(value, part string) bool {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return true
		}
	}
	return false
}
