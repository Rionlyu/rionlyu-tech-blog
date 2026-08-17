FROM golang:1.26.6-alpine3.24 AS build
WORKDIR /src
COPY deploy/caddy/go.mod deploy/caddy/go.sum ./
RUN go mod download all
RUN CADDY_VERSION="$(go list -m -f '{{.Version}}' github.com/caddyserver/caddy/v2)" \
    && CGO_ENABLED=0 go build -trimpath \
        -ldflags="-s -w -X github.com/caddyserver/caddy/v2.CustomVersion=${CADDY_VERSION}" \
        -o /out/caddy github.com/caddyserver/caddy/v2/cmd/caddy

FROM alpine:3.24.1
LABEL org.opencontainers.image.source="https://github.com/Rionlyu/rionlyu-tech-blog" \
      org.opencontainers.image.licenses="Apache-2.0"
RUN apk add --no-cache ca-certificates \
    && addgroup -S caddy \
    && adduser -S -G caddy -H -s /sbin/nologin caddy \
    && mkdir -p /config /data /etc/caddy /usr/share/licenses/caddy \
    && chown -R caddy:caddy /config /data
COPY --from=build /out/caddy /usr/bin/caddy
COPY deploy/Caddyfile /etc/caddy/Caddyfile
COPY LICENSES/Apache-2.0.txt /usr/share/licenses/caddy/Apache-2.0.txt
ENV XDG_CONFIG_HOME=/config \
    XDG_DATA_HOME=/data
VOLUME ["/config", "/data"]
USER caddy:caddy
EXPOSE 80 443 443/udp
ENTRYPOINT ["/usr/bin/caddy"]
CMD ["run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
