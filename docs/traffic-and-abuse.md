# Traffic and abuse plan

This site does not need a queue for ordinary growth. It needs cheap public pages, an expensive-path guard for the contact form, and a controlled failure mode if the origin is ever saturated.

```text
Visitor
   │
   ▼
Cloudflare DDoS protection + WAF + contact rate rule
   │
   ├── cached public page ─────────────────────────────► visitor
   │
   ▼
Cloudflare Tunnel
   │
   ▼
Go request gate (64 active requests by default)
   │
   ├── capacity available ─► normal route
   └── capacity exhausted ─► 503 + Retry-After: 5
```

The waiting response is the last safety valve, not the normal scaling mechanism. Most legitimate traffic to articles should be answered by Cloudflare without reaching Mikrus at all.

## Protections already in the application

- There are no accounts, uploads, database queries, admin routes, or runtime content writes.
- The contact form uses a signed double-submit CSRF token, a hidden honeypot, an exact field allowlist, a 64 KiB request limit, bounded field lengths, and email validation.
- Valid contact delivery attempts count against both a per-client limit of five per hour and a global limit of 100 per hour by default, whether or not the downstream webhook accepts them. This caps webhook cost during a distributed attack. Client addresses are salted and hashed in memory rather than logged, and the limiter's distinct-client state is capped at 4,096 entries. At capacity it rejects new senders until old entries expire instead of growing memory without bound.
- Webhook delivery has an eight-second deadline, refuses redirects, and never logs names, addresses, messages, or tokens.
- `MAX_IN_FLIGHT_REQUESTS` defaults to `64`. When all slots are occupied, a request is rejected immediately with `503 Service Unavailable`, `Cache-Control: no-store`, and `Retry-After: 5`; browser page requests receive a small readable waiting page. `/healthz` bypasses this gate.
- Read and write deadlines prevent slow clients from holding connections indefinitely.
- The production containers are non-root, read-only, capability-free, and not published on a host port in the Mikrus deployment.

The in-memory contact limit is intentionally a second line of defense. A distributed botnet could use many addresses, so Cloudflare should reject bursts before they consume tunnel or origin capacity.

## Cloudflare settings before launch

### 1. Managed protection

Keep Cloudflare's automatic DDoS protection enabled and confirm that the **Cloudflare Free Managed Ruleset**, which Cloudflare deploys by default on Free zones, is active. These cover network floods and common web exploit patterns without application changes.

Do not expose a second DNS record or Docker port that bypasses the tunnel. If attackers cannot address the origin directly, the edge controls remain the only public path.

### 2. Use the free rate-limit rule for contact

Create the account's one free rate-limiting rule with this match:

```text
URI Path equals /contact
```

Set the threshold to **10 requests in 10 seconds** and the action to **Managed Challenge**. On the Free plan, leave the challenge behavior at request throttling; Cloudflare does not offer a selectable mitigation duration for challenge actions on this plan. This deliberately covers both `GET` and `POST`, because method matching is not included in the Free rule fields. A real visitor will not load or submit the form ten times in ten seconds, while a simple flood is challenged before it reaches the application.

Keep the application's slower per-client and global limits as separate backstops. Do not key a Cloudflare rule on an untrusted forwarding header; use Cloudflare's normal client-IP characteristic.

### 3. Cache only public, read-only routes

Create one Cache Rule with this expression:

```text
(http.request.uri.path eq "/") or
(starts_with(http.request.uri.path, "/posts/")) or
(http.request.uri.path eq "/projects") or
(http.request.uri.path in {"/rss.xml" "/sitemap.xml" "/robots.txt"})
```

Set **Cache eligibility** to **Eligible for cache** and leave edge and browser TTLs set to respect the origin. The application sends browser policy in `Cache-Control` and a separate edge policy in `Cloudflare-CDN-Cache-Control`: 60 seconds fresh, five minutes stale while revalidating, and one day stale on an origin `5xx`. Do not add an Edge Cache TTL override, because that disables the origin's revalidation directives. Fingerprinted assets already carry a one-year immutable policy.

Never include `/contact`, `/healthz`, redirects, errors, or arbitrary future paths in that rule. Those routes emit `no-store` where appropriate. Avoid a site-wide “Cache Everything” rule.

After deployment, request a post twice and confirm `CF-Cache-Status` changes from `MISS` to `HIT` or `REVALIDATED`:

```sh
curl -sI https://rionlyu.dev/posts/acknowledgement-durability-boundary | grep -Ei '^(cache-control|cf-cache-status|age):'
```

### 4. Treat bot challenges as escalation

Leave **Bot Fight Mode** off initially. On the Free plan it applies broadly and cannot be finely exempted, so it is unnecessary friction for RSS readers, command-line clients, and benign monitors when there is no demonstrated bot problem. If Security Events show sustained automated abuse outside `/contact`, enable it temporarily and verify those clients still work.

If realistic spam gets through the honeypot and both rate limits, add Cloudflare Turnstile in Managed mode. That requires a small application change as well as site and secret keys: the server must validate every token with Siteverify. Merely inserting the browser widget is not protection. Turnstile is therefore an evidence-driven next step, not a launch dependency.

## What happens during a spike

| Situation | Expected response |
|---|---|
| A post becomes popular | Cloudflare serves cached HTML and immutable assets; Mikrus sees only cache fills and revalidation. |
| A bot floods the contact form | The edge rate rule challenges the burst; the honeypot, validation, and per-client limit handle requests that pass. |
| Generic malicious traffic | Cloudflare DDoS/WAF controls absorb or block it before the tunnel. |
| The Go process reaches its active-request ceiling | New origin requests get the short `503` waiting response and retry hint instead of accumulating in memory. |
| The origin briefly fails | Eligible public pages may be served stale; contact stays unavailable rather than pretending a message was accepted. |
| Legitimate traffic stays high for days | Check cache hit ratio first, then move to a larger host or add replicas with a shared edge limit. Do not raise the request ceiling blindly. |

Cloudflare Waiting Room is a Business/Enterprise feature and is not justified here. Waiting rooms are useful when users compete for scarce inventory or a strictly limited backend transaction. A read-only personal blog should cache the content and shed excess origin work instead.

## Monitoring and incident response

Before launch, configure an external HTTPS check of `/healthz` every few minutes. It should expect status `200` and body `ok`. Also review Cloudflare Analytics and Security Events after publishing a link.

On the server, these checks do not reveal secrets:

```sh
docker compose --env-file .env -f deploy/mikrus/compose.yaml ps
docker stats --no-stream
docker system df
docker compose --env-file .env -f deploy/mikrus/compose.yaml logs --since=15m --tail=300 blog cloudflared
```

If traffic is abusive:

1. Confirm the route, source pattern, cache status, and WAF action in Cloudflare rather than guessing from raw volume.
2. Tighten the narrowest relevant edge rule. Do not block whole countries or enable a site-wide challenge without evidence.
3. Use Cloudflare's Under Attack mode only as a temporary emergency measure, then disable it after the event.
4. Rotate the tunnel token immediately if it might have leaked. Rotate webhook and CSRF secrets if `.env` might have leaked.
5. Preserve logs and request IDs, update the vulnerable component, and redeploy a reviewed commit. Never publish contact payloads while investigating.

If traffic is legitimate, first confirm that the public-page Cache Rule is producing a high hit ratio. CPU saturation with a poor hit ratio is a cache configuration problem; memory growth or repeated restarts with a high hit ratio is an origin problem. The Mikrus 3.5 host is suitable for this small single-instance design, but sustained origin load is the signal to migrate to a conventional VPS or managed container platform.

References: [Cloudflare DDoS protection](https://developers.cloudflare.com/ddos-protection/about/), [WAF managed rules](https://developers.cloudflare.com/waf/managed-rules/), [rate-limiting rules](https://developers.cloudflare.com/waf/rate-limiting-rules/), [CDN cache control](https://developers.cloudflare.com/cache/concepts/cdn-cache-control/), [Cache Rules](https://developers.cloudflare.com/cache/how-to/cache-rules/), [Bot Fight Mode](https://developers.cloudflare.com/bots/plans/free/), [Turnstile server-side validation](https://developers.cloudflare.com/turnstile/get-started/server-side-validation/), and [Waiting Room plans](https://developers.cloudflare.com/waiting-room/plans/).
