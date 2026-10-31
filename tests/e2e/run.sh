#!/usr/bin/env bash
# 端到端测试：在以 systemd 为 PID 1 的 Debian 12 / Ubuntu 24.04 容器中用安装脚本安装本仓库构建的二进制，
# 然后从宿主机跑 e2e_test.go 的完整流程。需要 Docker（容器以 --privileged 运行）和访问 GitHub 的网络。
#
# 用法：tests/e2e/run.sh [debian-12] [ubuntu-24.04]   不带参数时两个都跑
# 环境变量：
#   PSW_E2E_PROXY  下载 xray / mihomo / geo 使用的代理，例如 http://user:pass@host:port
#   KEEP=1         测试结束后保留容器，便于排查
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
DISTROS=("$@")
[[ ${#DISTROS[@]} -gt 0 ]] || DISTROS=(debian-12 ubuntu-24.04)

case "$(docker info -f '{{.Architecture}}')" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported docker architecture" >&2; exit 1 ;;
esac

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

log() { echo "==> $*"; }

if [[ ! -f $ROOT/web/dist/index.html ]]; then
    log "building the web UI"
    make -C "$ROOT" web
fi
log "building proxystack-web for linux/$ARCH"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$ARCH go build -trimpath \
    -ldflags "-X proxystack-web/internal/version.Version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)" \
    -o "$WORK/proxystack-web" ./cmd/proxystack-web)

run_distro() {
    local distro=$1 base expect
    case "$distro" in
        debian-12) base=debian:12 expect="Debian GNU/Linux 12" ;;
        ubuntu-24.04) base=ubuntu:24.04 expect="Ubuntu 24.04" ;;
        *) echo "unknown distro: $distro (debian-12 or ubuntu-24.04)" >&2; return 2 ;;
    esac
    local name=psw-e2e-$distro image=psw-e2e:$distro

    log "[$distro] building the test machine image"
    docker build -q -t "$image" --build-arg BASE="$base" "$ROOT/tests/e2e" >/dev/null

    docker rm -f "$name" >/dev/null 2>&1 || true
    docker run -d --name "$name" --privileged --cgroupns=host \
        -v /sys/fs/cgroup:/sys/fs/cgroup:rw --tmpfs /run --tmpfs /run/lock \
        -p 127.0.0.1::9443 "$image" >/dev/null
    # 不论成败都清理容器（KEEP=1 时保留）。
    if [[ ${KEEP:-} != 1 ]]; then
        trap "docker rm -f $name >/dev/null 2>&1 || true; rm -rf '$WORK'" EXIT
    fi
    docker exec "$name" systemctl is-system-running --wait >/dev/null || true

    log "[$distro] running install.sh --role all"
    docker cp "$WORK/proxystack-web" "$name:/root/proxystack-web"
    docker cp "$ROOT/deploy" "$name:/root/deploy"
    docker exec "$name" bash /root/deploy/install.sh --role all --binary /root/proxystack-web | tee "$WORK/install-$distro.log"

    local password port
    password=$(sed -n 's|^ *Password: *||p' "$WORK/install-$distro.log")
    port=$(docker port "$name" 9443/tcp | head -1 | sed 's|.*:||')
    [[ -n $password && -n $port ]] || { echo "could not parse the install output" >&2; return 1; }

    log "[$distro] running the end-to-end test"
    local status=0
    (cd "$ROOT" && PSW_E2E_CONTAINER=$name PSW_E2E_URL="http://127.0.0.1:$port" \
        PSW_E2E_PASSWORD=$password PSW_E2E_EXPECT_OS="$expect" \
        go test -tags e2e -count=1 -v -timeout 40m ./tests/e2e/) || status=$?
    if [[ $status -ne 0 ]]; then
        log "[$distro] FAILED; recent proxystack-web logs:"
        docker exec "$name" journalctl -u proxystack-web -u 'psw-*' --no-pager -n 80 || true
    fi
    if [[ ${KEEP:-} != 1 ]]; then
        docker rm -f "$name" >/dev/null 2>&1 || true
    else
        log "[$distro] container $name kept (panel http://127.0.0.1:$port/)"
    fi
    return $status
}

failed=()
for distro in "${DISTROS[@]}"; do
    run_distro "$distro" || failed+=("$distro")
done
if [[ ${#failed[@]} -gt 0 ]]; then
    echo "end-to-end test failed on: ${failed[*]}" >&2
    exit 1
fi
log "end-to-end test passed on: ${DISTROS[*]}"
