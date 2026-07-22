// Package content loads and validates the site's immutable authored content.
package content

import (
	"bytes"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

var (
	slugPattern       = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
	headingPattern    = regexp.MustCompile(`<h([2-6]) id="([^"]+)">`)
	headingIDPattern  = regexp.MustCompile(`^[\pL\pN_.:-]+$`)
	languagePattern   = regexp.MustCompile(`^language-[A-Za-z0-9_-]+$`)
	errMissingDivider = errors.New("front matter must end with a --- divider")
)

// Site is editable copy and navigation data, separate from view logic.
type Site struct {
	Identity   Identity   `yaml:"identity"`
	Navigation Navigation `yaml:"navigation"`
	Pages      Pages      `yaml:"pages"`
	Footer     Footer     `yaml:"footer"`
	Labels     Labels     `yaml:"labels"`
	Projects   []Project  `yaml:"projects"`
}

type Identity struct {
	Name     string `yaml:"name"`
	Wordmark string `yaml:"wordmark"`
	GitHub   string `yaml:"github"`
}

type Navigation struct {
	Writing  string `yaml:"writing"`
	Projects string `yaml:"projects"`
	Contact  string `yaml:"contact"`
	GitHub   string `yaml:"github"`
}

type PageCopy struct {
	Title       string `yaml:"title"`
	Description string `yaml:"description"`
	Heading     string `yaml:"heading"`
	Body        string `yaml:"body"`
	LinkLabel   string `yaml:"link_label"`
}

type ProjectsCopy struct {
	PageCopy    `yaml:",inline"`
	SourceLabel string `yaml:"source_label"`
}

type ContactCopy struct {
	PageCopy            `yaml:",inline"`
	Introduction        string `yaml:"introduction"`
	NameLabel           string `yaml:"name_label"`
	EmailLabel          string `yaml:"email_label"`
	MessageLabel        string `yaml:"message_label"`
	SendLabel           string `yaml:"send_label"`
	SuccessHeading      string `yaml:"success_heading"`
	SuccessBody         string `yaml:"success_body"`
	SuccessAnnouncement string `yaml:"success_announcement"`
	Unavailable         string `yaml:"unavailable"`
	FormExpired         string `yaml:"form_expired"`
	RequestTooLarge     string `yaml:"request_too_large"`
	InvalidSubmission   string `yaml:"invalid_submission"`
	RateLimited         string `yaml:"rate_limited"`
	DeliveryFailed      string `yaml:"delivery_failed"`
}

type Pages struct {
	Home     PageCopy     `yaml:"home"`
	Projects ProjectsCopy `yaml:"projects"`
	Contact  ContactCopy  `yaml:"contact"`
	NotFound PageCopy     `yaml:"not_found"`
	Error    PageCopy     `yaml:"error"`
}

type Footer struct {
	Contact string `yaml:"contact"`
	RSS     string `yaml:"rss"`
	GitHub  string `yaml:"github"`
}

type Labels struct {
	SkipContent       string `yaml:"skip_content"`
	WritingHome       string `yaml:"writing_home"`
	PrimaryNavigation string `yaml:"primary_navigation"`
	PublishedWriting  string `yaml:"published_writing"`
	AdjacentArticles  string `yaml:"adjacent_articles"`
	Honeypot          string `yaml:"honeypot"`
	FeedTitle         string `yaml:"feed_title"`
	Published         string `yaml:"published"`
	Updated           string `yaml:"updated"`
	MinuteRead        string `yaml:"minute_read"`
	OlderPost         string `yaml:"older_post"`
	NewerPost         string `yaml:"newer_post"`
	HeadingLink       string `yaml:"heading_link"`
}

type Project struct {
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	Summary string `yaml:"summary"`
}

// Post is a fully validated and sanitized article ready for rendering.
type Post struct {
	Title       string
	Slug        string
	Date        time.Time
	Updated     time.Time
	Summary     string
	HTML        template.HTML
	ReadingTime int
	WordCount   int
}

func (p Post) DateDisplay() string {
	return p.Date.Format("January 2, 2006")
}

func (p Post) UpdatedDisplay() string {
	if p.Updated.IsZero() {
		return ""
	}
	return p.Updated.Format("January 2, 2006")
}

// Store is an immutable startup snapshot. Its accessors return value copies.
type Store struct {
	site   Site
	posts  []Post
	bySlug map[string]int
}

type frontMatter struct {
	Title   string `yaml:"title"`
	Slug    string `yaml:"slug"`
	Date    string `yaml:"date"`
	Updated string `yaml:"updated"`
	Summary string `yaml:"summary"`
	Draft   bool   `yaml:"draft"`
}

// Load validates site copy and every Markdown file before publishing a snapshot.
func Load(fsys fs.FS) (*Store, error) {
	site, err := loadSite(fsys)
	if err != nil {
		return nil, fmt.Errorf("load site copy: %w", err)
	}

	paths, err := fs.Glob(fsys, "posts/*.md")
	if err != nil {
		return nil, fmt.Errorf("list posts: %w", err)
	}
	if len(paths) == 0 {
		return nil, errors.New("load posts: no Markdown files found")
	}

	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(goldmarkhtml.WithXHTML()),
	)
	policy := articlePolicy()
	seen := make(map[string]string, len(paths))
	posts := make([]Post, 0, len(paths))

	for _, path := range paths {
		data, readErr := fs.ReadFile(fsys, path)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", path, readErr)
		}
		post, draft, parseErr := parsePost(markdown, policy, data, site.Labels.HeadingLink)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s: %w", path, parseErr)
		}
		if other, exists := seen[post.Slug]; exists {
			return nil, fmt.Errorf("parse %s: duplicate slug %q also used by %s", path, post.Slug, other)
		}
		seen[post.Slug] = path
		if !draft {
			posts = append(posts, post)
		}
	}

	if len(posts) == 0 {
		return nil, errors.New("load posts: no published posts found")
	}
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Slug < posts[j].Slug
		}
		return posts[i].Date.After(posts[j].Date)
	})
	bySlug := make(map[string]int, len(posts))
	for i := range posts {
		bySlug[posts[i].Slug] = i
	}

	return &Store{site: site, posts: posts, bySlug: bySlug}, nil
}

func (s *Store) Site() Site {
	site := s.site
	site.Projects = slices.Clone(s.site.Projects)
	return site
}

func (s *Store) Posts() []Post {
	return slices.Clone(s.posts)
}

func (s *Store) Post(slug string) (Post, bool) {
	index, ok := s.bySlug[slug]
	if !ok {
		return Post{}, false
	}
	return s.posts[index], true
}

// Neighbors returns the older and newer posts around slug.
func (s *Store) Neighbors(slug string) (older Post, hasOlder bool, newer Post, hasNewer bool) {
	index, ok := s.bySlug[slug]
	if !ok {
		return Post{}, false, Post{}, false
	}
	if index+1 < len(s.posts) {
		older, hasOlder = s.posts[index+1], true
	}
	if index > 0 {
		newer, hasNewer = s.posts[index-1], true
	}
	return older, hasOlder, newer, hasNewer
}

func loadSite(fsys fs.FS) (Site, error) {
	data, err := fs.ReadFile(fsys, "site.yaml")
	if err != nil {
		return Site{}, err
	}
	var site Site
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&site); err != nil {
		return Site{}, fmt.Errorf("decode site.yaml: %w", err)
	}
	if err := validateSite(site); err != nil {
		return Site{}, err
	}
	return site, nil
}

func validateSite(site Site) error {
	required := map[string]string{
		"identity.name":                      site.Identity.Name,
		"identity.wordmark":                  site.Identity.Wordmark,
		"identity.github":                    site.Identity.GitHub,
		"navigation.writing":                 site.Navigation.Writing,
		"navigation.projects":                site.Navigation.Projects,
		"navigation.contact":                 site.Navigation.Contact,
		"navigation.github":                  site.Navigation.GitHub,
		"pages.home.title":                   site.Pages.Home.Title,
		"pages.home.description":             site.Pages.Home.Description,
		"pages.home.heading":                 site.Pages.Home.Heading,
		"pages.projects.title":               site.Pages.Projects.Title,
		"pages.projects.description":         site.Pages.Projects.Description,
		"pages.projects.heading":             site.Pages.Projects.Heading,
		"pages.projects.source_label":        site.Pages.Projects.SourceLabel,
		"pages.contact.title":                site.Pages.Contact.Title,
		"pages.contact.description":          site.Pages.Contact.Description,
		"pages.contact.heading":              site.Pages.Contact.Heading,
		"pages.contact.introduction":         site.Pages.Contact.Introduction,
		"pages.contact.name_label":           site.Pages.Contact.NameLabel,
		"pages.contact.email_label":          site.Pages.Contact.EmailLabel,
		"pages.contact.message_label":        site.Pages.Contact.MessageLabel,
		"pages.contact.send_label":           site.Pages.Contact.SendLabel,
		"pages.contact.form_expired":         site.Pages.Contact.FormExpired,
		"pages.contact.request_too_large":    site.Pages.Contact.RequestTooLarge,
		"pages.contact.invalid_submission":   site.Pages.Contact.InvalidSubmission,
		"pages.contact.rate_limited":         site.Pages.Contact.RateLimited,
		"pages.contact.delivery_failed":      site.Pages.Contact.DeliveryFailed,
		"pages.contact.success_heading":      site.Pages.Contact.SuccessHeading,
		"pages.contact.success_body":         site.Pages.Contact.SuccessBody,
		"pages.contact.success_announcement": site.Pages.Contact.SuccessAnnouncement,
		"pages.contact.unavailable":          site.Pages.Contact.Unavailable,
		"pages.not_found.title":              site.Pages.NotFound.Title,
		"pages.not_found.description":        site.Pages.NotFound.Description,
		"pages.not_found.heading":            site.Pages.NotFound.Heading,
		"pages.not_found.body":               site.Pages.NotFound.Body,
		"pages.not_found.link_label":         site.Pages.NotFound.LinkLabel,
		"pages.error.title":                  site.Pages.Error.Title,
		"pages.error.description":            site.Pages.Error.Description,
		"pages.error.heading":                site.Pages.Error.Heading,
		"pages.error.body":                   site.Pages.Error.Body,
		"footer.contact":                     site.Footer.Contact,
		"footer.rss":                         site.Footer.RSS,
		"footer.github":                      site.Footer.GitHub,
		"labels.skip_content":                site.Labels.SkipContent,
		"labels.writing_home":                site.Labels.WritingHome,
		"labels.primary_navigation":          site.Labels.PrimaryNavigation,
		"labels.published_writing":           site.Labels.PublishedWriting,
		"labels.adjacent_articles":           site.Labels.AdjacentArticles,
		"labels.honeypot":                    site.Labels.Honeypot,
		"labels.feed_title":                  site.Labels.FeedTitle,
		"labels.published":                   site.Labels.Published,
		"labels.updated":                     site.Labels.Updated,
		"labels.minute_read":                 site.Labels.MinuteRead,
		"labels.older_post":                  site.Labels.OlderPost,
		"labels.newer_post":                  site.Labels.NewerPost,
		"labels.heading_link":                site.Labels.HeadingLink,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	if !validHTTPSURL(site.Identity.GitHub) {
		return errors.New("identity.github must be an absolute https URL")
	}
	if len(site.Projects) == 0 {
		return errors.New("projects must contain at least one entry")
	}
	seen := make(map[string]struct{}, len(site.Projects))
	for i, project := range site.Projects {
		if strings.TrimSpace(project.Name) == "" || strings.TrimSpace(project.URL) == "" || strings.TrimSpace(project.Summary) == "" {
			return fmt.Errorf("projects[%d] requires name, url, and summary", i)
		}
		key := strings.ToLower(project.Name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("projects[%d] duplicates name %q", i, project.Name)
		}
		if !validHTTPSURL(project.URL) {
			return fmt.Errorf("projects[%d].url must be an absolute https URL", i)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validHTTPSURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}

func parsePost(markdown goldmark.Markdown, policy *bluemonday.Policy, data []byte, headingLinkLabel string) (Post, bool, error) {
	front, body, err := splitFrontMatter(data)
	if err != nil {
		return Post{}, false, err
	}
	var metadata frontMatter
	decoder := yaml.NewDecoder(bytes.NewReader(front))
	decoder.KnownFields(true)
	if err := decoder.Decode(&metadata); err != nil {
		return Post{}, false, fmt.Errorf("decode front matter: %w", err)
	}
	if err := validateFrontMatter(metadata, body); err != nil {
		return Post{}, false, err
	}

	published, _ := time.Parse("2006-01-02", metadata.Date)
	var updated time.Time
	if metadata.Updated != "" {
		updated, _ = time.Parse("2006-01-02", metadata.Updated)
	}

	var rendered bytes.Buffer
	if err := markdown.Convert(body, &rendered); err != nil {
		return Post{}, false, fmt.Errorf("render Markdown: %w", err)
	}
	withAnchors := headingPattern.ReplaceAllStringFunc(rendered.String(), func(match string) string {
		parts := headingPattern.FindStringSubmatch(match)
		return fmt.Sprintf(`<h%s id="%s"><a class="heading-anchor" href="#%s" aria-label="%s">#</a>`, parts[1], parts[2], parts[2], template.HTMLEscapeString(headingLinkLabel))
	})
	sanitized := policy.Sanitize(withAnchors)
	words := len(strings.Fields(string(body)))
	readingTime := max(1, (words+219)/220)

	return Post{
		Title:       strings.TrimSpace(metadata.Title),
		Slug:        metadata.Slug,
		Date:        published,
		Updated:     updated,
		Summary:     strings.TrimSpace(metadata.Summary),
		HTML:        template.HTML(sanitized), // sanitized by the explicit article policy above
		ReadingTime: readingTime,
		WordCount:   words,
	}, metadata.Draft, nil
}

func splitFrontMatter(data []byte) ([]byte, []byte, error) {
	normalized := bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	if !bytes.HasPrefix(normalized, []byte("---\n")) {
		return nil, nil, errors.New("front matter must start with a --- divider")
	}
	remainder := normalized[4:]
	divider := bytes.Index(remainder, []byte("\n---\n"))
	if divider < 0 {
		return nil, nil, errMissingDivider
	}
	return remainder[:divider], bytes.TrimSpace(remainder[divider+5:]), nil
}

func validateFrontMatter(metadata frontMatter, body []byte) error {
	if strings.TrimSpace(metadata.Title) == "" {
		return errors.New("title is required")
	}
	if !slugPattern.MatchString(metadata.Slug) {
		return fmt.Errorf("slug %q must contain lowercase letters, numbers, and single hyphens", metadata.Slug)
	}
	published, err := time.Parse("2006-01-02", metadata.Date)
	if err != nil || published.Format("2006-01-02") != metadata.Date {
		return fmt.Errorf("date %q must use YYYY-MM-DD", metadata.Date)
	}
	if metadata.Updated != "" {
		updated, updateErr := time.Parse("2006-01-02", metadata.Updated)
		if updateErr != nil || updated.Format("2006-01-02") != metadata.Updated {
			return fmt.Errorf("updated %q must use YYYY-MM-DD", metadata.Updated)
		}
		if updated.Before(published) {
			return errors.New("updated date cannot be before publication date")
		}
	}
	if len(body) == 0 {
		return errors.New("article body is required")
	}
	return nil
}

func articlePolicy() *bluemonday.Policy {
	policy := bluemonday.NewPolicy()
	policy.AllowElements(
		"p", "br", "hr", "blockquote", "ul", "ol", "li",
		"strong", "em", "del", "code", "pre",
		"h2", "h3", "h4", "h5", "h6",
		"table", "thead", "tbody", "tr", "th", "td",
	)
	policy.AllowAttrs("href", "title").OnElements("a")
	policy.AllowAttrs("class").Matching(regexp.MustCompile(`^heading-anchor$`)).OnElements("a")
	policy.AllowAttrs("aria-label").OnElements("a")
	policy.AllowAttrs("id").Matching(headingIDPattern).OnElements("h2", "h3", "h4", "h5", "h6")
	policy.AllowAttrs("class").Matching(languagePattern).OnElements("code")
	policy.AllowAttrs("align").Matching(regexp.MustCompile(`^(left|center|right)$`)).OnElements("th", "td")
	policy.AllowRelativeURLs(true)
	policy.AllowURLSchemes("http", "https", "mailto")
	policy.RequireParseableURLs(true)
	return policy
}
