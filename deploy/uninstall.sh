#!/usr/bin/env bash
# proxystack-web 卸载脚本：停止并删除全部服务、unit 文件、polkit 规则和软链接。
# 默认保留 data 和 backups；--purge 删除整个安装目录和服务账号。
set -euo pipefail

BASE=/opt/proxystack-web
SERVICE_USER=proxystack-web
PURGE=0

case "${1:-}" in
    --purge) PURGE=1 ;;
    "") ;;
    *) echo "usage: uninstall.sh [--purge]" >&2; exit 2 ;;
esac

[[ $(id -u) -eq 0 ]] || { echo "error: run as root" >&2; exit 1; }

echo "==> stopping services"
systemctl disable --now proxystack-web.service 2>/dev/null || true
for unit in $(systemctl list-units --all --plain --no-legend 'psw-xray@*.service' 'psw-mihomo@*.service' | awk '{print $1}'); do
    systemctl stop "$unit" || true
done

echo "==> removing units, polkit rule and symlink"
rm -f /etc/systemd/system/proxystack-web.service \
      /etc/systemd/system/psw-xray@.service \
      /etc/systemd/system/psw-mihomo@.service \
      /etc/polkit-1/rules.d/50-proxystack-web.rules \
      /usr/local/bin/proxystack-web
systemctl daemon-reload
systemctl reset-failed 'psw-*' 2>/dev/null || true

if [[ $PURGE -eq 1 ]]; then
    echo "==> purging $BASE and service account"
    rm -rf "${BASE:?}"
    userdel "$SERVICE_USER" 2>/dev/null || true
else
    echo "==> removing binaries and runtime files (keeping $BASE/data and $BASE/backups)"
    rm -rf "${BASE:?}/bin" "${BASE:?}/geo" "${BASE:?}/runtime" "${BASE:?}/downloads"
fi
echo "proxystack-web uninstalled."
