package web

import "embed"

// Files contains the parsed-at-startup templates and fingerprinted production assets.
//
//go:embed templates/*.tmpl assets/dist/*
var Files embed.FS
