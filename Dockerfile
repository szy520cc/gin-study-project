# 多阶段构建：编译产物与运行环境分离，最终镜像不含 Go 工具链与源码。
# 基础镜像版本必须 >= go.mod 里的 go 指令，否则编译直接失败。
FROM golang:1.25-alpine AS builder

WORKDIR /src

# 先只拷依赖描述文件，利用 Docker 层缓存：代码变更不会重新下载依赖
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# 版本号由构建方传入：docker build --build-arg VERSION=$(git describe --tags --always)
ARG VERSION=dev

# 静态编译：去掉调试信息与符号表，产物可在 alpine/scratch 中直接运行
# -trimpath 去掉绝对路径，避免泄露构建机目录结构
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X myproject/pkg/buildinfo.Version=${VERSION}" \
    -o /out/server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X myproject/pkg/buildinfo.Version=${VERSION}" \
    -o /out/migrate ./cmd/migrate

FROM alpine:3.19

# tzdata 用于容器内时区正确（配置里 DSN 使用 loc=Local）
# ca-certificates 用于访问 HTTPS 下游
RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /out/server /out/migrate /app/
COPY configs/config.yaml configs/config.prod.yaml /app/configs/

# 以非 root 运行
RUN mkdir -p /app/logs && chown -R appuser:appuser /app
USER appuser

ENV APP_ENV=prod TZ=Asia/Shanghai

# 8080 业务端口；admin（/metrics、/debug/pprof）默认只监听回环，不对外暴露。
# 需要被 Prometheus 跨机抓取时，通过 APP_ADMIN_ADDR=0.0.0.0:9090 放开，
# 并由安全组限制来源 —— 不要直接暴露到公网。
EXPOSE 8080

# 敏感配置由运行时注入，例如：
#   docker run -e APP_JWT_SECRET=... -e APP_DATABASE_PASSWORD=... image
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/livez || exit 1

ENTRYPOINT ["/app/server"]
