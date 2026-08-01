# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
RUN apk add --no-cache git build-base linux-headers
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-cni ./cmd/cni \
 && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-agent ./cmd/agent \
 && CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-s -w -X github.com/hamid/hamid-cni/pkg/version.Version=${VERSION}" -o /out/hamid-controller ./cmd/controller

FROM alpine:3.21
RUN apk add --no-cache iptables iproute2 ca-certificates
COPY --from=builder /out/hamid-cni /usr/local/bin/hamid-cni
COPY --from=builder /out/hamid-agent /usr/local/bin/hamid-agent
COPY --from=builder /out/hamid-controller /usr/local/bin/hamid-controller
ENTRYPOINT ["/usr/local/bin/hamid-agent"]
