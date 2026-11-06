# proxystack-web

proxystack-web 在一台 Linux 服务器上集中管理 xray 与 mihomo 代理服务，并为代理用户提供订阅。系统由两个服务组成：

- **管理端**：Web 管理面板。负责录入用户与 stack、生成并下发代理配置、启停代理服务、管理依赖版本、统计流量、执行定时任务和签发订阅包。
- **订阅端**：向代理客户端提供订阅内容。订阅端与管理端在网络上相互隔离，数据全部来自管理端签发的订阅包；订阅包可以由管理端经 SSH 在线发布，也可以在网络互不连通时人工拷贝导入。

两个服务都以单个二进制文件发布，内置 Web 界面和 SQLite 数据库，不依赖其他外部服务。

## 功能

- **Stack 管理**：一个 stack 是一组独立运行的 xray 与 mihomo 进程。stack 分为两类：edge stack 直接出网，auto stack 把流量经 edge stack 的 relay 入站转发，并在多个 edge stack 之间测速或负载均衡。按模板新建后可以编辑入站、出站、上游、代理组和规则，端口从端口池自动分配。Stack 列表顶部的流量拓扑概括整体流向，单个 stack 的编辑页展示从入站到上游的完整流向。保存只写入数据库，应用后才生成配置并重启相关服务。xray 与 mihomo 由独立的 systemd unit 承载，管理服务重启期间代理流量不中断。
- **用户与凭据**：每个用户拥有默认凭据和任意数量的凭据档案，可以设置额度、每日用量阈值、重置周期和到期时间。用户可以针对全部或部分 stack 暂停，暂停经 xray 的用户管理接口立即生效，无需重新应用配置。额度超限、每日用量达到阈值或到期时，系统按设置自动暂停并发送邮件通知。
- **订阅**：订阅支持 Clash、Premium Clash 和 Surge 三种格式，模板可以在内置模板基础上自定义，并按任意用户实时预览。订阅包经签名后发布到一个或多个订阅端，订阅端只接受受信任来源签发的订阅包。
- **流量统计**：按设定间隔采集各 stack 的计数，按分钟、小时、日、月四级汇总，各级保留期可以单独设置。统计分为按用户和按 Stack 两种视角，粒度可选小时、日、月、年，时间范围任意，支持与上一期对比、峰值速率和 CSV 导出；排行按今日、昨日、本月、上月或自定义时间段列出用户用量。跨 stack 的合计不含 auto stack，避免与 edge stack 的 relay 入站重复计数；无法归属到用户的流量分为经 relay 入站和共享账号两类单独列出。采集中断的时段被记录并在页面中标出。
- **依赖管理**：在面板中安装、升级和回滚 xray、mihomo 与 geo 数据。新版本先通过当前配置的校验再替换，失败自动回滚。全部对外下载可以经下载代理进行。
- **运维**：内置数据库备份、流量数据清理、geo 数据更新、依赖版本检查与自动升级、远端节点源同步和对账等定时任务；健康检查页面汇总依赖、服务状态、配置漂移、端口占用、流量采集、数据库、备份和磁盘空间等检查项，并给出处理建议。管理员操作、配置版本和邮件通知均有历史记录。
- **账号安全**：首次登录强制修改密码，支持两步验证、登录失败锁定和一次性密码重置链接。
- **升级与迁移**：管理端可以在面板中立即升级或预约升级，升级前校验发布附带的 SHA-256 并保留上一版本。整机迁移包包含全部数据，默认以口令加密，可以在新机器安装时直接恢复。

## 部署结构

- 管理端默认监听 `0.0.0.0:9443`，安装在 `/opt/proxystack-web`，服务账号为 `proxystack-web`，服务名为 `proxystack-web.service`。
- 订阅端默认监听 `127.0.0.1:9444`，安装在 `/opt/proxystack-web-sub`，服务账号为 `proxystack-web-sub`，服务名为 `proxystack-web-sub.service`。
- 两个服务都只提供明文 HTTP，TLS 由 nginx 终结。反向代理管理端时需要传递 `X-Forwarded-For`（取 `$remote_addr`）和 `X-Forwarded-Proto`（取 `$scheme`），关闭响应缓冲（`proxy_buffering off`），把读超时设为一小时左右，并取消请求体大小限制（`client_max_body_size 0`）。

## 系统要求

- Linux，使用 systemd 226 及以上版本。经过完整验证的发行版为 Debian 12 和 Ubuntu 24.04。
- CPU 架构为 amd64 或 arm64。
- 安装需要 root 权限。管理端依赖 polkit，系统未安装时安装脚本通过 apt、dnf 或 yum 自动安装。
- 管理端下载 xray、mihomo 和 geo 数据需要访问 GitHub；无法直连时可以在面板中配置下载代理。

## 安装

安装管理端：

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh | sudo bash
```

服务器访问 GitHub 需要代理时，先设置环境变量，使安装脚本内部的下载同样经过代理：

```bash
export https_proxy=http://127.0.0.1:7890
curl -fsSL --proxy "$https_proxy" https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh | sudo -E bash
```

安装完成后，终端打印面板地址、用户名和初始密码。初始密码只打印这一次。

单独安装订阅端时，先在管理端的「发布订阅包」页面复制来源名和签名公钥，然后在订阅端机器上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh \
  | sudo bash -s -- --role sub --source main --public-key 'ed25519:...'
```

安装脚本的常用参数：

| 参数 | 说明 |
| --- | --- |
| `--role panel\|sub\|all` | 安装角色，默认 `panel`；`all` 把管理端与订阅端装在同一台机器上，订阅端自动信任本机管理端的签名公钥 |
| `--version vA.B.C` | 安装指定版本，默认最新正式版 |
| `--listen ADDR`、`--port PORT` | 管理端监听地址与端口 |
| `--sub-listen ADDR`、`--sub-port PORT` | 订阅端监听地址与端口 |
| `--restore FILE` | 用迁移包初始化管理端 |
| `--binary FILE` | 安装本地的二进制文件，不从网络下载 |

安装脚本只负责首次安装。目标机器上已经存在管理端数据库时，脚本在执行任何改动之前退出，已有服务和数据不受影响。

## 首次使用

1. 用安装时打印的地址、用户名和初始密码登录，按提示设置至少 10 个字符的新密码。
2. 建议在「账号安全」页面开启两步验证。
3. 按「总览」页面顶部初始化清单的顺序完成：设置对外地址、安装 xray 和 mihomo、创建第一个用户、创建第一个 Stack、应用配置，以及按需发布订阅包。

## 升级

管理端在「系统」页面的「版本与升级」中升级，可以立即升级，也可以预约在新版本发布后自动升级。升级依次执行查找版本、下载、校验 SHA-256、试运行并原子替换程序、重启管理服务和确认新版本，失败时停在出错的阶段。

订阅端再次执行订阅端安装命令即可升级：安装命令保留配置和已导入的订阅包，替换二进制、重启服务并做版本健康检查，失败自动恢复上一版。

## 备份与迁移

- 定时任务「数据库备份」定期把数据库快照写入 `/opt/proxystack-web/backups`，超过保留份数的旧备份自动删除。
- 「备份」页面可以导出整机迁移包，也可以在命令行执行 `sudo proxystack-web backup --output FILE`。迁移包包含配置、凭据、订阅 token、流量记录、审计日志和管理员账号，不含 xray、mihomo 与 geo 文件。
- 迁移到新机器时，在安装命令后附加 `--restore`，脚本从终端读取迁移包口令，恢复后使用源实例的管理员账号登录，再在「依赖」页面安装与源实例相同版本的 xray、mihomo 与 geo：

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh \
  | sudo bash -s -- --restore /root/backup.pswbak
```

迁移包和备份文件包含全部凭据，应保存在可信位置并及时删除副本。

## 救援命令

以下命令以 root 或服务账号身份在服务器上执行，直接写入数据库，不需要重启服务：

| 命令 | 作用 |
| --- | --- |
| `proxystack-web admin reset-password` | 生成新的随机密码并打印；附加 `--password-stdin` 时从标准输入读取指定密码 |
| `proxystack-web admin reset-link` | 打印一次性密码重置链接，15 分钟内有效 |
| `proxystack-web admin set-listen --listen ADDR --port PORT` | 修改面板监听地址或端口，重启管理服务后生效 |
| `proxystack-web info` | 显示面板地址和订阅包签名公钥 |

`reset-password` 与 `reset-link` 都可以附加 `--disable-totp`，同时关闭两步验证。

## 卸载

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/uninstall.sh \
  | sudo bash -s -- --role all
```

卸载停止并删除服务、unit 文件、polkit 规则和命令链接，保留管理端的数据与备份目录，以及订阅端的配置和已导入的订阅包。附加 `--purge` 时删除整个安装目录和服务账号。

经 SSH 部署的订阅端也可以在管理端「SSH 发布与部署」页面卸载：卸载在后台任务中删除订阅服务、程序、配置、已导入的订阅包和服务账号，不保留数据，可以选择成功后删除管理端的订阅端配置。同一 SSH 主机和端口上有多个订阅端时，面板拒绝卸载。SSH 公钥、sudoers 规则和 sshd 限制配置需要手动清理。

## 开发

开发环境需要 Go 1.26 和 Node.js 24，生成接口代码还需要 sqlc。

| 命令 | 作用 |
| --- | --- |
| `make dev-init` | 在仓库下的 `.dev` 目录初始化开发实例，面板监听 `127.0.0.1:9090` |
| `make dev-serve` | 以开发模式启动管理端，systemd 与 xray 接口由模拟实现代替 |
| `make web` | 安装前端依赖并构建 Web 界面，构建结果嵌入二进制 |
| `make build` | 构建本机二进制 |
| `make gen` | 重新生成数据库查询代码和前端接口类型 |
| `make test` | 运行后端测试和前端类型检查 |
| `make e2e` | 在 Debian 12 和 Ubuntu 24.04 的 systemd 容器中安装并跑完整流程，需要 Docker 和外网 |
| `make dist` | 构建 amd64 与 arm64 的发布产物及其 SHA-256 校验文件 |

前端单独调试时，在前端目录执行 `npm run dev`，开发服务器把接口请求转发到本地以开发模式运行的管理端。前端单元测试使用 `node --test` 运行。

推送以 `v` 开头的标签时，发布流程运行测试、构建 amd64 与 arm64 的二进制和离线安装包，并发布到 GitHub Release。安装脚本和面板自更新都从 GitHub Release 下载正式版本。
