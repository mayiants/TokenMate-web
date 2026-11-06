#!/bin/bash
# ============================================
# AI Platform Go - 一键启动脚本
# ============================================
# Go Portal + Go Proxy
#
# 使用方法:
#   ./start.sh                         # 交互式选择模式
#   ./start.sh mvp                     # MVP/生产模式 (Portal + Proxy)
#   ./start.sh simulate                # 模拟模式 (Portal + Proxy)
#   ./start.sh portal                  # 仅启动 Portal
#   ./start.sh proxy                   # 仅启动 Proxy (连接远端 Portal)
#   ./start.sh stop                    # 停止所有服务
#   ./start.sh stop-portal             # 停止 Portal
#   ./start.sh stop-proxy              # 停止 Proxy
#   ./start.sh status                  # 查看状态
#   ./start.sh build                   # 构建所有服务
#   ./start.sh build-portal            # 仅构建 Portal
#   ./start.sh build-proxy             # 仅构建 Proxy
#   ./start.sh clean                   # 清理旧配置
#   ./start.sh clean-all               # 完全重置
#   ./start.sh rebuild                 # 重新构建并启动 (默认先自动快照)
#   ./start.sh snapshot                # 手动快照数据库到 backups/ (保留)
#   ./start.sh restore <dump> [db]     # 恢复快照到新库名
#
# 分布式部署:
#   Portal 服务器: ./start.sh portal
#   Proxy 服务器:  ./start.sh proxy --portal-url=http://portal-server:3001
# ============================================

set -e

# ============================================
# 目录检测
# ============================================
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PORTAL_DIR="${SCRIPT_DIR}"

# Proxy 目录检测 (优先级: 环境变量 > 同级目录 > 子目录)
detect_proxy_dir() {
    if [[ -n "${AI_PROXY_GO_DIR:-}" ]] && [[ -d "${AI_PROXY_GO_DIR}" ]]; then
        echo "${AI_PROXY_GO_DIR}"
        return
    fi
    local sibling_dir="$(dirname "${SCRIPT_DIR}")/ai-proxy-go"
    if [[ -d "${sibling_dir}" ]]; then
        echo "${sibling_dir}"
        return
    fi
    if [[ -d "${SCRIPT_DIR}/ai-proxy-go" ]]; then
        echo "${SCRIPT_DIR}/ai-proxy-go"
        return
    fi
    echo ""
}

PROXY_DIR=$(detect_proxy_dir)

# ============================================
# 系统环境动态检测
# ============================================
detect_system_info() {
    # 检测主机 IP (排除 127.0.0.1 和 docker 网桥)
    DETECTED_HOST_IP=$(ip -4 route get 1 2>/dev/null | head -1 | awk '{print $7}' | head -1)
    if [[ -z "${DETECTED_HOST_IP}" ]] || [[ "${DETECTED_HOST_IP}" == "127.0.0.1" ]]; then
        DETECTED_HOST_IP=$(hostname -I 2>/dev/null | awk '{print $1}')
    fi
    if [[ -z "${DETECTED_HOST_IP}" ]]; then
        DETECTED_HOST_IP="127.0.0.1"
    fi

    # 检测可用端口 (依次检测 3001/8080/5432/6379)
    detect_port() {
        local default_port="$1"
        if ss -tlnp 2>/dev/null | grep -q ":${default_port} "; then
            # 端口被占用，尝试 +1 递增
            local port="$((default_port + 1))"
            while ss -tlnp 2>/dev/null | grep -q ":${port} " && [[ "$port" -lt "$((default_port + 100))" ]]; do
                port=$((port + 1))
            done
            echo "$port"
        else
            echo "$default_port"
        fi
    }

    DETECTED_PORTAL_PORT=$(detect_port 3001)
    DETECTED_PROXY_PORT=$(detect_port 8080)
    DETECTED_PG_PORT=$(detect_port 5432)
    DETECTED_REDIS_PORT=$(detect_port 6379)
    DETECTED_PROXY_REDIS_PORT=$(detect_port 6380)

    # 从已运行的容器获取实际宿主机端口映射
    get_container_host_port() {
        local container_name="$1"
        local container_port="$2"
        local host_port
        host_port=$(docker port "${container_name}" "${container_port}/tcp" 2>/dev/null | head -1 | awk -F: '{print $NF}')
        if [[ -n "${host_port}" ]] && [[ "${host_port}" =~ ^[0-9]+$ ]]; then
            echo "${host_port}"
        fi
    }

    local real_portal_port real_proxy_port real_pg_port real_redis_port real_proxy_redis_port
    real_portal_port=$(get_container_host_port "ai-portal-go" 3001)
    real_proxy_port=$(get_container_host_port "ai-proxy-go" 8080)
    real_pg_port=$(get_container_host_port "ai-portal-postgres" 5432)
    real_redis_port=$(get_container_host_port "ai-portal-redis" 6379)
    real_proxy_redis_port=$(get_container_host_port "ai-proxy-redis" 6379)

    # 如果容器已在运行，使用实际映射的端口覆盖检测值
    if [[ -n "${real_portal_port}" ]]; then
        DETECTED_PORTAL_PORT="${real_portal_port}"
    fi
    if [[ -n "${real_proxy_port}" ]]; then
        DETECTED_PROXY_PORT="${real_proxy_port}"
    fi
    if [[ -n "${real_pg_port}" ]]; then
        DETECTED_PG_PORT="${real_pg_port}"
    fi
    if [[ -n "${real_redis_port}" ]]; then
        DETECTED_REDIS_PORT="${real_redis_port}"
    fi
    if [[ -n "${real_proxy_redis_port}" ]]; then
        DETECTED_PROXY_REDIS_PORT="${real_proxy_redis_port}"
    fi

    # 检测 Docker 数据卷实际路径
    detect_docker_volume_path() {
        local volume_name="$1"
        # 尝试从 docker inspect 获取
        local path
        path=$(docker volume inspect "${volume_name}" --format '{{.Mountpoint}}' 2>/dev/null || true)
        if [[ -n "${path}" ]] && [[ -d "${path}" ]]; then
            echo "${path}"
            return
        fi
        # 尝试常见路径
        local candidates=(
            "/var/lib/docker/volumes/${volume_name}/_data"
            "/mnt/workspace/docker/volumes/${volume_name}"
            "/opt/docker/volumes/${volume_name}/_data"
        )
        for candidate in "${candidates[@]}"; do
            if [[ -d "${candidate}" ]]; then
                echo "${candidate}"
                return
            fi
        done
        # 返回默认路径 (docker compose down -v 会自动清理)
        echo ""
    }

    DETECTED_PG_VOLUME=$(detect_docker_volume_path "ai-portal-go_postgres-data")
    DETECTED_REDIS_VOLUME=$(detect_docker_volume_path "ai-portal-go_redis-data")

    # 检测外部访问基础 URL (用于邮件链接、邀请链接等)
    # 必须是公网域名，不能是 IP 地址。优先级：命令行参数 > 环境变量 APP_BASE_URL > EXTERNAL_URL > 空
    if [[ -n "${APP_BASE_URL_ARG:-}" ]]; then
        DETECTED_APP_BASE_URL="${APP_BASE_URL_ARG}"
    elif [[ -n "${APP_BASE_URL:-}" ]]; then
        DETECTED_APP_BASE_URL="${APP_BASE_URL}"
    elif [[ -n "${EXTERNAL_URL:-}" ]]; then
        DETECTED_APP_BASE_URL="${EXTERNAL_URL}"
    else
        DETECTED_APP_BASE_URL=""
    fi

    # 检测 Portal URL (用于 Proxy 连接)
    # 优先级: 命令行参数 > 环境变量 > 单机部署(容器名) > 本地 IP
    if [[ -n "${PORTAL_URL_ARG:-}" ]]; then
        DETECTED_PORTAL_URL="${PORTAL_URL_ARG}"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q "ai-portal-go"; then
        DETECTED_PORTAL_URL="http://ai-portal-go:${DETECTED_PORTAL_PORT}"
    else
        DETECTED_PORTAL_URL="http://127.0.0.1:${DETECTED_PORTAL_PORT}"
    fi

    # 检测是否为 root 用户
    DETECTED_IS_ROOT=false
    if [[ "$(id -u)" -eq 0 ]]; then
        DETECTED_IS_ROOT=true
    fi
}

# ============================================
# 颜色定义
# ============================================
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m'

# ============================================
# 全局变量
# ============================================
RUN_MODE="${1:-}"
shift 2>/dev/null || true

# 解析额外参数
PORTAL_URL=""
PROXY_SECRET=""
JWT_SECRET_ARG=""
REDIS_PASSWORD_ARG=""
PROXY_REDIS_PASSWORD_ARG=""
APP_BASE_URL_ARG=""
POSITIONAL_ARGS=()
while [[ $# -gt 0 ]]; do
    case "$1" in
        --portal-url=*)
            PORTAL_URL="${1#*=}"
            shift
            ;;
        --proxy-secret=*)
            PROXY_SECRET="${1#*=}"
            shift
            ;;
        --jwt-secret=*)
            JWT_SECRET_ARG="${1#*=}"
            shift
            ;;
        --redis-password=*)
            REDIS_PASSWORD_ARG="${1#*=}"
            shift
            ;;
        --proxy-redis-password=*)
            PROXY_REDIS_PASSWORD_ARG="${1#*=}"
            shift
            ;;
        --app-base-url=*)
            APP_BASE_URL_ARG="${1#*=}"
            shift
            ;;
        *)
            POSITIONAL_ARGS+=("$1")
            shift
            ;;
    esac
done

# Docker Compose 命令检测
if docker compose version &>/dev/null; then
    DOCKER_COMPOSE_CMD="docker compose"
elif docker-compose version &>/dev/null; then
    DOCKER_COMPOSE_CMD="docker-compose"
else
    echo -e "${RED}✗ 错误: 未找到 Docker Compose${NC}"
    exit 1
fi

# ============================================
# 显示信息
# ============================================
show_info() {
    echo -e "${CYAN}========================================${NC}"
    echo -e "${CYAN}  AI Platform Go${NC}"
    echo -e "${CYAN}========================================${NC}"
    echo -e "${BLUE}Portal 目录: ${PORTAL_DIR}${NC}"
    if [[ -n "${PROXY_DIR}" ]]; then
        echo -e "${BLUE}Proxy 目录:  ${PROXY_DIR}${NC}"
    else
        echo -e "${YELLOW}Proxy 目录:  未检测到${NC}"
    fi
    echo -e "${BLUE}主机 IP:     ${DETECTED_HOST_IP}${NC}"
    echo -e "${BLUE}Portal 端口: ${DETECTED_PORTAL_PORT}${NC}"
    echo -e "${BLUE}Proxy 端口:  ${DETECTED_PROXY_PORT}${NC}"
    echo -e "${BLUE}Portal Redis 端口: ${DETECTED_REDIS_PORT}${NC}"
    echo -e "${BLUE}Proxy Redis 端口:  ${DETECTED_PROXY_REDIS_PORT:-6380}${NC}"
    echo ""
}

# ============================================
# 选择运行模式
# ============================================
select_mode() {
    if [[ -n "$RUN_MODE" ]]; then
        case "$RUN_MODE" in
            mvp|prod|production|simulate|test|demo|portal|proxy)
                return
                ;;
            stop|stop-portal|stop-proxy|status|logs|clean|clean-all|build|build-portal|build-proxy|rebuild)
                return
                ;;
        esac
    fi

    echo "" >&2
    echo -e "${YELLOW}========================================${NC}" >&2
    echo -e "${YELLOW}  请选择运行模式${NC}" >&2
    echo -e "${YELLOW}========================================${NC}" >&2
    echo "" >&2
    echo -e "${GREEN}1) MVP/生产模式${NC}" >&2
    echo "   - Portal + Proxy 完整部署" >&2
    echo "   - 使用真实 API Key" >&2
    echo "" >&2
    echo -e "${BLUE}2) 模拟模式${NC}" >&2
    echo "   - Portal + Proxy 完整部署" >&2
    echo "   - 不消耗真实 API Key" >&2
    echo "" >&2
    echo -e "${CYAN}3) 仅 Portal${NC}" >&2
    echo "   - 适合分布式部署" >&2
    echo "" >&2
    echo -e "${CYAN}4) 仅 Proxy${NC}" >&2
    echo "   - 连接远端 Portal" >&2
    echo "" >&2

    read -p "请选择 [1-4, 默认2]: " mode_choice

    case "$mode_choice" in
        1) RUN_MODE="mvp" ;;
        3) RUN_MODE="portal" ;;
        4) RUN_MODE="proxy" ;;
        *) RUN_MODE="simulate" ;;
    esac
}

# ============================================
# 检查配置
# ============================================
check_config() {
    local config_file="${PORTAL_DIR}/.env.go-platform"

    # 命令行显式指定了 APP_BASE_URL：即使配置文件已存在也要更新它
    if [[ -n "${APP_BASE_URL_ARG:-}" ]] && [[ -f "$config_file" ]]; then
        if grep -q '^APP_BASE_URL=' "$config_file"; then
            sed -i "s|^APP_BASE_URL=.*|APP_BASE_URL=${APP_BASE_URL_ARG}|" "$config_file"
        else
            echo "APP_BASE_URL=${APP_BASE_URL_ARG}" >> "$config_file"
        fi
        echo -e "${GREEN}✓ 已更新 APP_BASE_URL=${APP_BASE_URL_ARG}${NC}"
        # 让后续 detect_system_info 的结果与文件保持一致
        DETECTED_APP_BASE_URL="${APP_BASE_URL_ARG}"
    fi

    if [[ -f "$config_file" ]]; then
        ensure_db_vars "$config_file"
        ensure_e2e_env_vars "$config_file"
        ensure_build_mirror_vars "$config_file"
        ensure_proxy_runtime_vars "$config_file"
        echo -e "${GREEN}✓ 配置文件已存在${NC}"
        return
    fi

    echo -e "${YELLOW}=== 首次配置 ===${NC}"
    echo ""

    echo -e "${YELLOW}生成随机密钥...${NC}"

    # 从 .env 加载已有配置（如 SMTP）
    if [[ -f "${PORTAL_DIR}/.env" ]]; then
        set -a
        source "${PORTAL_DIR}/.env"
        set +a
    fi

    # 优先复用 .env 中的密码，避免重新生成导致与数据卷不一致
    local db_password=$(grep '^DB_PASSWORD=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    # 数据库连接参数（DB_HOST=postgres 表示本地 Docker 容器；填 Aurora/外部端点即外部模式）
    local db_host=$(grep '^DB_HOST=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "postgres")
    local db_port=$(grep '^DB_PORT=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "5432")
    local db_name=$(grep '^DB_NAME=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "ai_proxy_portal")
    local db_user=$(grep '^DB_USER=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "postgres")
    local redis_password=$(grep '^REDIS_PASSWORD=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    # Proxy Redis 默认与 Portal Redis 使用相同密码，便于本地调试；生产建议独立设置
    local proxy_redis_password=$(grep '^PROXY_REDIS_PASSWORD=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || grep '^REDIS_PASSWORD=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    local jwt_secret=$(grep '^JWT_SECRET=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    local encryption_key=$(grep '^ENCRYPTION_KEY=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    local proxy_sync_secret=$(grep '^PROXY_SYNC_SECRET=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || openssl rand -hex 32)
    local allowed_origins=$(grep '^ALLOWED_ORIGINS=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "")
    # Proxy 实例标识（多实例/多区域部署时建议设置，如 proxy-sg-01 / sg）
    local server_id=$(grep '^SERVER_ID=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "")
    local server_region=$(grep '^SERVER_REGION=' "${PORTAL_DIR}/.env" 2>/dev/null | cut -d= -f2 || echo "")

    # 如果未提供 APP_BASE_URL，回退到 .env 中的 APP_BASE_URL；仍为空则留作占位
    local app_base_url_value="${DETECTED_APP_BASE_URL:-${APP_BASE_URL:-}}"

    # 创建默认配置
    cat > "$config_file" << EOF
# ============================================
# AI Platform Go 环境变量配置
# ============================================
# 生成时间: $(date '+%Y-%m-%d %H:%M:%S')
# 主机 IP: ${DETECTED_HOST_IP}

# 数据库配置
# DB_HOST=postgres 表示使用本地 Docker 容器；填 Aurora/外部端点即切换为外部模式（不再拉起本地 PG）
DB_HOST=${db_host}
DB_PORT=${db_port}
DB_NAME=${db_name}
DB_USER=${db_user}
DB_PASSWORD=${db_password}
# 本地调试可保持 disable；跨 AZ / 公网 / 生产必须改为 require / verify-full
DB_SSLMODE=${DB_SSLMODE:-disable}
DB_SSL_ROOT_CERT=${DB_SSL_ROOT_CERT:-}

# Redis 配置 (Portal 自身使用)
REDIS_PASSWORD=${redis_password}
# 本地调试 false；生产必须 true
REDIS_TLS=${REDIS_TLS:-false}
REDIS_TLS_SKIP_VERIFY=${REDIS_TLS_SKIP_VERIFY:-false}

# Proxy Redis 配置（ai-proxy-go 独立使用，生产环境必须与 Portal Redis 分离）
PROXY_REDIS_HOST=redis-proxy
PROXY_REDIS_PORT=6379
PROXY_REDIS_HOST_PORT=${DETECTED_PROXY_REDIS_PORT:-6380}
PROXY_REDIS_PASSWORD=${proxy_redis_password}
PROXY_REDIS_DB=0

# JWT 安全配置
JWT_SECRET=${jwt_secret}

# 默认管理员配置
# 生产/mvp 模式必须在首次启动前覆盖 ADMIN_PASSWORD；下方默认值仅用于本地 simulate/test，会被生产校验拒绝
ADMIN_EMAIL=${ADMIN_EMAIL:-admin@example.com}
ADMIN_PASSWORD=${ADMIN_PASSWORD:-Admin@123456!}
ADMIN_NAME=${ADMIN_NAME:-Admin}

# Admin 模式 (development / production)
ADMIN_MODE=production

# 加密密钥 (≥32 字符，用于上游 API Key AES-256-GCM 加密)
ENCRYPTION_KEY=${encryption_key}

# Proxy 同步密钥 (≥32 字符，用于 Proxy ↔ Portal 通信签名)
PROXY_SYNC_SECRET=${proxy_sync_secret}

# Admin Secret (≥32 字符，用于 Proxy 管理接口认证)
ADMIN_SECRET=${proxy_sync_secret}

# Proxy 实例标识（多实例/多区域部署时设置，如 SERVER_ID=proxy-sg-01, SERVER_REGION=sg）
# 留空时 Proxy 不会向 Portal 上报固定实例 ID，建议生产环境显式设置
SERVER_ID=${server_id}
SERVER_REGION=${server_region}

# Portal URL (用于 Proxy 连接，动态检测)
PORTAL_URL=${DETECTED_PORTAL_URL}

# 外部访问基础 URL (用于 cookie domain、邮件链接等)
# 必须是公网域名，例如 https://portal.woodgit.com；留空时应用会回退到示例域名，请尽快修改
APP_BASE_URL=${app_base_url_value}

# CORS 允许的来源 (留空 = 允许所有，用于反向代理/同站场景)
ALLOWED_ORIGINS=${allowed_origins}

# 端口配置 (动态检测，避免冲突)
PORTAL_PORT=${DETECTED_PORTAL_PORT}
PROXY_PORT=${DETECTED_PROXY_PORT}
POSTGRES_PORT=${DETECTED_PG_PORT}
REDIS_PORT=${DETECTED_REDIS_PORT}
PROXY_REDIS_HOST_PORT=${DETECTED_PROXY_REDIS_PORT:-6380}

# SMTP 配置 (从 .env 继承，不存在则留空)
SMTP_HOST=${SMTP_HOST:-}
SMTP_PORT=${SMTP_PORT:-465}
SMTP_SECURE=${SMTP_SECURE:-true}
SMTP_USER=${SMTP_USER:-}
SMTP_PASSWORD=${SMTP_PASSWORD:-}
SMTP_FROM=${SMTP_FROM:-}

# 文件存储配置（企业申请资质文件）
FILE_STORAGE_PROVIDER=${FILE_STORAGE_PROVIDER:-local}
FILE_STORAGE_LOCAL_ROOT=${FILE_STORAGE_LOCAL_ROOT:-./uploads}
FILE_STORAGE_ENDPOINT=${FILE_STORAGE_ENDPOINT:-}
FILE_STORAGE_REGION=${FILE_STORAGE_REGION:-ap-southeast-1}
FILE_STORAGE_BUCKET=${FILE_STORAGE_BUCKET:-ai-portal-uploads}
FILE_STORAGE_ACCESS_KEY=${FILE_STORAGE_ACCESS_KEY:-}
FILE_STORAGE_SECRET_KEY=${FILE_STORAGE_SECRET_KEY:-}
FILE_STORAGE_USE_SSL=${FILE_STORAGE_USE_SSL:-true}
FILE_STORAGE_PATH_PREFIX=${FILE_STORAGE_PATH_PREFIX:-enterprise-apps}
FILE_MAX_SIZE_MB=${FILE_MAX_SIZE_MB:-10}
FILE_MAX_TOTAL_MB_PER_APPLICATION=${FILE_MAX_TOTAL_MB_PER_APPLICATION:-50}
FILE_ALLOWED_TYPES=${FILE_ALLOWED_TYPES:-application/pdf,image/jpeg,image/png}

# E2E 测试账号 (从 .env 继承，不存在则使用默认值)
# 仅在 SEED_E2E_ACCOUNTS=true 时 Portal 启动才会写入数据库。
# 默认 false：生产库的长期 e2e 账号由 e2e/e2e-accounts.sh 在测试前后
# 切换 status，启动种子只应在显式开启时用于全新环境，避免账号被删除后
# 随重启以默认密码重新激活。
SEED_E2E_ACCOUNTS=${SEED_E2E_ACCOUNTS:-false}
E2E_ADMIN_EMAIL=${E2E_ADMIN_EMAIL:-e2e-admin@example.com}
E2E_ADMIN_PASSWORD=${E2E_ADMIN_PASSWORD:-E2E_AdminPass123!}
E2E_USER_EMAIL=${E2E_USER_EMAIL:-e2e-user@example.com}
E2E_USER_PASSWORD=${E2E_USER_PASSWORD:-E2E_TestPass123!}
E2E_ENTERPRISE_EMAIL=${E2E_ENTERPRISE_EMAIL:-e2e-enterprise@example.com}
E2E_ENTERPRISE_PASSWORD=${E2E_ENTERPRISE_PASSWORD:-E2E_TestPass123!}

# 可选的硬编码测试企业 (默认不启用；仅在 SEED_TEST_ENTERPRISE=true 时写入数据库)
SEED_TEST_ENTERPRISE=${SEED_TEST_ENTERPRISE:-false}
TEST_ENTERPRISE_NAME="${TEST_ENTERPRISE_NAME:-Test Enterprise}"
TEST_ENTERPRISE_ADMIN_EMAIL=${TEST_ENTERPRISE_ADMIN_EMAIL:-ent-admin@test.local}
TEST_ENTERPRISE_MEMBER_EMAIL=${TEST_ENTERPRISE_MEMBER_EMAIL:-ent-member@test.local}
TEST_ENTERPRISE_PASSWORD=${TEST_ENTERPRISE_PASSWORD:-TestPass123!}

# Docker 构建镜像/代理配置（默认官方源；中国网络可改为 mirrors.aliyun.com / https://goproxy.cn,direct）
APK_MIRROR=${APK_MIRROR:-dl-cdn.alpinelinux.org}
GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
EOF

    echo -e "${GREEN}✓ 已创建配置文件: ${config_file}${NC}"
}

# ============================================
# 确保配置文件中包含 E2E 测试账号变量（兼容旧 .env.go-platform）
# ============================================
ensure_e2e_env_vars() {
    local config_file="$1"
    local defaults=(
        "SEED_E2E_ACCOUNTS=false"
        "E2E_ADMIN_EMAIL=e2e-admin@example.com"
        "E2E_ADMIN_PASSWORD=E2E_AdminPass123!"
        "E2E_USER_EMAIL=e2e-user@example.com"
        "E2E_USER_PASSWORD=E2E_TestPass123!"
        "E2E_ENTERPRISE_EMAIL=e2e-enterprise@example.com"
        "E2E_ENTERPRISE_PASSWORD=E2E_TestPass123!"
        "SEED_TEST_ENTERPRISE=false"
        "TEST_ENTERPRISE_NAME=\"Test Enterprise\""
        "TEST_ENTERPRISE_ADMIN_EMAIL=ent-admin@test.local"
        "TEST_ENTERPRISE_MEMBER_EMAIL=ent-member@test.local"
        "TEST_ENTERPRISE_PASSWORD=TestPass123!"
    )

    for entry in "${defaults[@]}"; do
        local key="${entry%%=*}"
        if ! grep -qE "^${key}=" "$config_file" 2>/dev/null; then
            echo "$entry" >> "$config_file"
        fi
    done

    # Normalize TEST_ENTERPRISE_NAME so the value containing a space is always
    # quoted when the file is sourced by bash.
    if grep -qE '^TEST_ENTERPRISE_NAME=Test Enterprise$' "$config_file" 2>/dev/null; then
        sed -i 's/^TEST_ENTERPRISE_NAME=Test Enterprise$/TEST_ENTERPRISE_NAME="Test Enterprise"/' "$config_file"
    fi
}

# ============================================
# 确保配置文件中包含 Docker 构建镜像/代理变量（兼容旧 .env.go-platform）
# ============================================
ensure_build_mirror_vars() {
    local config_file="$1"
    local defaults=(
        "APK_MIRROR=dl-cdn.alpinelinux.org"
        "GOPROXY=https://proxy.golang.org,direct"
    )

    for entry in "${defaults[@]}"; do
        local key="${entry%%=*}"
        if ! grep -qE "^${key}=" "$config_file" 2>/dev/null; then
            echo "$entry" >> "$config_file"
        fi
    done
}

# ============================================
# 确保配置文件中包含 Proxy 运行时变量（兼容旧 .env.go-platform）
# ============================================
ensure_proxy_runtime_vars() {
    local config_file="$1"
    local defaults=(
        "PROXY_REDIS_HOST=redis-proxy"
        "PROXY_REDIS_PORT=6379"
        "PROXY_REDIS_HOST_PORT=6380"
        "PROXY_REDIS_PASSWORD="
        "PROXY_REDIS_DB=0"
        "SERVER_ID="
        "SERVER_REGION="
    )

    for entry in "${defaults[@]}"; do
        local key="${entry%%=*}"
        if ! grep -qE "^${key}=" "$config_file" 2>/dev/null; then
            echo "$entry" >> "$config_file"
        fi
    done
}

# ============================================
# 判断当前是否使用外部数据库（Aurora / 任意外部 PG）
# DB_HOST 为空 / postgres / 127.0.0.1 / localhost 视为本地 Docker 容器；其余视为外部
# 返回 0=外部, 1=本地（配合 `if is_external_db; then ...` 使用）
# ============================================
is_external_db() {
    local h="${DB_HOST:-postgres}"
    case "${h}" in
        ""|postgres|127.0.0.1|localhost) return 1 ;;
        *) return 0 ;;
    esac
}

# ============================================
# 确保配置文件中包含数据库连接变量（兼容旧 .env.go-platform）
# ============================================
ensure_db_vars() {
    local config_file="$1"
    local defaults=(
        "DB_HOST=postgres"
        "DB_PORT=5432"
        "DB_NAME=ai_proxy_portal"
        "DB_USER=postgres"
        "DB_SSLMODE=disable"
        "DB_SSL_ROOT_CERT="
    )
    for entry in "${defaults[@]}"; do
        local key="${entry%%=*}"
        if ! grep -qE "^${key}=" "$config_file" 2>/dev/null; then
            echo "$entry" >> "$config_file"
        fi
    done
}

# ============================================
# 加载配置
# ============================================
load_config() {
    local config_file="${PORTAL_DIR}/.env.go-platform"
    if [[ -f "$config_file" ]]; then
        # 使用 set -a 自动导出所有变量
        set -a
        source "$config_file"
        set +a
    fi
}

# 生产/mvp 模式拒绝默认或弱管理员密码，避免首次启动带起已知弱口令。
validate_production_admin_password() {
    case "${RUN_MODE:-}" in
        mvp|prod|production) ;;
        *) return 0 ;;
    esac

    load_config

    local pw="${ADMIN_PASSWORD:-}"
    local weak=0
    if [[ -z "$pw" || ${#pw} -lt 12 ]]; then
        weak=1
    fi
    case "$pw" in
        Admin@123456!|Admin@123456|admin123|admin|password|changeme|12345678|123456789012|YourStrongAdminPassword12!|CHANGE_ME_admin_password_min_12_chars|*CHANGE_ME*|*CHANGEME*)
            weak=1
            ;;
    esac

    if [[ "$weak" -eq 1 ]]; then
        echo -e "${RED}✗ 生产/mvp 模式不能使用默认或弱 ADMIN_PASSWORD${NC}" >&2
        echo -e "${YELLOW}  请编辑 ${PORTAL_DIR}/.env.go-platform，将 ADMIN_PASSWORD 改为 ≥12 字符的强密码后重试。${NC}" >&2
        echo -e "${YELLOW}  示例: ADMIN_PASSWORD=\$(openssl rand -base64 18)${NC}" >&2
        exit 1
    fi
}

# ============================================
# 构建 Go Portal
# ============================================
build_portal() {
    echo -e "${YELLOW}[构建] 编译 Go Portal...${NC}"

    if [[ ! -d "${PORTAL_DIR}" ]]; then
        echo -e "${RED}✗ Portal 目录不存在: ${PORTAL_DIR}${NC}"
        return 1
    fi

    cd "${PORTAL_DIR}"

    # 确保 tailwind.min.css 存在且大小合理
    ensure_tailwind_css

    # 设置 Go 缓存目录
    export GOCACHE=${HOME}/go-cache
    export GOPATH=${HOME}/go
    export GOMODCACHE=${HOME}/go/pkg/mod

    VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
    BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    go build -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
        -o portal ./cmd/server

    echo -e "${GREEN}✓ Go Portal 编译完成${NC}"
}

# ============================================
# 确保 Tailwind CSS 存在
# ============================================
ensure_tailwind_css() {
    local CSS_FILE="web/static/css/tailwind.min.css"
    local MAX_SIZE=500000  # 500KB

    if [[ -f "${CSS_FILE}" ]]; then
        local SIZE=$(stat -c%s "${CSS_FILE}" 2>/dev/null || echo 0)
        if [[ ${SIZE} -le ${MAX_SIZE} ]]; then
            echo -e "${GREEN}✓ tailwind.min.css 已存在 (${SIZE} bytes)${NC}"
            return 0
        else
            echo -e "${YELLOW}⚠ tailwind.min.css 过大 (${SIZE} bytes)，重新生成...${NC}"
        fi
    else
        echo -e "${YELLOW}⚠ tailwind.min.css 不存在，重新生成...${NC}"
    fi

    # 使用 scripts/rebuild-tailwind.sh 生成
    if [[ -x scripts/rebuild-tailwind.sh ]]; then
        bash scripts/rebuild-tailwind.sh
    else
        # Fallback: 直接用 npx tailwindcss
        echo -e "${YELLOW}[Tailwind] 使用 npx 直接构建...${NC}"
        mkdir -p /tmp/tw-build
        (cd /tmp/tw-build && \
            if [[ ! -d node_modules/tailwindcss ]]; then
                npm init -y >/dev/null 2>&1
                npm install tailwindcss@3 --cache /tmp/npm-cache >/dev/null 2>&1
            fi && \
            cat > tailwind.config.js << TWEOF
/** @type{import('tailwindcss').Config} */
module.exports = {
  content: ["${PORTAL_DIR}/web/templates/**/*.html"],
  darkMode: ['class', '[data-theme="dark"]'],
  theme: { extend: { colors: { primary: 'var(--primary)', secondary: 'var(--text-muted)' } } },
  plugins: [],
}
TWEOF
            cat > input.css << TWEOF
@tailwind base; @tailwind components; @tailwind utilities;
TWEOF
            npx tailwindcss -i input.css -o "${PORTAL_DIR}/${CSS_FILE}" --minify 2>&1 | tail -1
        )
    fi

    if [[ -f "${CSS_FILE}" ]]; then
        local SIZE=$(stat -c%s "${CSS_FILE}" 2>/dev/null || echo 0)
        echo -e "${GREEN}✓ tailwind.min.css 生成完成 (${SIZE} bytes)${NC}"
    else
        echo -e "${RED}✗ tailwind.min.css 生成失败${NC}"
        return 1
    fi
}

# ============================================
# 校验 APP_BASE_URL
# ============================================
validate_app_base_url() {
    local url="${APP_BASE_URL:-}"

    if [[ -z "${url}" ]]; then
        echo -e "${RED}✗ APP_BASE_URL 未设置${NC}"
        echo -e "${YELLOW}   邮件邀请、密码重置链接需要公网域名。请设置后重试：${NC}"
        echo -e "${YELLOW}   ./start.sh portal --app-base-url=https://portal.example.com${NC}"
        echo -e "${YELLOW}   或直接编辑 ${PORTAL_DIR}/.env.go-platform 中的 APP_BASE_URL${NC}"
        return 1
    fi

    # 去除协议头后检查是否为 IP 地址
    local host_and_port="${url#*://}"
    local host="${host_and_port%%/*}"
    host="${host%%:*}"

    if [[ "${host}" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        echo -e "${RED}✗ APP_BASE_URL 不能是 IP 地址：${url}${NC}"
        echo -e "${YELLOW}   请使用公网域名，例如 https://portal.woodgit.com${NC}"
        return 1
    fi

    echo -e "${GREEN}✓ APP_BASE_URL=${url}${NC}"
    return 0
}

# ============================================
# 构建 Go Proxy
# ============================================
build_proxy() {
    if [[ -z "${PROXY_DIR}" ]] || [[ ! -d "${PROXY_DIR}" ]]; then
        echo -e "${RED}✗ Proxy 目录不存在${NC}"
        return 1
    fi

    echo -e "${YELLOW}[构建] 编译 Go Proxy...${NC}"

    cd "${PROXY_DIR}"

    # 设置 Go 缓存目录
    export GOCACHE=${HOME}/go-cache
    export GOPATH=${HOME}/go
    export GOMODCACHE=${HOME}/go/pkg/mod

    if ! command -v go &>/dev/null; then
        echo -e "${YELLOW}[提示] 本机未安装 Go，跳过本地编译，将由 start_proxy 通过 Docker 构建${NC}"
        return 0
    fi

    VERSION=$(git describe --tags --always 2>/dev/null || echo "dev")
    BUILD_TIME=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    go build -ldflags "-X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
        -o ai-proxy-go ./cmd/server

    echo -e "${GREEN}✓ Go Proxy 编译完成${NC}"
}

# ============================================
# 构建所有服务
# ============================================
build_all() {
    build_portal
    if [[ -n "${PROXY_DIR}" ]] && [[ -d "${PROXY_DIR}" ]]; then
        build_proxy
    fi
}

# ============================================
# 创建 Portal Docker Compose
# ============================================
create_portal_compose() {
    local portal_port="${DETECTED_PORTAL_PORT:-3001}"
    local pg_port="${DETECTED_PG_PORT:-5432}"
    local redis_port="${DETECTED_REDIS_PORT:-6379}"
    local bind_addr="0.0.0.0"
    local external="false"
    if is_external_db; then external="true"; fi

    # 仅本地模式需要 postgres 服务 / depends_on / 数据卷片段
    local pg_service=""
    local pg_depends=""
    local pg_volume=""
    if [[ "${external}" != "true" ]]; then
        pg_service=$(cat <<PG
  # PostgreSQL 数据库
  postgres:
    image: postgres:15-alpine
    container_name: ai-portal-postgres
    restart: always
    mem_limit: 1g
    environment:
      POSTGRES_DB: ai_proxy_portal
      POSTGRES_USER: postgres
      POSTGRES_PASSWORD: \${DB_PASSWORD}
    ports:
      - "127.0.0.1:${pg_port}:5432"
    volumes:
      - postgres-data:/var/lib/postgresql/data
    networks:
      - ai-platform-net
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U postgres"]
      interval: 10s
      timeout: 5s
      retries: 5
PG
)
        pg_depends="      postgres:
        condition: service_healthy"
        pg_volume="  postgres-data:"
    fi

    cat > "${PORTAL_DIR}/docker-compose.portal.yml" << EOF
services:
${pg_service}

  # Redis 缓存
  redis:
    image: redis:7-alpine
    container_name: ai-portal-redis
    restart: always
    mem_limit: 256m
    command: redis-server --requirepass "\${REDIS_PASSWORD}"
    ports:
      - "127.0.0.1:${redis_port}:6379"
    volumes:
      - redis-data:/data
    networks:
      - ai-platform-net
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a '\${REDIS_PASSWORD}' ping | grep PONG"]
      interval: 10s
      timeout: 3s
      retries: 5

  # Go Portal 服务
  portal:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        APK_MIRROR: \${APK_MIRROR:-dl-cdn.alpinelinux.org}
        GOPROXY: \${GOPROXY:-https://proxy.golang.org,direct}
    container_name: ai-portal-go
    restart: always
    mem_limit: 1g
    environment:
      PORT: 3001
      GIN_MODE: release
      DB_HOST: \${DB_HOST}
      DB_PORT: \${DB_PORT}
      DB_NAME: \${DB_NAME}
      DB_USER: \${DB_USER}
      DB_PASSWORD: \${DB_PASSWORD}
      DB_SSLMODE: \${DB_SSLMODE:-disable}
      DB_SSL_ROOT_CERT: \${DB_SSL_ROOT_CERT:-}
      REDIS_HOST: redis
      REDIS_PORT: 6379
      REDIS_PASSWORD: \${REDIS_PASSWORD}
      REDIS_TLS: \${REDIS_TLS:-false}
      REDIS_TLS_SKIP_VERIFY: \${REDIS_TLS_SKIP_VERIFY:-false}
      JWT_SECRET: \${JWT_SECRET}
      ADMIN_EMAIL: \${ADMIN_EMAIL}
      ADMIN_PASSWORD: \${ADMIN_PASSWORD}
      ADMIN_NAME: \${ADMIN_NAME:-Admin}
      ADMIN_MODE: \${ADMIN_MODE:-production}
      PROXY_SYNC_SECRET: \${PROXY_SYNC_SECRET}
      # Comma-separated list of Proxy HTTPS callback URLs. Used by Portal to push
      # sync and balance-update events directly to Proxies without relying on a
      # shared Redis channel. In the Docker stack the proxy service is reachable at
      # http://ai-proxy-go:8080 from the Portal container.
      PROXY_CALLBACK_URLS: \${PROXY_CALLBACK_URLS:-http://ai-proxy-go:8080}
      ENCRYPTION_KEY: \${ENCRYPTION_KEY}
      ADMIN_SECRET: \${ADMIN_SECRET}
      # Base URL used in password-reset and enterprise-invitation emails.
      # Must be a public domain (e.g. https://api.woodgit.com), not an IP.
      # Set APP_BASE_URL in .env.go-platform before starting.
      APP_BASE_URL: \${APP_BASE_URL}
      ALLOWED_ORIGINS: \${ALLOWED_ORIGINS:-}
      SMTP_HOST: \${SMTP_HOST}
      SMTP_PORT: \${SMTP_PORT:-465}
      SMTP_SECURE: \${SMTP_SECURE:-true}
      SMTP_USER: \${SMTP_USER}
      SMTP_PASSWORD: \${SMTP_PASSWORD}
      SMTP_FROM: \${SMTP_FROM}
      SEED_E2E_ACCOUNTS: \${SEED_E2E_ACCOUNTS:-false}
      E2E_ADMIN_EMAIL: \${E2E_ADMIN_EMAIL}
      E2E_ADMIN_PASSWORD: \${E2E_ADMIN_PASSWORD}
      E2E_USER_EMAIL: \${E2E_USER_EMAIL}
      E2E_USER_PASSWORD: \${E2E_USER_PASSWORD}
      E2E_ENTERPRISE_EMAIL: \${E2E_ENTERPRISE_EMAIL}
      E2E_ENTERPRISE_PASSWORD: \${E2E_ENTERPRISE_PASSWORD}
      SEED_TEST_ENTERPRISE: \${SEED_TEST_ENTERPRISE:-false}
      TEST_ENTERPRISE_NAME: \${TEST_ENTERPRISE_NAME:-Test Enterprise}
      TEST_ENTERPRISE_ADMIN_EMAIL: \${TEST_ENTERPRISE_ADMIN_EMAIL:-ent-admin@test.local}
      TEST_ENTERPRISE_MEMBER_EMAIL: \${TEST_ENTERPRISE_MEMBER_EMAIL:-ent-member@test.local}
      TEST_ENTERPRISE_PASSWORD: \${TEST_ENTERPRISE_PASSWORD:-TestPass123!}
      # File storage configuration for enterprise application documents.
      FILE_STORAGE_PROVIDER: \${FILE_STORAGE_PROVIDER:-local}
      FILE_STORAGE_LOCAL_ROOT: /app/uploads
      FILE_STORAGE_ENDPOINT: \${FILE_STORAGE_ENDPOINT:-}
      FILE_STORAGE_REGION: \${FILE_STORAGE_REGION:-ap-southeast-1}
      FILE_STORAGE_BUCKET: \${FILE_STORAGE_BUCKET:-ai-portal-uploads}
      FILE_STORAGE_ACCESS_KEY: \${FILE_STORAGE_ACCESS_KEY:-}
      FILE_STORAGE_SECRET_KEY: \${FILE_STORAGE_SECRET_KEY:-}
      FILE_STORAGE_USE_SSL: \${FILE_STORAGE_USE_SSL:-true}
      FILE_STORAGE_PATH_PREFIX: \${FILE_STORAGE_PATH_PREFIX:-enterprise-apps}
      FILE_MAX_SIZE_MB: \${FILE_MAX_SIZE_MB:-10}
      FILE_MAX_TOTAL_MB_PER_APPLICATION: \${FILE_MAX_TOTAL_MB_PER_APPLICATION:-50}
      FILE_ALLOWED_TYPES: \${FILE_ALLOWED_TYPES:-application/pdf,image/jpeg,image/png}
    ports:
      - "${bind_addr}:${portal_port}:3001"
    volumes:
      # Bind mount for uploaded enterprise documents. Persisted on the host.
      - ./uploads:/app/uploads:rw
    networks:
      - ai-platform-net
    depends_on:
      redis:
        condition: service_healthy
${pg_depends}
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:3001/api/health || exit 1"]
      interval: 10s
      timeout: 3s
      retries: 3

networks:
  ai-platform-net:
    driver: bridge
    name: ai-platform-net

volumes:
${pg_volume}
  redis-data:
EOF
}

# ============================================
# 创建 Proxy Docker Compose（自带独立 Redis）
# ============================================
create_proxy_compose() {
    local portal_url="${1:-http://127.0.0.1:${DETECTED_PORTAL_PORT:-3001}}"
    local proxy_secret="${2:-${PROXY_SYNC_SECRET:-}}"
    local proxy_port="${DETECTED_PROXY_PORT:-8080}"
    local proxy_redis_host="${PROXY_REDIS_HOST:-redis-proxy}"
    local proxy_redis_port="${PROXY_REDIS_PORT:-6379}"
    local proxy_redis_host_port="${PROXY_REDIS_HOST_PORT:-6380}"
    local proxy_redis_password="${PROXY_REDIS_PASSWORD:-${REDIS_PASSWORD:-}}"
    local proxy_redis_db="${PROXY_REDIS_DB:-0}"

    # 将函数参数 export 为环境变量，供 docker-compose 的 --env-file 解析
    export PORTAL_URL="${portal_url}"
    # start.sh 用于本地/Docker 开发环境，默认开启 dev_mode，允许 http:// 内网地址
    # 如需生产模式（https:// + dev_mode=false），可通过环境变量 PORTAL_DEV_MODE=false 覆盖
    export PORTAL_DEV_MODE="${PORTAL_DEV_MODE:-true}"
    export PORTAL_SYNC_SECRET="${proxy_secret}"
    # JWT_SECRET：CLI 参数 > 环境变量 > 空
    if [[ -n "${JWT_SECRET_ARG}" ]]; then
        export JWT_SECRET="${JWT_SECRET_ARG}"
    fi
    # PROXY_REDIS_PASSWORD：CLI 参数 > 环境变量 > Portal Redis 密码
    if [[ -n "${PROXY_REDIS_PASSWORD_ARG}" ]]; then
        export PROXY_REDIS_PASSWORD="${PROXY_REDIS_PASSWORD_ARG}"
        proxy_redis_password="${PROXY_REDIS_PASSWORD_ARG}"
    fi
    # ADMIN_SECRET 复用 sync secret
    export ADMIN_SECRET="${ADMIN_SECRET:-${PROXY_SYNC_SECRET:-}}"

    cat > "${PROXY_DIR}/docker-compose.proxy.yml" << EOF
services:
  # Proxy 专用 Redis（生产环境必须与 Portal Redis 分离）
  redis-proxy:
    image: redis:7-alpine
    container_name: ai-proxy-redis
    restart: always
    command: redis-server --requirepass "\${PROXY_REDIS_PASSWORD:-\${REDIS_PASSWORD}}"
    ports:
      - "127.0.0.1:${proxy_redis_host_port}:6379"
    volumes:
      - redis-proxy-data:/data
    networks:
      - ai-platform-net
    healthcheck:
      test: ["CMD-SHELL", "redis-cli -a '\${PROXY_REDIS_PASSWORD:-\${REDIS_PASSWORD}}' ping | grep PONG"]
      interval: 10s
      timeout: 3s
      retries: 5

  # Go Proxy 服务（连接 Proxy 自有 Redis）
  proxy:
    build:
      context: .
      dockerfile: Dockerfile
      args:
        APK_MIRROR: \${APK_MIRROR:-dl-cdn.alpinelinux.org}
        GOPROXY: \${GOPROXY:-https://proxy.golang.org,direct}
    container_name: ai-proxy-go
    restart: always
    environment:
      PORT: 8080
      GIN_MODE: release
      SERVER_ID: "\${SERVER_ID:-}"
      SERVER_REGION: "\${SERVER_REGION:-}"
      PORTAL_URL: \${PORTAL_URL}
      PORTAL_DEV_MODE: "\${PORTAL_DEV_MODE:-false}"
      PORTAL_SYNC_SECRET: \${PROXY_SYNC_SECRET}
      JWT_SECRET: \${JWT_SECRET}
      RATE_LIMIT_ENABLED: "true"
      RATE_LIMIT_DEFAULT_RPM: 60
      REDIS_HOST: ${proxy_redis_host}
      REDIS_PORT: ${proxy_redis_port}
      REDIS_PASSWORD: \${PROXY_REDIS_PASSWORD:-\${REDIS_PASSWORD}}
      REDIS_DB: ${proxy_redis_db}
      REDIS_TLS: \${REDIS_TLS:-false}
      REDIS_TLS_SKIP_VERIFY: \${REDIS_TLS_SKIP_VERIFY:-false}
      ADMIN_SECRET: \${ADMIN_SECRET}
      ENCRYPTION_KEY: \${ENCRYPTION_KEY}
      SIMULATE_ENABLED: "\${SIMULATE_ENABLED:-false}"
      SIMULATE_LATENCY_MS: "\${SIMULATE_LATENCY_MS:-100}"
    ports:
      - "0.0.0.0:${proxy_port}:8080"
    networks:
      - ai-platform-net
    depends_on:
      redis-proxy:
        condition: service_healthy
    healthcheck:
      test: ["CMD-SHELL", "wget -q -O /dev/null http://127.0.0.1:8080/health || exit 1"]
      interval: 10s
      timeout: 3s
      retries: 3

networks:
  ai-platform-net:
    external: true
    name: ai-platform-net

volumes:
  redis-proxy-data:
EOF
}

# ============================================
# 启动 Portal
# ============================================
start_portal() {
    echo -e "${YELLOW}[启动] 启动 Go Portal...${NC}"
    
    cd "${PORTAL_DIR}"
    load_config
    
    # 校验外部访问域名
    validate_app_base_url || exit 1
    
    # 如果 compose 文件不存在则生成；已存在的文件优先保留，避免 clean-all 后丢失自定义配置
    if [[ ! -f "docker-compose.portal.yml" ]]; then
        echo -e "${YELLOW}[提示] docker-compose.portal.yml 不存在，正在生成默认模板...${NC}"
        create_portal_compose
    else
        echo -e "${BLUE}[提示] 使用已存在的 docker-compose.portal.yml${NC}"
    fi

    # 外部数据库：启动前做连通性检查，失败立即报错（避免长时间等容器健康检查）
    if is_external_db; then
        echo -e "${BLUE}  外部数据库: ${DB_HOST}:${DB_PORT:-5432}/${DB_NAME:-ai_proxy_portal} (sslmode=${DB_SSLMODE:-require})${NC}"
        local _db_ok=0 _i
        for _i in {1..30}; do
            if (exec 3<>/dev/tcp/${DB_HOST}/${DB_PORT:-5432}) 2>/dev/null; then
                exec 3>&- 2>/dev/null || true
                _db_ok=1
                break
            fi
            sleep 1
        done
        if [[ "${_db_ok}" != "1" ]]; then
            echo -e "${RED}✗ 无法连接外部数据库 ${DB_HOST}:${DB_PORT:-5432}，请检查安全组 / VPC / 端点${NC}"
            return 1
        fi
        echo -e "${GREEN}  ✓ 外部数据库可达${NC}"
    fi
    
    # 预创建上传目录并设置权限，避免容器以 root 创建后宿主机无法访问
    local upload_dir="${PORTAL_DIR}/uploads"
    mkdir -p "${upload_dir}"
    if command -v chown &>/dev/null; then
        chown -R "$(id -u):$(id -g)" "${upload_dir}" 2>/dev/null || true
    fi
    chmod -R u+rwx "${upload_dir}" 2>/dev/null || true
    
    # 启动服务 (带 --build 确保代码更新)
    ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform up -d --build
    
    echo -e "${GREEN}✓ Go Portal 启动完成${NC}"
    echo -e "${BLUE}Portal: http://${DETECTED_HOST_IP}:${DETECTED_PORTAL_PORT}${NC}"
}

# ============================================
# 启动 Proxy
# ============================================
start_proxy() {
    if [[ -z "${PROXY_DIR}" ]] || [[ ! -d "${PROXY_DIR}" ]]; then
        echo -e "${RED}✗ Proxy 目录不存在${NC}"
        return 1
    fi

    echo -e "${YELLOW}[启动] 启动 Go Proxy...${NC}"
    
    cd "${PROXY_DIR}"
    load_config
    
    # 使用命令行参数或检测到的 Portal URL
    local portal_url="${PORTAL_URL:-${DETECTED_PORTAL_URL:-http://127.0.0.1:${DETECTED_PORTAL_PORT:-3001}}}"
    local proxy_secret="${PROXY_SECRET:-${PROXY_SYNC_SECRET:-}}"
    
    # 检查是否是单机部署 (Portal 容器存在)
    if docker ps --format '{{.Names}}' 2>/dev/null | grep -q "ai-portal-go"; then
        portal_url="http://ai-portal-go:${DETECTED_PORTAL_PORT:-3001}"
    fi
    
    # 导出变量，供 --env-file 与 compose 模板使用；CLI/环境变量优先级高于 .env.go-platform
    export PORTAL_URL="${portal_url}"
    # start.sh 用于本地/Docker 开发环境，默认开启 dev_mode，允许 http:// 内网地址
    # 如需生产模式（https:// + dev_mode=false），可通过环境变量 PORTAL_DEV_MODE=false 覆盖
    export PORTAL_DEV_MODE="${PORTAL_DEV_MODE:-true}"
    export PORTAL_SYNC_SECRET="${proxy_secret}"
    # JWT_SECRET：CLI 参数 > 环境变量 > 空
    if [[ -n "${JWT_SECRET_ARG}" ]]; then
        export JWT_SECRET="${JWT_SECRET_ARG}"
    fi
    # PROXY_REDIS_PASSWORD：CLI 参数 > 环境变量 > Portal Redis 密码
    if [[ -n "${PROXY_REDIS_PASSWORD_ARG}" ]]; then
        export PROXY_REDIS_PASSWORD="${PROXY_REDIS_PASSWORD_ARG}"
    fi
    # ADMIN_SECRET 复用 sync secret
    export ADMIN_SECRET="${ADMIN_SECRET:-${PROXY_SYNC_SECRET:-}}"
    
    # 如果 compose 文件不存在则生成；已提交/已存在的文件优先保留，避免 clean-all 后丢失自定义配置
    if [[ ! -f "docker-compose.proxy.yml" ]]; then
        echo -e "${YELLOW}[提示] docker-compose.proxy.yml 不存在，正在生成默认模板...${NC}"
        create_proxy_compose "$portal_url" "$proxy_secret"
    else
        echo -e "${BLUE}[提示] 使用已存在的 docker-compose.proxy.yml（变量来自 .env.go-platform 或当前环境）${NC}"
    fi

    # 启动服务 (带 --build 确保代码更新)
    local env_file="${PORTAL_DIR}/.env.go-platform"
    local env_flag=""
    if [[ -f "${env_file}" ]]; then
        env_flag="--env-file ${env_file}"
    fi
    ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml ${env_flag} up -d --build
    
    echo -e "${GREEN}✓ Go Proxy 启动完成${NC}"
    echo -e "${BLUE}Proxy: http://${DETECTED_HOST_IP}:${DETECTED_PROXY_PORT}${NC}"
}

# ============================================
# 启动所有服务 (完整部署)
# ============================================
start_all() {
    local mode="$1"
    
    echo -e "${YELLOW}[启动] 启动 AI Platform Go...${NC}"
    
    load_config
    
    # 设置模拟模式
    if [[ "$mode" == "simulate" ]]; then
        export SIMULATE_ENABLED=true
        export SIMULATE_LATENCY_MS=100
        echo -e "${BLUE}  模拟模式: 启用${NC}"
    else
        export SIMULATE_ENABLED=false
    fi
    
    # 启动 Portal
    start_portal
    
    # 等待 Portal 就绪
    echo -e "${BLUE}  等待 Portal 就绪...${NC}"
    for i in {1..30}; do
        if curl -s -m 2 "http://127.0.0.1:${DETECTED_PORTAL_PORT:-3001}/api/health" > /dev/null 2>&1; then
            echo -e "${GREEN}  ✓ Portal 就绪${NC}"
            break
        fi
        sleep 1
    done
    
    # 启动 Proxy
    start_proxy
    
    echo ""
    echo -e "${GREEN}✓ AI Platform Go 启动完成${NC}"
    echo -e "${BLUE}Portal: http://${DETECTED_HOST_IP}:${DETECTED_PORTAL_PORT}${NC}"
    echo -e "${BLUE}Proxy:  http://${DETECTED_HOST_IP}:${DETECTED_PROXY_PORT}${NC}"
}

# ============================================
# 停止 Portal
# ============================================
stop_portal() {
    echo -e "${YELLOW}[停止] 停止 Go Portal...${NC}"
    
    cd "${PORTAL_DIR}"
    
    if [[ -f "docker-compose.portal.yml" ]]; then
        ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform down
        echo -e "${GREEN}✓ Go Portal 已停止${NC}"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q "ai-portal-go"; then
        echo -e "${YELLOW}  compose 文件缺失，直接停止容器...${NC}"
        docker stop ai-portal-go ai-portal-postgres ai-portal-redis 2>/dev/null || true
        docker rm ai-portal-go ai-portal-postgres ai-portal-redis 2>/dev/null || true
        echo -e "${GREEN}✓ Go Portal 已停止${NC}"
    else
        echo -e "${GREEN}✓ Go Portal 已停止 (未运行)${NC}"
    fi
}

# ============================================
# 停止 Proxy
# ============================================
stop_proxy() {
    if [[ -z "${PROXY_DIR}" ]] || [[ ! -d "${PROXY_DIR}" ]]; then
        # Proxy 目录不存在，尝试直接停止容器
        if docker ps --format '{{.Names}}' 2>/dev/null | grep -qE "ai-proxy-go|ai-proxy-redis"; then
            echo -e "${YELLOW}[停止] 停止 Go Proxy (直接停止容器)...${NC}"
            docker stop ai-proxy-go ai-proxy-redis 2>/dev/null || true
            docker rm ai-proxy-go ai-proxy-redis 2>/dev/null || true
            echo -e "${GREEN}✓ Go Proxy 已停止${NC}"
        else
            echo -e "${YELLOW}Proxy 目录不存在且容器未运行，跳过${NC}"
        fi
        return
    fi

    echo -e "${YELLOW}[停止] 停止 Go Proxy...${NC}"
    
    cd "${PROXY_DIR}"
    
    if [[ -f "docker-compose.proxy.yml" ]]; then
        ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml down
        echo -e "${GREEN}✓ Go Proxy 已停止${NC}"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -qE "ai-proxy-go|ai-proxy-redis"; then
        echo -e "${YELLOW}  compose 文件缺失，直接停止容器...${NC}"
        docker stop ai-proxy-go ai-proxy-redis 2>/dev/null || true
        docker rm ai-proxy-go ai-proxy-redis 2>/dev/null || true
        echo -e "${GREEN}✓ Go Proxy 已停止${NC}"
    else
        echo -e "${GREEN}✓ Go Proxy 已停止 (未运行)${NC}"
    fi
}

# ============================================
# 停止所有服务
# ============================================
stop_all() {
    # 先停 Proxy 再停 Portal，避免 Portal compose 删除共享网络时 Proxy 还在使用
    stop_proxy
    stop_portal
}

# ============================================
# 查看状态
# ============================================
show_status() {
    echo -e "${YELLOW}[状态] AI Platform Go 状态${NC}"
    echo ""
    load_config

    # Portal 状态
    echo -e "${CYAN}Portal (端口 ${DETECTED_PORTAL_PORT:-3001}):${NC}"
    cd "${PORTAL_DIR}"
    if [[ -f "docker-compose.portal.yml" ]]; then
        ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform ps 2>/dev/null || echo "  未运行"
    else
        echo "  未配置"
    fi
    
    echo ""
    
    # Proxy 状态
    echo -e "${CYAN}Proxy (端口 ${DETECTED_PROXY_PORT:-8080}):${NC}"
    if [[ -n "${PROXY_DIR}" ]] && [[ -d "${PROXY_DIR}" ]]; then
        cd "${PROXY_DIR}"
        if [[ -f "docker-compose.proxy.yml" ]]; then
            ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml ps 2>/dev/null || echo "  未运行"
        else
            echo "  未配置"
        fi
    else
        echo "  目录不存在"
    fi
    
    echo ""
    echo -e "${BLUE}健康检查:${NC}"

    # 检查 Portal (API health endpoint)
    local portal_port="${DETECTED_PORTAL_PORT:-3001}"
    local portal_response=$(curl -s -m 2 "http://127.0.0.1:${portal_port}/api/health" 2>/dev/null || true)
    if echo "$portal_response" | grep -q '"status":"ok"'; then
        echo -e "  Portal: ${GREEN}✓ 正常${NC} (http://127.0.0.1:${portal_port})"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q "ai-portal-go"; then
        echo -e "  Portal: ${YELLOW}⚠ 启动中${NC}"
    else
        echo -e "  Portal: ${RED}✗ 未运行${NC}"
    fi

    # 检查 Proxy
    local proxy_port="${DETECTED_PROXY_PORT:-8080}"
    local proxy_response=$(curl -s -m 2 "http://127.0.0.1:${proxy_port}/health" 2>/dev/null || true)
    if echo "$proxy_response" | grep -q '"status":"ok"\|"status":"healthy"\|"healthy":true'; then
        echo -e "  Proxy:  ${GREEN}✓ 正常${NC} (http://127.0.0.1:${proxy_port})"
    elif docker ps --format '{{.Names}}' 2>/dev/null | grep -q "ai-proxy-go"; then
        echo -e "  Proxy:  ${YELLOW}⚠ 启动中${NC}"
    else
        echo -e "  Proxy:  ${RED}✗ 未运行${NC}"
    fi

    echo ""
    echo -e "${BLUE}检测到的环境信息:${NC}"
    echo -e "  主机 IP:     ${DETECTED_HOST_IP}"
    echo -e "  Portal 端口: ${DETECTED_PORTAL_PORT}"
    echo -e "  Proxy 端口:  ${DETECTED_PROXY_PORT}"
    if is_external_db; then
        if (exec 3<>/dev/tcp/${DB_HOST}/${DB_PORT:-5432}) 2>/dev/null; then
            exec 3>&- 2>/dev/null || true
            echo -e "  数据库:      外部 ${DB_HOST}:${DB_PORT:-5432}/${DB_NAME:-ai_proxy_portal} [${GREEN}可达${NC}]"
        else
            echo -e "  数据库:      外部 ${DB_HOST}:${DB_PORT:-5432}/${DB_NAME:-ai_proxy_portal} [${RED}不可达${NC}]"
        fi
    else
        echo -e "  PostgreSQL:  本地容器 (端口 ${DETECTED_PG_PORT})"
    fi
    echo -e "  Portal Redis:${DETECTED_REDIS_PORT}"
    echo -e "  Proxy Redis: ${DETECTED_PROXY_REDIS_PORT:-6380}"
}

# ============================================
# 查看日志
# ============================================
show_logs() {
    local service="$1"
    
    case "$service" in
        portal)
            cd "${PORTAL_DIR}"
            if [[ -f "docker-compose.portal.yml" ]]; then
                ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform logs -f
            fi
            ;;
        proxy)
            if [[ -n "${PROXY_DIR}" ]] && [[ -d "${PROXY_DIR}" ]]; then
                cd "${PROXY_DIR}"
                if [[ -f "docker-compose.proxy.yml" ]]; then
                    ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml logs -f
                fi
            fi
            ;;
        *)
            # 同时查看两个日志
            cd "${PORTAL_DIR}"
            if [[ -f "docker-compose.portal.yml" ]]; then
                ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform logs -f &
            fi
            if [[ -n "${PROXY_DIR}" ]] && [[ -d "${PROXY_DIR}" ]]; then
                cd "${PROXY_DIR}"
                if [[ -f "docker-compose.proxy.yml" ]]; then
                    ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml logs -f &
                fi
            fi
            wait
            ;;
    esac
}

# ============================================
# 清理配置
# ============================================
clean_config() {
    echo -e "${YELLOW}[清理] 清理旧配置...${NC}"
    
    # 保留 .env.go-platform（包含密码等敏感配置），只删除 docker-compose 文件
    rm -f "${PORTAL_DIR}/docker-compose.portal.yml"
    rm -f "${PROXY_DIR}/docker-compose.proxy.yml" 2>/dev/null || true
    
    echo -e "${GREEN}✓ 清理完成${NC}"
}

# ============================================
# 完全重置
# ============================================
clean_all() {
    echo -e "${YELLOW}[重置] 完全重置 AI Platform Go...${NC}"

    # 停止服务
    stop_all

    # 清理 Docker 卷 (必须在删除 docker-compose.yml 之前执行)
    cd "${PORTAL_DIR}"
    if [[ -f "docker-compose.portal.yml" ]]; then
        ${DOCKER_COMPOSE_CMD} -f docker-compose.portal.yml --env-file .env.go-platform down -v 2>/dev/null || true
    fi
    if [[ -n "${PROXY_DIR}" ]] && [[ -f "${PROXY_DIR}/docker-compose.proxy.yml" ]]; then
        cd "${PROXY_DIR}"
        ${DOCKER_COMPOSE_CMD} -f docker-compose.proxy.yml down -v 2>/dev/null || true
    fi

    # 清理配置
    clean_config

    # 清理构建产物
    rm -f "${PORTAL_DIR}/portal"
    rm -f "${PROXY_DIR}/ai-proxy-go" 2>/dev/null || true

    # 清理残留的 Docker 数据目录 (动态检测路径)
    if [[ -n "${DETECTED_PG_VOLUME:-}" ]]; then
        rm -rf "${DETECTED_PG_VOLUME}" 2>/dev/null || true
    fi
    if [[ -n "${DETECTED_REDIS_VOLUME:-}" ]]; then
        rm -rf "${DETECTED_REDIS_VOLUME}" 2>/dev/null || true
    fi

    # 同时尝试清理可能的旧路径 (兼容性)
    rm -rf /mnt/workspace/docker/volumes/ai-portal-go_postgres-data 2>/dev/null || true
    rm -rf /mnt/workspace/docker/volumes/ai-portal-go_redis-data 2>/dev/null || true
    rm -rf /mnt/workspace/docker/volumes/ai-proxy-go_redis-proxy-data 2>/dev/null || true

    # 显式删除 Docker 命名卷（不依赖 compose 文件是否存在，也不依赖宿主机路径）
    echo -e "${YELLOW}[重置] 删除命名卷...${NC}"
    docker volume rm -f ai-portal-go_postgres-data 2>/dev/null || true
    docker volume rm -f ai-portal-go_redis-data 2>/dev/null || true
    docker volume rm -f ai-proxy-go_redis-proxy-data 2>/dev/null || true

    # 清理可能残留的独立 Proxy Redis 容器
    docker stop ai-proxy-redis 2>/dev/null || true
    docker rm ai-proxy-redis 2>/dev/null || true

    echo -e "${GREEN}✓ 完全重置完成${NC}"
}

# ============================================
# 数据库快照 (发布/重建前的安全网)
# ============================================
# 自动快照命名为 snapshot-<UTC>.dump，按 SNAPSHOT_RETAIN_DAYS(默认14)清理；
# 手动 `snapshot` 命名为 snapshot-keep-<UTC>.dump，永不自动清理(发布留底)。
# pg_dump/pg_restore 在容器内执行，不依赖宿主机客户端。
snapshot_db() {
    local keep="${1:-}"  # "--keep" => 手动留底，不参与按天清理
    load_config
    local dir="${SNAPSHOT_DIR:-${PORTAL_DIR}/backups}"
    local retain_days="${SNAPSHOT_RETAIN_DAYS:-14}"
    local db_name="${DB_NAME:-ai_proxy_portal}"
    local db_user="${DB_USER:-postgres}"

    mkdir -p "${dir}"
    local ts="$(date -u +%Y%m%dT%H%M%SZ)"
    local prefix="snapshot"
    [[ "${keep}" == "--keep" ]] && prefix="snapshot-keep"
    local outfile="${dir}/${prefix}-${ts}.dump"

    if is_external_db; then
        local host="${DB_HOST}" port="${DB_PORT:-5432}" ssl="${DB_SSLMODE:-require}"
        echo -e "${CYAN}[快照] 外部数据库 ${host}:${port}/${db_name} -> ${outfile}${NC}"
        if ! docker run --rm --network host \
            -e PGPASSWORD="${DB_PASSWORD:-}" -e PGSSLMODE="${ssl}" \
            postgres:15-alpine \
            pg_dump -Fc -h "${host}" -p "${port}" -U "${db_user}" -d "${db_name}" --no-owner --no-acl \
            > "${outfile}" 2>/dev/null; then
            echo -e "${YELLOW}[快照] 警告: 外部 pg_dump 失败，已跳过(不阻断重建)${NC}"
            rm -f "${outfile}"
            return 0
        fi
    else
        local container="${SNAPSHOT_DB_CONTAINER:-ai-portal-postgres}"
        if ! docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null | grep -q true; then
            echo -e "${YELLOW}[快照] 跳过: 容器 ${container} 未运行${NC}"
            return 0
        fi
        echo -e "${CYAN}[快照] 本地数据库 ${db_name} -> ${outfile}${NC}"
        if ! docker exec "${container}" pg_dump -Fc -U "${db_user}" -d "${db_name}" > "${outfile}" 2>/dev/null; then
            echo -e "${YELLOW}[快照] 警告: pg_dump 失败，已跳过(不阻断重建)${NC}"
            rm -f "${outfile}"
            return 0
        fi
    fi

    # 用一次性客户端容器校验 dump 可读（本地/外部统一，避免依赖宿主机 pg_restore）
    if ! docker run --rm -i postgres:15-alpine pg_restore --list < "${outfile}" >/dev/null 2>&1; then
        echo -e "${YELLOW}[快照] 警告: 生成的 dump 无法被 pg_restore 读取，已删除${NC}"
        rm -f "${outfile}"
        return 0
    fi

    local size="$(du -h "${outfile}" | cut -f1)"
    echo -e "${GREEN}✓ 快照完成 (${size}): ${outfile}${NC}"

    if [[ "${retain_days}" =~ ^[0-9]+$ ]] && [[ "${retain_days}" -gt 0 ]]; then
        find "${dir}" -maxdepth 1 -name 'snapshot-[0-9]*.dump' -type f -mtime +"${retain_days}" -delete 2>/dev/null || true
    fi
    return 0
}

# 恢复到**新库名**，绝不原地覆盖当前库。验证无误后由用户手动切换。
restore_db() {
    local dumpfile="${1:-}"
    local target="${2:-}"
    load_config
    local db_user="${DB_USER:-postgres}"
    local cur_db="${DB_NAME:-ai_proxy_portal}"

    if [[ -z "${dumpfile}" ]]; then
        echo -e "${RED}用法: $0 restore <dump-file> [target-db-name]${NC}"
        echo -e "      快照目录: ${SNAPSHOT_DIR:-${PORTAL_DIR}/backups}"
        return 1
    fi
    if [[ ! -f "${dumpfile}" ]]; then
        echo -e "${RED}✗ 找不到 dump 文件: ${dumpfile}${NC}"
        return 1
    fi
    [[ -z "${target}" ]] && target="ai_proxy_portal_restored_$(date -u +%Y%m%dT%H%M%SZ)"
    if [[ ! "${target}" =~ ^[A-Za-z0-9_]+$ ]]; then
        echo -e "${RED}✗ 非法目标库名(仅允许字母/数字/下划线): ${target}${NC}"
        return 1
    fi

    echo -e "${CYAN}[恢复] 校验 dump 目录...${NC}"
    if ! docker run --rm -i postgres:15-alpine pg_restore --list < "${dumpfile}" >/dev/null 2>&1; then
        echo -e "${RED}✗ dump 文件损坏或无法读取: ${dumpfile}${NC}"
        return 1
    fi

    if is_external_db; then
        local host="${DB_HOST}" port="${DB_PORT:-5432}" ssl="${DB_SSLMODE:-require}"
        echo -e "${CYAN}[恢复] 外部 ${host}:${port} 创建目标库 ${target} 并导入(当前库 ${cur_db} 不受影响)...${NC}"
        if ! docker run --rm --network host -e PGPASSWORD="${DB_PASSWORD:-}" -e PGSSLMODE="${ssl}" postgres:15-alpine \
            createdb -h "${host}" -p "${port}" -U "${db_user}" "${target}" 2>/dev/null; then
            echo -e "${RED}✗ createdb ${target} 失败(可能已存在)${NC}"
            return 1
        fi
        if ! docker run --rm -i --network host -e PGPASSWORD="${DB_PASSWORD:-}" -e PGSSLMODE="${ssl}" postgres:15-alpine \
            pg_restore -Fc -h "${host}" -p "${port}" -U "${db_user}" -d "${target}" --no-owner --no-acl < "${dumpfile}" 2>/dev/null; then
            echo -e "${YELLOW}[恢复] 警告: pg_restore 报告了错误(部分对象可能已导入)，请检查目标库 ${target}${NC}"
        fi
    else
        local container="${SNAPSHOT_DB_CONTAINER:-ai-portal-postgres}"
        if ! docker inspect -f '{{.State.Running}}' "${container}" 2>/dev/null | grep -q true; then
            echo -e "${RED}✗ 容器 ${container} 未运行，无法 restore${NC}"
            return 1
        fi
        echo -e "${CYAN}[恢复] 创建目标库 ${target} 并导入(当前库 ${cur_db} 不受影响)...${NC}"
        if ! docker exec "${container}" createdb -U "${db_user}" "${target}" 2>/dev/null; then
            echo -e "${RED}✗ createdb ${target} 失败(可能已存在)${NC}"
            return 1
        fi
        if ! docker exec -i "${container}" pg_restore -Fc -U "${db_user}" -d "${target}" --no-owner --no-acl < "${dumpfile}" 2>/dev/null; then
            echo -e "${YELLOW}[恢复] 警告: pg_restore 报告了错误(部分对象可能已导入)，请检查目标库 ${target}${NC}"
        fi
    fi

    echo -e "${GREEN}✓ 已恢复到新库: ${target}${NC}"
    echo -e "${CYAN}验证无误后切换: 改 .env.go-platform 的 DB_NAME=${target} 后 ./start.sh rebuild，或在 psql 中 ALTER DATABASE 切换${NC}"
}

# ============================================
# 重新构建并启动
# ============================================
rebuild_services() {
    echo -e "${YELLOW}[重建] 重新构建并启动 AI Platform Go...${NC}"

    # 发布前自动快照(必须在 stop_all 之前，此时 PG 仍在运行)
    if [[ "${SNAPSHOT_ON_RESTART:-1}" == "1" ]]; then
        snapshot_db || true
    fi

    # 停止服务
    stop_all

    # 启动服务 (Docker 会自动重建镜像)
    start_all "mvp"
}

# ============================================
# 显示帮助
# ============================================
show_help() {
    echo -e "${CYAN}AI Platform Go - 启动脚本${NC}"
    echo ""
    echo "用法: $0 [命令] [选项]"
    echo ""
    echo "命令:"
    echo "  mvp              启动完整平台 (Portal + Proxy)"
    echo "  simulate         启动完整平台 (模拟模式)"
    echo "  portal           仅启动 Portal"
    echo "  proxy            仅启动 Proxy"
    echo "  stop             停止所有服务"
    echo "  stop-portal      停止 Portal"
    echo "  stop-proxy       停止 Proxy"
    echo "  status           查看状态"
    echo "  logs             查看日志"
    echo "  build            构建所有服务"
    echo "  build-portal     仅构建 Portal"
    echo "  build-proxy      仅构建 Proxy"
    echo "  clean            清理配置"
    echo "  clean-all        完全重置"
    echo "  rebuild          重新构建并启动 (默认先自动快照数据库)"
    echo "  snapshot         手动快照数据库到 backups/ (保留，不自动清理)"
    echo "  restore          恢复快照到新库: restore <dump> [target-db]"
    echo "  help             显示帮助"
    echo ""
    echo "选项:"
    echo "  --portal-url=URL          Portal URL (用于 Proxy 连接)"
    echo "  --proxy-secret=S          Secret (用于 Proxy ↔ Portal 通信签名)"
    echo "  --jwt-secret=S            JWT 密钥 (需与 Portal 的 JWT_SECRET 一致)"
    echo "  --redis-password=S        Portal Redis 密码"
    echo "  --proxy-redis-password=S  Proxy 独立 Redis 密码（默认与 Portal Redis 密码相同）"
    echo "  --app-base-url=URL        外部访问基础 URL (必须是公网域名，用于邮件链接)"
    echo ""
    echo "数据快照/恢复 (发布前安全网):"
    echo "  SNAPSHOT_ON_RESTART=1     rebuild 前自动快照 (默认 1，设 0 关闭)"
    echo "  SNAPSHOT_RETAIN_DAYS=14   自动快照保留天数 (snapshot-keep-* 永久保留)"
    echo "  SNAPSHOT_DIR=PATH         快照目录 (默认 ${PORTAL_DIR}/backups)"
    echo "  SNAPSHOT_DB_CONTAINER=ai-portal-postgres  SNAPSHOT_DB_NAME=ai_proxy_portal"
    echo ""
    echo "  发布留底: ./start.sh snapshot            # -> backups/snapshot-keep-<UTC>.dump"
    echo "  验证恢复: ./start.sh restore backups/snapshot-keep-<UTC>.dump portal_check"
    echo ""
    echo "本地/外部数据库切换 (由 .env.go-platform 的 DB_HOST 推导):"
    echo "  DB_HOST=postgres                       本地 Docker 容器 (默认，拉起 ai-portal-postgres)"
    echo "  DB_HOST=<Aurora writer endpoint>       外部数据库 (不拉起本地 PG，Portal 直连端点)"
    echo "  切换后执行: ./start.sh rebuild          snapshot/restore 自动适配本地/外部"
    echo ""
    echo "示例:"
    echo "  $0 portal --app-base-url=https://portal.example.com"
    echo "  $0 proxy --portal-url=http://10.0.0.1:3001 --proxy-secret=<secret> --jwt-secret=<secret> --proxy-redis-password=<pwd>"
    echo "  $0 mvp --app-base-url=https://portal.example.com"
}

# ============================================
# 主流程
# ============================================
main() {
    # 检测系统环境
    detect_system_info
    
    show_info
    
    # 处理非交互式命令
    case "$RUN_MODE" in
        stop|stop-all)
            stop_all
            exit 0
            ;;
        stop-portal)
            stop_portal
            exit 0
            ;;
        stop-proxy)
            stop_proxy
            exit 0
            ;;
        status)
            show_status
            exit 0
            ;;
        logs)
            show_logs "$2"
            exit 0
            ;;
        clean)
            clean_config
            exit 0
            ;;
        clean-all)
            clean_all
            exit 0
            ;;
        build)
            build_all
            exit 0
            ;;
        build-portal)
            build_portal
            exit 0
            ;;
        build-proxy)
            build_proxy
            exit 0
            ;;
        rebuild)
            rebuild_services
            exit 0
            ;;
        snapshot)
            snapshot_db "--keep"
            exit 0
            ;;
        restore)
            restore_db "${POSITIONAL_ARGS[0]:-}" "${POSITIONAL_ARGS[1]:-}"
            exit $?
            ;;
        help|-h|--help)
            show_help
            exit 0
            ;;
    esac

    # 检查配置
    check_config

    # 选择模式
    select_mode

    # 生产/mvp 模式必须先配置强管理员密码
    validate_production_admin_password

    # 根据模式执行
    case "$RUN_MODE" in
        portal)
            build_portal
            start_portal
            ;;
        proxy)
            if [[ -z "${PROXY_DIR}" ]] || [[ ! -d "${PROXY_DIR}" ]]; then
                echo -e "${RED}✗ Proxy 目录不存在${NC}"
                exit 1
            fi
            build_proxy
            start_proxy
            ;;
        mvp|simulate)
            build_all
            start_all "$RUN_MODE"
            ;;
        *)
            show_help
            exit 1
            ;;
    esac
}

# 运行主流程
main
