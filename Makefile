BINARY := cfstmgrd
PKG    := github.com/mia-clark/cfst-manager
VERSION ?= dev
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG)/internal/version.Number=$(VERSION) -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build run vet test clean docker tidy

build: ## 编译二进制到 bin/
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(BINARY) ./cmd/$(BINARY)

run: ## 本地运行（CFST_API_TOKEN=dev）
	CFST_API_TOKEN=dev CFST_DATA_DIR=./data go run ./cmd/$(BINARY) serve

vet: ## go vet
	go vet ./...

test: ## 运行单元测试
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf bin data

docker: ## 构建 Docker 镜像
	docker build -f deploy/Dockerfile -t cfst-manager:$(VERSION) --build-arg VERSION=$(VERSION) .
