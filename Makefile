.PHONY: all build run dev clean test test-race test-cover lint fmt deps swagger migrate docker-build docker-run up down vuln help

# 变量定义
APP_NAME := myproject
BUILD_DIR := ./build
SERVER_PKG := ./cmd/server
MIGRATE_PKG := ./cmd/migrate
GO := go
GOFLAGS := -v
ENV ?= dev
# 注入版本信息，便于线上排查所跑的是哪个提交。
# 注意注入目标是 pkg/buildinfo.Version：-X 对不存在的符号会被静默忽略，
# 放在独立包里可以被 server/migrate 共用，也不会因为改动 main 而失效。
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X myproject/pkg/buildinfo.Version=$(VERSION)

all: build

# 编译（同时产出 server 与 migrate）
build:
	@echo "Building $(APP_NAME) ($(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(APP_NAME) $(SERVER_PKG)
	$(GO) build $(GOFLAGS) -trimpath -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/migrate $(MIGRATE_PKG)
	@echo "Build complete: $(BUILD_DIR)/"

# 运行（make run ENV=prod 可切换环境）
run:
	$(GO) run $(SERVER_PKG) -env=$(ENV)

# 热重载运行（需要 air: go install github.com/cosmtrek/air@latest）
dev:
	air -c .air.toml

clean:
	@rm -rf $(BUILD_DIR) tmp coverage.out coverage.html
	@echo "Clean complete"

test:
	$(GO) test ./... -count=1

# 竞态检测：日志轮转、限流桶等并发路径必须跑这个
test-race:
	$(GO) test -race ./... -count=1

test-cover:
	$(GO) test -coverprofile=coverage.out ./... -count=1
	$(GO) tool cover -html=coverage.out -o coverage.html

# 静态检查（需要 golangci-lint）
lint:
	golangci-lint run ./...

# 依赖漏洞扫描（需要 govulncheck: go install golang.org/x/vuln/cmd/govulncheck@latest）
vuln:
	govulncheck ./...

# 本地依赖：起 MySQL + Redis
up:
	docker compose up -d

down:
	docker compose down

# 编译期检查 + 格式化，提交前最低要求
check: fmt vet test

vet:
	$(GO) vet ./...

fmt:
	$(GO) fmt ./...

deps:
	$(GO) mod download
	$(GO) mod tidy

# 生成 Swagger 文档（需要 swag: go install github.com/swaggo/swag/cmd/swag@latest）
swagger:
	swag init -g $(SERVER_PKG)/main.go -o ./docs/swagger

# 数据库迁移（make migrate ENV=dev）
migrate:
	$(GO) run $(MIGRATE_PKG) -env=$(ENV)

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(APP_NAME):$(VERSION) -t $(APP_NAME):latest .

# 敏感配置通过环境变量注入，不打进镜像
docker-run:
	docker run --rm -p 8080:8080 \
		-e APP_ENV=prod \
		-e APP_JWT_SECRET=$${APP_JWT_SECRET} \
		-e APP_DATABASE_HOST=$${APP_DATABASE_HOST} \
		-e APP_DATABASE_PASSWORD=$${APP_DATABASE_PASSWORD} \
		$(APP_NAME):latest

help:
	@echo "Usage: make [target] [ENV=dev|test|prod]"
	@echo ""
	@echo "Targets:"
	@echo "  build        编译 server 与 migrate"
	@echo "  run          运行服务（ENV 指定环境）"
	@echo "  dev          热重载运行（需要 air）"
	@echo "  up / down    启停本地依赖（MySQL + Redis）"
	@echo "  test         运行测试"
	@echo "  test-race    竞态检测"
	@echo "  test-cover   测试覆盖率报告"
	@echo "  check        fmt + vet + test"
	@echo "  lint         静态检查（需要 golangci-lint）"
	@echo "  vuln         依赖漏洞扫描（需要 govulncheck）"
	@echo "  migrate      执行数据库迁移"
	@echo "  swagger      生成 API 文档（需要 swag）"
	@echo "  docker-build 构建镜像"
	@echo "  docker-run   运行容器"
	@echo "  clean        清理构建产物"
