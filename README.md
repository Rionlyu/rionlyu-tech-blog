# rionlyu.dev

The source for Beniamin Kmieć’s technical blog. It is a small Go server that loads authored Markdown once, renders escaped HTML templates, and serves an immutable content snapshot. JavaScript is limited to the writing-index title experiment and the contact-success celebration.

The project is open source under the [MIT License](LICENSE). Bundled third-party artwork retains its separately documented Apache-2.0 license.

## Run locally

Requirements: Go 1.26, Node.js 24 or newer, and npm.

```sh
npm ci
npm run build:assets
go run ./cmd/blog
```

Open `http://localhost:8080`. Development defaults to the explicit `devnull` contact provider: it accepts and discards a message so the complete form flow can be tested without logging private content. It is rejected when `ENVIRONMENT=production`.

The application reads environment variables directly; it does not load `.env` files. The common local override is:

```sh
PORT=9090 CANONICAL_ORIGIN=http://localhost:9090 go run ./cmd/blog
```

Useful commands:

```sh
make assets       # fingerprint CSS, Pretext/HTMX bundles, favicon, and duck icon
make test         # unit, render-golden, and HTTP integration tests
make test-race
make check        # assets, vet, staticcheck, tests, race tests, vuln scan, build
make build        # bin/blog
```

## Architecture

The dependency direction is intentionally short:

```text
content/*.yaml + content/posts/*.md
                 │
                 ▼
         internal/content ──────► internal/render ──────► internal/httpserver
                                         ▲                       │
web/templates + fingerprinted assets ────┘                       ▼
                                                        internal/contact
                                                                 ▲
internal/config ─────────────► cmd/blog (composition root) ──────┘
```

- `internal/content` strictly decodes front matter, validates slugs/dates, renders Markdown, sanitizes the result, sorts posts, and exposes value copies from an immutable startup snapshot.
- `internal/render` parses one typed template set per page and validates the fingerprinted asset manifest at startup.
- `internal/httpserver` owns routing, canonical metadata, feeds, middleware, caching, proxy trust, CSRF, and the contact request/response variants.
- `internal/contact` owns message validation, the in-memory rate policy, and the only external delivery interface.
- `cmd/blog` constructs concrete dependencies and owns server timeouts, signals, and graceful shutdown.

A page request reads the immutable snapshot, builds a typed view model, renders into a buffer, and only then writes a response. Public pages advertise a short shared-cache lifetime so an edge cache can absorb traffic spikes. An active-request gate rejects excess origin work with a retryable `503` instead of building an unbounded queue. A contact request is size-limited and shape-checked, verifies a signed double-submit CSRF token, evaluates the honeypot and validation, applies the per-client rate limit, and calls `contact.Sender` with a deadline. The success marker is rendered only after the configured provider returns a 2xx acceptance.

There is no database, runtime content parsing, global service container, background framework, client-side router, or remote runtime asset.

### Deliberate tradeoffs

- The rate limiter is process-local. This is correct for one small instance; a multi-replica deployment needs a shared edge limit or a deliberately added shared limiter.
- Contact errors preserve only the validated name. Email addresses and message bodies are not reflected after a failed submission, reducing accidental retention in intermediary HTML caches and browser history.
- The title experiment reserves the semantic headline’s original height. Pretext handles only the active mouse-hovered title; touch, pen, reduced-motion, no-JavaScript, and unsupported layouts keep ordinary linked text.
- Article content is embedded in the binary. Publishing therefore creates a reviewable deployment artifact and avoids a writable production content store.

## Publish a post

Create `content/posts/<slug>.md`:

```md
---
title: A precise title
slug: a-precise-title
date: 2026-07-22
updated: 2026-07-24 # optional; never before date
summary: One optional line used in lists and metadata.
draft: false
---

Markdown starts here.
```

Slugs must use lowercase letters, numbers, and single hyphens. Dates use `YYYY-MM-DD`. Duplicate slugs, unknown front-matter fields, malformed content, unsafe URLs, or invalid templates fail startup and tests. Raw HTML is not emitted; rendered Markdown passes through an explicit allowlist sanitizer. Headings receive stable copyable fragment links.

Edit navigation, page descriptions, labels, identity copy, and the two project entries in `content/site.yaml`. Run `make test` before publishing. A rebuild embeds the new snapshot and automatically updates the index, RSS, sitemap, adjacent-post navigation, structured data, and canonical URLs.

## Contact provider

Production uses a generic HTTPS webhook. Configure:

```text
CONTACT_PROVIDER=webhook
CONTACT_WEBHOOK_URL=https://provider.example/accepted-messages
CONTACT_WEBHOOK_TOKEN=...
CONTACT_DELIVERY_TIMEOUT=8s
CONTACT_RATE_LIMIT=5
CONTACT_GLOBAL_RATE_LIMIT=100
CONTACT_RATE_WINDOW=1h
MAX_IN_FLIGHT_REQUESTS=64
CSRF_SECRET=...
```

The server sends one JSON request:

```json
{"name":"...","email":"...","message":"..."}
```

When a token is set it is sent as `Authorization: Bearer …`. Redirects are refused so credentials cannot cross origins. Any 2xx response means accepted; timeouts, transport failures, and other statuses produce a generic accessible error and never trigger the duck celebration. The server never logs message bodies, names, email addresses, tokens, or client IP addresses.

Generate a CSRF secret with at least 32 bytes of entropy, for example `openssl rand -base64 32`, and store it in the host’s secret manager. `.env.example` intentionally contains variable names only.

## Dependencies

Every non-standard runtime/build dependency is pinned:

| Dependency | Why it exists |
|---|---|
| `goldmark` 1.8.4 | CommonMark/GFM parsing with raw HTML disabled |
| `bluemonday` 1.0.27 | Defense-in-depth allowlist sanitation after Markdown rendering |
| `yaml.v3` 3.0.1 | Strict front matter and content-copy decoding |
| `x/net` 0.57.0 | Explicit secure override for the sanitizer’s HTML tokenizer dependency |
| `@chenglou/pretext` 0.0.8 | Cached variable-width layout for the one title interaction |
| `htmx.org` 2.0.10 | Progressive contact-form fragment submission |
| `esbuild` 0.28.1 | Tiny local build step for the two page-specific bundles and CSS |
| `pako` 3.0.1 | Platform-independent generation of committed precompressed assets |
| Caddy 2.11.4 with gRPC 1.82.1 | Minimal TLS edge image with the current fixed Go dependency set |
| cloudflared 2026.7.2 source, Go 1.26.5, gRPC 1.82.1 | Pinned outbound connector rebuilt with the current security fixes |
| Noto Emoji `duck` at `8998f5d` | Apache-2.0-licensed duck by Google, rendered as a monochrome text-color mask |

`package-lock.json` and `go.sum` record transitive dependencies. CI action revisions are pinned by commit SHA. The browser build contains no CDN imports, trackers, remotely hosted fonts, or animation libraries. See `THIRD_PARTY_NOTICES.md` and `LICENSES/Apache-2.0.txt` for the bundled icon notice and license.

## Production

For the selected Mikrus 3.5 setup—Docker Compose, an outbound Cloudflare Tunnel, exact trusted-proxy configuration, DNS, verification, updates, and rollback—follow the [Mikrus deployment guide](docs/mikrus-deployment.md). The [conventional VPS guide](docs/deployment.md) remains available for a host with dedicated public ports.

The image can also run directly on any container host:

Build and run the non-root container:

```sh
docker build -t rionlyu-blog:latest .
docker run --read-only --tmpfs /tmp \
  -p 8080:8080 \
  -e ENVIRONMENT=production \
  -e CANONICAL_ORIGIN=https://rionlyu.dev \
  -e CONTACT_PROVIDER=webhook \
  -e CONTACT_WEBHOOK_URL=https://provider.example/accepted-messages \
  -e CONTACT_WEBHOOK_TOKEN \
  -e CSRF_SECRET \
  rionlyu-blog:latest
```

The process reads `PORT`, exposes `/healthz`, sets explicit HTTP timeouts, and shuts down gracefully on `SIGTERM`. See [docs/deployment.md](docs/deployment.md) for DNS, TLS, proxy trust, redirects, and backup/restore.

## Security

CI runs formatting checks, npm audit, `go vet`, staticcheck, unit and HTTP integration tests, race tests, `govulncheck`, binary compilation, complete container builds, Compose validation, and high/critical vulnerability scans of the application, Caddy, and Cloudflare Tunnel images. Dependabot watches Go, npm, Caddy, container bases, and GitHub Actions dependencies; the pinned cloudflared source release is reviewed manually. The [traffic and abuse plan](docs/traffic-and-abuse.md) documents edge WAF/rate-limit/cache settings, graceful overload behavior, monitoring, and incident response. Please report suspected vulnerabilities through the private process in [SECURITY.md](SECURITY.md), not a public issue.
