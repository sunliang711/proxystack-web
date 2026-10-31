# 端到端测试用的「虚拟机」：以 systemd 为 PID 1 的 Debian / Ubuntu 容器。
# polkit 不预装，由安装脚本自行安装，与真实机器上的首次安装一致。
ARG BASE=debian:12
FROM ${BASE}
ENV container=docker DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
    && apt-get install -y --no-install-recommends systemd systemd-sysv dbus ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && systemctl mask systemd-logind.service getty.target console-getty.service systemd-udevd.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
