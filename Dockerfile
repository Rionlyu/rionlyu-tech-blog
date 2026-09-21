FROM node:24.18.0-alpine3.24 AS assets
WORKDIR /src
COPY package.json package-lock.json ./
COPY tools ./tools
COPY web/assets/src ./web/assets/src
RUN npm ci && npm run build:assets && npm run check:assets

FROM golang:1.26.5-alpine3.24 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=assets /src/web/assets/dist ./web/assets/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/blog ./cmd/blog

FROM alpine:3.24.2
LABEL org.opencontainers.image.source="https://github.com/Rionlyu/rionlyu-tech-blog" \
      org.opencontainers.image.licenses="MIT"
RUN apk add --no-cache ca-certificates \
    && addgroup -S blog \
    && adduser -S -G blog -H -s /sbin/nologin blog
COPY --from=build /out/blog /usr/local/bin/blog
COPY --from=build /src/THIRD_PARTY_NOTICES.md /usr/share/doc/rionlyu-blog/THIRD_PARTY_NOTICES.md
COPY --from=build /src/LICENSE /usr/share/licenses/rionlyu-blog/MIT.txt
COPY --from=build /src/LICENSES/Apache-2.0.txt /usr/share/licenses/rionlyu-blog/Apache-2.0.txt
USER blog:blog
EXPOSE 8080
ENV PORT=8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget -q -O- "http://127.0.0.1:${PORT}/healthz" >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/blog"]
