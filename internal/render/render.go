// Package render parses templates once and renders typed page models.
package render

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"mime"
	"path/filepath"
	"strings"

	"github.com/Rionlyu/rionlyu-tech-blog/internal/content"
)

type PageMeta struct {
	Title          string
	Description    string
	Canonical      string
	RSS            string
	OGType         string
	Section        string
	NoIndex        bool
	Published      string
	Updated        string
	StructuredData template.JS
	Scripts        []string
}

type Chrome struct {
	Meta PageMeta
	Site content.Site
	Year int
}

type HomePage struct {
	Chrome
	Posts []content.Post
}

type ProjectsPage struct{ Chrome }

type ArticlePage struct {
	Chrome
	Post     content.Post
	Older    content.Post
	Newer    content.Post
	HasOlder bool
	HasNewer bool
}

type ContactForm struct {
	CSRFToken   string
	Name        string
	Errors      map[string]string
	GlobalError string
	Available   bool
	Success     bool
	SuccessID   string
}

type ContactPage struct {
	Chrome
	Form ContactForm
}

type SimplePage struct {
	Chrome
	Heading   string
	Body      string
	LinkLabel string
}

type Asset struct {
	FileName    string
	ContentType string
	ETag        string
	Data        []byte
	GzipETag    string
	GzipData    []byte
}

type Renderer struct {
	templates map[string]*template.Template
	assets    map[string]Asset
	byFile    map[string]Asset
}

func New(fsys fs.FS) (*Renderer, error) {
	manifestData, err := fs.ReadFile(fsys, "assets/dist/manifest.json")
	if err != nil {
		return nil, fmt.Errorf("read asset manifest: %w", err)
	}
	var manifest map[string]string
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("decode asset manifest: %w", err)
	}
	assets := make(map[string]Asset, len(manifest))
	byFile := make(map[string]Asset, len(manifest))
	for logicalName, fileName := range manifest {
		if filepath.Base(fileName) != fileName || strings.Contains(fileName, "..") {
			return nil, fmt.Errorf("asset %q has unsafe file name %q", logicalName, fileName)
		}
		data, readErr := fs.ReadFile(fsys, "assets/dist/"+fileName)
		if readErr != nil {
			return nil, fmt.Errorf("read asset %q: %w", logicalName, readErr)
		}
		digest := sha256.Sum256(data)
		gzipData, gzipErr := fs.ReadFile(fsys, "assets/dist/"+fileName+".gz")
		if gzipErr != nil {
			return nil, fmt.Errorf("read compressed asset %q: %w", logicalName, gzipErr)
		}
		gzipDigest := sha256.Sum256(gzipData)
		contentType := mime.TypeByExtension(filepath.Ext(fileName))
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		asset := Asset{
			FileName:    fileName,
			ContentType: contentType,
			ETag:        `"` + hex.EncodeToString(digest[:]) + `"`,
			Data:        data,
			GzipETag:    `"` + hex.EncodeToString(gzipDigest[:]) + `"`,
			GzipData:    gzipData,
		}
		assets[logicalName] = asset
		byFile[fileName] = asset
	}
	for _, required := range []string{"site.css", "titles.js", "contact.js", "duck-icon.svg", "favicon.svg"} {
		if _, ok := assets[required]; !ok {
			return nil, fmt.Errorf("asset manifest is missing %q", required)
		}
	}

	renderer := &Renderer{templates: make(map[string]*template.Template), assets: assets, byFile: byFile}
	functions := template.FuncMap{
		"asset": func(name string) (string, error) {
			asset, ok := assets[name]
			if !ok {
				return "", fmt.Errorf("unknown asset %q", name)
			}
			return "/assets/" + asset.FileName, nil
		},
	}
	for name, page := range map[string]string{
		"home":     "home.tmpl",
		"projects": "projects.tmpl",
		"article":  "article.tmpl",
		"contact":  "contact.tmpl",
		"simple":   "simple.tmpl",
	} {
		parsed, parseErr := template.New(name).Funcs(functions).ParseFS(
			fsys,
			"templates/base.tmpl",
			"templates/partials.tmpl",
			"templates/"+page,
		)
		if parseErr != nil {
			return nil, fmt.Errorf("parse %s template: %w", name, parseErr)
		}
		renderer.templates[name] = parsed
	}
	return renderer, nil
}

func (r *Renderer) Home(page HomePage) ([]byte, error) { return r.execute("home", "base", page) }
func (r *Renderer) Projects(page ProjectsPage) ([]byte, error) {
	return r.execute("projects", "base", page)
}
func (r *Renderer) Article(page ArticlePage) ([]byte, error) {
	return r.execute("article", "base", page)
}
func (r *Renderer) Contact(page ContactPage) ([]byte, error) {
	return r.execute("contact", "base", page)
}
func (r *Renderer) Simple(page SimplePage) ([]byte, error) { return r.execute("simple", "base", page) }

func (r *Renderer) ContactFragment(page ContactPage) ([]byte, error) {
	return r.execute("contact", "contact-form", page)
}

func (r *Renderer) execute(templateName, definition string, data any) ([]byte, error) {
	var output bytes.Buffer
	if err := r.templates[templateName].ExecuteTemplate(&output, definition, data); err != nil {
		return nil, fmt.Errorf("execute %s: %w", templateName, err)
	}
	return output.Bytes(), nil
}

func (r *Renderer) AssetByFile(fileName string) (Asset, bool) {
	asset, ok := r.byFile[fileName]
	return asset, ok
}

func (r *Renderer) AssetURL(logicalName string) (string, bool) {
	asset, ok := r.assets[logicalName]
	if !ok {
		return "", false
	}
	return "/assets/" + asset.FileName, true
}
