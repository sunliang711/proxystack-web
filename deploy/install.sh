#!/usr/bin/env bash
# proxystack-web 安装脚本。按角色安装：
#   panel  管理端：创建服务账号和目录、安装二进制、写入 unit 与 polkit 规则、初始化数据库并启动服务。
#   sub    订阅端：独立的服务账号、目录和 unit，只通过管理端签发的订阅包获得数据；已安装时只升级二进制。
#   all    两者装在同一台机器上，订阅端自动信任本机管理端的公钥。
set -euo pipefail

BASE=/opt/proxystack-web
SERVICE_USER=proxystack-web
SUB_BASE=/opt/proxystack-web-sub
SUB_USER=proxystack-web-sub
SUB_UNIT=proxystack-web-sub.service
LEGACY_SUB_BASE=/opt/proxystack-sub
LEGACY_SUB_UNIT=proxystack-sub.service
RELEASE_BASE=${PSW_RELEASE_BASE:-https://github.com/sunliang711/proxystack-web/releases/download}
ROLE=panel
VERSION=latest
BINARY=""
LISTEN=""
PORT=9443
RESTORE=""
SOURCE=main
PUBLIC_KEY=""
SUB_LISTEN=""
SUB_PORT=""
MIGRATE_SUB=0

usage() {
    cat <<USAGE
Usage: install.sh [options]

  --role ROLE           panel | sub | all (default: panel)
  --version VERSION     release version to install (default: latest)
  --binary FILE         install a local proxystack-web binary instead of downloading

Panel options:
  --listen ADDR         panel listen address (default 0.0.0.0)
  --port PORT           panel port (default 9443)
  --restore FILE        initialize from a migration package instead of generating a new admin password

Subscription server options:
  --source NAME         the panel's source name (default: main)
  --public-key KEY      the panel's subscription public key (ed25519:...), shown on its 发布订阅包 page
  --sub-listen ADDR     listen address (default 127.0.0.1)
  --sub-port PORT       listen port (default 9444)
  --migrate-sub         migrate this project's old /opt/proxystack-sub installation (requires --role sub)

Running with --role sub on a machine that already has a subscription server upgrades its binary.
USAGE
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --role) ROLE=$2; shift 2 ;;
        --version) VERSION=$2; shift 2 ;;
        --binary) BINARY=$2; shift 2 ;;
        --listen) LISTEN=$2; shift 2 ;;
        --port) PORT=$2; shift 2 ;;
        --restore) RESTORE=$2; shift 2 ;;
        --source) SOURCE=$2; shift 2 ;;
        --public-key) PUBLIC_KEY=$2; shift 2 ;;
        --sub-listen) SUB_LISTEN=$2; shift 2 ;;
        --sub-port) SUB_PORT=$2; shift 2 ;;
        --migrate-sub) MIGRATE_SUB=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
    esac
done

die() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }

case "$ROLE" in
    panel|sub|all) ;;
    *) die "--role must be panel, sub or all" ;;
esac
[[ $MIGRATE_SUB -eq 0 || $ROLE == sub ]] || die "--migrate-sub requires --role sub"
if [[ $MIGRATE_SUB -eq 1 && ( -n $PUBLIC_KEY || -n $SUB_LISTEN || -n $SUB_PORT ) ]]; then
    die "--migrate-sub preserves the existing configuration; do not specify --public-key, --sub-listen or --sub-port"
fi

[[ $(id -u) -eq 0 ]] || die "run as root"
command -v systemctl >/dev/null || die "systemd is required"
[[ -d /run/systemd/system ]] || die "systemd is not the running init system"
SYSTEMD_VERSION=$(systemctl --version | awk 'NR==1 {print $2}')
[[ ${SYSTEMD_VERSION:-0} -ge 226 ]] || die "systemd >= 226 is required (found ${SYSTEMD_VERSION})"

SUB_UPGRADE=0
if [[ $ROLE != sub ]]; then
    [[ -e $BASE/data/proxystack.db ]] && die "$BASE is already initialized; use the panel to upgrade"
fi
if [[ -n $RESTORE ]]; then
    [[ $ROLE != sub ]] || die "--restore applies to the panel"
    [[ -r $RESTORE ]] || die "cannot read $RESTORE"
fi

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
        apt-get update -qq || die "apt package index update failed"
        apt-get install -y -qq polkitd || apt-get install -y -qq policykit-1 || die "polkit installation failed"
    elif command -v dnf >/dev/null; then
        dnf install -y -q polkit || die "polkit installation failed"
    elif command -v yum >/dev/null; then
        yum install -y -q polkit || die "polkit installation failed"
    else
        die "polkit is required; install it with your package manager"
    fi
    # 新装的 polkitd 带有自己的 D-Bus 策略文件；系统总线重新加载策略之前，polkitd 无法占用
    # org.freedesktop.PolicyKit1 而立即退出，管理服务的启停请求会一直等到超时。
    systemctl reload dbus.service 2>/dev/null || systemctl reload dbus-broker.service 2>/dev/null || true
}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# fetch_binary 把二进制准备到 $WORK/proxystack-web，管理端和订阅端各自安装一份。
fetch_binary() {
    local dest=$WORK/proxystack-web
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
    log "downloading $asset ($VERSION)"
    curl -fsSL -o "$WORK/$asset" "$url_base/$asset"
    curl -fsSL -o "$WORK/$asset.sha256" "$url_base/$asset.sha256"
    (cd "$WORK" && sha256sum -c "$asset.sha256" >/dev/null) || die "sha256 mismatch for $asset"
    install -m 0755 "$WORK/$asset" "$dest"
}

SCRIPT_DIR=""
if [[ -n ${BASH_SOURCE[0]:-} ]]; then
    SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
fi
asset_file() {
    # 优先使用与脚本同目录的 deploy 文件；不存在时从发布包下载。
    local rel=$1 dest=$2
    if [[ -n $SCRIPT_DIR && -f $SCRIPT_DIR/$rel ]]; then
        install -m 0644 "$SCRIPT_DIR/$rel" "$dest"
    else
        local ref=$VERSION
        [[ $ref == latest ]] && ref=main
        curl -fsSL -o "$dest" "https://raw.githubusercontent.com/sunliang711/proxystack-web/$ref/deploy/$rel"
        chmod 0644 "$dest"
    fi
}

create_account() {
    local user=$1 home=$2
    if ! id -u "$user" >/dev/null 2>&1; then
        useradd --system --home-dir "$home" --no-create-home --shell /usr/sbin/nologin "$user"
    fi
}

# 读取 systemd 生效后的命令，避免仅凭同名目录或 unit 文件认领其他项目。
sub_unit_exists() {
    local state
    state=$(systemctl show --value -p LoadState "$1" 2>/dev/null) || true
    [[ -n $state && $state != not-found ]]
}

sub_unit_owned() {
    local dir=$1 unit=$2 command
    command=$(systemctl show --value -p ExecStart "$unit" 2>/dev/null) || return 1
    [[ $command == "{ path=$dir/bin/proxystack-web ; argv[]=$dir/bin/proxystack-web sub --dir $dir serve ; "* ]]
}

check_sub_paths() {
    local dir=$1 links
    [[ ! -L $dir ]] || die "refusing symbolic link: $dir"
    if [[ -e $dir ]]; then
        [[ -d $dir ]] || die "not a directory: $dir"
        links=$(find "$dir" -type l -print -quit) || die "cannot inspect $dir"
        [[ -z $links ]] || die "refusing symbolic links inside $dir"
    fi
}

write_sub_marker() {
    printf '%s\n' proxystack-web-sub-v1 > "$SUB_BASE/.proxystack-web-sub"
    chown root:root "$SUB_BASE/.proxystack-web-sub"
    chmod 0644 "$SUB_BASE/.proxystack-web-sub"
}

prepare_sub() {
    check_sub_paths "$SUB_BASE"
    [[ ! -L /etc/systemd/system/$SUB_UNIT ]] || die "refusing a symbolic link for $SUB_UNIT"
    local marker=$SUB_BASE/.proxystack-web-sub
    if sub_unit_exists "$SUB_UNIT"; then
        sub_unit_owned "$SUB_BASE" "$SUB_UNIT" || die "$SUB_UNIT belongs to another installation; refusing to overwrite it"
    elif [[ -e $SUB_BASE && -n $(find "$SUB_BASE" -mindepth 1 -print -quit) ]]; then
        [[ -f $marker && $(cat "$marker") == proxystack-web-sub-v1 ]] || die "cannot confirm ownership of $SUB_BASE"
    fi
    if [[ -e $SUB_BASE/config.yaml ]]; then
        "$WORK/proxystack-web" sub --dir "$SUB_BASE" config >/dev/null || die "invalid proxystack-web subscription configuration in $SUB_BASE"
        [[ $ROLE != all ]] || die "$SUB_BASE is already initialized"
        SUB_UPGRADE=1
    elif [[ -e $SUB_BASE && -n $(find "$SUB_BASE" -mindepth 1 -print -quit) ]] || sub_unit_exists "$SUB_UNIT"; then
        die "$SUB_BASE has no configuration; refusing to overwrite an incomplete installation"
    fi

    if [[ $MIGRATE_SUB -eq 1 ]]; then
        command -v curl >/dev/null || die "curl is required to verify the migrated service"
        [[ ! -e $SUB_BASE ]] || die "migration destination already exists: $SUB_BASE"
        check_sub_paths "$LEGACY_SUB_BASE"
        sub_unit_owned "$LEGACY_SUB_BASE" "$LEGACY_SUB_UNIT" || die "$LEGACY_SUB_UNIT is not this project's old subscription service; pssub cannot be migrated"
        local fragment
        fragment=$(systemctl show --value -p FragmentPath "$LEGACY_SUB_UNIT")
        [[ $fragment == /etc/systemd/system/"$LEGACY_SUB_UNIT" && -f $fragment && ! -L $fragment ]] || die "migration requires a regular /etc/systemd/system/$LEGACY_SUB_UNIT"
        [[ ! -L /etc/systemd/system/$LEGACY_SUB_UNIT.d ]] || die "refusing a symbolic link in the legacy unit drop-ins"
        if [[ -d /etc/systemd/system/$LEGACY_SUB_UNIT.d ]]; then
            die "legacy unit has local overrides; merge them into the new unit manually before migrating"
        fi
        "$WORK/proxystack-web" sub --dir "$LEGACY_SUB_BASE" config >/dev/null || die "legacy configuration is not a valid proxystack-web subscription configuration"
    elif sub_unit_owned "$LEGACY_SUB_BASE" "$LEGACY_SUB_UNIT"; then
        die "old proxystack-web subscription installation found; run --role sub --migrate-sub first"
    fi
    if [[ $ROLE == sub && $SUB_UPGRADE -eq 0 && $MIGRATE_SUB -eq 0 && -z $PUBLIC_KEY ]]; then
        die "--public-key is required (copy it from the panel's 发布订阅包 page)"
    fi
    # 在修改账号、目录或旧服务之前下载好 unit，避免迁移中途依赖网络。
    asset_file systemd/proxystack-web-sub.service "$WORK/$SUB_UNIT"
    if getent passwd "$SUB_USER" >/dev/null; then
        [[ $(getent passwd "$SUB_USER" | cut -d: -f6) == "$SUB_BASE" ]] || die "service account $SUB_USER has an unexpected home directory"
    fi
}

# 迁移在子 shell 内执行，任何失败都会触发回滚；旧目录在成功前始终保留。
migrate_sub() (
    local backup active=0 enabled complete=0 new_created=0 unit_written=0 legacy_stopped=0
    backup=$(mktemp -d /opt/proxystack-web-sub-migration.XXXXXX)
    enabled=$(systemctl is-enabled "$LEGACY_SUB_UNIT" 2>/dev/null) || true
    if systemctl is-active --quiet "$LEGACY_SUB_UNIT"; then active=1; fi
    log "migration backup: $backup"
    cp -a /etc/systemd/system/"$LEGACY_SUB_UNIT" "$backup/$LEGACY_SUB_UNIT"
    printf 'active=%s\nenabled=%s\n' "$active" "$enabled" > "$backup/service-state"

    # shellcheck disable=SC2329 # 由 EXIT trap 调用。
    rollback_sub_migration() {
        local status=$?
        trap - EXIT
        if [[ $complete -eq 0 ]]; then
            log "migration failed; restoring the old subscription service (backup: $backup)"
            if [[ $unit_written -eq 1 ]]; then
                systemctl disable --now "$SUB_UNIT" || true
                rm -f /etc/systemd/system/"$SUB_UNIT"
            fi
            if [[ $new_created -eq 1 && -d $SUB_BASE ]]; then
                mv "$SUB_BASE" "$backup/failed-installation" || true
            fi
            if [[ -d $backup/retired && ! -e $LEGACY_SUB_BASE ]]; then
                mv "$backup/retired" "$LEGACY_SUB_BASE" || true
            elif [[ -d $backup/original && ! -e $LEGACY_SUB_BASE ]]; then
                mv "$backup/original" "$LEGACY_SUB_BASE" || true
            fi
            if [[ -L /usr/local/bin/proxystack-web && $(readlink /usr/local/bin/proxystack-web) == "$SUB_BASE/bin/proxystack-web" ]]; then
                ln -sfn "$LEGACY_SUB_BASE/bin/proxystack-web" /usr/local/bin/proxystack-web || true
            fi
            cp -a "$backup/$LEGACY_SUB_UNIT" /etc/systemd/system/"$LEGACY_SUB_UNIT" || true
            systemctl daemon-reload || true
            case "$enabled" in
                enabled) systemctl enable "$LEGACY_SUB_UNIT" || true ;;
                enabled-runtime) systemctl enable --runtime "$LEGACY_SUB_UNIT" || true ;;
            esac
            if [[ $active -eq 1 && $legacy_stopped -eq 1 ]]; then
                systemctl start "$LEGACY_SUB_UNIT" || true
            fi
            [[ $status -ne 0 ]] || status=1
        fi
        exit "$status"
    }
    trap rollback_sub_migration EXIT
    trap 'exit 130' INT
    trap 'exit 143' TERM

    legacy_stopped=1
    systemctl stop "$LEGACY_SUB_UNIT"
    cp -a "$LEGACY_SUB_BASE" "$backup/original"
    create_account "$SUB_USER" "$SUB_BASE"
    new_created=1
    cp -a "$backup/original" "$SUB_BASE"
    chown -hR "$SUB_USER:$SUB_USER" "$SUB_BASE"
    install -d -m 0750 -o "$SUB_USER" -g "$SUB_USER" "$SUB_BASE/bin" "$SUB_BASE/data"
    install -d -m 2770 -o "$SUB_USER" -g "$SUB_USER" "$SUB_BASE/inbox"
    install -m 0755 -o "$SUB_USER" -g "$SUB_USER" "$WORK/proxystack-web" "$SUB_BASE/bin/proxystack-web"
    write_sub_marker
    unit_written=1
    install -m 0644 "$WORK/$SUB_UNIT" /etc/systemd/system/"$SUB_UNIT"
    systemctl daemon-reload
    systemctl enable --now "$SUB_UNIT"

    local addr code attempt ready=0
    addr=$("$SUB_BASE/bin/proxystack-web" sub --dir "$SUB_BASE" config | sed -n 's/^Listen: //p')
    [[ -n $addr ]] || die "could not read the migrated listen address"
    addr=${addr/#0.0.0.0:/127.0.0.1:}
    addr=${addr/#\[::\]:/[::1]:}
    for ((attempt = 1; attempt <= 10; attempt++)); do
        code=$(curl --noproxy '*' -sS --max-time 2 -o "$WORK/sub-health" -w '%{http_code}' "http://$addr/health") || code=000
        # 尚未导入主来源时 /health 返回 503，也表示新进程已正常启动。
        if [[ $code == 200 || $code == 503 ]] && systemctl is-active --quiet "$SUB_UNIT" \
            && grep -Eq '"status":"(ok|degraded|unavailable)"' "$WORK/sub-health"; then
            ready=1
            break
        fi
        sleep 1
    done
    [[ $ready -eq 1 ]] || die "the migrated subscription service did not become ready"

    systemctl disable "$LEGACY_SUB_UNIT"
    rm /etc/systemd/system/"$LEGACY_SUB_UNIT"
    systemctl daemon-reload
    # 释放旧目录名供 pssub 使用，备份完整保留配置、订阅包、inbox 和旧二进制。
    mv "$LEGACY_SUB_BASE" "$backup/retired"
    if [[ -L /usr/local/bin/proxystack-web && $(readlink /usr/local/bin/proxystack-web) == "$LEGACY_SUB_BASE/bin/proxystack-web" ]]; then
        ln -sfn "$SUB_BASE/bin/proxystack-web" /usr/local/bin/proxystack-web
    elif [[ ! -e /usr/local/bin/proxystack-web && ! -L /usr/local/bin/proxystack-web ]]; then
        ln -s "$SUB_BASE/bin/proxystack-web" /usr/local/bin/proxystack-web
    fi
    complete=1
    log "migration complete; old files and rollback information are retained in $backup"
)

# restore_panel 用迁移包初始化数据库：口令从终端读取（不回显），未加密的迁移包不需要口令。
# 不生成初始管理员密码，恢复后使用源实例的管理员账号登录。
restore_panel() {
    log "restoring from $RESTORE"
    local package="$BASE/backups/restore$$.pswbak"
    install -m 0640 -o "$SERVICE_USER" -g "$SERVICE_USER" "$RESTORE" "$package"
    local args=(--base-dir "$BASE" restore --fresh "$package")
    if [[ $(head -c 21 "$package") == age-encryption.org/v1 ]]; then
        local passphrase
        if [[ -t 0 ]]; then
            read -rsp "Passphrase: " passphrase
            echo
        else
            read -r passphrase
        fi
        PANEL_OUTPUT=$(printf '%s\n' "$passphrase" | runuser -u "$SERVICE_USER" -- "$BASE/bin/proxystack-web" "${args[@]}") || { rm -f "$package"; die "restore failed"; }
    else
        PANEL_OUTPUT=$(runuser -u "$SERVICE_USER" -- "$BASE/bin/proxystack-web" "${args[@]}" --encrypted=false </dev/null) || { rm -f "$package"; die "restore failed"; }
    fi
    rm -f "$package"
}

install_panel() {
    install_polkit

    log "creating service account $SERVICE_USER"
    create_account "$SERVICE_USER" "$BASE"
    getent group systemd-journal >/dev/null && usermod -a -G systemd-journal "$SERVICE_USER"

    log "creating $BASE"
    install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_USER" \
        "$BASE" "$BASE/bin" "$BASE/geo" "$BASE/data" \
        "$BASE/runtime" "$BASE/runtime/generated" "$BASE/runtime/generated/xray" "$BASE/runtime/generated/mihomo" \
        "$BASE/runtime/mihomo" "$BASE/backups" "$BASE/downloads"

    install -m 0755 -o "$SERVICE_USER" -g "$SERVICE_USER" "$WORK/proxystack-web" "$BASE/bin/proxystack-web"
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

    if [[ -n $RESTORE ]]; then
        restore_panel
    else
        log "initializing database"
        local args=(--base-dir "$BASE" init --port "$PORT")
        [[ -n $LISTEN ]] && args+=(--listen "$LISTEN")
        PANEL_OUTPUT=$(runuser -u "$SERVICE_USER" -- "$BASE/bin/proxystack-web" "${args[@]}")
    fi

    log "starting proxystack-web.service"
    systemctl enable --now proxystack-web.service
}

install_sub() {
    if [[ $MIGRATE_SUB -eq 1 ]]; then
        migrate_sub
        SUB_OUTPUT=$("$SUB_BASE/bin/proxystack-web" sub --dir "$SUB_BASE" status)
        return
    fi
    if [[ $SUB_UPGRADE -eq 1 ]]; then
        # 卸载时保留配置和订阅包后重新安装也走这里，bin 目录和 unit 可能已不存在。
        log "upgrading the subscription server binary"
        create_account "$SUB_USER" "$SUB_BASE"
        install -d -m 0750 -o "$SUB_USER" -g "$SUB_USER" "$SUB_BASE/bin"
        install -m 0755 -o "$SUB_USER" -g "$SUB_USER" "$WORK/proxystack-web" "$SUB_BASE/bin/proxystack-web"
        [[ -e /usr/local/bin/proxystack-web ]] || ln -s "$SUB_BASE/bin/proxystack-web" /usr/local/bin/proxystack-web
        write_sub_marker
        install -m 0644 "$WORK/$SUB_UNIT" /etc/systemd/system/"$SUB_UNIT"
        systemctl daemon-reload
        systemctl enable proxystack-web-sub.service
        systemctl restart proxystack-web-sub.service
        SUB_OUTPUT=$("$SUB_BASE/bin/proxystack-web" sub --dir "$SUB_BASE" status)
        return
    fi

    log "creating service account $SUB_USER"
    create_account "$SUB_USER" "$SUB_BASE"

    log "creating $SUB_BASE"
    install -d -m 0750 -o "$SUB_USER" -g "$SUB_USER" "$SUB_BASE" "$SUB_BASE/bin" "$SUB_BASE/data"
    # 收件目录允许同组写入，便于把运维账号加入 proxystack-web-sub 组后直接放入订阅包。
    install -d -m 2770 -o "$SUB_USER" -g "$SUB_USER" "$SUB_BASE/inbox"
    install -m 0755 -o "$SUB_USER" -g "$SUB_USER" "$WORK/proxystack-web" "$SUB_BASE/bin/proxystack-web"
    [[ -e /usr/local/bin/proxystack-web ]] || ln -s "$SUB_BASE/bin/proxystack-web" /usr/local/bin/proxystack-web

    log "writing configuration"
    local args=(sub --dir "$SUB_BASE" init --source "$SOURCE" --public-key "$PUBLIC_KEY")
    [[ -n $SUB_LISTEN ]] && args+=(--listen "$SUB_LISTEN")
    [[ -n $SUB_PORT ]] && args+=(--port "$SUB_PORT")
    runuser -u "$SUB_USER" -- "$SUB_BASE/bin/proxystack-web" "${args[@]}" >/dev/null
    write_sub_marker

    log "writing systemd unit"
    install -m 0644 "$WORK/$SUB_UNIT" /etc/systemd/system/"$SUB_UNIT"
    systemctl daemon-reload

    log "starting proxystack-web-sub.service"
    systemctl enable --now proxystack-web-sub.service
    SUB_OUTPUT=$("$SUB_BASE/bin/proxystack-web" sub --dir "$SUB_BASE" status)
}

PANEL_OUTPUT=""
SUB_OUTPUT=""
fetch_binary
if [[ $ROLE != panel ]]; then
    prepare_sub
fi
if [[ $ROLE != sub ]]; then
    install_panel
fi
if [[ $ROLE == all ]]; then
    read -r SOURCE PUBLIC_KEY < <(runuser -u "$SERVICE_USER" -- "$BASE/bin/proxystack-web" --base-dir "$BASE" info --sub-key)
fi
if [[ $ROLE != panel ]]; then
    install_sub
fi

if [[ -n $PANEL_OUTPUT ]]; then
    echo
    echo "$PANEL_OUTPUT"
    echo
    echo "The panel serves plain HTTP on the address above. Put nginx in front of it;"
    echo "see deploy/nginx/proxystack-web.conf for an example."
fi
if [[ -n $SUB_OUTPUT ]]; then
    echo
    echo "Subscription server ($SUB_BASE):"
    echo "$SUB_OUTPUT"
    echo
    echo "Publish a bundle on the panel's 发布订阅包 page, copy it here, then run"
    echo "  proxystack-web sub --dir $SUB_BASE import FILE"
    echo "or drop it into $SUB_BASE/inbox/."
fi
