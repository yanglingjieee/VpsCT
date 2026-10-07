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
| 规则 | 一套规则给每种客户端各写一份 | `domain.Ruleset`；没有规则时用 `subscription.NoRules` |

几条必须保持的性质：

- **一个入站多个用户**：凭据是入站 `users` 里的一项，按 `auth_user` 路由到带各自 `routing_mark` 的出站，nftables 按 mark 计量和封禁。凭据不占端口。
- **落地只能是入口机不靠证书就能确认身份的协议**（VLESS Reality、Shadowsocks 2022，`domain.ProtocolLanding`）；入口可以是六种里任意一种。Snell、mieru、WireGuard 一个端口只有一个身份，不提供。
- **中转的 UDP**：落地是 Shadowsocks 时默认 UDP 走落地端口的 UDP；落地服务器打开 `udp_over_tcp`（主机的 UDP 端口映射丢包时用）才并进 TCP 连接。VLESS 落地的 UDP 一直在 TCP 连接里。
- **中转在入口机上完成**：中转线路的入口凭据带一个 `Relay`，入口机用该用户在落地机上的凭据转发过去。客户端拿到的每条线路都是入口机上的一条普通节点，落地机的地址和凭据不进任何用户配置。
- **两台机器都计量、都算给用户**。不要再引入“只算一次”的特例。
- **停用只影响本人**：用户不在正常状态时，他的凭据从入站用户列表里去掉、对应 mark 丢弃。
- **没有入站的服务器只被观察**：下发状态里不带主机调优（`desired.watchOnly`），agent 不动它的拥塞控制和时间同步；安装脚本只补装缺少的依赖。探针接入别的用途的机器时不能改动它。
- **不回退直连**：分组里没有线路时填 `REJECT`（`subscription.EmptyGroupPolicy`）。
- 用户的专属链接是 `/r/<24 位>`：浏览器打开返回前端的个人页（`web/src/pages/public.tsx`，数据来自同一地址加 `?page=1`），客户端打开返回配置。

## 2. 代码在哪

| 路径 | 干什么 |
|---|---|
| `cmd/ctlvpsd` | 控制端：API + 内嵌前端 |
| `cmd/ctlvps-agent` | VPS 端 |
| `web/` | React 面板，build 进 `web/dist`，再打进 `ctlvpsd` |
| `internal/api` | REST / SSE / 公开链接 `/s` `/r`；`lines.go`、`rulesets.go`、`people.go` 是本分支加的 |
| `internal/share` | 用户（上游叫分享）；`lines.go` 负责按线路发放和回收凭据 |
| `internal/desired` | 把库里的内容算成下发给 agent 的状态，含中转目标和落地来源限制 |
| `internal/core/singbox.go` | 生成 sing-box 服务端配置 |
| `internal/subscription` | 各客户端格式的渲染；`lines.go` 按线路出节点，`norules.go` 是内置无规则 |
| `internal/store/schema.go` | 数据库迁移；本分支从 v31 开始 |

上游的出口、转发、托管中转、模板、预设、多账号等后端代码仍在仓库里但没有界面入口，也没有被本分支的功能依赖。改动时不必为它们加新功能；要删就连同测试一起删干净。

前端改完要 `cd web && npm run typecheck && npm run build`，再编 `ctlvpsd` 才进二进制。验证：`bash scripts/check.sh`；CI（`.github/workflows/ci.yml`）还会跑容器里的安装、卸载、升级、真实 agent 和真实浏览器检查，以 CI 为准。

## 3. 发版和升级

- 仓库主人明确授权在这个分支上直接改、commit、push、发版。发版：推 `vX.Y.Z` tag → Release 工作流先跑完整 CI，再生成草稿 → `gh release edit vX.Y.Z --draft=false --latest` 发布。先在 `CHANGELOG.md` 顶部加上该版本的小节（发布说明从这里生成）。
- 发行来源写的是本仓库（`internal/secureupdate/checksum.go`、`internal/assets/install-agent.sh`、`internal/api/servers.go`）。改仓库名要一起改。
- 控制端升级：`install.sh --repo yanglingjieee/VpsCT --update --auto-rollback`；agent 随后自动同步，也可以在节点上执行控制端提供的 `/install-agent.sh --update`。
- 数据库迁移只追加，不改已发布的迁移。控制端和 agent 的协议变化要考虑“控制端先升、agent 后升”的那段时间。

## 4. 别做的事

- 不要把节点密码、订阅 token、专属链接、SSH 主机 IP 写进仓库或回复。
- 不要把个人域名、个人 IP 写进产品内置的配置（`norules.go` 等）；用户自己的规则在面板的数据里。
- sing-box 只用官方发行版；不修改或自行编译内核。
- 流量口径：入站 = 网卡收，出站 = 网卡发。用户的限额按汇总；服务器的配额按 `quota_billing`：`dual` 汇总，`out` 只算出站。用户凭据在远端一侧计量，入账时已换成用户视角的上传/下载。
- 重置日：1–28 固定那天；29/30/31 都是“每月最后一天”（存 31）。新的一期从那天 0 点开始，时区是设置 `quota.timezone`（`store.Location()`）；`traffic.PeriodStart` 按传入时间自带的时区计算，调用前先 `.In(...)`。
- 服务器的本期用量是 `server_usage` 里随心跳累计的计数加一个校正值（`traffic.ServerUsage`、`CalibrateServer`），不是从 `traffic_daily` 求和；后者按 UTC 日分桶，只用于图表和首次建立计数。
- 第三方许可文本由 `scripts/third-party.py` 生成；依赖变更要同步更新。
- 在线 GeoIP 默认关闭，启用会向第三方发送公网客户端 IP，不能静默开启。
- 不要改 git config，不要 `--no-verify`。
- 前端改了可见行为，能开浏览器就点一遍；不能开就说清楚验证了什么。
