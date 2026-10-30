#!/usr/bin/env bash
# proxystack-web 安装脚本：创建服务账号和目录、安装二进制、写入 unit 与 polkit 规则、初始化数据库并启动服务。
set -euo pipefail

BASE=/opt/proxystack-web
SERVICE_USER=proxystack-web
RELEASE_BASE=${PSW_RELEASE_BASE:-https://github.com/eagle/proxystack-web/releases/download}
VERSION=latest
BINARY=""
LISTEN=""
PORT=9443
TLS=self-signed
DOMAIN=""
EMAIL=""

usage() {
    cat <<USAGE
Usage: install.sh [options]

  --version VERSION   release version to install (default: latest)
  --binary FILE       install a local proxystack-web binary instead of downloading
  --listen ADDR       panel listen address (default 0.0.0.0, or 127.0.0.1 with --tls proxy)
  --port PORT         panel port (default 9443)
  --tls MODE          self-signed | acme | proxy (default self-signed)
  --domain DOMAIN     panel domain
  --email EMAIL       ACME account email
USAGE
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version) VERSION=$2; shift 2 ;;
        --binary) BINARY=$2; shift 2 ;;
        --listen) LISTEN=$2; shift 2 ;;
        --port) PORT=$2; shift 2 ;;
        --tls) TLS=$2; shift 2 ;;
        --domain) DOMAIN=$2; shift 2 ;;
        --email) EMAIL=$2; shift 2 ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

[[ $(id -u) -eq 0 ]] || die "run as root"
command -v systemctl >/dev/null || die "systemd is required"
[[ -d /run/systemd/system ]] || die "systemd is not the running init system"
SYSTEMD_VERSION=$(systemctl --version | awk 'NR==1 {print $2}')
[[ ${SYSTEMD_VERSION:-0} -ge 226 ]] || die "systemd >= 226 is required (found ${SYSTEMD_VERSION})"
[[ -e $BASE/data/proxystack.db ]] && die "$BASE is already initialized; use the panel to upgrade"

case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported architecture: $(uname -m)" ;;
esac

install_polkit() {
    # systemd 自带 /usr/share/polkit-1 下的 action 文件，目录存在不代表装了 polkitd，
    # 必须以 polkit 服务是否存在为准。
    if systemctl cat polkit.service >/dev/null 2>&1; then
        return
    fi
    log "installing polkit"
    if command -v apt-get >/dev/null; then
        apt-get update -qq && (apt-get install -y -qq polkitd || apt-get install -y -qq policykit-1)
    elif command -v dnf >/dev/null; then
        dnf install -y -q polkit
    elif command -v yum >/dev/null; then
        yum install -y -q polkit
    else
        die "polkit is required; install it with your package manager"
    fi
    # 新装的 polkitd 带有自己的 D-Bus 策略文件；系统总线重新加载策略之前，polkitd 无法占用
    # org.freedesktop.PolicyKit1 而立即退出，管理服务的启停请求会一直等到超时。
    systemctl reload dbus.service 2>/dev/null || systemctl reload dbus-broker.service 2>/dev/null || true
}

fetch_binary() {
    local dest=$1
    if [[ -n $BINARY ]]; then
        install -m 0755 "$BINARY" "$dest"
        return
    fi
    local url_base
    if [[ $VERSION == latest ]]; then
        url_base="${RELEASE_BASE%/download}/latest/download"
    else
        url_base="$RELEASE_BASE/$VERSION"
    fi
    local asset="proxystack-web-linux-$ARCH"
    local tmp
    tmp=$(mktemp -d)
    trap 'rm -rf "$tmp"' RETURN
    log "downloading $asset ($VERSION)"
    curl -fsSL -o "$tmp/$asset" "$url_base/$asset"
    curl -fsSL -o "$tmp/$asset.sha256" "$url_base/$asset.sha256"
    (cd "$tmp" && sha256sum -c "$asset.sha256" >/dev/null) || die "sha256 mismatch for $asset"
    install -m 0755 "$tmp/$asset" "$dest"
}

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
asset_file() {
    # 优先使用与脚本同目录的 deploy 文件；不存在时从发布包下载。
    local rel=$1 dest=$2
    if [[ -f $SCRIPT_DIR/$rel ]]; then
        install -m 0644 "$SCRIPT_DIR/$rel" "$dest"
    else
        local ref=$VERSION
        [[ $ref == latest ]] && ref=main
        curl -fsSL -o "$dest" "https://raw.githubusercontent.com/eagle/proxystack-web/$ref/deploy/$rel"
        chmod 0644 "$dest"
    fi
}

install_polkit

log "creating service account $SERVICE_USER"
if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
    useradd --system --home-dir "$BASE" --no-create-home --shell /usr/sbin/nologin "$SERVICE_USER"
fi
getent group systemd-journal >/dev/null && usermod -a -G systemd-journal "$SERVICE_USER"

log "creating $BASE"
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" \
    "$BASE" "$BASE/bin" "$BASE/geo" "$BASE/data" "$BASE/data/tls" \
    "$BASE/runtime" "$BASE/runtime/generated" "$BASE/runtime/generated/xray" "$BASE/runtime/generated/mihomo" \
    "$BASE/runtime/mihomo" "$BASE/backups" "$BASE/downloads"

fetch_binary "$BASE/bin/proxystack-web"
chown "$SERVICE_USER:$SERVICE_USER" "$BASE/bin/proxystack-web"
ln -sf "$BASE/bin/proxystack-web" /usr/local/bin/proxystack-web

log "writing systemd units and polkit rule"
asset_file systemd/proxystack-web.service /etc/systemd/system/proxystack-web.service
asset_file systemd/psw-xray@.service /etc/systemd/system/psw-xray@.service
asset_file systemd/psw-mihomo@.service /etc/systemd/system/psw-mihomo@.service
install -d -m 0755 /etc/polkit-1/rules.d
asset_file polkit/50-proxystack-web.rules /etc/polkit-1/rules.d/50-proxystack-web.rules
systemctl daemon-reload
systemctl restart polkit.service || die "polkit failed to start; check 'journalctl -u polkit'"
systemctl is-active --quiet polkit.service || die "polkit is not running; check 'journalctl -u polkit'"

log "initializing database"
INIT_ARGS=(--base-dir "$BASE" init --port "$PORT" --tls "$TLS")
[[ -n $LISTEN ]] && INIT_ARGS+=(--listen "$LISTEN")
[[ -n $DOMAIN ]] && INIT_ARGS+=(--domain "$DOMAIN")
[[ -n $EMAIL ]] && INIT_ARGS+=(--email "$EMAIL")
INIT_OUTPUT=$(runuser -u "$SERVICE_USER" -- "$BASE/bin/proxystack-web" "${INIT_ARGS[@]}")

log "starting proxystack-web.service"
systemctl enable --now proxystack-web.service

echo
echo "$INIT_OUTPUT"
