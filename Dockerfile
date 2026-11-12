# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.24.3

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS build

WORKDIR /src

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG APP_VERSION=dev
ARG APP_COMMIT=
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid= -X=mb/internal/buildinfo.Version=${APP_VERSION} -X=mb/internal/buildinfo.Commit=${APP_COMMIT}" -o /out/mb .
RUN mkdir -p /out/data /out/tmp

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/data /data
COPY --from=build --chown=10001:10001 /out/tmp /tmp
COPY --from=build /out/mb /usr/local/bin/mb

ENV MB_ADDR=:15000
ENV MB_DB=/data/mb.db
ENV SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
ENV TMPDIR=/tmp

WORKDIR /app
EXPOSE 15000
VOLUME ["/data"]

USER 10001:10001
ENTRYPOINT ["/usr/local/bin/mb"]
