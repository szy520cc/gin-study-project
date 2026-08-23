#!/bin/bash

# 部署脚本

set -e

# 变量
APP_NAME="myproject"
DEPLOY_DIR="/opt/${APP_NAME}"
SERVICE_NAME="${APP_NAME}"
# 部署环境，决定加载哪个 configs/config.<env>.yaml
APP_ENV="${APP_ENV:-prod}"
# 运行用户：不用 root，与 Dockerfile 里的 USER appuser 保持同一基线
RUN_USER="${RUN_USER:-myproject}"
# 凭据文件：0600 仅 root 可读，由 systemd 的 EnvironmentFile 加载
ENV_FILE="/etc/${APP_NAME}.env"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

# 检查参数
if [ -z "$1" ]; then
    echo -e "${YELLOW}Usage: $0 <binary_file>${NC}"
    echo "Example: $0 ./build/myproject"
    exit 1
fi

BINARY_FILE=$1

# 检查文件是否存在
if [ ! -f "${BINARY_FILE}" ]; then
    echo -e "${RED}Error: Binary file not found: ${BINARY_FILE}${NC}"
    exit 1
fi

# 前置检查：所有依赖必须在「停服务」之前确认齐全。
# 脚本开头是 set -e，任何一步失败就直接退出 —— 如果这些检查放在
# 停服务、换二进制之后，失败会把服务停在半截且没有回滚。
if [ ! -f "configs/config.yaml" ]; then
    echo -e "${RED}Error: 未找到 configs/config.yaml（请在项目根目录执行本脚本）${NC}"
    exit 1
fi
if [ ! -f "configs/config.${APP_ENV}.yaml" ]; then
    echo -e "${RED}Error: 未找到 configs/config.${APP_ENV}.yaml${NC}"
    exit 1
fi
: "${APP_JWT_SECRET:?必须注入 APP_JWT_SECRET（长度 >= 32），否则服务启动时会被配置校验拒绝}"
: "${APP_DATABASE_HOST:?必须注入 APP_DATABASE_HOST}"
: "${APP_DATABASE_USERNAME:?必须注入 APP_DATABASE_USERNAME}"
: "${APP_DATABASE_PASSWORD:?必须注入 APP_DATABASE_PASSWORD}"
: "${APP_DATABASE_DBNAME:?必须注入 APP_DATABASE_DBNAME}"

echo -e "${GREEN}Deploying ${APP_NAME}...${NC}"

# 创建部署目录
echo "Creating deploy directory..."
sudo mkdir -p ${DEPLOY_DIR}
sudo mkdir -p ${DEPLOY_DIR}/logs
# 目录名必须是 configs：程序默认 -config=./configs
sudo mkdir -p ${DEPLOY_DIR}/configs
# 运行用户不存在则创建（无登录权限的系统账号）
id -u "${RUN_USER}" >/dev/null 2>&1 || sudo useradd --system --no-create-home --shell /usr/sbin/nologin "${RUN_USER}"

# 停止服务
echo "Stopping service..."
sudo systemctl stop ${SERVICE_NAME} 2>/dev/null || true

# 备份旧版本
if [ -f "${DEPLOY_DIR}/${APP_NAME}" ]; then
    echo "Backing up old version..."
    sudo mv ${DEPLOY_DIR}/${APP_NAME} ${DEPLOY_DIR}/${APP_NAME}.bak
fi

# 复制新版本
echo "Copying new binary..."
sudo cp ${BINARY_FILE} ${DEPLOY_DIR}/${APP_NAME}
sudo chmod +x ${DEPLOY_DIR}/${APP_NAME}

# 复制配置文件（不含 *.local.yaml：那是本机凭据，且优先级高于环境配置）
echo "Copying config files..."
sudo cp configs/config.yaml configs/config.${APP_ENV}.yaml ${DEPLOY_DIR}/configs/
sudo chown -R ${RUN_USER}:${RUN_USER} ${DEPLOY_DIR}

# 凭据写进独立的 env 文件，0600 且仅 root 可读。
# 不能写进 unit 文件：sudo tee 产出的 unit 是 0644，机器上任何本地用户
# systemctl cat / cat 就能读到 JWT secret 与库密码，拿到 secret 等于可伪造任意用户 token。
echo "Writing credentials to ${ENV_FILE} (0600, root only)..."
sudo install -m 0600 -o root -g root /dev/null ${ENV_FILE}
sudo tee ${ENV_FILE} > /dev/null <<EOF
APP_ENV=${APP_ENV}
APP_JWT_SECRET=${APP_JWT_SECRET}
APP_DATABASE_HOST=${APP_DATABASE_HOST}
APP_DATABASE_USERNAME=${APP_DATABASE_USERNAME}
APP_DATABASE_PASSWORD=${APP_DATABASE_PASSWORD}
APP_DATABASE_DBNAME=${APP_DATABASE_DBNAME}
EOF

# 创建 systemd 服务文件
echo "Creating systemd service..."
sudo tee /etc/systemd/system/${SERVICE_NAME}.service > /dev/null <<EOF
[Unit]
Description=${APP_NAME} Service
After=network.target mysql.service redis.service

[Service]
Type=simple
User=${RUN_USER}
WorkingDirectory=${DEPLOY_DIR}
ExecStart=${DEPLOY_DIR}/${APP_NAME} -env ${APP_ENV} -config ${DEPLOY_DIR}/configs
EnvironmentFile=${ENV_FILE}
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true
Restart=always
RestartSec=5
StandardOutput=append:${DEPLOY_DIR}/logs/stdout.log
StandardError=append:${DEPLOY_DIR}/logs/stderr.log

[Install]
WantedBy=multi-user.target
EOF

# 重新加载 systemd
sudo systemctl daemon-reload

# 启动服务
echo "Starting service..."
sudo systemctl start ${SERVICE_NAME}
sudo systemctl enable ${SERVICE_NAME}

# 检查状态
sleep 2
if sudo systemctl is-active --quiet ${SERVICE_NAME}; then
    echo -e "${GREEN}Deploy successful!${NC}"
    sudo systemctl status ${SERVICE_NAME}
else
    echo -e "${RED}Deploy failed! Check logs:${NC}"
    sudo journalctl -u ${SERVICE_NAME} -n 20
    exit 1
fi