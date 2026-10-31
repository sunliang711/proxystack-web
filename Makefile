VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w \
	-X github.com/eagle/proxystack-web/internal/version.Version=$(VERSION) \
	-X github.com/eagle/proxystack-web/internal/version.Commit=$(COMMIT) \
	-X github.com/eagle/proxystack-web/internal/version.Date=$(DATE)
NPM ?= npm

.PHONY: all web gen build test dist dev-init dev-serve clean

all: web build

web:
	cd web && $(NPM) ci && $(NPM) run build

# 重新生成 sqlc 查询代码和前端 API 类型。
gen:
	sqlc generate
	cd web && $(NPM) run gen:api

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/proxystack-web ./cmd/proxystack-web

test:
	go test ./...
	cd web && $(NPM) run typecheck

# 发布产物：裸二进制供 install.sh 和面板自更新下载，tar.gz 带 deploy 目录用于离线安装。
dist: web
	rm -rf dist
	mkdir -p dist
	set -e; for arch in amd64 arm64; do \
		binary=proxystack-web-linux-$$arch; \
		package=proxystack-web-$(VERSION)-linux-$$arch; \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$binary ./cmd/proxystack-web; \
		mkdir dist/$$package; \
		cp dist/$$binary dist/$$package/proxystack-web; \
		cp -R deploy dist/$$package/deploy; \
		COPYFILE_DISABLE=1 tar -C dist -czf dist/$$package.tar.gz $$package; \
		rm -rf dist/$$package; \
		(cd dist && shasum -a 256 $$binary > $$binary.sha256 && shasum -a 256 $$package.tar.gz > $$package.tar.gz.sha256); \
	done

# 开发模式：实例目录 .dev，面板走 HTTP 监听 127.0.0.1:9090。
dev-init:
	go run ./cmd/proxystack-web --base-dir .dev init --tls proxy --listen 127.0.0.1 --port 9090

dev-serve:
	go run ./cmd/proxystack-web --base-dir .dev serve --dev

clean:
	rm -rf bin dist web/dist/assets web/dist/index.html
