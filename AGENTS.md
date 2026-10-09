# 土豆饼的家（VpsCT fork）

这是 [VpsCT](https://github.com/YongshengWin/VpsCT) 的自用分支：一个**机场 + 探针**面板。本地工作目录、二进制、数据目录、systemd 沿用上游的 **ctlvps** 命名：控制端 `ctlvpsd`，VPS 上 `ctlvps-agent`。回复用**简体中文**。

一句话：一台控制端管几台 VPS，既看它们的状态和流量，也在上面建入站和线路，分给不同的用户用。

## 1. 产品模型

界面只有六页：总览、服务器、节点、用户、规则、连接日志。新功能要落在这个模型里，不要把已经移除的页面（订阅编辑器、模板与预设、分享、面板多账号、出口、固定转发、托管中转、网卡计费）加回来。

| 概念 | 含义 | 代码里的名字 |
|---|---|---|
| 入站 | 某台服务器上监听的一个端口；协议限于能一个端口多个用户的六种 | `domain.Node`，`source=deployed`，没有 `attach_node_id`；`domain.ProtocolShareable` |
| 线路 | 用户在客户端里看到的一个节点：直连一个入站，或入口入站中转到落地入站 | `domain.Line` |
| 用户 | 一个使用者：线路范围、规则、限额、专属链接 | `domain.Share` 加它关联的 `Subscription` |
| 用户凭据 | 用户在某条线路的某台机器上的凭据，各自计量 | `domain.Node`，有 `attach_node_id`、`line_id`；落地一侧 `landing=true` |
| 规则 | 整个面板只有一份清单：一行一条“匹配什么 → PROXY / DIRECT / REJECT”，每个用户、Clash 和小火箭的配置都由它生成 | `domain.Rules`（表 `rulesets` 里唯一的一行，`store.Rules` / `SaveRules`）；清单的写法和内置的最简规则在 `internal/ruleset`，生成配置在 `subscription.Profile` |

几条必须保持的性质：

- **一个入站多个用户**：凭据是入站 `users` 里的一项，按 `auth_user` 路由到带各自 `routing_mark` 的出站，nftables 按 mark 计量和封禁。凭据不占端口。
- **落地只能是入口机不靠证书就能确认身份的协议**（VLESS Reality、Shadowsocks 2022，`domain.ProtocolLanding`）；入口可以是六种里任意一种。Snell、mieru、WireGuard 一个端口只有一个身份，不提供。
- **中转的 UDP**：落地是 Shadowsocks 时默认 UDP 走落地端口的 UDP；落地服务器打开 `udp_over_tcp`（主机的 UDP 端口映射丢包时用）才并进 TCP 连接。VLESS 落地的 UDP 一直在 TCP 连接里。
- **中转在入口机上完成**：中转线路的入口凭据带一个 `Relay`，入口机用该用户在落地机上的凭据转发过去。客户端拿到的每条线路都是入口机上的一条普通节点，落地机的地址和凭据不进任何用户配置。
- **两台机器都计量、都算给用户**。不要再引入“只算一次”的特例。
- **停用只影响本人**：用户不在正常状态时，他的凭据从入站用户列表里去掉、对应 mark 丢弃。
- **没有入站的服务器只被观察**：下发状态里不带主机调优（`desired.watchOnly`），agent 不动它的拥塞控制和时间同步；安装脚本只补装缺少的依赖。探针接入别的用途的机器时不能改动它。
- **实时数据不进心跳**：每秒的读数和探测结果走 agent 的常驻子进程（`internal/agentlive`，以 `ctlvps-net` 运行）到控制端 `internal/live.Hub` 的一条 WebSocket，再经事件流到网页；数据库只按分钟写。心跳仍是流量计量和配置下发的唯一通道，实时通道断了不影响它。子进程没有回到 root 进程的通道（只有日志），不要给它加。
- **探测目标是面板里的内容**（`probe_targets`），所有服务器都探；只有入口机的结果会触发 Telegram 提醒。
- **落地要能被入口机确认身份**（`domain.LandingProblem`）：VLESS Reality（公钥）、Shadowsocks 2022（密钥）、Hysteria2（面板签发的证书，`provision.IssueCertificate`，存在入站的 `tls_cert` / `tls_key` 里，DER 的 base64；入口机拿到的是 `RelaySpec.Cert`）。不要为了让某个协议能做落地去加 `insecure`。Hysteria2 落地是给“新建连接会丢包、长连接稳定”的落地机用的：每个用户到落地机只有一条 QUIC 连接。
- **不回退直连**：分组里没有线路时填 `REJECT`（`subscription.EmptyGroupPolicy`）。
- **整个面板只有一份规则**（用户 2026-10-09 定的：先是“不按客户端各写一份”，v0.4.0；再是“只维护一个规则，也不用无规则模式了”，v0.4.2）：没有多套规则、没有“无规则”这个选项、没有“默认规则”，用户也不各自选规则；还没写过规则时生效的是内置的最简规则（`ruleset.Default`），规则页显示的就是它。不要把这些加回来，也不要再给某个客户端加一份手写的配置或模板。客户端之间的差别写在代码里：`subscription.skeletonMihomo` / `skeletonShadowrocket` 是各自的底板（监听、DNS、嗅探），`mihomoProfile` / `shadowrocketProfile` 把同一份清单翻成各自的写法。Clash 里 `PROXY` 是名为 `group_name` 的 select 分组；小火箭里就是 `PROXY` 本身，即首页点中的那条线路，不建分组（分组在另一个页面选，首页点线路对它不起作用，2026-10-09 出过这个故障）。用户没有线路时两边的 `PROXY` 都变成 `REJECT`。
- **分流名单**（`ruleset.Lists`）是 `RULE-SET` 能引用的全部名字，文件由面板域名下的 `/rules/clash/<名字>.yaml`、`/rules/surge/<名字>.list` 提供（部署方每天从 Loyalsoldier/clash-rules 镜像）。加名单要两边一起加。
- 用户的专属链接是 `/r/<24 位>`：浏览器打开返回前端的个人页（`web/src/pages/public.tsx`，数据来自同一地址加 `?page=1`），客户端打开返回配置。小火箭和 Surge 用地址末段给配置起名，一键导入给它们的是带名字的形式 `/r/<码>/<格式>/<站点名>`（名字只是给客户端看的，服务端不读）；各客户端里配置的名字一律是站点名，不是用户名。

## 2. 代码在哪

| 路径 | 干什么 |
|---|---|
| `cmd/ctlvpsd` | 控制端：API + 内嵌前端 |
| `cmd/ctlvps-agent` | VPS 端 |
| `web/` | React 面板，build 进 `web/dist`，再打进 `ctlvpsd` |
| `internal/api` | REST / SSE / 公开链接 `/s` `/r`；`lines.go`、`rules.go`、`people.go` 是本分支加的 |
| `internal/share` | 用户（上游叫分享）；`lines.go` 负责按线路发放和回收凭据 |
| `internal/desired` | 把库里的内容算成下发给 agent 的状态，含中转目标和落地来源限制 |
| `internal/core/singbox.go` | 生成 sing-box 服务端配置 |
| `internal/subscription` | 各客户端格式的渲染；`lines.go` 按线路出节点，`profile.go` 从规则清单生成 Clash / 小火箭配置，`skeleton.go` 是两种客户端的底板 |
| `internal/ruleset` | 规则清单：解析、可用名单、内置默认、旧版 Clash 配置的转换 |
| `internal/store/schema.go` | 数据库迁移；本分支从 v31 开始 |
| `internal/liveproto`、`internal/agentlive`、`internal/live` | 实时通道：消息格式、agent 端的子进程（采样和 TCP 探测）、控制端的 Hub（内存里的现状、按分钟落库）；`internal/hostmetrics` 是读 `/proc` 的公共代码 |
| `internal/report` | Telegram 推送：每件事从发生到解决各说一次（`incidents` 表记着说过什么），以及日报。只推送，不接收消息 |

上游的模板、分组预设、从节点库生成的订阅、转存的外部订阅、链式节点、Surge 和 sing-box 客户端配置在 v0.4.1 删掉了（`rule_templates`、`proxy_group_presets` 两张表留着但清空，因为 `shares` 里有一列引用前者，SQLite 删不掉）。`subscription.SingBoxOutbound` 只给集成测试用：让官方 sing-box 客户端去连 agent 建的入站。

还在仓库里、没有界面入口的上游功能：面板多账号（`/api/v1/users`，很多权限测试靠它建普通账号）、出口 / 固定转发 / 托管中转 / 网卡计费 / 节点自定义监听（`internal/networkconfig`、`networkguard`、`netinventory`、`store/transit.go` 等，agent 协议里也有）、Snell / mieru / WireGuard 入站、分享的“专属入口”（`ShareTarget`）。它们和 agent、CI 的集成测试（`scripts/network-systemd-test`）连在一起，要删就单独发一版，连同 agent 协议、数据库表和集成测试一起删干净，不要只删接口。改动时不必为它们加新功能。

提交前除了 `go test ./...`，还要跑 `GOOS=linux go vet ./...`：`scripts/` 下的集成测试只在 Linux 上编译，本机默认检查不到（v0.4.0 第一次构建就是这样失败的）。

前端改完要 `cd web && npm run typecheck && npm run build`，再编 `ctlvpsd` 才进二进制。验证：`bash scripts/check.sh`；CI（`.github/workflows/ci.yml`）还会跑容器里的安装、卸载、升级、真实 agent 和真实浏览器检查，以 CI 为准。

## 3. 发版和升级

- 仓库主人明确授权在这个分支上直接改、commit、push、发版。发版：推 `vX.Y.Z` tag → Release 工作流先跑完整 CI，再生成草稿 → `gh release edit vX.Y.Z --draft=false --latest` 发布。先在 `CHANGELOG.md` 顶部加上该版本的小节（发布说明从这里生成）。
- 发行来源写的是本仓库（`internal/secureupdate/checksum.go`、`internal/assets/install-agent.sh`、`internal/api/servers.go`）。改仓库名要一起改。
- 控制端升级：`install.sh --repo yanglingjieee/VpsCT --update --auto-rollback`；agent 随后自动同步，也可以在节点上执行控制端提供的 `/install-agent.sh --update`。
- 数据库迁移只追加，不改已发布的迁移。控制端和 agent 的协议变化要考虑“控制端先升、agent 后升”的那段时间。

## 4. 别做的事

- 不要把节点密码、订阅 token、专属链接、SSH 主机 IP 写进仓库或回复。
- 不要把个人域名、个人 IP 写进产品内置的配置（`skeleton.go`、`ruleset.Default` 等）；用户自己的规则在面板的数据里。
- sing-box 只用官方发行版；不修改或自行编译内核。
- 流量口径：入站 = 网卡收，出站 = 网卡发。用户的限额按汇总；服务器的配额按 `quota_billing`：`dual` 汇总，`out` 只算出站。用户凭据在远端一侧计量，入账时已换成用户视角的上传/下载。
- 重置日：1–28 固定那天；29/30/31 都是“每月最后一天”（存 31）。新的一期从那天 0 点开始，时区是设置 `quota.timezone`（`store.Location()`）；`traffic.PeriodStart` 按传入时间自带的时区计算，调用前先 `.In(...)`。
- 服务器配额用完后停不停是每台服务器自己的选项（`quota_stop`）；停的状态是 `quota_stopped`，由 `checkServerQuota` 维护，下发时等同于停用（`desired.Build`），用量回到配额内自动恢复。不要再加面板级的开关。
- 服务器的本期用量是 `server_usage` 里随心跳累计的计数加一个校正值（`traffic.ServerUsage`、`CalibrateServer`），不是从 `traffic_daily` 求和；后者按 UTC 日分桶，只用于图表和首次建立计数。
- 第三方许可文本由 `scripts/third-party.py` 生成；依赖变更要同步更新。
- 在线 GeoIP 默认关闭，启用会向第三方发送公网客户端 IP，不能静默开启。
- 不要改 git config，不要 `--no-verify`。
- 前端改了可见行为，能开浏览器就点一遍；不能开就说清楚验证了什么。
