# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.24.3

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /out/mb .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 10001 mb \
    && adduser -S -D -H -h /app -u 10001 -G mb mb \
    && mkdir -p /data \
    && chown -R mb:mb /data

WORKDIR /app

COPY --from=build /out/mb /usr/local/bin/mb

ENV MB_ADDR=:15000
ENV MB_DB=/data/mb.db

EXPOSE 15000
VOLUME ["/data"]

USER mb:mb
ENTRYPOINT ["/usr/local/bin/mb"]
