BINARY := cfstmgrd
PKG    := github.com/nue-mic/cfst-manager
VERSION ?= dev
BUILD_DATE := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(PKG)/internal/version.Number=$(VERSION) -X $(PKG)/internal/version.BuildDate=$(BUILD_DATE)

.PHONY: build run vet test clean docker tidy ipk

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

# OpenWrt 单个 all 架构 ipk（壳子包，装时由 cfstmgrd-fetch 按 CPU 拉二进制）。
# 需 nfpm：go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
# VERSION 决定 fetcher 默认拉取的二进制版本，发布时由 CI 注入真实版本号。
ipk: ## 构建 OpenWrt all 架构 ipk（需 nfpm + VERSION）
	./openwrt/build-ipk.sh --version $(VERSION) --out dist-ipk
