# syntax=docker/dockerfile:1

# Cloudflare's 2026.7.2 image was built with Go 1.26.4 and gRPC 1.81.1.
# This reproducible rebuild keeps the exact upstream source commit while applying
# the two dependency-only updates recorded in cloudflared-security.patch.
FROM golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS build

ENV GOTOOLCHAIN=local

RUN apk add --no-cache git

ARG CLOUDFLARED_VERSION=2026.7.2
ARG CLOUDFLARED_COMMIT=8679787525edc8575b2948a7c4a50b6292c6d426
ARG CLOUDFLARED_BUILD_TIME=2026-07-15T11:01:07Z
ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

RUN git init --quiet \
    && git remote add origin https://github.com/cloudflare/cloudflared.git \
    && git fetch --quiet --depth 1 origin "${CLOUDFLARED_COMMIT}" \
    && git checkout --quiet --detach FETCH_HEAD \
    && test "$(git rev-parse HEAD)" = "${CLOUDFLARED_COMMIT}"

COPY deploy/mikrus/cloudflared-security.patch /tmp/cloudflared-security.patch
RUN git apply --check /tmp/cloudflared-security.patch \
    && git apply /tmp/cloudflared-security.patch \
    && go mod download all \
    && go mod verify

RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -mod=readonly -trimpath -buildvcs=false \
      -ldflags="-s -w -X main.Version=${CLOUDFLARED_VERSION} -X main.BuildTime=${CLOUDFLARED_BUILD_TIME} -X github.com/cloudflare/cloudflared/metrics.Runtime=virtual" \
      -o /out/cloudflared ./cmd/cloudflared

FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

ARG CLOUDFLARED_VERSION=2026.7.2
ARG CLOUDFLARED_COMMIT=8679787525edc8575b2948a7c4a50b6292c6d426

LABEL org.opencontainers.image.source="https://github.com/cloudflare/cloudflared" \
      org.opencontainers.image.revision="${CLOUDFLARED_COMMIT}" \
      org.opencontainers.image.version="${CLOUDFLARED_VERSION}-security.1" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.description="cloudflared 2026.7.2 rebuilt with Go 1.26.5 and gRPC 1.82.1"

COPY --from=build --chown=65532:65532 /out/cloudflared /usr/local/bin/cloudflared
COPY --from=build --chown=65532:65532 /src/LICENSE /usr/share/licenses/cloudflared/LICENSE

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/cloudflared"]
CMD ["version"]
