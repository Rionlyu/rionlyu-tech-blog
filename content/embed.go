package contentfiles

import "embed"

// Files contains the authored site copy and published posts.
//
//go:embed site.yaml posts/*.md
var Files embed.FS
