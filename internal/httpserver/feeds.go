package httpserver

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
)

type rssDocument struct {
	XMLName xml.Name   `xml:"rss"`
	Version string     `xml:"version,attr"`
	Atom    string     `xml:"xmlns:atom,attr"`
	Channel rssChannel `xml:"channel"`
}

type rssChannel struct {
	Title       string    `xml:"title"`
	Link        string    `xml:"link"`
	Description string    `xml:"description"`
	Language    string    `xml:"language"`
	Self        atomLink  `xml:"atom:link"`
	Items       []rssItem `xml:"item"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type rssItem struct {
	Title       string  `xml:"title"`
	Link        string  `xml:"link"`
	GUID        rssGUID `xml:"guid"`
	Description string  `xml:"description"`
	PubDate     string  `xml:"pubDate"`
}

type rssGUID struct {
	IsPermaLink bool   `xml:"isPermaLink,attr"`
	Value       string `xml:",chardata"`
}

func (s *Server) rss(w http.ResponseWriter, r *http.Request) {
	posts := s.content.Posts()
	items := make([]rssItem, 0, len(posts))
	for _, post := range posts {
		link := s.absolute("/posts/" + post.Slug)
		items = append(items, rssItem{
			Title:       post.Title,
			Link:        link,
			GUID:        rssGUID{IsPermaLink: true, Value: link},
			Description: post.Summary,
			PubDate:     post.Date.Format("Mon, 02 Jan 2006 15:04:05 -0700"),
		})
	}
	document := rssDocument{
		Version: "2.0",
		Atom:    "http://www.w3.org/2005/Atom",
		Channel: rssChannel{
			Title:       s.site.Labels.FeedTitle,
			Link:        s.absolute("/"),
			Description: s.site.Pages.Home.Description,
			Language:    "en",
			Self:        atomLink{Href: s.absolute("/rss.xml"), Rel: "self", Type: "application/rss+xml"},
			Items:       items,
		},
	}
	data, err := xml.MarshalIndent(document, "", "  ")
	if err != nil {
		s.renderFailure(w, r, fmt.Errorf("encode RSS: %w", err))
		return
	}
	data = append([]byte(xml.Header), data...)
	s.writeCacheable(w, r, http.StatusOK, "application/rss+xml; charset=utf-8", data)
}

type sitemapDocument struct {
	XMLName xml.Name     `xml:"urlset"`
	XMLNS   string       `xml:"xmlns,attr"`
	URLs    []sitemapURL `xml:"url"`
}

type sitemapURL struct {
	Location string `xml:"loc"`
	LastMod  string `xml:"lastmod,omitempty"`
}

func (s *Server) sitemap(w http.ResponseWriter, r *http.Request) {
	urls := []sitemapURL{
		{Location: s.absolute("/")},
		{Location: s.absolute("/projects")},
		{Location: s.absolute("/contact")},
	}
	for _, post := range s.content.Posts() {
		lastModified := post.Date
		if !post.Updated.IsZero() {
			lastModified = post.Updated
		}
		urls = append(urls, sitemapURL{
			Location: s.absolute("/posts/" + post.Slug),
			LastMod:  lastModified.Format("2006-01-02"),
		})
	}
	data, err := xml.MarshalIndent(sitemapDocument{
		XMLNS: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  urls,
	}, "", "  ")
	if err != nil {
		s.renderFailure(w, r, fmt.Errorf("encode sitemap: %w", err))
		return
	}
	data = append([]byte(xml.Header), data...)
	s.writeCacheable(w, r, http.StatusOK, "application/xml; charset=utf-8", data)
}

func (s *Server) robots(w http.ResponseWriter, r *http.Request) {
	data := []byte(strings.Join([]string{
		"User-agent: *",
		"Allow: /",
		"Sitemap: " + s.absolute("/sitemap.xml"),
		"",
	}, "\n"))
	s.writeCacheable(w, r, http.StatusOK, "text/plain; charset=utf-8", data)
}
