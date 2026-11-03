#!/usr/bin/env bash
# 在独立 systemd 容器中测试订阅端安装、共存、卸载和迁移；不操作宿主机的服务或目录。
# 可用 PSW_DEPLOY_IMAGE 指定已有的 systemd 测试镜像，否则构建 Debian 12 镜像。
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
WORK=$(mktemp -d)
NAME=psw-deploy-$(basename "$WORK" | tr '[:upper:]' '[:lower:]')
cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

case "$(docker info -f '{{.Architecture}}')" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported docker architecture" >&2; exit 1 ;;
esac
IMAGE=${PSW_DEPLOY_IMAGE:-psw-deploy-test:debian-12}
if [[ -z ${PSW_DEPLOY_IMAGE:-} ]]; then
    docker build -q -t "$IMAGE" --build-arg BASE=debian:12 "$ROOT/tests/e2e" >/dev/null
fi
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -ldflags "-X proxystack-web/internal/version.Version=v0.1.0-test" -o "$WORK/proxystack-web-v1" ./cmd/proxystack-web)
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -ldflags "-X proxystack-web/internal/version.Version=v0.2.0-test" -o "$WORK/proxystack-web-v2" ./cmd/proxystack-web)
docker run -d --name "$NAME" --privileged --cgroupns=host \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock "$IMAGE" >/dev/null
for ((attempt = 1; attempt <= 30; attempt++)); do
    if docker exec "$NAME" test -S /run/systemd/private; then break; fi
    sleep 1
done
docker exec "$NAME" systemctl is-system-running --wait >/dev/null || true
docker cp "$ROOT/deploy" "$NAME:/root/sub-isolation-deploy"
docker cp "$WORK/proxystack-web-v1" "$NAME:/root/sub-isolation-binary-v1"
docker cp "$WORK/proxystack-web-v2" "$NAME:/root/sub-isolation-binary-v2"
(cd "$ROOT" && PSW_DEPLOY_CONTAINER=$NAME go test -tags e2e -count=1 -v -timeout 6m ./tests/deploy)
