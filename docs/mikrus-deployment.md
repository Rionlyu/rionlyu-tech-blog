# Mikrus deployment

This is the prepared production path for a Mikrus 3.5 server running Ubuntu 24.04 LTS. It uses a remotely managed Cloudflare Tunnel because Mikrus provides native IPv6 and shared IPv4 ports rather than a dedicated public IPv4 address.

```text
Visitor → Cloudflare HTTPS → outbound tunnel → cloudflared 172.31.0.3 → blog 172.31.0.2:8080
```

Neither container publishes a host port. The tunnel creates outbound connections to Cloudflare, and the blog accepts forwarding headers only from the tunnel container's exact private address.

## Information needed after activation

Keep credentials private. The only non-secret server details needed to finish the deployment are:

- the Mikrus hostname, such as `srvNN.mikr.us`;
- the assigned SSH port;
- confirmation that Ubuntu 24.04 LTS is installed and SSH works.

Do not send anyone the root password, SSH private key, Cloudflare tunnel token, contact webhook token, or `.env` file.

## 1. Secure initial SSH access

Mikrus supplies a nonstandard SSH port in the welcome message. On the local computer, create a dedicated key if one does not already exist:

```sh
ssh-keygen -t ed25519 -a 64 -f ~/.ssh/mikrus-rionlyu
ssh-copy-id -i ~/.ssh/mikrus-rionlyu.pub -p <SSH_PORT> root@<MIKRUS_HOST>
ssh -i ~/.ssh/mikrus-rionlyu -p <SSH_PORT> root@<MIKRUS_HOST>
```

Open a second terminal and verify key-based login before changing SSH configuration. Do not close the working session until the second login succeeds. Then copy the tracked hardening drop-in and install it with root-only permissions:

```sh
scp -P <SSH_PORT> deploy/mikrus/sshd-hardening.conf root@<MIKRUS_HOST>:/tmp/rionlyu-sshd-hardening.conf
ssh -p <SSH_PORT> root@<MIKRUS_HOST> \
  'install -o root -g root -m 0600 /tmp/rionlyu-sshd-hardening.conf /etc/ssh/sshd_config.d/99-rionlyu-hardening.conf && rm /tmp/rionlyu-sshd-hardening.conf'
```

Validate before reloading, then test a new session again:

```sh
sshd -t
systemctl reload ssh
sshd -T | grep -E '^(permitrootlogin|passwordauthentication|kbdinteractiveauthentication|pubkeyauthentication|maxauthtries|x11forwarding|allowagentforwarding|allowtcpforwarding|permittunnel) '
```

The Mikrus image already disables password authentication. The drop-in makes that policy explicit, retains root access only through the installed public key, and disables SSH forwarding features this deployment does not use. Do not change Mikrus's internal SSH port 22; the provider maps the assigned public port to it.

## 2. Register the domain and prepare Cloudflare

Register `rionlyu.dev` with [Cloudflare Registrar](https://www.cloudflare.com/application-services/products/registrar/buy-dev-domains/), enable account two-factor authentication, registrar lock, DNSSEC, and automatic renewal. Do not purchase hosting, email, or an SSL certificate.

Cloudflare will be both the registrar/DNS provider and the public TLS edge. A `.dev` domain requires HTTPS from its first browser visit; the tunnel supplies it automatically.

## 3. Verify the preinstalled Docker runtime

The provisioned Ubuntu 24.04 image already includes current Docker Engine and the Compose plugin. Update the OS and verify them rather than installing a second Docker package:

```sh
apt update
apt full-upgrade -y
systemctl enable --now docker
docker version
docker compose version
docker info --format '{{json .SecurityOptions}} {{.CgroupVersion}} {{.Driver}}'
```

The expected storage driver is `overlayfs`, cgroups are version 2, and AppArmor plus Docker's default seccomp profile should appear in the security options. If Mikrus reports Docker Hub's unauthenticated pull limit, run its supplied `docker_proxy` command once and retry. Do not install Docker on Mikrus 1.0; this deployment is for Mikrus 3.5.

## 4. Create the remotely managed tunnel

Cloudflare recommends remotely managed tunnels for Docker. In the Cloudflare dashboard:

1. Open **Networking → Tunnels** and create a Cloudflared tunnel named `rionlyu-blog`.
2. Select the Docker connector instructions.
3. Copy only the long `eyJ...` tunnel token; do not run the displayed one-off Docker command.
4. Add a public application route for `rionlyu.dev` with service URL `http://blog:8080`.
5. Add a second route for `www.rionlyu.dev` with the same service URL.
6. Leave the HTTP Host Header unchanged so the application receives the public hostname.

The route creation manages the required proxied DNS records. Do not add separate `A` or `AAAA` records for these two names. No `cert.pem`, tunnel JSON file, public server IP, or inbound firewall opening is needed.

Anyone holding the tunnel token can run a connector for this tunnel. Store it only in `.env`; rotate it in Cloudflare immediately if it is exposed.

## 5. Clone and configure the blog

On the Mikrus server:

```sh
git clone https://github.com/Rionlyu/rionlyu-tech-blog.git
cd rionlyu-tech-blog
cp .env.example .env
chmod 600 .env
openssl rand -base64 48
```

Put the generated random value into `CSRF_SECRET`. Set these values in `.env`:

```text
CLOUDFLARE_TUNNEL_TOKEN=<the eyJ... connector token>
CONTACT_WEBHOOK_URL=https://provider.example/accepted-messages
CONTACT_WEBHOOK_TOKEN=
CSRF_SECRET=<the generated value>
CONTACT_DELIVERY_TIMEOUT=8s
CONTACT_RATE_LIMIT=5
CONTACT_GLOBAL_RATE_LIMIT=100
CONTACT_RATE_WINDOW=1h
MAX_IN_FLIGHT_REQUESTS=64
```

The contact endpoint must be HTTPS and must return a `2xx` response only after accepting the message. Leave `CONTACT_WEBHOOK_TOKEN` empty when the provider does not require a bearer token.

The application does not load `.env` itself; Compose supplies it. Never commit the file or paste its contents into an issue, chat, command output, or Cloudflare route field.

## 6. Validate and launch

The quiet validation form is intentional: ordinary `docker compose config` output would print interpolated secrets.

```sh
docker compose --env-file .env -f deploy/mikrus/compose.yaml config --quiet
docker compose --env-file .env -f deploy/mikrus/compose.yaml build --pull blog cloudflared
docker compose --env-file .env -f deploy/mikrus/compose.yaml up -d
docker compose --env-file .env -f deploy/mikrus/compose.yaml ps
```

Inspect startup without exposing the resolved Compose configuration:

```sh
docker compose --env-file .env -f deploy/mikrus/compose.yaml logs --tail=100 blog cloudflared
docker compose --env-file .env -f deploy/mikrus/compose.yaml exec -T blog \
  wget -q -O- http://127.0.0.1:8080/healthz
```

The tunnel should show four established edge connections and the internal health response should be `ok`.

## 7. Verify the public site

```sh
curl -fsS https://rionlyu.dev/healthz
curl -I https://rionlyu.dev/
curl -I https://www.rionlyu.dev/projects
curl -fsS https://rionlyu.dev/rss.xml
curl -fsS https://rionlyu.dev/sitemap.xml
```

Confirm that:

- the apex loads over HTTPS with CSP, HSTS, and `X-Content-Type-Options` headers;
- `www` returns a permanent `308` redirect to the same path on the apex;
- canonical URLs, RSS, and sitemap use `https://rionlyu.dev`;
- one real contact message arrives;
- a rejected webhook response never displays the success state.

Apply the narrowly scoped WAF, contact rate-limit, and public-page Cache Rules in the [traffic and abuse plan](traffic-and-abuse.md). Do not create a site-wide “Cache Everything” rule: contact pages and form responses must remain dynamic.

The application limits itself to 64 active requests by default. Public requests beyond that limit receive `503 Service Unavailable`, `Retry-After: 5`, and a compact waiting page; the health route remains available. This is a last safety valve. Cloudflare caching should absorb legitimate article traffic before it reaches this limit.

## Updates

```sh
git pull --ff-only
make test
docker compose --env-file .env -f deploy/mikrus/compose.yaml build --pull blog cloudflared
docker compose --env-file .env -f deploy/mikrus/compose.yaml up -d
```

Cloudflare's upstream `2026.7.2` image contains known high-severity findings in its Go runtime and gRPC dependency. The deployment therefore builds the exact tagged source commit with Go 1.26.5 and the minimal gRPC 1.82.1 security update. The source commit, builder, runtime base, dependency checksums, and resulting image version are pinned; CI scans the rebuilt connector. Review each later upstream release and return to the official image after it passes the same scan. Dependabot proposes updates to the build and runtime bases, while the source release is reviewed manually.

Check disk use periodically with `docker system df`; Mikrus has no reliable file-backed swap, so retain the 4 GB plan for container builds.

Review Cloudflare Analytics and Security Events after publishing a popular link, and use the checks in the [traffic and abuse plan](traffic-and-abuse.md) when investigating unusual traffic. Do not increase `MAX_IN_FLIGHT_REQUESTS` until measurements show the application is healthy and the edge cache is already working.

## Rollback and recovery

The blog has no database or content volume. To roll back, check out a previously reviewed commit and rebuild the `blog` service. Cloudflare stores the remotely managed tunnel routes, while the server stores only the connector token in `.env`.

Keep an encrypted off-server copy of the `.env` values or document how to rotate them. If the server is lost, reinstall Docker, clone the repository, create fresh CSRF and webhook secrets, rotate the tunnel token, and run the launch commands again.

## Why this differs from the conventional VPS deployment

Mikrus is an LXC-based service with shared IPv4 ports and native IPv6. The conventional [`deploy/compose.yaml`](../deploy/compose.yaml) publishes TCP 80/443 and lets Caddy obtain certificates, which assumes a dedicated public address. The Mikrus manifest instead publishes no ports and uses Cloudflare's outbound-only tunnel. The regular Caddy deployment remains available for a later move to a conventional VPS.

References: [Mikrus Cloudflare Tunnel guide](https://wiki.mikr.us/podpiecie_domeny_przez_tunel_cloudflare/), [Mikrus technical limitations](https://wiki.mikr.us/ograniczenia_techniczne_mikrusa/), [Cloudflare Tunnel setup](https://developers.cloudflare.com/tunnel/setup/), and [Docker Engine on Ubuntu](https://docs.docker.com/engine/install/ubuntu/).
