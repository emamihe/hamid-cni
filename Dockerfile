# syntax=docker/dockerfile:1

# Build on the native CI architecture and cross-compile to TARGETARCH.
# Without --platform=$BUILDPLATFORM, arm64 builds run Go under QEMU and appear stuck.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN apk add --no-cache git ca-certificates
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-cni ./cmd/cni \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-agent ./cmd/agent \
 && CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-controller ./cmd/controller

FROM alpine:3.21
RUN apk add --no-cache iptables iproute2 ca-certificates
COPY --from=builder /out/hamid-cni /usr/local/bin/hamid-cni
COPY --from=builder /out/hamid-agent /usr/local/bin/hamid-agent
COPY --from=builder /out/hamid-controller /usr/local/bin/hamid-controller
ENTRYPOINT ["/usr/local/bin/hamid-agent"]
