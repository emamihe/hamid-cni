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

# Fetch standard CNI plugins (loopback is required by containerd/CRI).
FROM --platform=$BUILDPLATFORM alpine:3.21 AS cni-plugins
ARG TARGETARCH
ARG CNI_PLUGINS_VERSION=v1.6.2
RUN apk add --no-cache curl ca-certificates \
 && mkdir -p /plugins \
 && curl -fsSL "https://github.com/containernetworking/plugins/releases/download/${CNI_PLUGINS_VERSION}/cni-plugins-linux-${TARGETARCH}-${CNI_PLUGINS_VERSION}.tgz" \
    | tar -xz -C /plugins

FROM alpine:3.21
RUN apk add --no-cache iptables iproute2 ca-certificates curl \
 && mkdir -p /opt/cni/bin
COPY --from=builder /out/hamid-cni /usr/local/bin/hamid-cni
COPY --from=builder /out/hamid-agent /usr/local/bin/hamid-agent
COPY --from=builder /out/hamid-controller /usr/local/bin/hamid-controller
# Standard plugins required by containerd/CRI and our conflist chain.
COPY --from=cni-plugins /plugins/loopback /opt/cni/bin/loopback
COPY --from=cni-plugins /plugins/portmap /opt/cni/bin/portmap
COPY --from=cni-plugins /plugins/bandwidth /opt/cni/bin/bandwidth
COPY --from=cni-plugins /plugins/host-local /opt/cni/bin/host-local
COPY hack/install-cni.sh /usr/local/bin/install-cni.sh
RUN chmod 755 /usr/local/bin/install-cni.sh \
 && chmod 755 /opt/cni/bin/*
ENTRYPOINT ["/usr/local/bin/hamid-agent"]
