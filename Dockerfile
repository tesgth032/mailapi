# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.1
ARG ALPINE_VERSION=3.22

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates git

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ENV CGO_ENABLED=0

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/mailapi-api ./cmd/api && \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/mailapi-smtp ./cmd/smtp && \
    GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/mailapi-worker ./cmd/worker

FROM alpine:${ALPINE_VERSION} AS runtime

RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S mailapi && \
    adduser -S -D -H -G mailapi mailapi

COPY --from=build /out/mailapi-api /usr/local/bin/mailapi-api
COPY --from=build /out/mailapi-smtp /usr/local/bin/mailapi-smtp
COPY --from=build /out/mailapi-worker /usr/local/bin/mailapi-worker
COPY docker/docker-entrypoint.sh /usr/local/bin/mailapi-entrypoint
COPY docker/healthcheck.sh /usr/local/bin/mailapi-healthcheck

RUN chmod +x /usr/local/bin/mailapi-api \
    /usr/local/bin/mailapi-smtp \
    /usr/local/bin/mailapi-worker \
    /usr/local/bin/mailapi-entrypoint \
    /usr/local/bin/mailapi-healthcheck && \
    mkdir -p /etc/mailapi && \
    chown -R mailapi:mailapi /etc/mailapi

USER mailapi
WORKDIR /srv/mailapi

ENV MAILAPI_CONFIG=/etc/mailapi/config.yaml

EXPOSE 8080 25 6060 6061 6062

ENTRYPOINT ["mailapi-entrypoint"]
CMD ["api"]
