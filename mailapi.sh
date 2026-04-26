#!/usr/bin/env bash
# =============================================================================
# MailAPI — One-click install, configure, and manage script
# =============================================================================
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"

# --------------- Paths (portable defaults) ---------------
# 默认安装目录：root 用 /opt/mailapi；非 root 用 $XDG_DATA_HOME 或 $HOME/.local/share。
DEFAULT_INSTALL_DIR="/opt/mailapi"
if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
    if [[ -n "${XDG_DATA_HOME:-}" ]]; then
        DEFAULT_INSTALL_DIR="${XDG_DATA_HOME%/}/mailapi"
    elif [[ -n "${HOME:-}" ]]; then
        DEFAULT_INSTALL_DIR="${HOME%/}/.local/share/mailapi"
    else
        DEFAULT_INSTALL_DIR="./.mailapi"
    fi
fi

INSTALL_DIR="${MAILAPI_DIR:-$DEFAULT_INSTALL_DIR}"
BIN_DIR="${MAILAPI_BIN_DIR:-$INSTALL_DIR/bin}"
LOG_DIR="${MAILAPI_LOG_DIR:-$INSTALL_DIR/logs}"
CONFIG_FILE="${MAILAPI_CONFIG:-$INSTALL_DIR/config.yaml}"
COMPOSE_FILE="${MAILAPI_COMPOSE:-$INSTALL_DIR/docker-compose.yml}"

ENV_FILE="${MAILAPI_ENV_FILE:-$INSTALL_DIR/infra.env}"

# Infra defaults（优先：显式环境变量；其次：ENV_FILE；最后：内置默认）
# 注意：这里先给出默认值，稍后会在 init_infra_env 中根据 ENV_FILE 再次归一化并 export。
INFRA_BIND_ADDR="${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}"
MINIO_ROOT_USER="${MAILAPI_MINIO_ROOT_USER:-minioadmin}"
MINIO_ROOT_PASSWORD="${MAILAPI_MINIO_ROOT_PASSWORD:-minioadmin}"
MONGO_IMAGE="${MAILAPI_MONGO_IMAGE:-mongo:7}"
REDIS_IMAGE="${MAILAPI_REDIS_IMAGE:-redis:7-alpine}"
NATS_IMAGE="${MAILAPI_NATS_IMAGE:-nats:2}"
MINIO_IMAGE="${MAILAPI_MINIO_IMAGE:-minio/minio:latest}"

# --------------- Colors ---------------
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
CYAN='\033[0;36m'; NC='\033[0m'

if [[ -n "${NO_COLOR:-}" || "${MAILAPI_NO_COLOR:-}" == "1" || ! -t 1 ]]; then
    RED=""; GREEN=""; YELLOW=""; CYAN=""; NC=""
fi

info()  { echo -e "${GREEN}[INFO]${NC}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# --------------- Utilities (bash 3.2 compatible) ---------------
lower() { echo "${1:-}" | tr '[:upper:]' '[:lower:]'; }

trim() {
    local s="${1:-}"
    # leading
    s="${s#"${s%%[![:space:]]*}"}"
    # trailing
    s="${s%"${s##*[![:space:]]}"}"
    echo "$s"
}

have_cmd() { command -v "${1:-}" &>/dev/null; }

is_tty() { [[ -t 0 && -t 1 ]]; }

is_noninteractive() {
    [[ "${MAILAPI_NONINTERACTIVE:-}" == "1" ]] && return 0
    is_tty || return 0
    return 1
}

load_infra_env_file() {
    # 安全加载 env 文件：只允许白名单 key，且不覆盖已显式设置的环境变量。
    # 该文件应只包含 KEY=VALUE（可选带引号）行，不执行任何代码。
    local f="${1:-}"
    [[ -n "$f" ]] || return 0
    [[ -f "$f" ]] || return 0

    local line key val
    while IFS= read -r line || [[ -n "$line" ]]; do
        # CRLF 兼容：去掉尾部 \r
        line="${line%$'\r'}"
        line="$(trim "$line")"
        [[ -z "$line" || "$line" == \#* ]] && continue

        if [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*(.*)$ ]]; then
            key="${BASH_REMATCH[1]}"
            val="${BASH_REMATCH[2]}"

            # 去掉一层引号（支持 "..." 或 '...'）
            if [[ "$val" =~ ^\".*\"$ ]]; then
                val="${val:1:${#val}-2}"
            elif [[ "$val" =~ ^\'.*\'$ ]]; then
                val="${val:1:${#val}-2}"
            fi

            case "$key" in
                MAILAPI_INFRA_BIND_ADDR)
                    [[ -z "${MAILAPI_INFRA_BIND_ADDR:-}" ]] && export MAILAPI_INFRA_BIND_ADDR="$val"
                    ;;
                MAILAPI_MINIO_ROOT_USER)
                    [[ -z "${MAILAPI_MINIO_ROOT_USER:-}" ]] && export MAILAPI_MINIO_ROOT_USER="$val"
                    ;;
                MAILAPI_MINIO_ROOT_PASSWORD)
                    [[ -z "${MAILAPI_MINIO_ROOT_PASSWORD:-}" ]] && export MAILAPI_MINIO_ROOT_PASSWORD="$val"
                    ;;
                MAILAPI_MONGO_IMAGE)
                    [[ -z "${MAILAPI_MONGO_IMAGE:-}" ]] && export MAILAPI_MONGO_IMAGE="$val"
                    ;;
                MAILAPI_REDIS_IMAGE)
                    [[ -z "${MAILAPI_REDIS_IMAGE:-}" ]] && export MAILAPI_REDIS_IMAGE="$val"
                    ;;
                MAILAPI_NATS_IMAGE)
                    [[ -z "${MAILAPI_NATS_IMAGE:-}" ]] && export MAILAPI_NATS_IMAGE="$val"
                    ;;
                MAILAPI_MINIO_IMAGE)
                    [[ -z "${MAILAPI_MINIO_IMAGE:-}" ]] && export MAILAPI_MINIO_IMAGE="$val"
                    ;;
            esac
        fi
    done <"$f"
}

init_infra_env() {
    # 解析 ENV_FILE，并把最终值同步到本脚本变量 + export（供 docker compose 使用）。
    load_infra_env_file "$ENV_FILE"

    INFRA_BIND_ADDR="${MAILAPI_INFRA_BIND_ADDR:-$INFRA_BIND_ADDR}"
    MINIO_ROOT_USER="${MAILAPI_MINIO_ROOT_USER:-$MINIO_ROOT_USER}"
    MINIO_ROOT_PASSWORD="${MAILAPI_MINIO_ROOT_PASSWORD:-$MINIO_ROOT_PASSWORD}"
    MONGO_IMAGE="${MAILAPI_MONGO_IMAGE:-$MONGO_IMAGE}"
    REDIS_IMAGE="${MAILAPI_REDIS_IMAGE:-$REDIS_IMAGE}"
    NATS_IMAGE="${MAILAPI_NATS_IMAGE:-$NATS_IMAGE}"
    MINIO_IMAGE="${MAILAPI_MINIO_IMAGE:-$MINIO_IMAGE}"

    export MAILAPI_INFRA_BIND_ADDR="$INFRA_BIND_ADDR"
    export MAILAPI_MINIO_ROOT_USER="$MINIO_ROOT_USER"
    export MAILAPI_MINIO_ROOT_PASSWORD="$MINIO_ROOT_PASSWORD"
    export MAILAPI_MONGO_IMAGE="$MONGO_IMAGE"
    export MAILAPI_REDIS_IMAGE="$REDIS_IMAGE"
    export MAILAPI_NATS_IMAGE="$NATS_IMAGE"
    export MAILAPI_MINIO_IMAGE="$MINIO_IMAGE"
}

init_infra_env

confirm_danger() {
    local prompt="${1:-Proceed?}"

    if is_noninteractive; then
        [[ "${MAILAPI_CONFIRM:-}" == "1" || "${MAILAPI_FORCE:-}" == "1" ]]
        return $?
    fi

    local ans=""
    read -rp "${prompt} [y/N] " ans
    [[ "$(lower "${ans:-}")" == "y" ]]
}

read_default() {
    local prompt="$1"
    local default="$2"

    if is_noninteractive; then
        echo "$default"
        return 0
    fi

    local ans=""
    read -rp "$prompt" ans
    echo "${ans:-$default}"
}

read_secret_default() {
    # 与 read_default 类似，但交互模式下不回显输入（适合密码/密钥）。
    local prompt="$1"
    local default="$2"

    if is_noninteractive; then
        echo "$default"
        return 0
    fi

    local ans=""
    read -rsp "$prompt" ans
    echo ""
    echo "${ans:-$default}"
}

normalize_yes() {
    local v
    v="$(lower "$(trim "${1:-}")")"
    case "$v" in
        y|yes|1|true|on) echo "y" ;;
        *) echo "n" ;;
    esac
}

is_number() {
    [[ "${1:-}" =~ ^[0-9]+$ ]]
}

validate_port() {
    local p="${1:-}"
    local name="${2:-port}"

    if ! is_number "$p"; then
        error "Invalid ${name}: $p (must be 1-65535)"
        return 1
    fi
    if ((p < 1 || p > 65535)); then
        error "Invalid ${name}: $p (must be 1-65535)"
        return 1
    fi
    return 0
}

random_hex() {
    local bytes="${1:-32}"

    if have_cmd openssl; then
        openssl rand -hex "$bytes"
        return 0
    fi

    if [[ -r /dev/urandom ]] && have_cmd od; then
        # od 输出可能包含空格/换行
        od -An -tx1 -N "$bytes" /dev/urandom | tr -d ' \n'
        return 0
    fi

    error "Cannot generate random bytes: need openssl or /dev/urandom+od"
    return 1
}

docker_compose() {
    # 兼容 Docker Compose V2（docker compose）与 V1（docker-compose）
    local env_args=()
    if [[ -f "$ENV_FILE" ]]; then
        env_args+=(--env-file "$ENV_FILE")
    fi
    if docker compose version &>/dev/null 2>&1; then
        docker compose "${env_args[@]}" -f "$COMPOSE_FILE" "$@"
        return 0
    fi
    if have_cmd docker-compose; then
        docker-compose "${env_args[@]}" -f "$COMPOSE_FILE" "$@"
        return 0
    fi

    error "Docker Compose not found: need 'docker compose' or 'docker-compose'"
    return 1
}

# --------------- Safety / IO helpers ---------------
resolve_abs() {
    # 兼容 macOS（无 realpath）/ Linux：用 dirname + pwd -P 解析绝对路径。
    # 注意：该函数允许目标不存在（只要其父目录存在）。
    local p="${1:-}"
    [[ -n "$p" ]] || return 1
    local d b
    d="$(cd "$(dirname "$p")" 2>/dev/null && pwd -P)" || return 1
    b="$(basename "$p")"
    printf "%s/%s\n" "$d" "$b"
}

path_is_under() {
    local child="$1" root="$2"
    [[ "$child" == "$root" || "$child" == "$root/"* ]]
}

assert_safe_path_under_install() {
    # 仅允许卸载删除 INSTALL_DIR 之下的路径，避免环境变量误配置导致误删系统目录。
    local p="${1:-}"
    [[ -n "$p" ]] || return 1

    local root abs home
    root="$(resolve_abs "$INSTALL_DIR")" || return 1
    abs="$(resolve_abs "$p")" || return 1

    # INSTALL_DIR 本身必须是“足够具体”的目录，禁止指向系统根目录或 HOME 根目录。
    case "$root" in
        ""|"/") return 1 ;;
        "/etc"|"/usr"|"/var"|"/bin"|"/sbin"|"/lib"|"/lib64") return 1 ;;
    esac
    home="${HOME:-}"
    if [[ -n "$home" ]]; then
        home="$(resolve_abs "$home" 2>/dev/null || echo "$home")"
        case "$root" in
            "$home"|"$home/") return 1 ;;
        esac
    fi

    case "$abs" in
        ""|"/") return 1 ;;
        "/etc"|"/usr"|"/var"|"/bin"|"/sbin"|"/lib"|"/lib64") return 1 ;;
    esac

    path_is_under "$abs" "$root"
}

write_file_atomic() {
    # 用法：write_file_atomic /path/to/file <<EOF ... EOF
    # 原子写入 + 默认收紧权限（用于含密钥的 config）。
    local target="${1:-}"
    [[ -n "$target" ]] || return 1

    local dir tmp
    dir="$(dirname "$target")"
    mkdir -p "$dir"

    umask 077
    tmp="$(mktemp_compat)"
    cat >"$tmp"
    mv -f "$tmp" "$target"
}

write_file_atomic_mode() {
    # 用法：write_file_atomic_mode /path/to/file 0644 <<EOF ... EOF
    local target="${1:-}"
    local mode="${2:-0644}"
    [[ -n "$target" ]] || return 1

    local dir tmp
    dir="$(dirname "$target")"
    mkdir -p "$dir"

    tmp="$(mktemp_compat)"
    cat >"$tmp"
    chmod "$mode" "$tmp" 2>/dev/null || true
    mv -f "$tmp" "$target"
}

backup_file() {
    local f="${1:-}"
    [[ -n "$f" ]] || return 0
    [[ -f "$f" ]] || return 0

    local ts
    ts="$(date +%Y%m%d%H%M%S 2>/dev/null || echo $$)"
    if cp -p "$f" "${f}.bak.${ts}" 2>/dev/null; then
        info "Backup created: ${f}.bak.${ts}"
        return 0
    fi
    if cp "$f" "${f}.bak.${ts}" 2>/dev/null; then
        info "Backup created: ${f}.bak.${ts}"
        return 0
    fi
    warn "Failed to create backup for: $f"
    return 1
}

write_infra_env_file() {
    # 把 infra 相关变量持久化到 ENV_FILE（包含 MinIO 凭证），便于跨会话保持一致。
    # 默认不覆盖已有文件；需要覆盖时传入 "1" 或设置 MAILAPI_ENV_OVERWRITE=1。
    local overwrite="${1:-0}"
    if [[ "${MAILAPI_ENV_OVERWRITE:-}" == "1" ]]; then
        overwrite="1"
    fi

    mkdir -p "$(dirname "$ENV_FILE")"

    if [[ -f "$ENV_FILE" ]] && [[ "$overwrite" != "1" ]]; then
        return 0
    fi
    if [[ -f "$ENV_FILE" ]]; then
        backup_file "$ENV_FILE" || true
    fi

    {
        echo "MAILAPI_INFRA_BIND_ADDR=$INFRA_BIND_ADDR"
        echo "MAILAPI_MINIO_ROOT_USER=$MINIO_ROOT_USER"
        echo "MAILAPI_MINIO_ROOT_PASSWORD=$MINIO_ROOT_PASSWORD"
        echo "MAILAPI_MONGO_IMAGE=$MONGO_IMAGE"
        echo "MAILAPI_REDIS_IMAGE=$REDIS_IMAGE"
        echo "MAILAPI_NATS_IMAGE=$NATS_IMAGE"
        echo "MAILAPI_MINIO_IMAGE=$MINIO_IMAGE"
    } | write_file_atomic "$ENV_FILE"

    info "Infra env written to $ENV_FILE"
}

ensure_infra_env_file() {
    [[ -f "$ENV_FILE" ]] && return 0
    write_infra_env_file 0
}

pid_matches_service() {
    # 避免 PID reuse 误杀无关进程：校验 pid 对应的 command 确实是本服务。
    local svc="$1" pid="$2"
    have_cmd ps || return 0
    local cmd
    cmd="$(ps -p "$pid" -o command= 2>/dev/null || true)"
    [[ -n "$cmd" ]] && [[ "$cmd" == *"/mailapi-${svc}"* ]]
}

with_service_lock() {
    # 进程内互斥：避免多次 start/stop 竞态（无 flock 时回退 mkdir 锁）。
    local svc="$1"; shift
    local lock_dir="${INSTALL_DIR%/}/.${svc}.lock"

    local i=0
    while ! mkdir "$lock_dir" 2>/dev/null; do
        i=$((i + 1))
        if ((i > 50)); then
            error "Failed to acquire lock for ${svc}: $lock_dir"
            return 1
        fi
        sleep 0.1
    done

    trap 'rmdir "$lock_dir" 2>/dev/null || true' RETURN
    "$@"
    local rc=$?
    trap - RETURN
    rmdir "$lock_dir" 2>/dev/null || true
    return $rc
}

retry() {
    # 用法：retry <attempts> <sleep_ms> -- <cmd...>
    local attempts="${1:-5}"
    local sleep_ms="${2:-200}"
    shift 2
    [[ "${1:-}" == "--" ]] && shift

    local i=1
    while true; do
        "$@" && return 0
        [[ $i -ge $attempts ]] && return 1
        # 兼容 bash：用整型运算拼出秒的小数表示，避免依赖 awk。
        local s=$((sleep_ms / 1000))
        local ms=$((sleep_ms % 1000))
        if ((ms == 0)); then
            sleep "$s"
        else
            sleep "$(printf "%d.%03d" "$s" "$ms")"
        fi
        sleep_ms=$((sleep_ms * 2))
        i=$((i + 1))
    done
}

probe_port() {
    local h="$1" p="$2"
    if have_cmd nc; then
        nc -z -w 1 "$h" "$p" &>/dev/null
        return $?
    fi
    # /dev/tcp 仅在 bash 下可用（本脚本要求 bash）。
    (exec 3<>"/dev/tcp/${h}/${p}") &>/dev/null
}

# =====================================================================
# help
# =====================================================================
show_help() {
    cat <<EOF
${CYAN}MailAPI Management Script${NC}

Usage: $0 <command> [args]

${CYAN}Setup:${NC}
  install                   Build binaries, generate config, and (optionally) start infra
  build                     Build Go binaries only
  config                    Generate or reconfigure config.yaml
  infra-up                  Start infrastructure (MongoDB, Redis, NATS, MinIO)
  infra-down                Stop infrastructure
  infra-clean               Stop infra and REMOVE volumes (DANGEROUS)

${CYAN}Service:${NC}
  start [service]           Start services (api|smtp|worker|all, default: all)
  stop [service]            Stop services (api|smtp|worker|all, default: all)
  restart [service]         Restart services (api|smtp|worker|all, default: all)
  status [service]          Show service status (api|smtp|worker|all, default: all)

${CYAN}Logs:${NC}
  logs [service] [lines]    Tail logs (default: all, 200 lines)

${CYAN}Ops:${NC}
  doctor|check              Diagnostics: env/deps/infra/services (+ debug health if enabled)
  upgrade [--pull|pull]     Rebuild binaries and restart services; with pull: pull & recreate infra
  uninstall [--purge]       Remove services/binaries (keep config by default; --purge also removes config & docker volumes)
  compose <args...>         Pass-through to docker compose using managed compose file
  setcap-smtp               (Linux) Grant mailapi-smtp cap_net_bind_service for binding :25 as non-root
  smoke                     Smoke test (debug healthz/readyz if enabled + key API flow)

${CYAN}Domain & Key:${NC}
  genkey [name] [prefix]    Generate an API key (prefix: sk or dk; default: sk)
  list-domains              List configured domains
  list-apikeys              List configured API keys (redacted)

${CYAN}Misc:${NC}
  help                      Show this help message

Environment (paths):
  MAILAPI_DIR                  Override install dir (default: /opt/mailapi or XDG_DATA_HOME)
  MAILAPI_BIN_DIR              Override binary dir
  MAILAPI_LOG_DIR              Override log dir
  MAILAPI_CONFIG               Override config file path
  MAILAPI_COMPOSE              Override docker-compose.yml path
  MAILAPI_ENV_FILE             Infra env file path (default: <INSTALL_DIR>/infra.env)
  MAILAPI_NO_COLOR=1           Disable ANSI colors (or set NO_COLOR)

Environment (infra):
  MAILAPI_SKIP_INFRA=1         Skip docker infra (no docker required)
  MAILAPI_INFRA_BIND_ADDR      Bind addr for infra ports (default: 127.0.0.1)
  MAILAPI_MINIO_ROOT_USER      MinIO root user
  MAILAPI_MINIO_ROOT_PASSWORD  MinIO root password
  MAILAPI_MONGO_IMAGE          Mongo image (default: mongo:7)
  MAILAPI_REDIS_IMAGE          Redis image (default: redis:7-alpine)
  MAILAPI_NATS_IMAGE           NATS image (default: nats:2)
  MAILAPI_MINIO_IMAGE          MinIO image (default: minio/minio:latest)
  MAILAPI_INFRA_WAIT_TIMEOUT   Wait seconds for infra ports (default: 30)

Environment (config, non-interactive):
  MAILAPI_NONINTERACTIVE=1     Disable all prompts (use defaults/env)
  MAILAPI_CONFIRM=1            Auto-confirm dangerous ops in non-interactive mode (or MAILAPI_FORCE=1)
  MAILAPI_CONFIG_OVERWRITE=1   Overwrite existing config in non-interactive mode
  MAILAPI_ENV_OVERWRITE=1      Overwrite existing infra.env when generating config
  MAILAPI_COMPOSE_OVERWRITE=1  Overwrite existing docker-compose.yml when generating compose
  MAILAPI_SERVICE_USER         systemd: set User= for services (optional; beware smtp :25)
  MAILAPI_SERVICE_GROUP        systemd: set Group= for services (default: same as user)
  MAILAPI_DOMAINS              Comma-separated domains (e.g. a.com,b.com)
  MAILAPI_PRIVATE_DOMAINS      Comma-separated private domains (optional; wildcard API keys will NOT implicitly include them)
  MAILAPI_API_PORT             API port (default: 8080)
  MAILAPI_SMTP_PORT            SMTP port (default: 25)
  MAILAPI_SMTP_DOMAIN          SMTP EHLO domain (default: mail.<first domain>)
  MAILAPI_SMTP_TLS_ENABLED      y/yes/1 to enable SMTP STARTTLS (RFC 3207)
  MAILAPI_SMTP_TLS_CERT_FILE    SMTP TLS cert file path (PEM; relative paths are relative to config.yaml)
  MAILAPI_SMTP_TLS_KEY_FILE     SMTP TLS key file path (PEM)
  MAILAPI_SMTP_TLS_REQUIRE_TLS  y/yes/1 to require STARTTLS before MAIL/RCPT/DATA (returns 530 otherwise)
  MAILAPI_SMTP_TLS_MIN_VERSION  TLS min version (1.2|1.3; default: 1.2)
  MAILAPI_SMTP_TLS_SELF_SIGNED  y/yes/1 to auto-generate self-signed cert/key when missing (requires openssl; default: y when TLS enabled)
  MAILAPI_CFWORKER_UPSTREAM     cfworker upstream URL (optional)
  MAILAPI_ENABLE_DIALECTS       y/yes/1 to enable dialect routing
  MAILAPI_BASE_HOST             baseHost when dialects enabled (default: api.<first domain>)
  MAILAPI_DEFAULT_DIALECT       defaultDialect (default: duck)
  MAILAPI_ENABLED_DIALECTS      enabledDialects CSV (default: duck,cfworker,yyds)
  MAILAPI_UNKNOWN_DIALECT       unknownDialect (reject|fallback; default: reject)
  MAILAPI_KEY_PREFIX            Default prefix for genkey (sk or dk)

Environment (smoke):
  MAILAPI_SMOKE_URL             Base URL for smoke test (default: http://127.0.0.1:<apiPort>)
  MAILAPI_SMOKE_DEBUG_URL       Debug base URL for health/metrics (default: from server.api.debug when enabled)
  MAILAPI_SMOKE_HOST            Optional Host header (useful for dialect routing tests)
  MAILAPI_SMOKE_API_KEY         API key for smoke (default: first key in config.yaml)
  MAILAPI_SMOKE_DOMAIN          Domain for smoke (default: first domain in config.yaml)
  MAILAPI_SMOKE_PASSWORD        Password for smoke-created account (optional; default: random)
  MAILAPI_SMOKE_TIMEOUT         Per-request timeout seconds (default: 10)
  MAILAPI_CURL_INSECURE=1       Pass -k to curl (skip TLS verification; use only for dev/testing)

EOF
}

# =====================================================================
# prerequisites
# =====================================================================
check_go() {
    if ! command -v go &>/dev/null; then
        error "Go is not installed. Please install Go from https://go.dev/dl/"
        return 1
    fi

    local required=""
    if [[ -f "$SRC_DIR/go.mod" ]]; then
        required=$(awk '/^go / {print $2; exit}' "$SRC_DIR/go.mod" 2>/dev/null || true)
    fi
    if [[ -n "$required" ]]; then
        info "Go $(go version | awk '{print $3}') (go.mod: go ${required})"
    else
        info "Go $(go version | awk '{print $3}')"
    fi
}

check_docker() {
    if ! command -v docker &>/dev/null; then
        error "Docker is not installed. Please install Docker: https://docs.docker.com/get-docker/"
        return 1
    fi
    info "Docker $(docker --version | awk '{print $3}' | tr -d ',')"

    # Compose V2 或 docker-compose
    if docker compose version &>/dev/null 2>&1; then
        info "Docker Compose: docker compose"
        :
    fi
    if have_cmd docker-compose; then
        info "Docker Compose: docker-compose"
        :
    fi

    if ! (docker compose version &>/dev/null 2>&1 || have_cmd docker-compose); then
        error "Docker Compose is required: install Docker Compose V2 (docker compose) or docker-compose"
        return 1
    fi

    # 尽早发现“docker 客户端已安装但 daemon 不可用/无权限”的场景。
    # 这些问题在 docker compose 阶段才暴露会更难定位。
    if ! docker version &>/dev/null 2>&1; then
        local hint=""
        case "$(lower "$(uname -s 2>/dev/null || echo unknown)")" in
            darwin) hint="Hint: On macOS, ensure Docker Desktop is running." ;;
            linux)
                if uname -r 2>/dev/null | tr '[:upper:]' '[:lower:]' | grep -q "microsoft"; then
                    hint="Hint: In WSL, ensure Docker Desktop integration is enabled, or start the docker daemon inside WSL."
                else
                    hint="Hint: On Linux, try: sudo systemctl start docker (and add your user to the docker group)."
                fi
                ;;
        esac
        error "Docker daemon is not reachable (docker version failed)."
        [[ -n "$hint" ]] && error "$hint"
        error "If you don't want docker infra, set: MAILAPI_SKIP_INFRA=1"
        return 1
    fi
    return 0
}

# =====================================================================
# build
# =====================================================================
do_build() {
    info "Building MailAPI binaries..."
    check_go

    mkdir -p "$BIN_DIR"

    (cd "$SRC_DIR" && go build -o "$BIN_DIR/mailapi-api"    ./cmd/api)
    info "Built mailapi-api"

    (cd "$SRC_DIR" && go build -o "$BIN_DIR/mailapi-smtp"   ./cmd/smtp)
    info "Built mailapi-smtp"

    (cd "$SRC_DIR" && go build -o "$BIN_DIR/mailapi-worker" ./cmd/worker)
    info "Built mailapi-worker"

    info "All binaries installed to $BIN_DIR"
}

# =====================================================================
# infrastructure (docker compose)
# =====================================================================
write_compose() {
    mkdir -p "$INSTALL_DIR"

    if [[ -f "$COMPOSE_FILE" ]] && [[ "${MAILAPI_COMPOSE_OVERWRITE:-}" != "1" ]]; then
        warn "Compose file already exists at $COMPOSE_FILE (set MAILAPI_COMPOSE_OVERWRITE=1 to overwrite)."
        return 0
    fi
    if [[ -f "$COMPOSE_FILE" ]]; then
        backup_file "$COMPOSE_FILE" || true
    fi

    write_file_atomic "$COMPOSE_FILE" <<'COMPOSE'
version: "3.8"
services:
  mongodb:
    image: ${MAILAPI_MONGO_IMAGE:-mongo:7}
    restart: unless-stopped
    ports: ["${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}:27017:27017"]
    volumes: ["mongo_data:/data/db"]

  redis:
    image: ${MAILAPI_REDIS_IMAGE:-redis:7-alpine}
    restart: unless-stopped
    ports: ["${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}:6379:6379"]

  nats:
    image: ${MAILAPI_NATS_IMAGE:-nats:2}
    restart: unless-stopped
    command: ["-js", "-sd", "/data"]
    ports: ["${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}:4222:4222"]
    volumes: ["nats_data:/data"]

  minio:
    image: ${MAILAPI_MINIO_IMAGE:-minio/minio:latest}
    restart: unless-stopped
    command: server /data --console-address ":9001"
    ports:
      - "${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}:9000:9000"
      - "${MAILAPI_INFRA_BIND_ADDR:-127.0.0.1}:9001:9001"
    environment:
      MINIO_ROOT_USER: ${MAILAPI_MINIO_ROOT_USER:-minioadmin}
      MINIO_ROOT_PASSWORD: ${MAILAPI_MINIO_ROOT_PASSWORD:-minioadmin}
    volumes: ["minio_data:/data"]

volumes:
  mongo_data:
  nats_data:
  minio_data:
COMPOSE
    info "Docker Compose file written to $COMPOSE_FILE"
}

do_infra_up() {
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        warn "MAILAPI_SKIP_INFRA=1, skipping infra-up"
        return 0
    fi

    check_docker
    ensure_infra_env_file

    if [[ ! -f "$COMPOSE_FILE" ]]; then
        write_compose
    fi
    info "Starting infrastructure containers..."
    docker_compose up -d

    # Wait for infra ports (best-effort)
    local timeout="${MAILAPI_INFRA_WAIT_TIMEOUT:-30}"
    local host="$INFRA_BIND_ADDR"
    if [[ "$host" == "0.0.0.0" || -z "$host" ]]; then
        host="127.0.0.1"
    fi

    wait_port() {
        local h="$1" p="$2" name="$3" t="${4:-30}"
        local end=$((SECONDS + t))
        while ((SECONDS < end)); do
            if probe_port "$h" "$p" &>/dev/null; then
                info "${name} is ready (${h}:${p})"
                return 0
            fi
            sleep 1
        done
        warn "${name} not ready after ${t}s (${h}:${p})"
        return 1
    }

    info "Waiting for infrastructure to be ready (timeout=${timeout}s)..."
    wait_port "$host" 27017 "MongoDB" "$timeout" || true
    wait_port "$host" 6379 "Redis" "$timeout" || true
    wait_port "$host" 4222 "NATS" "$timeout" || true
    wait_port "$host" 9000 "MinIO" "$timeout" || true

    info "Infrastructure is running"
}

do_infra_down() {
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        warn "MAILAPI_SKIP_INFRA=1, skipping infra-down"
        return 0
    fi

    check_docker
    ensure_infra_env_file

    if [[ ! -f "$COMPOSE_FILE" ]]; then
        warn "No docker-compose.yml found at $COMPOSE_FILE"
        return
    fi
    info "Stopping infrastructure containers..."
    docker_compose down
    info "Infrastructure stopped"
}

do_infra_clean() {
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        warn "MAILAPI_SKIP_INFRA=1, skipping infra-clean"
        return 0
    fi

    check_docker
    ensure_infra_env_file

    if [[ ! -f "$COMPOSE_FILE" ]]; then
        warn "No docker-compose.yml found at $COMPOSE_FILE"
        return
    fi

    warn "This will stop infra and REMOVE volumes (MongoDB/NATS/MinIO data will be deleted)."
    if ! confirm_danger "Proceed?"; then
        if is_noninteractive; then
            warn "Refusing to run infra-clean without confirmation in non-interactive mode."
            warn "Set MAILAPI_CONFIRM=1 to proceed."
        fi
        info "Cancelled."
        return 0
    fi

    info "Stopping infrastructure containers and removing volumes..."
    docker_compose down -v
    info "Infrastructure cleaned"
}

# =====================================================================
# config generation
# =====================================================================
generate_api_key() {
    # 32-byte random key (hex, 64 chars) with sk_ prefix by default.
    local prefix="${1:-${MAILAPI_KEY_PREFIX:-sk}}"
    prefix="$(lower "$(trim "$prefix")")"
    case "$prefix" in
        sk|sk_) prefix="sk_" ;;
        dk|dk_) prefix="dk_" ;;
        *) prefix="sk_" ;;
    esac
    echo "${prefix}$(random_hex 32)"
}

do_config() {
    mkdir -p "$INSTALL_DIR"
    local overwriting=0

    if [[ -f "$CONFIG_FILE" ]]; then
        warn "Config already exists at $CONFIG_FILE"
        if is_noninteractive; then
            if [[ "${MAILAPI_CONFIG_OVERWRITE:-}" == "1" ]]; then
                warn "MAILAPI_CONFIG_OVERWRITE=1, overwriting config."
                overwriting=1
            else
                info "Keeping existing config (set MAILAPI_CONFIG_OVERWRITE=1 to overwrite)."
                return 0
            fi
        else
            if ! confirm_danger "Overwrite config?"; then
                info "Keeping existing config."
                return 0
            fi
            overwriting=1
        fi
    fi

    echo ""
    echo -e "${CYAN}=== MailAPI Configuration ===${NC}"
    echo ""

    # Domains（支持 MAILAPI_DOMAINS=example.com,example.org 非交互模式）
    local domains=()
    if [[ -n "${MAILAPI_DOMAINS:-}" ]]; then
        IFS=',' read -r -a domains <<<"${MAILAPI_DOMAINS}"
    else
        if is_noninteractive; then
            domains=("example.com")
            warn "MAILAPI_DOMAINS is empty in non-interactive mode, using example.com"
        else
            while true; do
                read -rp "Add email domain (empty to finish): " dom
                dom="$(trim "$dom")"
                [[ -z "$dom" ]] && break
                domains+=("$dom")
            done
        fi
    fi

    # sanitize domains (trim, drop empties)
    local dom
    local cleaned=()
    for dom in "${domains[@]}"; do
        dom="$(trim "$dom")"
        [[ -z "$dom" ]] && continue
        cleaned+=("$dom")
    done
    domains=("${cleaned[@]}")

    if [[ ${#domains[@]} -eq 0 ]]; then
        domains=("example.com")
        warn "No domains entered, using example.com"
    fi

    # 私有域名（可选）：isPrivate=true 的域名不会被 wildcard（"*"）API key 隐式放开，必须显式列在 apiKeys[].domains 中。
    local private_domains_csv_default="${MAILAPI_PRIVATE_DOMAINS:-}"
    local private_domains_csv="$private_domains_csv_default"
    if ! is_noninteractive; then
        private_domains_csv="$(read_default "Private domains (comma-separated, empty=none) [${private_domains_csv_default}]: " "$private_domains_csv_default")"
    fi
    private_domains_csv="$(trim "$private_domains_csv")"

    # 归一化成 ",a.com,b.com," 形式，便于精确 contains 判断（避免部分匹配）。
    local private_domains_norm=","
    if [[ -n "$private_domains_csv" ]]; then
        local _pd
        local _priv_arr=()
        IFS=',' read -r -a _priv_arr <<<"$private_domains_csv"
        for _pd in "${_priv_arr[@]}"; do
            _pd="$(lower "$(trim "$_pd")")"
            [[ -z "$_pd" ]] && continue
            private_domains_norm+="${_pd},"
        done
    fi

    # API key
    local admin_key
    admin_key=$(generate_api_key)
    info "Generated admin API key: $admin_key"

    # JWT secret
    local jwt_secret
    jwt_secret=$(random_hex 32)

    # SMTP domain
    local smtp_domain_default="${MAILAPI_SMTP_DOMAIN:-mail.${domains[0]}}"
    smtp_domain="$(read_default "SMTP EHLO domain [${smtp_domain_default}]: " "$smtp_domain_default")"

    # API port
    local api_port_default="${MAILAPI_API_PORT:-8080}"
    api_port="$(read_default "API port [${api_port_default}]: " "$api_port_default")"

    # SMTP port
    local smtp_port_default="${MAILAPI_SMTP_PORT:-25}"
    smtp_port="$(read_default "SMTP port [${smtp_port_default}]: " "$smtp_port_default")"

    validate_port "$api_port" "API port"
    validate_port "$smtp_port" "SMTP port"

    # SMTP STARTTLS（可选）
    local smtp_tls_enabled="${MAILAPI_SMTP_TLS_ENABLED:-}"
    if [[ -z "$smtp_tls_enabled" ]]; then
        if is_noninteractive; then
            smtp_tls_enabled="n"
        else
            read -rp "Enable SMTP STARTTLS (RFC 3207)? [y/N] " smtp_tls_enabled
        fi
    fi
    smtp_tls_enabled="$(normalize_yes "$smtp_tls_enabled")"

    local smtp_tls_certFile="" smtp_tls_keyFile="" smtp_tls_require="n" smtp_tls_minVersion="1.2"
    if [[ "$smtp_tls_enabled" == "y" ]]; then
        local smtp_tls_cert_default="${MAILAPI_SMTP_TLS_CERT_FILE:-certs/smtp.crt}"
        local smtp_tls_key_default="${MAILAPI_SMTP_TLS_KEY_FILE:-certs/smtp.key}"
        local smtp_tls_require_default="${MAILAPI_SMTP_TLS_REQUIRE_TLS:-n}"
        local smtp_tls_min_default="${MAILAPI_SMTP_TLS_MIN_VERSION:-1.2}"

        smtp_tls_certFile="$(read_default "SMTP TLS cert file (PEM) [${smtp_tls_cert_default}]: " "$smtp_tls_cert_default")"
        smtp_tls_keyFile="$(read_default "SMTP TLS key file (PEM) [${smtp_tls_key_default}]: " "$smtp_tls_key_default")"
        smtp_tls_require="$(read_default "Require STARTTLS before MAIL/RCPT/DATA? [${smtp_tls_require_default}]: " "$smtp_tls_require_default")"
        smtp_tls_minVersion="$(read_default "SMTP TLS min version (1.2|1.3) [${smtp_tls_min_default}]: " "$smtp_tls_min_default")"

        smtp_tls_certFile="$(trim "$smtp_tls_certFile")"
        smtp_tls_keyFile="$(trim "$smtp_tls_keyFile")"
        smtp_tls_minVersion="$(trim "$smtp_tls_minVersion")"
        smtp_tls_require="$(normalize_yes "$smtp_tls_require")"

        if [[ -z "$smtp_tls_certFile" || -z "$smtp_tls_keyFile" ]]; then
            error "SMTP TLS is enabled but cert/key file is empty. Set MAILAPI_SMTP_TLS_CERT_FILE / MAILAPI_SMTP_TLS_KEY_FILE (or provide input)."
            return 1
        fi

        if [[ "$smtp_tls_minVersion" != "1.3" ]]; then
            smtp_tls_minVersion="1.2"
        fi

        # 若证书文件不存在：尝试按需生成自签名证书，保证一键安装可用（仅建议用于开发/测试）。
        # 注意：相对路径按 config.yaml 所在目录解析（与服务端一致）。
        local smtp_tls_self_signed="${MAILAPI_SMTP_TLS_SELF_SIGNED:-}"
        if [[ -z "$smtp_tls_self_signed" ]]; then
            if is_noninteractive; then
                smtp_tls_self_signed="y"
            else
                read -rp "Generate self-signed SMTP TLS cert/key if missing? [Y/n] " smtp_tls_self_signed
                [[ -z "$smtp_tls_self_signed" ]] && smtp_tls_self_signed="y"
            fi
        fi
        smtp_tls_self_signed="$(normalize_yes "$smtp_tls_self_signed")"

        local cfg_dir_abs
        cfg_dir_abs="$(cd "$(dirname "$CONFIG_FILE")" && pwd)"
        local cert_abs="$smtp_tls_certFile"
        local key_abs="$smtp_tls_keyFile"
        if [[ "$cert_abs" != /* ]]; then cert_abs="${cfg_dir_abs%/}/${cert_abs}"; fi
        if [[ "$key_abs" != /* ]]; then key_abs="${cfg_dir_abs%/}/${key_abs}"; fi

        if [[ ! -f "$cert_abs" || ! -f "$key_abs" ]]; then
            if [[ "$smtp_tls_self_signed" == "y" ]]; then
                if ! have_cmd openssl; then
                    error "SMTP TLS cert/key not found and openssl is not available to generate a self-signed certificate."
                    error "Missing: $cert_abs / $key_abs"
                    return 1
                fi
                mkdir -p "$(dirname "$cert_abs")" "$(dirname "$key_abs")"
                info "Generating self-signed SMTP TLS cert/key:"
                info "  cert: $cert_abs"
                info "  key:  $key_abs"
                openssl req -x509 -newkey rsa:2048 -sha256 -days 3650 -nodes \
                    -keyout "$key_abs" -out "$cert_abs" -subj "/CN=${smtp_domain}"
                chmod 600 "$key_abs" 2>/dev/null || true
            else
                error "SMTP TLS is enabled but cert/key file not found:"
                error "  cert: $cert_abs"
                error "  key:  $key_abs"
                error "Provide existing files or set MAILAPI_SMTP_TLS_SELF_SIGNED=1 to auto-generate."
                return 1
            fi
        fi
    fi

    # MinIO 凭证（会写入 config.yaml，同时写入 infra.env 供 docker compose 使用，确保两者一致）。
    local minio_user_default="$MINIO_ROOT_USER"
    local minio_pass_default="$MINIO_ROOT_PASSWORD"
    if [[ $overwriting -eq 1 ]]; then
        # 尝试从旧配置中提取，避免误改导致现有 MinIO 无法访问。
        local cfg_minio_user cfg_minio_pass
        cfg_minio_user="$(config_minio_access_key "$CONFIG_FILE")"
        cfg_minio_pass="$(config_minio_secret_key "$CONFIG_FILE")"
        [[ -n "$cfg_minio_user" ]] && minio_user_default="$cfg_minio_user"
        [[ -n "$cfg_minio_pass" ]] && minio_pass_default="$cfg_minio_pass"
    fi

    # 安全默认：若仍是弱口令且 env 未显式给出，则为“新配置”生成随机密码。
    if [[ "$minio_pass_default" == "minioadmin" ]] && [[ -z "${MAILAPI_MINIO_ROOT_PASSWORD:-}" ]] && [[ -z "${MAILAPI_MINIO_ROOT_USER:-}" ]]; then
        minio_pass_default="$(random_hex 16)"
    fi

    local minio_user minio_pass
    minio_user="$(read_default "MinIO root user [${minio_user_default}]: " "$minio_user_default")"
    minio_pass="$(read_secret_default "MinIO root password (input hidden, empty=default): " "$minio_pass_default")"

    MINIO_ROOT_USER="$(trim "$minio_user")"
    MINIO_ROOT_PASSWORD="$(trim "$minio_pass")"
    export MAILAPI_MINIO_ROOT_USER="$MINIO_ROOT_USER"
    export MAILAPI_MINIO_ROOT_PASSWORD="$MINIO_ROOT_PASSWORD"

    # 写入/更新 infra.env（包含 MinIO 凭证），保证 docker compose 与服务配置一致。
    write_infra_env_file 1

    # cfworker upstream（可选）
    local cfworker_upstream_default="${MAILAPI_CFWORKER_UPSTREAM:-}"
    cfworker_upstream="$(read_default "cfworker upstream URL (empty to skip) [${cfworker_upstream_default}]: " "$cfworker_upstream_default")"

    # API dialect 路由（可选）
    local enable_dialects="${MAILAPI_ENABLE_DIALECTS:-}"
    if [[ -z "$enable_dialects" ]]; then
        if is_noninteractive; then
            enable_dialects="n"
        else
            read -rp "Enable API dialect routing by subdomain? [y/N] " enable_dialects
        fi
    fi
    enable_dialects="$(normalize_yes "$enable_dialects")"

    local base_host=""
    local default_dialect="duck"
    local enabled_dialects_csv="duck,cfworker,yyds"
    local unknown_dialect="reject"
    if [[ "$enable_dialects" == "y" ]]; then
        local base_host_default="${MAILAPI_BASE_HOST:-api.${domains[0]}}"
        local default_dialect_default="${MAILAPI_DEFAULT_DIALECT:-duck}"
        local enabled_dialects_csv_default="${MAILAPI_ENABLED_DIALECTS:-duck,cfworker,yyds}"
        local unknown_dialect_default="${MAILAPI_UNKNOWN_DIALECT:-reject}"

        base_host="$(read_default "API baseHost [${base_host_default}]: " "$base_host_default")"
        default_dialect="$(read_default "Default dialect [${default_dialect_default}]: " "$default_dialect_default")"
        enabled_dialects_csv="$(read_default "Enabled dialects (comma-separated) [${enabled_dialects_csv_default}]: " "$enabled_dialects_csv_default")"
        unknown_dialect="$(read_default "Unknown dialect strategy (reject|fallback) [${unknown_dialect_default}]: " "$unknown_dialect_default")"
    fi

    base_host="$(trim "$base_host")"
    default_dialect="$(lower "$(trim "$default_dialect")")"
    unknown_dialect="$(lower "$(trim "$unknown_dialect")")"
    if [[ "$unknown_dialect" != "fallback" ]]; then
        unknown_dialect="reject"
    fi

    local enabled_dialects_yaml="[]"
    if [[ "$enable_dialects" == "y" ]]; then
        enabled_dialects_yaml="["
        local first=1
        local dname
        local dialect_arr=()
        IFS=',' read -r -a dialect_arr <<<"$enabled_dialects_csv"
        for dname in "${dialect_arr[@]}"; do
            dname="$(lower "$(trim "$dname")")"
            [[ -z "$dname" ]] && continue
            if [[ $first -eq 0 ]]; then
                enabled_dialects_yaml+=", "
            fi
            first=0
            enabled_dialects_yaml+="\"$dname\""
        done
        enabled_dialects_yaml+="]"
    fi

    local smtp_tls_enabled_bool="false"
    local smtp_tls_require_bool="false"
    if [[ "$smtp_tls_enabled" == "y" ]]; then
        smtp_tls_enabled_bool="true"
        [[ "$smtp_tls_require" == "y" ]] && smtp_tls_require_bool="true"
    fi

    # Write config
    if [[ -f "$CONFIG_FILE" ]]; then
        backup_file "$CONFIG_FILE" || true
    fi
    {
        echo "domains:"
        for d in "${domains[@]}"; do
            local d_lc is_private
            d_lc="$(lower "$(trim "$d")")"
            is_private="false"
            if [[ "$private_domains_norm" == *",$d_lc,"* ]]; then
                is_private="true"
            fi
            cat <<EOF
  - domain: "$d"
    isActive: true
    isPrivate: ${is_private}
    ips: []
EOF
        done

        cat <<EOF

apiKeys:
  - key: "$admin_key"
    name: "Admin"
    domains: ["*"]
    #defaultDomain: "example.com"
    #defaultSubdomain: "mail"
    rpmLimit: 0

rateLimit:
  global: 100

server:
  api:
    host: "0.0.0.0"
    port: $api_port
    # bcrypt 并发闸门：创建账号/登录会触发 bcrypt（CPU 密集），高并发时建议限制并发避免 CPU 打满导致整体雪崩
    maxConcurrentBcrypt: 0
    # 可信代理（影响 ClientIP 解析与 IP 限流）。生产环境强烈建议显式配置，避免被伪造 X-Forwarded-For 绕过。
    #trustedProxies: ["127.0.0.1", "10.0.0.0/8"]
    # 访问日志（高并发强烈建议开启采样/仅错误/仅慢请求，否则日志格式化与 IO 会成为 CPU 热点）
    accessLog:
      enabled: true
      sampleEvery: 100
      slowThreshold: 500ms
      errorsOnly: false
    # Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1。
    # 注意：debug server 不做鉴权，若暴露到公网可能泄露运行时信息。
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6060
      metrics: true
      pprof: false
EOF
        if [[ -n "$base_host" ]]; then
            cat <<EOF
    # 多 API 风格（dialect）路由：通过 \`<dialect>.<baseHost>\` 的子域前缀选择不同 API 兼容层。
    baseHost: "$base_host"
    defaultDialect: "$default_dialect"
    enabledDialects: $enabled_dialects_yaml
    unknownDialect: "$unknown_dialect"
EOF
        else
            cat <<EOF
    # 多 API 风格（dialect）路由：通过 \`<dialect>.<baseHost>\` 的子域前缀选择不同 API 兼容层。
    #baseHost: "api.mailapi.com"
    #defaultDialect: "duck"
    #enabledDialects: ["duck", "cfworker", "yyds"]
    #unknownDialect: "reject"
EOF
        fi

        cat <<EOF
  smtp:
    port: $smtp_port
    host: "0.0.0.0"
    domain: "$smtp_domain"
    maxMessageBytes: 20971520
    maxRecipients: 50
    readTimeout: 60s
    writeTimeout: 60s
    tls:
      enabled: ${smtp_tls_enabled_bool}
      certFile: "${smtp_tls_certFile}"
      keyFile: "${smtp_tls_keyFile}"
      requireTLS: ${smtp_tls_require_bool}
      minVersion: "${smtp_tls_minVersion}"
    # Debug HTTP server（健康检查/指标/pprof）。默认关闭；建议仅绑定 127.0.0.1。
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6061
      metrics: true
      pprof: false

  # Worker 没有业务监听端口，但可以开启 debug HTTP server 用于 ready/metrics/pprof。
  worker:
    debug:
      enabled: false
      host: "127.0.0.1"
      port: 6062
      metrics: true
      pprof: false

mongodb:
  uri: "mongodb://localhost:27017"
  database: "mailapi"
  maxPoolSize: 0
  minPoolSize: -1
  maxConnecting: 0
  connectTimeout: 10s
  serverSelectionTimeout: 10s
  maxConnIdleTime: 5m
  appName: "mailapi"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0

nats:
  url: "nats://localhost:4222"
  stream: "EMAILS"
  subject: "emails.incoming"
  consumerAckWait: 30s
  consumerMaxDeliver: 3

minio:
  endpoint: "localhost:9000"
  accessKey: "$MINIO_ROOT_USER"
  secretKey: "$MINIO_ROOT_PASSWORD"
  bucket: "attachments"
  useSSL: false

dialects:
  cfworker:
    upstream: "$cfworker_upstream"
    timeout: 15s
  yyds:
    publicBaseURL: ""
    plans: []
    pricing:
      currency:
        code: "CNY"
        suffix: ""
        symbol: "¥"
      packages: []
      rateLimits: []
    domainReward:
      creditExpireDays: 0
      creditsPerCycle: 0
      runHour: 0
      usagePerCredit: 0
    stats:
      totalUsers: 0
      totalDomains: 0
      verifiedDomains: 0
      publicDomains: 0
      totalInboxes: 0
      anonInboxes: 0
      totalStoredMessages: 0
      totalHistoricalMessages: 0
      totalCreatedInboxes: 0
      totalMessages: 0
      todayApiCalls: 0
      topDomains: []
      hourlyActivity: []
      dailyTrend: []

jwt:
  secret: "$jwt_secret"
  expiry: 1h

account:
  ttl: 168h

message:
  ttl: 168h
EOF
    } | write_file_atomic "$CONFIG_FILE"

    info "Config written to $CONFIG_FILE"
    echo ""
    echo -e "${YELLOW}Important: Save your admin API key:${NC}"
    echo -e "  ${GREEN}$admin_key${NC}"
    echo ""
}

# =====================================================================
# systemd services
# =====================================================================
install_services() {
    info "Installing systemd service files..."
    mkdir -p "$LOG_DIR"

    local want_docker=1
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        want_docker=0
    fi

    local service_user="${MAILAPI_SERVICE_USER:-}"
    local service_group="${MAILAPI_SERVICE_GROUP:-$service_user}"

    local svc
    for svc in api smtp worker; do
        local unit="/etc/systemd/system/mailapi-${svc}.service"

        [[ -f "$unit" ]] && backup_file "$unit" || true

        {
            cat <<EOF
[Unit]
Description=MailAPI ${svc}
After=network-online.target
Wants=network-online.target
EOF

            if [[ $want_docker -eq 1 ]]; then
                cat <<EOF
After=docker.service
Wants=docker.service
EOF
            fi

            cat <<EOF

[Service]
Type=simple
WorkingDirectory=$INSTALL_DIR
ExecStart=$BIN_DIR/mailapi-${svc} --config $CONFIG_FILE
Restart=on-failure
RestartSec=5
LimitNOFILE=65535
EOF
            if [[ -n "$service_user" ]]; then
                echo "User=$service_user"
                [[ -n "$service_group" ]] && echo "Group=$service_group"
            fi

            cat <<EOF

[Install]
WantedBy=multi-user.target
EOF
        } | write_file_atomic_mode "$unit" 0644
    done

    systemctl daemon-reload
    info "Systemd services installed (mailapi-api, mailapi-smtp, mailapi-worker)"

    if [[ -n "$service_user" ]]; then
        warn "MAILAPI_SERVICE_USER is set ($service_user). If SMTP binds to port 25, ensure either:"
        warn "  - Run smtp service as root (do not set MAILAPI_SERVICE_USER), or"
        warn "  - Use setcap on $BIN_DIR/mailapi-smtp (Linux): $0 setcap-smtp, or"
        warn "  - Change server.smtp.port to 2525+ in config.yaml"
    fi
}

# =====================================================================
# install (full)
# =====================================================================
do_install() {
    info "=== MailAPI Full Installation ==="
    echo ""

    check_go
    if [[ "${MAILAPI_SKIP_INFRA:-}" != "1" ]]; then
        check_docker
    else
        warn "MAILAPI_SKIP_INFRA=1 — skipping docker checks (infra will not be started by this script)."
    fi

    mkdir -p "$INSTALL_DIR" "$BIN_DIR" "$LOG_DIR"

    # Build
    do_build

    # Docker Compose
    write_compose

    # Config
    if [[ ! -f "$CONFIG_FILE" ]]; then
        do_config
    else
        info "Using existing config at $CONFIG_FILE"
    fi

    # Infrastructure
    do_infra_up

    # Systemd services
    if [[ $EUID -eq 0 ]]; then
        if systemd_running; then
            install_services
        else
            warn "systemd is not available/running — skipping systemd service installation."
            warn "Start manually (background mode):"
            echo "  $BIN_DIR/mailapi-api    $CONFIG_FILE"
            echo "  $BIN_DIR/mailapi-smtp   $CONFIG_FILE"
            echo "  $BIN_DIR/mailapi-worker $CONFIG_FILE"
        fi
    else
        warn "Not running as root — skipping systemd service installation."
        warn "Run with sudo to install systemd services, or start manually:"
        echo "  $BIN_DIR/mailapi-api    $CONFIG_FILE"
        echo "  $BIN_DIR/mailapi-smtp   $CONFIG_FILE"
        echo "  $BIN_DIR/mailapi-worker $CONFIG_FILE"
    fi

    echo ""
    info "=== Installation complete ==="
    echo ""
    echo "Next steps:"
    echo "  1. Edit config:    $CONFIG_FILE"
    echo "  2. Start services: $0 start"
    echo "  3. Check status:   $0 status"
    echo ""
}

# =====================================================================
# start / stop / restart / status
# =====================================================================
is_systemd() {
    [[ "${EUID:-$(id -u)}" -eq 0 ]] && have_cmd systemctl && systemd_running && systemctl list-unit-files mailapi-api.service &>/dev/null 2>&1
}

systemd_running() {
    have_cmd systemctl || return 1
    local st
    st="$(systemctl is-system-running 2>/dev/null || true)"
    [[ "$st" == "running" || "$st" == "degraded" ]]
}

resolve_config_file() {
    local cfg="$CONFIG_FILE"
    if [[ -f "$cfg" ]]; then
        echo "$cfg"
        return 0
    fi
    if [[ -f "$SRC_DIR/config.yaml" ]]; then
        warn "Config not found at $cfg, using source default: $SRC_DIR/config.yaml" >&2
        echo "$SRC_DIR/config.yaml"
        return 0
    fi
    error "No config file found: $cfg (or $SRC_DIR/config.yaml)"
    return 1
}

ensure_binary() {
    local svc="$1"
    local bin="$BIN_DIR/mailapi-${svc}"
    if [[ ! -x "$bin" ]]; then
        error "Binary not found or not executable: $bin"
        error "Run: $0 build  (or $0 install)"
        return 1
    fi
}

try_ulimit_nofile() {
    local target="${1:-65535}"
    if ulimit -n "$target" &>/dev/null; then
        info "ulimit -n $target"
        return 0
    fi
    warn "Failed to set ulimit -n $target (current: $(ulimit -n 2>/dev/null || echo unknown))"
    return 0
}

start_bg_service() {
    local svc="$1" cfg="$2"
    local bin="$BIN_DIR/mailapi-${svc}"
    local pidfile="$INSTALL_DIR/${svc}.pid"
    local logfile="$LOG_DIR/${svc}.log"

    mkdir -p "$INSTALL_DIR" "$LOG_DIR"

    if [[ -f "$pidfile" ]]; then
        local oldpid
        oldpid=$(cat "$pidfile" 2>/dev/null || true)
        if [[ -n "$oldpid" ]] && kill -0 "$oldpid" 2>/dev/null; then
            if pid_matches_service "$svc" "$oldpid"; then
                warn "$svc already running (PID $oldpid)"
                return 0
            fi
            warn "$svc pidfile points to PID $oldpid but command does not match mailapi-${svc}; treating as stale."
        fi
        rm -f "$pidfile" 2>/dev/null || true
    fi

    ensure_binary "$svc"

    if have_cmd nohup; then
        nohup "$bin" "$cfg" >>"$logfile" 2>&1 &
    else
        "$bin" "$cfg" >>"$logfile" 2>&1 &
    fi

    local pid=$!
    echo "$pid" >"$pidfile"
    sleep 0.3
    if ! kill -0 "$pid" 2>/dev/null; then
        warn "$svc failed to start (see $logfile)"
        rm -f "$pidfile"
        tail -n 50 "$logfile" 2>/dev/null || true
        return 1
    fi
    info "Started $svc (PID $pid)"
}

stop_bg_service() {
    local svc="$1"
    local pidfile="$INSTALL_DIR/${svc}.pid"

    if [[ ! -f "$pidfile" ]]; then
        warn "$svc pidfile not found: $pidfile"
        return 0
    fi

    local pid
    pid=$(cat "$pidfile" 2>/dev/null || true)
    if [[ -z "$pid" ]]; then
        rm -f "$pidfile"
        warn "$svc pidfile empty: $pidfile"
        return 0
    fi

    if ! kill -0 "$pid" 2>/dev/null; then
        warn "$svc (PID $pid) not running"
        rm -f "$pidfile"
        return 0
    fi

    if ! pid_matches_service "$svc" "$pid"; then
        warn "$svc pidfile points to PID $pid but command does not match mailapi-${svc}; refusing to kill."
        # 避免卡住后续运维：把 pidfile 标记为 stale（尽力而为）。
        local ts
        ts="$(date +%Y%m%d%H%M%S 2>/dev/null || echo $$)"
        mv -f "$pidfile" "${pidfile}.stale.${ts}" 2>/dev/null || rm -f "$pidfile" 2>/dev/null || true
        return 1
    fi

    kill "$pid" 2>/dev/null || true
    local end=$((SECONDS + 10))
    while kill -0 "$pid" 2>/dev/null && ((SECONDS < end)); do
        sleep 0.2
    done

    if kill -0 "$pid" 2>/dev/null; then
        warn "$svc did not stop in time, forcing kill -9 (PID $pid)"
        kill -9 "$pid" 2>/dev/null || true
    fi

    rm -f "$pidfile"
    info "Stopped $svc (PID $pid)"
}

do_start() {
    local target="${1:-all}"
    local cfg
    cfg="$(resolve_config_file)"

    case "$target" in
    all|api|smtp|worker) ;;
    *)
        error "Invalid service: $target (use all|api|smtp|worker)"
        return 1
        ;;
    esac

    if is_systemd; then
        info "Starting MailAPI via systemd ($target)..."
        do_infra_up
        if [[ "$target" == "all" ]]; then
            systemctl start mailapi-api mailapi-smtp mailapi-worker
        else
            systemctl start "mailapi-${target}"
        fi
        info "Start command sent"
        return 0
    fi

    do_infra_up
    info "Starting MailAPI in background ($target)..."
    try_ulimit_nofile 65535

    if [[ "${EUID:-$(id -u)}" -ne 0 ]] && [[ "$target" == "smtp" || "$target" == "all" ]]; then
        warn "SMTP on ports <1024 requires root or setcap. If smtp fails, consider:"
        warn "  - Change server.smtp.port to 2525+ in config.yaml"
        warn "  - Or run: sudo setcap 'cap_net_bind_service=+ep' ${BIN_DIR}/mailapi-smtp (Linux)"
    fi

    if [[ "$target" == "all" ]]; then
        with_service_lock api start_bg_service api "$cfg"
        with_service_lock smtp start_bg_service smtp "$cfg"
        with_service_lock worker start_bg_service worker "$cfg"
    else
        with_service_lock "$target" start_bg_service "$target" "$cfg"
    fi
}

do_stop() {
    local target="${1:-all}"

    case "$target" in
    all|api|smtp|worker) ;;
    *)
        error "Invalid service: $target (use all|api|smtp|worker)"
        return 1
        ;;
    esac

    if is_systemd; then
        info "Stopping MailAPI via systemd ($target)..."
        if [[ "$target" == "all" ]]; then
            systemctl stop mailapi-api mailapi-smtp mailapi-worker 2>/dev/null || true
        else
            systemctl stop "mailapi-${target}" 2>/dev/null || true
        fi
        info "Stop command sent"
        return 0
    fi

    info "Stopping MailAPI background services ($target)..."
    if [[ "$target" == "all" ]]; then
        with_service_lock worker stop_bg_service worker
        with_service_lock smtp stop_bg_service smtp
        with_service_lock api stop_bg_service api
    else
        with_service_lock "$target" stop_bg_service "$target"
    fi
    info "Stop complete"
}

do_restart() {
    local target="${1:-all}"
    do_stop "$target"
    sleep 1
    do_start "$target"
}

do_status() {
    local target="${1:-all}"

    case "$target" in
    all|api|smtp|worker) ;;
    *)
        error "Invalid service: $target (use all|api|smtp|worker)"
        return 1
        ;;
    esac

    echo -e "${CYAN}=== MailAPI Status ===${NC}"
    echo ""

    # Infrastructure
    echo -e "${CYAN}Infrastructure:${NC}"
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        echo "  (skipped: MAILAPI_SKIP_INFRA=1)"
    elif [[ -f "$COMPOSE_FILE" ]]; then
        if have_cmd docker; then
            if docker_compose ps --format "table {{.Name}}\t{{.Status}}\t{{.Ports}}" 2>/dev/null; then
                :
            else
                docker_compose ps 2>/dev/null || warn "Cannot read Docker status"
            fi
        else
            warn "Docker not found"
        fi
    else
        warn "No docker-compose.yml found at $COMPOSE_FILE"
    fi
    echo ""

    # Services
    echo -e "${CYAN}Services:${NC}"
    local svcs=(api smtp worker)
    if [[ "$target" != "all" ]]; then
        svcs=("$target")
    fi

    if is_systemd; then
        local svc
        for svc in "${svcs[@]}"; do
            local status
            status=$(systemctl is-active "mailapi-${svc}" 2>/dev/null || echo "not installed")
            if [[ "$status" == "active" ]]; then
                echo -e "  mailapi-${svc}: ${GREEN}$status${NC}"
            else
                echo -e "  mailapi-${svc}: ${RED}$status${NC}"
            fi
        done
    else
        local svc
        for svc in "${svcs[@]}"; do
            local pidfile="$INSTALL_DIR/${svc}.pid"
            if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
                echo -e "  mailapi-${svc}: ${GREEN}running${NC} (PID $(cat "$pidfile"))"
            else
                echo -e "  mailapi-${svc}: ${RED}stopped${NC}"
            fi
        done
    fi
    echo ""
}

# =====================================================================
# logs
# =====================================================================
do_logs() {
    local svc="${1:-all}"
    local lines="${2:-200}"

    if [[ "$svc" =~ ^[0-9]+$ ]]; then
        lines="$svc"
        svc="all"
    fi

    if [[ ! "$lines" =~ ^[0-9]+$ ]]; then
        lines="200"
    fi

    case "$svc" in
    all|api|smtp|worker) ;;
    *)
        error "Invalid service: $svc (use all|api|smtp|worker)"
        return 1
        ;;
    esac

    if is_systemd && have_cmd journalctl; then
        info "Tailing systemd journal (last ${lines} lines)..."
        if [[ "$svc" == "all" ]]; then
            journalctl --no-pager -n "$lines" -f -u mailapi-api -u mailapi-smtp -u mailapi-worker || true
        else
            journalctl --no-pager -n "$lines" -f -u "mailapi-${svc}" || true
        fi
        return 0
    fi

    if [[ "$svc" == "all" ]]; then
        tail -n "$lines" -f "$LOG_DIR/api.log" "$LOG_DIR/smtp.log" "$LOG_DIR/worker.log" 2>/dev/null || \
            warn "No log files found in $LOG_DIR"
    else
        tail -n "$lines" -f "$LOG_DIR/${svc}.log" 2>/dev/null || \
            warn "No log file found: $LOG_DIR/${svc}.log"
    fi
}

# =====================================================================
# domain & key helpers
# =====================================================================
do_genkey() {
    local name="${1:-Unnamed}"
    local prefix="${2:-}"
    local key
    key=$(generate_api_key "${prefix:-}")
    echo ""
    echo -e "${CYAN}Generated API Key:${NC}"
    echo -e "  Name: $name"
    echo -e "  Key:  ${GREEN}$key${NC}"
    echo ""
    echo "Add to your config.yaml:"
    echo ""
    echo "  - key: \"$key\""
    echo "    name: \"$name\""
    echo "    domains: [\"*\"]"
    echo ""
}

do_list_domains() {
    local cfg="${CONFIG_FILE}"
    if [[ ! -f "$cfg" ]]; then
        cfg="$SRC_DIR/config.yaml"
    fi
    if [[ ! -f "$cfg" ]]; then
        error "No config file found"
        return 1
    fi

    echo -e "${CYAN}Configured Domains:${NC}"
    # 轻量 YAML 解析（仅提取 domains[*].domain），避免 grep/sed 的 \s 兼容性问题（macOS/BSD 也能跑）。
    awk '
      /^domains:/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk && $0 ~ /^[[:space:]]*-?[[:space:]]*domain:/ {
        line=$0
        sub(/.*domain:[[:space:]]*/, "", line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print "  " line; n++ }
      }
      END { if (n==0) exit 1 }
    ' "$cfg" || echo "  (none)"
    echo ""
}

do_list_apikeys() {
    local cfg="${CONFIG_FILE}"
    if [[ ! -f "$cfg" ]]; then
        cfg="$SRC_DIR/config.yaml"
    fi
    if [[ ! -f "$cfg" ]]; then
        error "No config file found"
        return 1
    fi

    echo -e "${CYAN}Configured API Keys:${NC}"
    # 轻量 YAML 解析（仅提取 apiKeys 列表里的 key/name/domains），避免 grep/sed 的 \s 兼容性问题。
    awk '
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      function unquote(s) { s=trim(s); sub(/^"/, "", s); sub(/"$/, "", s); return s }
      /^apiKeys:/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk {
        if ($0 ~ /^[[:space:]]*-?[[:space:]]*key:/) {
          line=$0; sub(/.*key:[[:space:]]*/, "", line); key=unquote(line)
          red=key
          if (length(key) > 6) red=substr(key,1,6) "..."
          printf "  Key: %s  ", red
          n=1
        } else if ($0 ~ /^[[:space:]]*name:/) {
          line=$0; sub(/.*name:[[:space:]]*/, "", line); name=unquote(line)
          printf "Name: %s  ", name
        } else if ($0 ~ /^[[:space:]]*domains:/) {
          line=$0; sub(/.*domains:[[:space:]]*/, "", line); doms=trim(line)
          printf "Domains: %s\n", doms
        }
      }
      END { if (n==0) exit 1 }
    ' "$cfg" || echo "  (none)"
    echo ""
}

# =====================================================================
# ops helpers (config parsing / http / json)
# =====================================================================
config_first_domain() {
    local cfg="$1"
    awk '
      /^domains:/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk && $0 ~ /^[[:space:]]*-?[[:space:]]*domain:/ {
        line=$0
        sub(/.*domain:[[:space:]]*/, "", line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_first_apikey() {
    local cfg="$1"
    awk '
      /^apiKeys:/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk && $0 ~ /^[[:space:]]*-?[[:space:]]*key:/ {
        line=$0
        sub(/.*key:[[:space:]]*/, "", line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_minio_access_key() {
    # 从 config.yaml 提取 minio.accessKey（最小解析器）。
    local cfg="$1"
    awk '
      /^minio:[[:space:]]*$/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk && $0 ~ /^[[:space:]]*accessKey:/ {
        line=$0
        sub(/.*accessKey:[[:space:]]*/, "", line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_minio_secret_key() {
    # 从 config.yaml 提取 minio.secretKey（最小解析器）。
    local cfg="$1"
    awk '
      /^minio:[[:space:]]*$/ {inblk=1; next}
      inblk && /^[^[:space:]]/ {inblk=0}
      inblk && $0 ~ /^[[:space:]]*secretKey:/ {
        line=$0
        sub(/.*secretKey:[[:space:]]*/, "", line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_server_port() {
    # 用途：从 config.yaml 提取 server.<section>.port（section=api|smtp）。
    # 注意：这是“最小解析器”，假设结构类似脚本生成的 config.yaml。
    local cfg="$1"
    local section="$2"
    awk -v sec="$section" '
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      BEGIN {in_server=0; in_sec=0}
      /^server:[[:space:]]*$/ {in_server=1; in_sec=0; next}
      in_server && /^[^[:space:]]/ {in_server=0; in_sec=0}
      in_server && $0 ~ "^[[:space:]]*" sec ":[[:space:]]*$" {in_sec=1; next}
      in_sec && /^[^[:space:]]/ {in_sec=0}
      in_sec && $0 ~ /^[[:space:]]*port:/ {
        line=$0
        sub(/.*port:[[:space:]]*/, "", line)
        line=trim(line)
        gsub(/"/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_server_host() {
    # 从 config.yaml 提取 server.api.host
    local cfg="$1"
    local section="$2"
    awk -v sec="$section" '
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      BEGIN {in_server=0; in_sec=0}
      /^server:[[:space:]]*$/ {in_server=1; in_sec=0; next}
      in_server && /^[^[:space:]]/ {in_server=0; in_sec=0}
      in_server && $0 ~ "^[[:space:]]*" sec ":[[:space:]]*$" {in_sec=1; next}
      in_sec && /^[^[:space:]]/ {in_sec=0}
      in_sec && $0 ~ /^[[:space:]]*host:/ {
        line=$0
        sub(/.*host:[[:space:]]*/, "", line)
        line=trim(line)
        sub(/^"/, "", line)
        sub(/"$/, "", line)
        if (line != "") { print line; exit }
      }
    ' "$cfg" 2>/dev/null || true
}

config_server_debug_enabled() {
    # 从 config.yaml 提取 server.<section>.debug.enabled（section=api|smtp|worker）。
    local cfg="$1"
    local section="$2"
    awk -v sec="$section" '
      function lead(s) { match(s, /^[[:space:]]*/); return RLENGTH }
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      BEGIN { in_server=0; in_sec=0; in_dbg=0; serverIndent=-1; secIndent=-1; dbgIndent=-1 }
      /^[[:space:]]*#/ { next }
      /^[[:space:]]*$/ { next }
      {
        ind = lead($0)
        line = $0

        if (!in_server) {
          if (line ~ /^server:[[:space:]]*$/) { in_server=1; serverIndent=ind; next }
          next
        }

        # leaving server block
        if (ind <= serverIndent && line !~ /^server:[[:space:]]*$/) { exit }

        if (!in_sec) {
          if (line ~ "^[[:space:]]*" sec ":[[:space:]]*$") { in_sec=1; secIndent=ind; in_dbg=0; next }
          next
        }

        # leaving section block
        if (ind <= secIndent && line !~ "^[[:space:]]*" sec ":[[:space:]]*$") { exit }

        if (!in_dbg) {
          if (line ~ "^[[:space:]]*debug:[[:space:]]*$") { in_dbg=1; dbgIndent=ind; next }
          next
        }

        # leaving debug block
        if (ind <= dbgIndent) { in_dbg=0; next }

        if (line ~ /^[[:space:]]*enabled:/) {
          sub(/^[[:space:]]*enabled:[[:space:]]*/, "", line)
          line=trim(line)
          gsub(/"/, "", line)
          print line
          exit
        }
      }
    ' "$cfg" 2>/dev/null || true
}

config_server_debug_port() {
    # 从 config.yaml 提取 server.<section>.debug.port（section=api|smtp|worker）。
    local cfg="$1"
    local section="$2"
    awk -v sec="$section" '
      function lead(s) { match(s, /^[[:space:]]*/); return RLENGTH }
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      BEGIN { in_server=0; in_sec=0; in_dbg=0; serverIndent=-1; secIndent=-1; dbgIndent=-1 }
      /^[[:space:]]*#/ { next }
      /^[[:space:]]*$/ { next }
      {
        ind = lead($0)
        line = $0

        if (!in_server) {
          if (line ~ /^server:[[:space:]]*$/) { in_server=1; serverIndent=ind; next }
          next
        }

        if (ind <= serverIndent && line !~ /^server:[[:space:]]*$/) { exit }

        if (!in_sec) {
          if (line ~ "^[[:space:]]*" sec ":[[:space:]]*$") { in_sec=1; secIndent=ind; in_dbg=0; next }
          next
        }

        if (ind <= secIndent && line !~ "^[[:space:]]*" sec ":[[:space:]]*$") { exit }

        if (!in_dbg) {
          if (line ~ "^[[:space:]]*debug:[[:space:]]*$") { in_dbg=1; dbgIndent=ind; next }
          next
        }

        if (ind <= dbgIndent) { in_dbg=0; next }

        if (line ~ /^[[:space:]]*port:/) {
          sub(/^[[:space:]]*port:[[:space:]]*/, "", line)
          line=trim(line)
          gsub(/"/, "", line)
          print line
          exit
        }
      }
    ' "$cfg" 2>/dev/null || true
}

config_server_debug_host() {
    # 从 config.yaml 提取 server.<section>.debug.host（section=api|smtp|worker）。
    local cfg="$1"
    local section="$2"
    awk -v sec="$section" '
      function lead(s) { match(s, /^[[:space:]]*/); return RLENGTH }
      function trim(s) { sub(/^[[:space:]]+/, "", s); sub(/[[:space:]]+$/, "", s); return s }
      BEGIN { in_server=0; in_sec=0; in_dbg=0; serverIndent=-1; secIndent=-1; dbgIndent=-1 }
      /^[[:space:]]*#/ { next }
      /^[[:space:]]*$/ { next }
      {
        ind = lead($0)
        line = $0

        if (!in_server) {
          if (line ~ /^server:[[:space:]]*$/) { in_server=1; serverIndent=ind; next }
          next
        }

        if (ind <= serverIndent && line !~ /^server:[[:space:]]*$/) { exit }

        if (!in_sec) {
          if (line ~ "^[[:space:]]*" sec ":[[:space:]]*$") { in_sec=1; secIndent=ind; in_dbg=0; next }
          next
        }

        if (ind <= secIndent && line !~ "^[[:space:]]*" sec ":[[:space:]]*$") { exit }

        if (!in_dbg) {
          if (line ~ "^[[:space:]]*debug:[[:space:]]*$") { in_dbg=1; dbgIndent=ind; next }
          next
        }

        if (ind <= dbgIndent) { in_dbg=0; next }

        if (line ~ /^[[:space:]]*host:/) {
          sub(/^[[:space:]]*host:[[:space:]]*/, "", line)
          line=trim(line)
          sub(/^"/, "", line)
          sub(/"$/, "", line)
          if (line != "") { print line }
          exit
        }
      }
    ' "$cfg" 2>/dev/null || true
}

normalize_client_host() {
    local h
    h="$(trim "${1:-}")"
    h="$(lower "$h")"
    case "$h" in
        ""|"0.0.0.0"|"::") echo "127.0.0.1" ;;
        *) echo "$h" ;;
    esac
}

http_url_host() {
    # 将 host 转成 URL 里可用的 host（主要处理 IPv6 需要方括号）。
    local h
    h="$(trim "${1:-}")"
    if [[ -z "$h" ]]; then
        echo ""
        return 0
    fi
    if [[ "$h" == \[*\] ]]; then
        echo "$h"
        return 0
    fi
    if [[ "$h" == *:* ]]; then
        echo "[$h]"
        return 0
    fi
    echo "$h"
}

mktemp_compat() {
    local dir="${TMPDIR:-/tmp}"
    dir="${dir%/}"
    if have_cmd mktemp; then
        mktemp "${dir}/mailapi.XXXXXXXX" 2>/dev/null && return 0
        mktemp 2>/dev/null && return 0
    fi
    echo "${dir}/mailapi.$$.$RANDOM"
}

json_extract_string() {
    # 极简 JSON 字符串提取器：读取形如 "key":"value" 的 value（不处理转义/嵌套）。
    local key="$1"
    local json="${2:-}"
    printf "%s" "$json" | awk -v k="$key" '
      {
        gsub(/\r/, "", $0)
        gsub(/\n/, "", $0)

        # 匹配 "key" : "value"
        r = "\"" k "\"[[:space:]]*:[[:space:]]*\"[^\"]*\""
        if (!match($0, r)) { exit 0 }

        s = substr($0, RSTART, RLENGTH)
        sub(/^"[^\"]+"[[:space:]]*:[[:space:]]*"/, "", s)
        sub(/"$/, "", s)
        print s
        exit 0
      }
    '
}

json_get() {
    # 从 JSON 对象中提取 key 对应的值（优先 jq，回退到极简解析器）。
    # 输出为空表示不存在/解析失败。
    local key="$1"
    local json="${2:-}"

    if have_cmd jq; then
        # jq 解析失败不应让脚本中止（set -e），因此必须吞掉错误。
        local v
        v="$(printf "%s" "$json" | jq -r --arg k "$key" '.[$k] // empty' 2>/dev/null || true)"
        if [[ -n "$v" ]]; then
            printf "%s" "$v"
            return 0
        fi
    fi

    json_extract_string "$key" "$json"
}

json_quote() {
    # 将任意字符串编码为 JSON 字符串字面量（包含外层双引号）。
    local s="${1:-}"

    if have_cmd jq; then
        # jq 输出已包含外层引号；失败时回退。
        local out
        out="$(printf "%s" "$s" | jq -Rs @json 2>/dev/null || true)"
        if [[ -n "$out" ]]; then
            printf "%s" "$out"
            return 0
        fi
    fi

    # 轻量回退：转义 \ " \t \r 与换行（换行编码为 \n）。
    printf "%s" "$s" | awk '
      BEGIN { ORS=""; first=1; print "\"" }
      {
        gsub(/\\/, "\\\\")
        gsub(/"/, "\\\"")
        gsub(/\t/, "\\t")
        gsub(/\r/, "\\r")
        if (!first) print "\\n"
        first=0
        print $0
      }
      END { print "\"" }
    '
}

curl_request() {
    # stdout: body; echo http code to stderr? 这里采用“写入文件 + 返回 code”的方式更稳定。
    # 用法：curl_request METHOD URL OUTFILE AUTH HOSTHDR
    local method="$1"
    local url="$2"
    local outfile="$3"
    local auth="${4:-}"
    local hosthdr="${5:-}"
    local timeout="${MAILAPI_SMOKE_TIMEOUT:-10}"

    local args=()
    args+=(-sS --max-time "$timeout" -o "$outfile" -w "%{http_code}")
    args+=(-X "$method")
    if [[ "${MAILAPI_CURL_INSECURE:-}" == "1" ]]; then
        args+=(-k)
    fi
    if [[ -n "$hosthdr" ]]; then
        args+=(-H "Host: ${hosthdr}")
    fi
    if [[ -n "$auth" ]]; then
        args+=(-H "Authorization: Bearer ${auth}")
    fi

    curl "${args[@]}" "$url"
}

curl_request_json() {
    # 用法：curl_request_json METHOD URL JSON OUTFILE AUTH HOSTHDR
    local method="$1"
    local url="$2"
    local json="$3"
    local outfile="$4"
    local auth="${5:-}"
    local hosthdr="${6:-}"
    local timeout="${MAILAPI_SMOKE_TIMEOUT:-10}"

    local args=()
    args+=(-sS --max-time "$timeout" -o "$outfile" -w "%{http_code}")
    args+=(-X "$method" -H "Content-Type: application/json" --data "$json")
    if [[ "${MAILAPI_CURL_INSECURE:-}" == "1" ]]; then
        args+=(-k)
    fi
    if [[ -n "$hosthdr" ]]; then
        args+=(-H "Host: ${hosthdr}")
    fi
    if [[ -n "$auth" ]]; then
        args+=(-H "Authorization: Bearer ${auth}")
    fi

    curl "${args[@]}" "$url"
}

# =====================================================================
# doctor / check
# =====================================================================
do_doctor() {
    local rc=0
    echo -e "${CYAN}=== MailAPI Doctor ===${NC}"
    echo ""

    echo -e "${CYAN}Paths:${NC}"
    echo "  SRC_DIR:      $SRC_DIR"
    echo "  INSTALL_DIR:  $INSTALL_DIR"
    echo "  BIN_DIR:      $BIN_DIR"
    echo "  LOG_DIR:      $LOG_DIR"
    echo "  CONFIG_FILE:  $CONFIG_FILE"
    echo "  COMPOSE_FILE: $COMPOSE_FILE"
    echo ""

    echo -e "${CYAN}Environment:${NC}"
    echo "  EUID: ${EUID:-$(id -u)}"
    echo "  bash: ${BASH_VERSION:-unknown}"
    echo "  uname: $(uname -a 2>/dev/null || echo unknown)"
    echo ""

    echo -e "${CYAN}Dependencies:${NC}"
    if have_cmd go; then
        info "go: $(go version | awk '{print $3}')"
    else
        warn "go: not found (build/upgrade will fail)"
    fi
    if have_cmd docker; then
        info "docker: $(docker --version 2>/dev/null || echo ok)"
    else
        warn "docker: not found (infra commands will fail unless MAILAPI_SKIP_INFRA=1)"
        if [[ "${MAILAPI_SKIP_INFRA:-}" != "1" ]]; then
            rc=1
        fi
    fi
    if have_cmd curl; then
        info "curl: $(curl --version 2>/dev/null | head -n 1 || echo ok)"
    else
        warn "curl: not found (smoke/API checks will be limited)"
    fi
    if have_cmd jq; then
        info "jq: $(jq --version 2>/dev/null || echo ok)"
    else
        warn "jq: not found (smoke will use a minimal JSON parser)"
    fi
    echo ""

    echo -e "${CYAN}Binaries:${NC}"
    local b
    for b in mailapi-api mailapi-smtp mailapi-worker; do
        if [[ -x "$BIN_DIR/$b" ]]; then
            echo -e "  $b: ${GREEN}OK${NC} ($BIN_DIR/$b)"
        else
            echo -e "  $b: ${YELLOW}missing${NC} ($BIN_DIR/$b)"
        fi
    done
    echo ""

    echo -e "${CYAN}Config:${NC}"
    local cfg=""
    if cfg="$(resolve_config_file 2>/dev/null)"; then
        echo -e "  Using: ${GREEN}$cfg${NC}"
        local dom key api_port api_host
        dom="$(config_first_domain "$cfg")"
        key="$(config_first_apikey "$cfg")"
        api_port="$(config_server_port "$cfg" "api")"
        api_host="$(config_server_host "$cfg" "api")"
        [[ -n "$dom" ]] && echo "  First domain: $dom"
        [[ -n "$api_port" ]] && echo "  server.api.port: $api_port"
        [[ -n "$api_host" ]] && echo "  server.api.host: $api_host"
        if [[ -n "$key" ]]; then
            echo "  First apiKey: ${key:0:6}..."
        else
            echo "  apiKeys: (none)"
        fi
    else
        warn "Config not found."
        rc=1
    fi
    echo ""

    echo -e "${CYAN}Config Validation:${NC}"
    if [[ -n "${cfg:-}" ]]; then
        local svc bin
        for svc in api smtp worker; do
            bin="$BIN_DIR/mailapi-${svc}"
            if [[ -x "$bin" ]]; then
                if "$bin" --check-config --config "$cfg" >/dev/null 2>&1; then
                    echo -e "  ${svc}: ${GREEN}OK${NC}"
                else
                    echo -e "  ${svc}: ${RED}FAIL${NC} (run: $bin --check-config --config $cfg)"
                    rc=1
                fi
            else
                echo -e "  ${svc}: ${YELLOW}skip${NC} (binary missing: $bin)"
            fi
        done
    else
        echo -e "  ${YELLOW}skip${NC} (no config found)"
    fi
    echo ""

    echo -e "${CYAN}Infra:${NC}"
    if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
        echo "  (skipped: MAILAPI_SKIP_INFRA=1)"
    elif [[ -f "$COMPOSE_FILE" ]]; then
        if have_cmd docker; then
            if docker_compose ps 2>/dev/null; then
                :
            else
                warn "Cannot query docker compose status (is docker running? compose available?)"
                rc=1
            fi
        else
            warn "Docker not found"
        fi
    else
        warn "No docker-compose.yml found at $COMPOSE_FILE"
    fi
    echo ""

    echo -e "${CYAN}Services:${NC}"
    if is_systemd; then
        local s
        for s in api smtp worker; do
            local st
            st="$(systemctl is-active "mailapi-${s}" 2>/dev/null || echo "inactive")"
            echo "  mailapi-${s}: $st"
        done
    else
        local s
        for s in api smtp worker; do
            local pidfile="$INSTALL_DIR/${s}.pid"
            if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then
                echo "  mailapi-${s}: running (PID $(cat "$pidfile"))"
            else
                echo "  mailapi-${s}: stopped"
            fi
        done
    fi
    echo ""

    # API probe (best-effort)
    if have_cmd curl && [[ -n "$cfg" ]]; then
        local api_port api_host api_client_host api_base_url hosthdr
        api_port="$(config_server_port "$cfg" "api")"
        api_host="$(config_server_host "$cfg" "api")"
        api_client_host="$(normalize_client_host "${api_host:-127.0.0.1}")"
        api_base_url="${MAILAPI_SMOKE_URL:-http://$(http_url_host "$api_client_host"):${api_port:-8080}}"
        hosthdr="${MAILAPI_SMOKE_HOST:-}"

        echo -e "${CYAN}API Probe:${NC}"
        echo "  API Base URL: $api_base_url"
        [[ -n "$hosthdr" ]] && echo "  Host: $hosthdr"

        # Prefer debug server for /healthz /readyz (it bypasses gin middlewares).
        local dbg_enabled dbg_host dbg_port dbg_base_url
        dbg_enabled="$(lower "$(trim "$(config_server_debug_enabled "$cfg" "api")")")"
        dbg_host="$(config_server_debug_host "$cfg" "api")"
        dbg_port="$(config_server_debug_port "$cfg" "api")"
        if [[ -z "$dbg_host" ]]; then dbg_host="127.0.0.1"; fi
        if [[ -z "$dbg_port" ]]; then dbg_port="6060"; fi
        dbg_base_url="${MAILAPI_SMOKE_DEBUG_URL:-http://$(http_url_host "$dbg_host"):${dbg_port}}"

        if [[ "$dbg_enabled" == "true" || -n "${MAILAPI_SMOKE_DEBUG_URL:-}" ]]; then
            echo "  Debug Base URL: $dbg_base_url"

            local tmp code
            tmp="$(mktemp_compat)"
            if code="$(retry 5 200 -- curl_request "GET" "${dbg_base_url%/}/healthz" "$tmp" "" "")"; then
                if [[ "$code" == "200" ]]; then
                    echo -e "  GET /healthz: ${GREEN}200${NC}"
                else
                    echo -e "  GET /healthz: ${YELLOW}${code}${NC}"
                    rc=1
                fi
            else
                warn "Failed to request /healthz (debug server)"
                rc=1
            fi
            rm -f "$tmp" 2>/dev/null || true

            tmp="$(mktemp_compat)"
            if code="$(retry 5 200 -- curl_request "GET" "${dbg_base_url%/}/readyz" "$tmp" "" "")"; then
                if [[ "$code" == "200" ]]; then
                    echo -e "  GET /readyz: ${GREEN}200${NC}"
                else
                    echo -e "  GET /readyz: ${YELLOW}${code}${NC}"
                    rc=1
                fi
            else
                warn "Failed to request /readyz (debug server)"
                rc=1
            fi
            rm -f "$tmp" 2>/dev/null || true
        else
            warn "Debug server disabled; skipping /healthz and /readyz probe."
            warn "Hint: set server.api.debug.enabled=true to enable debug endpoints."
        fi

        # Always probe API business endpoint (should exist even when debug server is disabled).
        local tmp2 code2
        tmp2="$(mktemp_compat)"
        if code2="$(retry 5 200 -- curl_request "GET" "${api_base_url%/}/domains" "$tmp2" "$key" "$hosthdr")"; then
            if [[ "$code2" == "200" ]]; then
                echo -e "  GET /domains: ${GREEN}200${NC}"
            else
                echo -e "  GET /domains: ${YELLOW}${code2}${NC}"
                rc=1
            fi
        else
            warn "Failed to request /domains"
            rc=1
        fi
        rm -f "$tmp2" 2>/dev/null || true

        echo ""
    fi

    if [[ $rc -eq 0 ]]; then
        info "Doctor: OK"
    else
        warn "Doctor: issues detected"
    fi
    return "$rc"
}

# =====================================================================
# compose passthrough
# =====================================================================
do_compose() {
    check_docker
    ensure_infra_env_file
    if [[ ! -f "$COMPOSE_FILE" ]]; then
        write_compose
    fi
    docker_compose "$@"
}

# =====================================================================
# upgrade
# =====================================================================
do_upgrade() {
    local pull_infra=0
    while [[ $# -gt 0 ]]; do
        case "${1:-}" in
        --pull|pull) pull_infra=1; shift ;;
        -h|--help)
            echo "Usage: $0 upgrade [--pull]"
            echo "  --pull   Pull & recreate infra containers (keeps volumes)"
            return 0
            ;;
        *)
            error "Unknown option for upgrade: $1"
            return 1
            ;;
        esac
    done

    info "Upgrading MailAPI (rebuild binaries)..."
    do_build

    if [[ $pull_infra -eq 1 ]]; then
        if [[ "${MAILAPI_SKIP_INFRA:-}" == "1" ]]; then
            warn "MAILAPI_SKIP_INFRA=1, skipping infra pull/recreate"
        else
            check_docker
            ensure_infra_env_file
            if [[ ! -f "$COMPOSE_FILE" ]]; then
                write_compose
            fi
            info "Pulling infra images..."
            docker_compose pull
            info "Recreating infra containers (volumes preserved)..."
            docker_compose up -d --force-recreate
        fi
    fi

    # Restart services if installed/running
    if is_systemd; then
        info "Restarting systemd services..."
        systemctl restart mailapi-api mailapi-smtp mailapi-worker
        info "Restart command sent"
        return 0
    fi

    local any=0
    local s
    for s in api smtp worker; do
        if [[ -f "$INSTALL_DIR/${s}.pid" ]]; then
            any=1
        fi
    done
    if [[ $any -eq 1 ]]; then
        info "Restarting background services..."
        do_restart all
    else
        info "No running services detected. Run: $0 start"
    fi
}

# =====================================================================
# uninstall
# =====================================================================
do_uninstall() {
    local purge=0
    while [[ $# -gt 0 ]]; do
        case "${1:-}" in
        --purge|purge) purge=1; shift ;;
        -h|--help)
            echo "Usage: $0 uninstall [--purge]"
            echo "  (default) keep config at: $CONFIG_FILE"
            echo "  --purge  also remove config and docker volumes (DATA LOSS)"
            return 0
            ;;
        *)
            error "Unknown option for uninstall: $1"
            return 1
            ;;
        esac
    done

    warn "This will stop services and remove binaries/logs."
    warn "Config will be kept by default: $CONFIG_FILE"
    if [[ $purge -eq 1 ]]; then
        warn "PURGE enabled: config file and docker volumes will be removed (DATA LOSS)."
    fi

    if ! confirm_danger "Proceed with uninstall?"; then
        if is_noninteractive; then
            warn "Refusing to uninstall without confirmation in non-interactive mode."
            warn "Set MAILAPI_CONFIRM=1 to proceed."
        fi
        info "Cancelled."
        return 0
    fi

    if [[ $purge -eq 1 ]]; then
        if ! confirm_danger "Confirm PURGE (data loss)?"; then
            if is_noninteractive; then
                warn "Refusing to purge without confirmation in non-interactive mode."
                warn "Set MAILAPI_CONFIRM=1 to proceed."
            fi
            info "Cancelled."
            return 0
        fi
    fi

    # Stop services
    if is_systemd; then
        info "Stopping systemd services..."
        systemctl stop mailapi-api mailapi-smtp mailapi-worker 2>/dev/null || true
        info "Disabling systemd services..."
        systemctl disable mailapi-api mailapi-smtp mailapi-worker 2>/dev/null || true
    else
        do_stop all || true
    fi

    # Stop infra (keep volumes unless purge)
    if [[ "${MAILAPI_SKIP_INFRA:-}" != "1" ]] && [[ -f "$COMPOSE_FILE" ]] && have_cmd docker; then
        if [[ $purge -eq 1 ]]; then
            info "Stopping infra containers and removing volumes (purge)..."
            docker_compose down -v || true
        else
            info "Stopping infra containers (volumes preserved)..."
            docker_compose down || true
        fi
    fi

    # Remove systemd unit files (root only, best-effort)
    if [[ "${EUID:-$(id -u)}" -eq 0 ]] && have_cmd systemctl; then
        local u
        for u in api smtp worker; do
            rm -f "/etc/systemd/system/mailapi-${u}.service" 2>/dev/null || true
        done
        systemctl daemon-reload 2>/dev/null || true
        systemctl reset-failed 2>/dev/null || true
    fi

    # Remove binaries/logs/pidfiles
    info "Removing binaries and logs..."
    if assert_safe_path_under_install "$BIN_DIR"; then
        rm -rf "$BIN_DIR" 2>/dev/null || true
    else
        warn "Refusing to remove BIN_DIR (unsafe path): $BIN_DIR"
    fi
    if assert_safe_path_under_install "$LOG_DIR"; then
        rm -rf "$LOG_DIR" 2>/dev/null || true
    else
        warn "Refusing to remove LOG_DIR (unsafe path): $LOG_DIR"
    fi
    rm -f "$INSTALL_DIR/api.pid" "$INSTALL_DIR/smtp.pid" "$INSTALL_DIR/worker.pid" 2>/dev/null || true

    info "Removing compose file..."
    if assert_safe_path_under_install "$COMPOSE_FILE"; then
        rm -f "$COMPOSE_FILE" 2>/dev/null || true
    else
        warn "Refusing to remove COMPOSE_FILE (unsafe path): $COMPOSE_FILE"
    fi

    if [[ $purge -eq 1 ]]; then
        info "Removing config file (purge)..."
        if assert_safe_path_under_install "$CONFIG_FILE"; then
            rm -f "$CONFIG_FILE" 2>/dev/null || true
        else
            warn "Refusing to remove CONFIG_FILE (unsafe path): $CONFIG_FILE"
        fi

        info "Removing infra env file (purge)..."
        if assert_safe_path_under_install "$ENV_FILE"; then
            rm -f "$ENV_FILE" 2>/dev/null || true
        else
            warn "Refusing to remove ENV_FILE (unsafe path): $ENV_FILE"
        fi
    fi

    info "Uninstall complete."
}

# =====================================================================
# setcap-smtp
# =====================================================================
do_setcap_smtp() {
    local os
    os="$(uname -s 2>/dev/null || echo unknown)"
    if [[ "$(lower "$os")" != "linux" ]]; then
        error "setcap-smtp is only supported on Linux (uname=$os)"
        return 1
    fi

    if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
        error "setcap requires root. Please run:"
        error "  sudo $0 setcap-smtp"
        return 1
    fi

    if ! have_cmd setcap; then
        error "setcap not found. Install libcap (e.g. apt-get install -y libcap2-bin)."
        return 1
    fi

    local bin="${MAILAPI_SMTP_BIN:-$BIN_DIR/mailapi-smtp}"
    if [[ ! -x "$bin" ]]; then
        error "SMTP binary not found or not executable: $bin"
        error "Run: $0 build"
        return 1
    fi

    info "Setting cap_net_bind_service on: $bin"
    setcap "cap_net_bind_service=+ep" "$bin"
    if have_cmd getcap; then
        info "getcap:"
        getcap "$bin" || true
    fi
    info "Done. You can now run mailapi-smtp as non-root on port 25."
}

# =====================================================================
# smoke test
# =====================================================================
do_smoke() {
    if ! have_cmd curl; then
        error "curl is required for smoke tests"
        return 1
    fi

    local cfg
    if ! cfg="$(resolve_config_file)"; then
        return 1
    fi

    local api_port api_host client_host base_url hosthdr
    api_port="$(config_server_port "$cfg" "api")"
    api_host="$(config_server_host "$cfg" "api")"
    client_host="$(normalize_client_host "${api_host:-127.0.0.1}")"
    base_url="${MAILAPI_SMOKE_URL:-http://$(http_url_host "$client_host"):${api_port:-8080}}"
    hosthdr="${MAILAPI_SMOKE_HOST:-}"

    local api_key domain
    api_key="${MAILAPI_SMOKE_API_KEY:-$(config_first_apikey "$cfg")}"
    domain="${MAILAPI_SMOKE_DOMAIN:-$(config_first_domain "$cfg")}"

    if [[ -z "$domain" ]]; then
        error "No domain found in config. Set MAILAPI_SMOKE_DOMAIN or add domains in config.yaml."
        return 1
    fi

    echo -e "${CYAN}=== MailAPI Smoke Test ===${NC}"
    echo "Base URL: $base_url"
    [[ -n "$hosthdr" ]] && echo "Host: $hosthdr"
    echo "Domain: $domain"
    if [[ -n "$api_key" ]]; then
        echo "API key: ${api_key:0:6}..."
    else
        echo "API key: (none)"
    fi
    echo ""

    local tmp code body

    # 健康检查：优先通过 debug server（绕过 gin 中间件），未开启时跳过但会给出提示。
    local dbg_enabled dbg_host dbg_port dbg_base_url
    dbg_enabled="$(lower "$(trim "$(config_server_debug_enabled "$cfg" "api")")")"
    dbg_host="$(config_server_debug_host "$cfg" "api")"
    dbg_port="$(config_server_debug_port "$cfg" "api")"
    if [[ -z "$dbg_host" ]]; then dbg_host="127.0.0.1"; fi
    if [[ -z "$dbg_port" ]]; then dbg_port="6060"; fi
    dbg_base_url="${MAILAPI_SMOKE_DEBUG_URL:-http://$(http_url_host "$dbg_host"):${dbg_port}}"

    if [[ "$dbg_enabled" == "true" || -n "${MAILAPI_SMOKE_DEBUG_URL:-}" ]]; then
        echo "Debug Base URL: $dbg_base_url"

        tmp="$(mktemp_compat)"
        if ! code="$(retry 5 200 -- curl_request "GET" "${dbg_base_url%/}/healthz" "$tmp" "" "")"; then
            rm -f "$tmp" 2>/dev/null || true
            error "GET /healthz failed (debug server request error)"
            return 1
        fi
        rm -f "$tmp" 2>/dev/null || true
        if [[ "$code" != "200" ]]; then
            error "GET /healthz failed (debug server HTTP $code)"
            return 1
        fi
        info "GET /healthz OK (debug)"

        tmp="$(mktemp_compat)"
        if ! code="$(retry 5 200 -- curl_request "GET" "${dbg_base_url%/}/readyz" "$tmp" "" "")"; then
            rm -f "$tmp" 2>/dev/null || true
            error "GET /readyz failed (debug server request error)"
            return 1
        fi
        rm -f "$tmp" 2>/dev/null || true
        if [[ "$code" != "200" ]]; then
            error "GET /readyz failed (debug server HTTP $code)"
            return 1
        fi
        info "GET /readyz OK (debug)"
    else
        warn "Debug server disabled; skipping /healthz and /readyz."
        warn "Hint: set server.api.debug.enabled=true (or set MAILAPI_SMOKE_DEBUG_URL=...)."
    fi

    # 关键 API 流程：/domains -> /accounts -> /token -> /me -> /messages -> delete account
    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "GET" "${base_url%/}/domains" "$tmp" "$api_key" "$hosthdr")"; then
        rm -f "$tmp" 2>/dev/null || true
        error "GET /domains failed (request error)"
        return 1
    fi
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "GET /domains failed (HTTP $code)"
        return 1
    fi
    info "GET /domains OK"

    local password
    if [[ -n "${MAILAPI_SMOKE_PASSWORD:-}" ]]; then
        password="$MAILAPI_SMOKE_PASSWORD"
    else
        password="smoke_${RANDOM}${RANDOM}"
    fi
    local create_body
    create_body="{\"domain\":$(json_quote "$domain"),\"password\":$(json_quote "$password")}"

    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request_json "POST" "${base_url%/}/accounts" "$create_body" "$tmp" "$api_key" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "POST /accounts failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "201" ]]; then
        error "POST /accounts failed (HTTP $code): $body"
        if [[ "$code" == "401" && -z "$api_key" ]]; then
            error "Hint: API keys are configured; set MAILAPI_SMOKE_API_KEY or use the key in config.yaml."
        fi
        return 1
    fi
    local address account_id
    address="$(json_get "address" "$body")"
    account_id="$(json_get "id" "$body")"
    if [[ -z "$account_id" ]]; then
        # 兼容少数实现可能用 _id 输出的情况
        account_id="$(json_get "_id" "$body")"
    fi
    if [[ -z "$address" || -z "$account_id" ]]; then
        error "Failed to parse account creation response: $body"
        return 1
    fi
    info "POST /accounts OK (address=$address)"

    local token_body token
    token_body="{\"address\":$(json_quote "$address"),\"password\":$(json_quote "$password")}"
    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request_json "POST" "${base_url%/}/token" "$token_body" "$tmp" "$api_key" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "POST /token failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "POST /token failed (HTTP $code): $body"
        return 1
    fi
    token="$(json_get "token" "$body")"
    if [[ -z "$token" ]]; then
        error "Failed to parse token response: $body"
        return 1
    fi
    info "POST /token OK"

    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "GET" "${base_url%/}/me" "$tmp" "$token" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "GET /me failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "GET /me failed (HTTP $code): $body"
        return 1
    fi
    info "GET /me OK"

    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "GET" "${base_url%/}/messages" "$tmp" "$token" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "GET /messages failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "GET /messages failed (HTTP $code): $body"
        return 1
    fi
    info "GET /messages OK"

    # seen filter
    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "GET" "${base_url%/}/messages?seen=true" "$tmp" "$token" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "GET /messages?seen=true failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "GET /messages?seen=true failed (HTTP $code): $body"
        return 1
    fi
    info "GET /messages?seen=true OK"

    # bulk update flags (all=true)
    local bulk_body
    bulk_body='{"all":true,"seen":true}'
    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request_json "PATCH" "${base_url%/}/messages" "$bulk_body" "$tmp" "$token" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "PATCH /messages (bulk) failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "PATCH /messages (bulk) failed (HTTP $code): $body"
        return 1
    fi
    info "PATCH /messages (bulk) OK"

    # bulk delete by account (may delete 0; just ensure endpoint works)
    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "DELETE" "${base_url%/}/messages?limit=1" "$tmp" "$token" "$hosthdr")"; then
        body="$(cat "$tmp" 2>/dev/null || true)"
        rm -f "$tmp" 2>/dev/null || true
        error "DELETE /messages (bulk) failed (request error): $body"
        return 1
    fi
    body="$(cat "$tmp" 2>/dev/null || true)"
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "200" ]]; then
        error "DELETE /messages (bulk) failed (HTTP $code): $body"
        return 1
    fi
    info "DELETE /messages (bulk) OK"

    tmp="$(mktemp_compat)"
    if ! code="$(retry 5 200 -- curl_request "DELETE" "${base_url%/}/accounts/${account_id}" "$tmp" "$token" "$hosthdr")"; then
        rm -f "$tmp" 2>/dev/null || true
        warn "DELETE /accounts/${account_id} failed (request error; cleanup may be needed)"
        info "Smoke test OK"
        return 0
    fi
    rm -f "$tmp" 2>/dev/null || true
    if [[ "$code" != "204" ]]; then
        warn "DELETE /accounts/${account_id} returned HTTP $code (cleanup may be needed)"
    else
        info "DELETE /accounts/${account_id} OK"
    fi

    info "Smoke test OK"
}

# =====================================================================
# main dispatch
# =====================================================================
case "${1:-help}" in
    install)        do_install ;;
    build)          do_build ;;
    config)         do_config ;;
    doctor|check)   do_doctor ;;
    upgrade)        shift; do_upgrade "$@" ;;
    uninstall)      shift; do_uninstall "$@" ;;
    compose)        shift; do_compose "$@" ;;
    setcap-smtp)    do_setcap_smtp ;;
    smoke)          do_smoke ;;
    infra-up)       do_infra_up ;;
    infra-down)     do_infra_down ;;
    infra-clean)    do_infra_clean ;;
    start)          do_start "${2:-all}" ;;
    stop)           do_stop "${2:-all}" ;;
    restart)        do_restart "${2:-all}" ;;
    status)         do_status "${2:-all}" ;;
    logs)           do_logs "${2:-all}" "${3:-200}" ;;
    genkey)         do_genkey "${2:-Unnamed}" "${3:-}" ;;
    list-domains)   do_list_domains ;;
    list-apikeys)   do_list_apikeys ;;
    help|--help|-h) show_help ;;
    *)
        error "Unknown command: $1"
        show_help
        exit 1
        ;;
esac
