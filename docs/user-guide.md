# proxystack-web 使用手册

proxystack-web 用于在一台 Linux 服务器上集中管理 xray 与 mihomo 代理服务，并为代理用户提供订阅。系统由两个服务组成：

- **管理端**：提供 Web 管理面板，负责录入用户与 stack、生成并下发代理配置、启停代理服务、管理依赖版本、监控流量、执行定时任务和签发订阅包。
- **订阅端**：向代理客户端提供订阅内容。订阅端的数据全部来自管理端签发的订阅包，可以由管理端通过 SSH 正向转发在线发布，也可以在网络互不连通时人工拷贝。

本手册按安装、首次使用、日常操作、账号安全、升级、备份迁移、救援和卸载的顺序说明。

## 1. 系统要求

- Linux，使用 systemd 226 及以上版本作为 init 系统。经过完整验证的发行版为 Debian 12 和 Ubuntu 24.04。
- CPU 架构为 amd64 或 arm64。
- 安装时需要 root 权限。管理端依赖 polkit，系统未安装时安装脚本通过 apt、dnf 或 yum 自动安装。
- 管理端下载 xray、mihomo 和 geo 数据需要访问 GitHub。服务器无法直连时，可以在面板中配置下载代理（见第 7.2 节）。

## 2. 安装

安装脚本按角色安装：

| 角色 | 安装内容 |
| --- | --- |
| `panel`（默认） | 管理端：服务账号 `proxystack-web`、目录 `/opt/proxystack-web`、systemd unit 与 polkit 规则，初始化数据库并启动服务 |
| `sub` | 订阅端：服务账号 `proxystack-web-sub`、目录 `/opt/proxystack-web-sub` 与 systemd unit |
| `all` | 管理端与订阅端安装在同一台机器上，订阅端自动信任本机管理端的签名公钥 |

### 2.1 安装管理端

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh | sudo bash
```

服务器访问 GitHub 需要代理时，先设置环境变量，使安装脚本内部的下载同样经过代理：

```bash
export https_proxy=http://127.0.0.1:7890
curl -fsSL --proxy "$https_proxy" https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh | sudo -E bash
```

常用参数：

| 参数 | 说明 |
| --- | --- |
| `--role panel\|sub\|all` | 安装角色，默认 `panel` |
| `--version vA.B.C` | 安装指定版本，默认最新版本 |
| `--listen ADDR`、`--port PORT` | 管理端监听地址与端口，默认 `0.0.0.0:9443` |
| `--restore FILE` | 用迁移包初始化管理端，不生成新的管理员密码（见第 8.2 节） |
| `--binary FILE` | 安装本地的二进制文件，不从网络下载 |

安装完成后，终端打印面板地址、用户名和初始密码：

```text
proxystack-web initialized.
  Panel URL:  http://203.0.113.10:9443/
  Username:   admin
  Password:   ********************
```

初始密码只打印这一次，数据库只保存其哈希值。面板挂载在根路径下，在浏览器中打开 `http://<服务器地址>:9443/` 即可进入登录页。面板直接暴露在公网时，建议用 nginx 加 TLS，或只对可信来源开放该端口。

安装脚本只负责首次安装。目标机器上已经存在管理端数据库时，脚本在执行任何改动之前退出，正在运行的服务和已有数据不受影响。管理端的升级在面板中完成（见第 7.1 节）。

### 2.2 安装订阅端

订阅端单独部署时，先在管理端的「发布订阅包」页面复制来源名和签名公钥，然后在订阅端机器上执行：

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh \
  | sudo bash -s -- --role sub --source main --public-key 'ed25519:...'
```

订阅端默认监听 `127.0.0.1:9444`，可以用 `--sub-listen` 和 `--sub-port` 指定。安装脚本只是 `sub deploy apply` 的薄封装；再次执行时会保留配置和已导入的订阅包，替换二进制、重启服务并做版本健康检查，失败自动恢复上一版。

脚本地址中的 `main` 只决定安装脚本的版本；未指定 `--binary` 时，脚本下载 GitHub Release 的二进制，默认使用最新正式发布版本，不会从 `main` 源码编译。旧版二进制的 CLI 默认目录仍可能是 `/opt/proxystack-sub`，管理订阅数据时请显式指定 `sub --dir /opt/proxystack-web-sub`，以便与服务读取的目录保持一致。

本项目使用 `/opt/proxystack-web-sub`、账号 `proxystack-web-sub` 和 `proxystack-web-sub.service`，与 proxystack-go 的 pssub（`/opt/proxystack-sub`、`proxystack-sub.service`）独立，可以同时安装。管理订阅服务使用：

```bash
sudo systemctl status proxystack-web-sub.service
sudo systemctl restart proxystack-web-sub.service
sudo journalctl -u proxystack-web-sub.service
```

安装和卸载会检查服务实际执行的命令、配置和安装标记，遇到归属不明的目录或同名服务时退出。普通卸载保留 `.proxystack-web-sub` 标记，便于之后重装或执行 `--purge`；不要手工复制该标记到其他项目目录。

离线使用裸二进制时也调用同一部署命令：

```bash
sudo ./proxystack-web-linux-amd64 sub --dir /opt/proxystack-web-sub deploy apply \
  --sha256 '<发布页校验值>' --source main --public-key 'ed25519:...'
sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy rollback
sudo /opt/proxystack-web-sub/bin/proxystack-web sub --dir /opt/proxystack-web-sub deploy uninstall
```

普通卸载保留配置和订阅数据；追加 `--purge` 才删除整个目录和服务账号。新版不再支持 `--migrate-sub`，旧目录安装应先用支持迁移的旧版本完成迁移，再升级到新版。

### 2.3 配置 nginx 反向代理

管理端和订阅端都只提供明文 HTTP，TLS 应由 nginx 终结。反向代理管理端时需要满足以下要求：

- 传递 `X-Forwarded-For`，取值为 `$remote_addr`；传递 `X-Forwarded-Proto`，取值为 `$scheme`。管理端以经 HTTPS 访问作为会话 cookie 携带 Secure 属性的依据，并以此记录审计日志和登录限速使用的来源地址。
- 关闭响应缓冲（`proxy_buffering off`），并把读超时设为一小时左右。运行状态、作业进度和日志通过服务器推送事件实时传输。
- 取消请求体大小限制（`client_max_body_size 0`），以便上传迁移包和依赖安装包。

管理端只采信本机回环地址连入时携带的 `X-Forwarded-For`，并取其中最后一个地址。nginx 部署在其他机器上时，审计日志记录的是 nginx 的地址，登录限速也按该地址计算。

## 3. 首次登录

1. 用安装时打印的地址、用户名和初始密码登录。
2. 首次登录后必须先设置新密码，新密码至少 10 个字符。改密完成前，面板的其他功能不可用。
3. 建议随后在「账号安全」页面开启两步验证（见第 6 节）。

## 4. 日常操作流程

### 4.1 准备依赖

在「依赖」页面安装 xray、mihomo 和 geo 数据。新版本先在暂存目录中验证能否接受当前全部配置，验证通过后才替换现有版本，并逐个重启使用它的服务；重启后服务未能正常运行时自动回滚到原版本。每个组件保留上一个版本，可以手动回滚。

### 4.2 录入用户与 stack

1. 在「系统设置 → 基本」中填写对外地址，即订阅节点默认使用的域名或 IP。
2. 在「用户」页面新建代理用户。新建 stack 的入站需要引用用户，因此至少要先有一个用户。
3. 在「Stack」页面按模板新建 stack。一个 stack 对应一组独立运行的 xray 与 mihomo 进程，端口从「系统设置 → 端口池」中自动分配，凭据随机生成。新建后可以进入编辑页调整入站、出站、上游、代理组和规则。编辑页中新增的上游自动加入代理组并排在 DIRECT 之前：只有 DIRECT 的代理组会以新上游作为默认选择，含自动测速或负载均衡组的 stack 把新上游加入这些组。stack 改名时，以旧名字开头的入站显示名同步换成新名字，手工改过的显示名保持不变。「Stack」页面顶部的流量拓扑展示客户端、各 stack 之间的上游引用和出口；编辑页的「流量图示」标签展示单个 stack 从入站、出站、规则、代理组到上游的完整流向，按编辑中的内容实时绘制。列表中的出口 IP 列同时显示 IPv4 和 IPv6 出口，查不到的显示为 -。

用户基本信息中的「备注」仅用于管理识别，不覆盖订阅节点名，也不作为展示名模板中的 `.remark`。例如用户 `xc` 备注为 `xiaochen`，加入 `usa1`、`usa3` 的 VMess 入站后，节点仍可分别使用 `usa1 vmess`、`usa3 vmess`。展示名模板依次采用：入站用户单独配置、所选用户档案（default 即用户本身）、Stack 入站、系统设置中的全局模板；均为空时再使用生效的节点备注或 `Stack 名称 + 协议`。普通节点和直连节点使用同一优先级；仅授权直连且不属于任何订阅入站的用户不会借用无关入站模板。同一用户的最终节点名必须唯一。

在 Stack 详情的「入站」中编辑用户列表时，可以点击「添加全部」补齐尚未选择的用户。已有用户的凭据档案和覆盖设置保持不变，新加入的用户使用 default 档案；暂停的用户也会加入，但不会因此恢复。添加结果需先确认入站编辑，再保存 Stack，应用后才会生效。

### 4.3 应用配置

保存操作只写入数据库，不影响正在运行的代理。顶栏的「待应用变更」显示生成物的差异和将要重启的服务，确认后点击「应用」：系统生成配置、校验，然后重启受影响的服务。每次配置变更都会在「配置历史」中保存一个完整快照，可以比较任意两个版本，或回滚到选中的版本；回滚同样需要应用后才生效。

### 4.4 暂停与恢复用户

在用户列表或用户详情中暂停用户后，系统立即通过 xray 的用户管理接口把该用户从代理端移除，配置保持不变，无需应用；恢复时立即加回。暂停可以针对全部 stack，也可以只针对部分 stack。用户的额度超限、每日用量达到阈值或到期时，系统按设置自动暂停并发送邮件通知。

### 4.5 发布订阅

1. 在「订阅模板」页面可以查看或自定义 Clash、Premium Clash 和 Surge 三种格式的模板。
2. 需要在线发布或部署时，先在「SSH 发布与部署」生成管理端 SSH Ed25519 密钥。同一公钥可以安装到多台订阅端，私钥只保存在管理端，不通过 API 或页面返回。
3. 添加订阅端目标，填写 SSH 地址、SSH 用户、Host Key 算法与 SHA256 指纹、订阅端 HTTP 端口和来源名。远端 HTTP 主机固定为 `127.0.0.1`，不能配置成其他地址。
4. 按页面的逐步说明，在订阅端完成以下配置：

   - 把页面生成的受限 `authorized_keys` 条目加入 SSH 用户的 `~/.ssh/authorized_keys`。`permitopen` 只允许向指定的订阅端回环端口做正向转发，同时保留部署命令执行能力。
   - 按页面给出的 `sshd_config` 片段限制转发目标、TTY、X11 和 Agent Forwarding。应用配置前先执行 `sshd -t`，然后 reload sshd。
   - 非 root SSH 用户按页面命令配置受限的 `sudo -n`。由于部署过程会上传并以 root 执行新二进制，这个 SSH 公钥具有部署级 root 权限，必须使用专用账号并严格保护私钥。
   - 执行 `sudo proxystack-web sub source set-publish-token 来源名`，按提示粘贴页面显示的发布 Secret。订阅端只保存 Secret 的 SHA-256 哈希；每个目标使用独立 Secret。
   - 通过独立可信渠道核对 SSH Host Key 指纹。例如可以在订阅端本机运行 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`，不要只信任管理端首次网络扫描得到的结果。

5. 回到目标列表先点击「检测部署」，检查 Linux、CPU 架构、`sudo -n`、安装状态和当前版本；随后可安装、更新或回滚。管理端下载对应架构的 Release 裸二进制并校验 SHA256，通过 SSH 上传后调用远端同一个 `sub deploy` 实现。
6. 点击「测试」检查 SSH TCP、Host Key、SSH 公钥认证、到 `127.0.0.1:<订阅端端口>` 的正向转发、`/health` 健康状态和发布 Secret。全部通过后，在「发布订阅包」页面选择「SSH 在线发布」。

在线发布只要求管理端能够连接订阅端的 SSH 端口。订阅端不需要额外监听隧道管理端口，也不会主动连接管理端；订阅导入 HTTP 接口仍可只监听回环地址。管理端严格校验已配置的 SSH Host Key，并禁止 HTTP 重定向。

普通目标列表只显示 Secret 掩码。需要重新配置订阅端时，点击操作列的「完整配置」可以再次查看当前 Secret、`authorized_keys`、`sshd_config` 和导入命令；每次查看都会记入审计日志，响应禁止缓存，窗口关闭后页面清除本次显示的明文。怀疑 Secret 泄露时再执行轮换并到订阅端重新运行 `set-publish-token`。轮换后管理端立即改用新 Secret，在订阅端更新前在线发布会失败；订阅端完成更新后，旧 Secret 才会失效。

目标来源名必须与管理端「系统设置 → 订阅」中的当前来源名一致。修改管理端来源名后，需要同步更新订阅端信任来源和目标配置，并轮换、重新配置该目标的发布 Secret。

无法建立 SSH 连接或需要保持网络隔离时，在「发布订阅包」页面选择「离线签发下载」，把文件拷贝到订阅端并执行以下命令，或者直接放入订阅端的 `inbox` 目录：

```bash
sudo proxystack-web sub --dir /opt/proxystack-web-sub import FILE
```

订阅端拒收签名不符、来源与 Secret 绑定不符或序号更旧的订阅包。相同序号且原始文件完全一致时返回已导入，相同序号但内容不同时拒绝。HTTP、CLI 和 `inbox` 导入共用跨进程文件锁与原子替换，避免并发导入使旧包覆盖新包。订阅内容在上次签发之后发生变化时，「发布订阅包」页面会给出提示。暂停用户在代理端立即生效，订阅中对应节点的移除要等下次发布并导入后才生效。

多台管理端可以向同一个订阅端发布，节点按用户名合并。在订阅端执行 `proxystack-web sub source add NAME PUBLIC_KEY` 信任新的来源。

用户列表和用户详情提供的订阅链接包含用户名，例如 `/s/eagle/<token>/clash`，Premium Clash 和 Surge 的末段分别为 `premium`、`surge`。用户名用于识别，仍由随机 token 鉴权；用户改名后，已分发的链接仍可使用，重置 token 并发布导入后旧 token 失效。订阅端兼容原有 `/s/<token>/<格式>` 链接；使用带用户名的新路径时，管理端与订阅端都需要升级到支持该格式的版本。

## 5. 流量、通知与运维

系统设置中的各个分组首次默认折叠，浏览器会记住每个分组及 policy 统计开关的展开状态。折叠不会丢弃未保存的输入；保存校验失败时自动展开含错误字段的分组。该偏好只保存于当前浏览器，不写入服务配置。

- **流量**：「总览」对比今日与本月的各 Stack 入站、用户可归属及 relay / 未归属口径差额，并显示实时速率和日月用量排行；「流量明细」按小时、日、月、年查看用户、入站或出站的流量，可以导出 CSV。采集间隔和数据保留期在「系统设置 → 流量」中设置。
- **详情用量**：用户详情的「用量」每 10 秒自动刷新，也可手动刷新，不会覆盖正在编辑的用户信息。Stack 详情的「用量统计」显示本 Stack 的今日、本月和近 30 天用量及日趋势，每 10 秒刷新；可以指定用户或清空选择查看全部用户。全部流量使用入站计数，指定用户时合并其全部凭据档案，共享认证入站的流量可能无法归属到用户。统计按系统时区计算，历史日明细受保留策略影响；离开统计页面后停止轮询，刷新失败时保留上次成功获取的结果并提示。
- **通知**：额度超限、每日阈值触发和到期暂停等事件通过「系统设置 → 通知」中配置的 SMTP 服务器发送邮件。发送失败的记录在「通知记录」页面可以手动重发。
- **定时任务**：「定时任务」页面列出数据库备份、流量数据清理、geo 数据更新、依赖版本检查、依赖自动升级、远端节点源同步和对账等内置任务，可以修改计划、超时或立即执行。
- **健康检查**：「健康检查」页面汇总依赖、服务运行状态、配置漂移、端口占用、流量采集、数据库、备份和磁盘空间等检查项，每项给出通过、警告或失败的结论和处理建议。
- **审计日志**：管理员的登录、登录失败、改密、两步验证变更、配置修改、查看明文凭据、下载备份等操作都记入「审计日志」。

## 6. 账号安全

### 6.1 两步验证

在右上角账号菜单的「账号安全」页面开启两步验证：

1. 点击「开启两步验证」，页面显示二维码和对应的密钥。
2. 用验证器应用（例如 Google Authenticator、Microsoft Authenticator 或 1Password）扫描二维码；无法扫码时手工输入密钥，类型选择基于时间、6 位、30 秒。
3. 输入应用显示的验证码并确认。开启成功后，该管理员在其他设备上的登录全部失效。

开启后，登录时需要在密码之后再输入验证码。验证码允许前后各 30 秒的时钟偏差，每个验证码只能使用一次。关闭两步验证需要同时提供当前密码和验证码。

验证器设备丢失时，在服务器上执行以下命令，在重置密码的同时关闭两步验证：

```bash
sudo proxystack-web admin reset-password --disable-totp
```

### 6.2 登录保护

| 规则 | 说明 |
| --- | --- |
| 账号锁定 | 同一账号连续 5 次登录失败（密码错误或验证码错误）后锁定 15 分钟，锁定期间即使密码正确也无法登录 |
| 来源地址限速 | 同一来源地址 15 分钟内认证失败 10 次后，15 分钟内拒绝该地址的登录与重置请求，响应状态码为 429，并在 Retry-After 响应头中给出等待秒数 |
| 会话有效期 | 闲置 12 小时后会话失效；持续使用的会话在创建 7 天后同样失效，需要重新登录 |
| 会话注销 | 修改密码或开启两步验证后，该管理员的其他会话全部失效；重置密码后全部会话失效 |

来源地址的失败计数保存在内存中，管理服务重启后清零；账号锁定状态保存在数据库中。不存在的用户名、错误的重置链接，以及已登录状态下改密或关闭两步验证时输错密码，均计入来源地址的失败次数。

### 6.3 凭据保护

- 管理员密码以 argon2id 哈希保存。会话 cookie 设置 HttpOnly 与 SameSite=Strict，所有修改类请求都要求携带 CSRF token。
- 面板默认以遮蔽值显示代理凭据、订阅 token 和 SMTP 密码等敏感字段，点击「显示」时单独请求明文，并记入审计日志。
- 日志中不记录密码、token、uuid 和完整订阅内容。
- 管理服务以普通用户身份运行，只能启停 `psw-xray@*` 与 `psw-mihomo@*` 两类代理服务，无法直接获得 root 权限。

## 7. 升级

### 7.1 升级管理端

在「系统」页面检查新版本并点击更新。系统下载新版本并校验 SHA-256，原子替换当前二进制后重启管理服务。xray 与 mihomo 由独立的 systemd unit 承载，管理服务重启期间代理流量不中断。

### 7.2 下载代理

管理端的全部对外下载，包括依赖安装与升级、管理端自更新以及版本检查，统一使用「系统设置 → 下载代理」中的设置，不读取系统环境变量。可选模式如下：

| 模式 | 说明 |
| --- | --- |
| 直连 | 默认模式，直接访问 GitHub |
| 经指定 stack 的 mihomo 监听 | 通过本机某个 stack 的 mihomo SOCKS 或 HTTP 监听出站，要求该 stack 正在运行 |
| 自定义代理 | 填写 HTTP 或 SOCKS5 代理的地址、端口和可选的用户名密码 |

保存前可以点击测试，用表单当前的值请求一次 GitHub。执行升级时，作业日志的第一行记录本次使用的代理，其中的密码已遮蔽。

## 8. 备份与迁移

### 8.1 备份

- 定时任务「数据库备份」定期把数据库快照写入 `/opt/proxystack-web/backups`，超过保留份数的旧备份自动删除。
- 在「备份」页面可以导出整机迁移包。迁移包包含数据库中的全部数据：配置、凭据、订阅 token、流量记录、审计日志和管理员账号，不含 xray、mihomo 与 geo 文件。迁移包默认以口令加密。
- 在命令行导出迁移包：`sudo proxystack-web backup --output FILE`。

备份文件和迁移包包含全部凭据，下载操作记入审计日志，请妥善保管。

### 8.2 迁移到新机器

推荐在新机器上安装时直接恢复：

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/install.sh \
  | sudo bash -s -- --restore /root/backup.pswbak
```

脚本从终端读取迁移包口令（不回显），恢复后使用源实例的管理员账号登录。随后在「依赖」页面按源实例的版本安装 xray、mihomo 与 geo，安装完成后系统自动重新生成配置并启动 stack。

也可以在已安装的实例上通过「备份」页面上传迁移包恢复。恢复会整体替换当前数据库，执行前自动备份当前数据库，然后重启管理服务。

## 9. 救援命令

以下命令需要以 root 或服务账号身份在服务器上执行。它们直接写入数据库，不需要重启服务。

| 命令 | 作用 |
| --- | --- |
| `proxystack-web admin reset-password` | 生成新的随机密码并打印；`--password-stdin` 从标准输入读取指定密码。下次登录必须修改密码 |
| `proxystack-web admin reset-link` | 打印一次性密码重置链接，15 分钟内有效，只能使用一次 |
| `proxystack-web admin set-listen --listen ADDR --port PORT` | 修改面板监听地址或端口，重启管理服务后生效 |
| `proxystack-web info` | 显示面板地址和订阅包签名公钥 |

`reset-password` 与 `reset-link` 都可以附加 `--disable-totp`，同时关闭两步验证。重置成功后，该管理员的全部会话立即失效，登录失败计数和锁定状态清零，操作记入审计日志。

## 10. 卸载

```bash
curl -fsSL https://raw.githubusercontent.com/sunliang711/proxystack-web/refs/heads/main/deploy/uninstall.sh \
  | sudo bash -s -- --role all
```

`--role` 可选 `panel`、`sub` 或 `all`，默认卸载已安装的全部角色：停止并删除服务、unit 文件、polkit 规则和命令链接，保留管理端的 `data` 与 `backups` 目录，以及订阅端的配置和已导入的订阅包。附加 `--purge` 时删除整个安装目录和服务账号。

## 11. 故障排查

| 现象 | 处理方法 |
| --- | --- |
| 忘记面板地址 | 在服务器上执行 `sudo proxystack-web info` |
| 忘记密码或验证器设备丢失 | 执行 `sudo proxystack-web admin reset-password`，设备丢失时附加 `--disable-totp` |
| 登录提示账号已锁定 | 等待 15 分钟，或执行 `reset-password` 立即解除 |
| 登录提示失败次数过多 | 来源地址被暂时限速，按提示的时间等待后重试 |
| 修改监听地址后无法访问面板 | 在服务器上执行 `sudo proxystack-web admin set-listen` 恢复，然后重启管理服务 |
| 依赖下载失败 | 在「系统设置 → 下载代理」中配置并测试代理 |
| stack 未运行或配置异常 | 查看「健康检查」页面和 stack 编辑页的「诊断」「日志」标签页 |
| 订阅端返回 404 或旧内容 | 执行 `proxystack-web sub --dir /opt/proxystack-web-sub status` 查看已导入的订阅包，重新发布并导入 |
