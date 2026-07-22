# Deployment guide

The recommended first deployment is one small Docker host with Caddy in front of the blog. Caddy obtains and renews public TLS certificates; the Go container remains private on a fixed Docker network and runs read-only as a non-root user.

```text
Internet :80/:443 → Caddy 172.30.0.3 → blog 172.30.0.2:8080
```

Only Caddy is published on the host. The blog trusts forwarded HTTPS and client-address headers only from Caddy’s exact container address.

## 1. Prepare the domain and host

You need:

- control of `rionlyu.dev` DNS;
- a Linux host with a public IP address;
- TCP ports 80 and 443 open, plus UDP 443 if you want HTTP/3;
- Docker Engine with the Compose plugin, installed from Docker’s [official instructions](https://docs.docker.com/engine/install/);
- an HTTPS webhook that accepts the contact form.

At the DNS provider, create:

```text
Type   Name   Value
A      @      <server IPv4 address>
AAAA   @      <server IPv6 address, only if IPv6 works on the host>
CNAME  www    rionlyu.dev.
```

Do not publish an `AAAA` record merely because the provider offers one; a broken IPv6 route makes the site intermittently unreachable. DNS must point at the host before Caddy can obtain certificates.

## 2. Clone and configure

On the server:

```sh
git clone https://github.com/Rionlyu/rionlyu-tech-blog.git
cd rionlyu-tech-blog
cp .env.example .env
chmod 600 .env
openssl rand -base64 48
```

Copy the generated random value into `CSRF_SECRET` in `.env`. Fill these values:

```text
ACME_EMAIL=you@example.com
CONTACT_WEBHOOK_URL=https://provider.example/accepted-messages
CONTACT_WEBHOOK_TOKEN=
CSRF_SECRET=<the generated value>
CONTACT_DELIVERY_TIMEOUT=8s
CONTACT_RATE_LIMIT=5
CONTACT_RATE_WINDOW=1h
```

The webhook receives one JSON request:

```json
{"name":"...","email":"...","message":"..."}
```

It must return a `2xx` response only after accepting the message. Set `CONTACT_WEBHOOK_TOKEN` when the endpoint expects `Authorization: Bearer <token>`; otherwise leave it empty. Redirects are refused so credentials cannot be forwarded to another host.

Do not commit `.env`. Anyone with Docker or root access on the host can inspect container environment variables, so restrict server access as carefully as SSH keys.

## 3. Validate and launch

Validate the fully resolved configuration before starting it:

```sh
docker compose --env-file .env -f deploy/compose.yaml config --quiet
docker compose --env-file .env -f deploy/compose.yaml build --pull
docker compose --env-file .env -f deploy/compose.yaml up -d
docker compose --env-file .env -f deploy/compose.yaml ps
```

Watch the first certificate issuance and application startup:

```sh
docker compose --env-file .env -f deploy/compose.yaml logs --tail=100 blog caddy
```

The blog container publishes no host port. Do not add an `8080:8080` mapping; bypassing Caddy would bypass the intended TLS boundary.

## 4. Verify the public deployment

After DNS has propagated:

```sh
curl -fsS https://rionlyu.dev/healthz
curl -I https://rionlyu.dev/
curl -I https://www.rionlyu.dev/posts/acknowledgement-durability-boundary
curl -fsS https://rionlyu.dev/rss.xml
curl -fsS https://rionlyu.dev/sitemap.xml
```

Confirm that:

- the health response is `ok`;
- the apex returns HTTPS with CSP, HSTS, and `X-Content-Type-Options` headers;
- `www.rionlyu.dev` returns a permanent `308` redirect to the same path on the apex;
- canonical URLs, RSS, and sitemap entries use `https://rionlyu.dev`;
- one real contact message arrives;
- a rejected webhook response does not display the success state.

## Updates and rollback

Run the test suite before deploying an update:

```sh
git pull --ff-only
make test
docker compose --env-file .env -f deploy/compose.yaml build --pull
docker compose --env-file .env -f deploy/compose.yaml up -d
```

For a rollback, check out a previously reviewed commit or tag and run the final two Compose commands again. The application has no database or mutable content migration.

## Operational security

- Keep only SSH, TCP 80/443, and optionally UDP 443 reachable from the internet.
- Use SSH keys, install operating-system security updates, and restrict membership in the `docker` group.
- Keep `.env` mode `0600`; rotate the webhook token and CSRF secret if they are exposed.
- Review Dependabot and CI results before merging dependency updates.
- Back up the Git repository and protected release tags. Caddy’s named volumes contain replaceable certificate state, not blog content.
- The rate limiter is process-local. Add an edge rate limit before running multiple replicas.

## Other container hosts

The image works on managed container platforms too. Set `ENVIRONMENT=production`, `CANONICAL_ORIGIN=https://rionlyu.dev`, the contact settings, and a 32-byte-or-longer `CSRF_SECRET`. Configure `/healthz` as the health check.

The platform will normally terminate TLS before forwarding HTTP to the container. Add only the platform’s documented proxy CIDRs to `TRUSTED_PROXY_CIDRS`; never use `0.0.0.0/0` or `::/0`. The proxy must replace client-supplied forwarding headers. Without that exact boundary, use the Caddy deployment above.

If `rionlyu.com` is acquired later, add `rionlyu.com` and `www.rionlyu.com` to the Caddy site-address line and point both names at the same host. The application already redirects them permanently to `https://rionlyu.dev`.
