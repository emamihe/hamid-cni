IMAGE ?= emamihe/hamid-cni
VERSION ?= 0.1.0
GOOS ?= linux
GOARCH ?= amd64
LDFLAGS := -s -w -X github.com/hamid/hamid-cni/pkg/version.Version=$(VERSION)

.PHONY: build build-native test tidy docker-build helm-lint fmt vet

tidy:
	go mod tidy

fmt:
	gofmt -w $(shell find . -name '*.go' -not -path './vendor/*')

vet:
	GOOS=$(GOOS) go vet ./...

build: tidy
	mkdir -p bin
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hamid-cni ./cmd/cni
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hamid-agent ./cmd/agent
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hamid-controller ./cmd/controller

# Build for the local OS (controller only useful on non-Linux).
build-native:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/hamid-controller ./cmd/controller

test:
	go test ./pkg/ipam/... ./pkg/config/... ./pkg/agentapi/... ./pkg/agent/ -count=1

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

helm-lint:
	helm lint deploy/helm/hamid-cni
