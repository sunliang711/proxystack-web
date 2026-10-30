#!/usr/bin/env bash
# proxystack-web 卸载脚本：停止并删除服务、unit 文件、polkit 规则和软链接。
# --role 选择卸载管理端、订阅端或两者（默认两者，只处理已安装的部分）。
# 默认保留管理端的 data 和 backups、订阅端的配置和已导入的订阅包；--purge 删除整个目录和服务账号。
set -euo pipefail

BASE=/opt/proxystack-web
SERVICE_USER=proxystack-web
SUB_BASE=/opt/proxystack-sub
SUB_USER=proxystack-sub
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
    [[ -d $SUB_BASE || -e /etc/systemd/system/proxystack-sub.service ]] || return 0
    echo "==> stopping the subscription server"
    systemctl disable --now proxystack-sub.service 2>/dev/null || true
    rm -f /etc/systemd/system/proxystack-sub.service
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
systemctl reset-failed 'psw-*' 'proxystack-*' 2>/dev/null || true
echo "proxystack-web uninstalled ($ROLE)."
