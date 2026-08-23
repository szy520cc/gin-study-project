#!/bin/bash

# 构建脚本

set -e

# 变量
APP_NAME="myproject"
BUILD_DIR="./build"
VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_TIME=$(date +"%Y-%m-%d %H:%M:%S")
GO_VERSION=$(go version | awk '{print $3}')

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

echo -e "${GREEN}Building ${APP_NAME}...${NC}"
echo "Version: ${VERSION}"
echo "Build Time: ${BUILD_TIME}"
echo "Go Version: ${GO_VERSION}"

# 创建构建目录
mkdir -p ${BUILD_DIR}

# 设置编译参数
LDFLAGS="-s -w -X main.Version=${VERSION} -X 'main.BuildTime=${BUILD_TIME}'"

# 编译
echo -e "${YELLOW}Compiling...${NC}"
CGO_ENABLED=0 go build -ldflags "${LDFLAGS}" -o ${BUILD_DIR}/${APP_NAME} ./cmd/server/main.go

# 检查编译结果
if [ $? -eq 0 ]; then
    echo -e "${GREEN}Build successful!${NC}"
    echo "Output: ${BUILD_DIR}/${APP_NAME}"
    ls -lh ${BUILD_DIR}/${APP_NAME}
else
    echo -e "${RED}Build failed!${NC}"
    exit 1
fi