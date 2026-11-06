#!/bin/bash
# ============================================
# AI Proxy Go - 独立启动脚本
# ============================================
# 用途: 在Proxy服务器上独立运行 Go Proxy（新加坡区域）
# 配置: 环境变量或 config.yaml
# ============================================

set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 获取脚本目录
if [[ -n "${BASH_SOURCE[0]:-}" ]]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    SCRIPT_DIR="$(pwd)"
fi
PROJECT_DIR="${SCRIPT_DIR}"

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}  AI Proxy Go (Singapore)${NC}"
echo -e "${YELLOW}========================================${NC}"

# 默认配置
export AI_PROXY_SERVER_PORT="${AI_PROXY_SERVER_PORT:-8080}"
export AI_PROXY_SERVER_MODE="${AI_PROXY_SERVER_MODE:-release}"
# ai-proxy-go 读取 REDIS_* 环境变量（非 AI_PROXY_REDIS_*）
export REDIS_HOST="${REDIS_HOST:-${AI_PROXY_REDIS_HOST:-localhost}}"
export REDIS_PORT="${REDIS_PORT:-${AI_PROXY_REDIS_PORT:-6379}}"
export REDIS_PASSWORD="${REDIS_PASSWORD:-}"
export AI_PROXY_PORTAL_URL="${AI_PROXY_PORTAL_URL:-http://localhost:3001}"
export PORTAL_URL="${PORTAL_URL:-${AI_PROXY_PORTAL_URL}}"
export AI_PROXY_PORTAL_SYNC_SECRET="${AI_PROXY_PORTAL_SYNC_SECRET:-dev-secret}"
export PORTAL_SYNC_SECRET="${PORTAL_SYNC_SECRET:-${AI_PROXY_PORTAL_SYNC_SECRET}}"
export AI_PROXY_RATE_LIMIT_ENABLED="${AI_PROXY_RATE_LIMIT_ENABLED:-true}"
export RATE_LIMIT_ENABLED="${RATE_LIMIT_ENABLED:-${AI_PROXY_RATE_LIMIT_ENABLED}}"
export AI_PROXY_RATE_LIMIT_DEFAULT_RPM="${AI_PROXY_RATE_LIMIT_DEFAULT_RPM:-60}"
export RATE_LIMIT_DEFAULT_RPM="${RATE_LIMIT_DEFAULT_RPM:-${AI_PROXY_RATE_LIMIT_DEFAULT_RPM}}"

# Simulate 模式配置
export AI_PROXY_SIMULATE_ENABLED="${AI_PROXY_SIMULATE_ENABLED:-false}"
export SIMULATE_ENABLED="${SIMULATE_ENABLED:-${AI_PROXY_SIMULATE_ENABLED}}"
export AI_PROXY_SIMULATE_LATENCY_MS="${AI_PROXY_SIMULATE_LATENCY_MS:-100}"
export SIMULATE_LATENCY_MS="${SIMULATE_LATENCY_MS:-${AI_PROXY_SIMULATE_LATENCY_MS}}"

# 显示配置
echo -e "${BLUE}[Config] 服务配置:${NC}"
echo -e "  端口: ${AI_PROXY_SERVER_PORT}"
echo -e "  模式: ${AI_PROXY_SERVER_MODE}"
echo -e "  Portal: ${PORTAL_URL}"
echo -e "  Redis: ${REDIS_HOST}:${REDIS_PORT}"
echo -e "  Simulate: ${SIMULATE_ENABLED}"

# 检查是否启用 simulate 模式
if [[ "${AI_PROXY_SIMULATE_ENABLED}" == "true" ]]; then
    echo -e "${YELLOW}⚠ Simulate 模式已启用 (延迟: ${AI_PROXY_SIMULATE_LATENCY_MS}ms)${NC}"
fi

# 检查 Redis
echo -e "${YELLOW}[1/2] 检查 Redis...${NC}"
if redis-cli -h "${REDIS_HOST}" -p "${REDIS_PORT}" ${REDIS_PASSWORD:+-a "${REDIS_PASSWORD}"} ping &>/dev/null; then
    echo -e "${GREEN}✓ Redis 已就绪${NC}"
else
    echo -e "${YELLOW}⚠ Redis 未运行，尝试启动...${NC}"
    if command -v docker &>/dev/null; then
        docker run -d --name ai-proxy-redis -p ${REDIS_PORT}:6379 redis:7-alpine 2>/dev/null || true
        sleep 2
    fi
fi

# 构建检查
echo -e "${YELLOW}[2/2] 检查 Go Proxy...${NC}"
cd "${PROJECT_DIR}"

if [[ ! -f "ai-proxy-go" ]] || [[ "*.go" -nt "ai-proxy-go" ]]; then
    echo -e "${YELLOW}构建 Go Proxy...${NC}"
    go build -o ai-proxy-go ./cmd/server
    echo -e "${GREEN}✓ 构建完成${NC}"
fi

# 检查是否已运行
if pgrep -f "./ai-proxy-go" > /dev/null; then
    echo -e "${YELLOW}Go Proxy 已在运行，正在重启...${NC}"
    pkill -f "./ai-proxy-go"
    sleep 2
fi

# 启动 Go Proxy
nohup ./ai-proxy-go > proxy.log 2>&1 &
PROXY_PID=$!

# 等待启动
for i in {1..30}; do
    if curl -s "http://localhost:${AI_PROXY_SERVER_PORT}/health" > /dev/null 2>&1; then
        echo -e "${GREEN}✓ Go Proxy 启动成功${NC}"
        echo -e "${BLUE}  PID: $PROXY_PID${NC}"
        echo -e "${BLUE}  地址: http://localhost:${AI_PROXY_SERVER_PORT}${NC}"
        echo -e "${BLUE}  端点: http://localhost:${AI_PROXY_SERVER_PORT}/v1/chat/completions${NC}"
        echo -e "${BLUE}  日志: ${PROJECT_DIR}/proxy.log${NC}"
        echo ""
        echo -e "${YELLOW}使用方法:${NC}"
        echo -e "  停止: kill $PROXY_PID"
        echo -e "  日志: tail -f ${PROJECT_DIR}/proxy.log"
        exit 0
    fi
    sleep 1
done

echo -e "${RED}✗ Go Proxy 启动失败${NC}"
tail -20 proxy.log
exit 1
