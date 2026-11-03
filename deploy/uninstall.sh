#!/usr/bin/env bash
# proxystack-web 卸载脚本：停止并删除服务、unit 文件、polkit 规则和软链接。
# --role 选择卸载管理端、订阅端或两者（默认两者，只处理已安装的部分）。
# 默认保留管理端的 data 和 backups、订阅端的配置和已导入的订阅包；--purge 删除整个目录和服务账号。
set -euo pipefail

BASE=/opt/proxystack-web
SERVICE_USER=proxystack-web
SUB_BASE=/opt/proxystack-web-sub
SUB_USER=proxystack-web-sub
SUB_UNIT=proxystack-web-sub.service
ROLE=all
PURGE=0

usage() { echo "usage: uninstall.sh [--role panel|sub|all] [--purge]" >&2; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --purge) PURGE=1; shift ;;
        --role) ROLE=$2; shift 2 ;;
        *) usage; exit 2 ;;
    esac
done
case "$ROLE" in
    panel|sub|all) ;;
    *) usage; exit 2 ;;
esac

[[ $(id -u) -eq 0 ]] || { echo "error: run as root" >&2; exit 1; }

die() { echo "error: $*" >&2; exit 1; }

# 与安装脚本采用相同的归属判定；脚本可独立下载运行，不依赖额外的公共脚本。
sub_unit_owned() {
    local dir=$1 unit=$2 command
    command=$(systemctl show --value -p ExecStart "$unit" 2>/dev/null) || return 1
    [[ $command == "{ path=$dir/bin/proxystack-web ; argv[]=$dir/bin/proxystack-web sub --dir $dir serve ; "* ]]
}

check_sub_ownership() {
    local links
    [[ ! -L $SUB_BASE ]] || die "refusing symbolic link: $SUB_BASE"
    [[ ! -L /etc/systemd/system/$SUB_UNIT ]] || die "refusing a symbolic link for $SUB_UNIT"
    if [[ -e $SUB_BASE ]]; then
        [[ -d $SUB_BASE ]] || die "not a directory: $SUB_BASE"
        links=$(find "$SUB_BASE" -type l -print -quit) || die "cannot inspect $SUB_BASE"
        [[ -z $links ]] || die "refusing symbolic links inside $SUB_BASE"
    fi
    local state marker=$SUB_BASE/.proxystack-web-sub
    state=$(systemctl show --value -p LoadState "$SUB_UNIT" 2>/dev/null) || true
    if [[ -n $state && $state != not-found ]]; then
        sub_unit_owned "$SUB_BASE" "$SUB_UNIT" || die "$SUB_UNIT belongs to another installation; refusing to remove it"
    elif [[ -e $SUB_BASE ]]; then
        [[ -f $marker && ( $(cat "$marker") == proxystack-web-sub-v1 || $(cat "$marker") == proxystack-web-sub-v2 ) ]] || die "cannot confirm ownership of $SUB_BASE"
    else
        if sub_unit_owned /opt/proxystack-sub proxystack-sub.service; then
            die "old proxystack-web subscription installation found; migrate it with install.sh --role sub --migrate-sub first"
        fi
        return
    fi
    if [[ -e $SUB_BASE/config.yaml ]]; then
        if [[ -x $SUB_BASE/bin/proxystack-web ]]; then
            "$SUB_BASE/bin/proxystack-web" sub --dir "$SUB_BASE" config >/dev/null || die "invalid subscription configuration; refusing to remove $SUB_BASE"
        else
            # 普通卸载会保留此标记，后续 --purge 无需重新安装二进制即可确认归属。
            [[ -f $marker && ( $(cat "$marker") == proxystack-web-sub-v1 || $(cat "$marker") == proxystack-web-sub-v2 ) ]] || die "cannot verify the retained subscription data in $SUB_BASE"
        fi
    fi
    if getent passwd "$SUB_USER" >/dev/null; then
        [[ $(getent passwd "$SUB_USER" | cut -d: -f6) == "$SUB_BASE" ]] || die "service account $SUB_USER has an unexpected home directory"
    fi
}

# 在卸载任一角色前检查，避免 --role all 在发现冲突之前已删除管理端。
if [[ $ROLE != panel ]]; then
    check_sub_ownership
fi

uninstall_panel() {
    [[ -d $BASE || -e /etc/systemd/system/proxystack-web.service ]] || return 0
    echo "==> stopping the management service and proxy units"
    systemctl disable --now proxystack-web.service 2>/dev/null || true
    for unit in $(systemctl list-units --all --plain --no-legend 'psw-xray@*.service' 'psw-mihomo@*.service' | awk '{print $1}'); do
        systemctl stop "$unit" || true
    done
    rm -f /etc/systemd/system/proxystack-web.service \
          /etc/systemd/system/psw-xray@.service \
          /etc/systemd/system/psw-mihomo@.service \
          /etc/polkit-1/rules.d/50-proxystack-web.rules
    if [[ $PURGE -eq 1 ]]; then
        echo "==> purging $BASE and service account $SERVICE_USER"
        rm -rf "${BASE:?}"
        userdel "$SERVICE_USER" 2>/dev/null || true
    else
        echo "==> removing panel binaries and runtime files (keeping $BASE/data and $BASE/backups)"
        rm -rf "${BASE:?}/bin" "${BASE:?}/geo" "${BASE:?}/runtime" "${BASE:?}/downloads"
    fi
}

uninstall_sub() {
    [[ -d $SUB_BASE || -e /etc/systemd/system/proxystack-web-sub.service ]] || return 0
    if [[ -x $SUB_BASE/bin/proxystack-web ]]; then
        local args=(sub --dir "$SUB_BASE" deploy uninstall)
        [[ $PURGE -eq 1 ]] && args+=(--purge)
        "$SUB_BASE/bin/proxystack-web" "${args[@]}"
        return
    fi
    echo "==> stopping the subscription server"
    systemctl disable --now proxystack-web-sub.service 2>/dev/null || true
    rm -f /etc/systemd/system/proxystack-web-sub.service
    if [[ $PURGE -eq 1 ]]; then
        echo "==> purging $SUB_BASE and service account $SUB_USER"
        rm -rf "${SUB_BASE:?}"
        userdel "$SUB_USER" 2>/dev/null || true
    else
        echo "==> removing the subscription server binary (keeping $SUB_BASE/config.yaml and $SUB_BASE/data)"
        rm -rf "${SUB_BASE:?}/bin"
    fi
}

[[ $ROLE != sub ]] && uninstall_panel
[[ $ROLE != panel ]] && uninstall_sub

# 软链接指向已删除的二进制时一并删除；另一个角色仍在时改指向它。
link=/usr/local/bin/proxystack-web
if [[ -L $link && ! -e $link ]]; then
    rm -f "$link"
    if [[ -x $BASE/bin/proxystack-web ]]; then
        ln -s "$BASE/bin/proxystack-web" "$link"
    elif [[ -x $SUB_BASE/bin/proxystack-web ]]; then
        ln -s "$SUB_BASE/bin/proxystack-web" "$link"
    fi
fi
systemctl daemon-reload
systemctl reset-failed 'psw-*' proxystack-web.service "$SUB_UNIT" 2>/dev/null || true
echo "proxystack-web uninstalled ($ROLE)."
