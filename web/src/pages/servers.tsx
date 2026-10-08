import { ConfigStatusNotice } from "@/components/config-status";
import * as React from "react";
import { MaintenancePanel } from "@/components/maintenance";
import { ServerActions } from "@/components/server-actions";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ArrowDown, ArrowUp, ArrowUpDown, Copy, Gauge, KeyRound, Plus, RefreshCw, Trash2, Pencil, ShieldCheck, Wrench } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import type { Server, Node, Series } from "@/lib/types";
import { fmtBytes, fmtAgo, fmtDuration, fmtRate, gbToBytes, bytesToGb, parseResetDay, copyText, STATUS_LABELS, PROTOCOL_LABELS, BILLING_LABELS, fmtDate, fmtPeriodDay, fmtResetIn } from "@/lib/utils";
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, Confirm, Dialog, Empty, Field, Input, PageHeader, Progress, Select, Spinner, Switch, Table, Td, Th, Tr, Textarea, Pre, Tabs } from "@/components/ui";
import { useToast } from "@/components/toast";
import { ResetDayInput } from "@/components/datetime-picker";
import { TrafficBars, RateArea } from "@/components/charts";
import { TrafficIO } from "@/components/traffic-ways";
import { ServerCard } from "@/components/server-card";

// ---------- shared form ----------
interface ServerForm {
  name: string;
  region: string;
  public_host: string;
  tags: string;
  notes: string;
  quota_gb: string;
  quota_reset_day: string;
  quota_billing: Server["quota_billing"];
  quota_stop: boolean;
  core_mode: string;
  ip_pref: "ipv4" | "ipv6" | "ipv4_only";
  ingress_ack: boolean;
  strict_source: boolean;
  udp_over_tcp: boolean;
  cert_mode: string;
  enabled: boolean;
}

const emptyForm: ServerForm = { name: "", region: "", public_host: "", tags: "", notes: "", quota_gb: "", quota_reset_day: "1", quota_billing: "dual", quota_stop: false, core_mode: "lean", ip_pref: "ipv4", ingress_ack: false, strict_source: false, udp_over_tcp: false, cert_mode: "self_signed", enabled: true };

function toForm(s: Server): ServerForm {
  return { name: s.name, region: s.region, public_host: s.public_host, tags: s.tags.join(","), notes: s.notes, quota_gb: bytesToGb(s.quota_bytes), quota_reset_day: String(s.quota_reset_day ?? 0), quota_billing: s.quota_billing, quota_stop: s.quota_stop, core_mode: s.core_mode, ip_pref: s.ipv4_only ? "ipv4_only" : s.prefer_ipv6 ? "ipv6" : "ipv4", ingress_ack: s.ingress_ack, strict_source: s.strict_source, udp_over_tcp: s.udp_over_tcp, cert_mode: s.cert_mode, enabled: s.enabled };
}

function toPayload(f: ServerForm) {
  return { ...f, ipv4_only: f.ip_pref === "ipv4_only", prefer_ipv6: f.ip_pref === "ipv6", tags: f.tags.split(",").map((t) => t.trim()).filter(Boolean), quota_bytes: gbToBytes(f.quota_gb), quota_reset_day: parseResetDay(f.quota_reset_day) };
}

export function ServerDialog({ open, onClose, server }: { open: boolean; onClose: () => void; server?: Server }) {
  const qc = useQueryClient();
  const toast = useToast();
  const [f, setF] = React.useState<ServerForm>(emptyForm);
  React.useEffect(() => setF(server ? toForm(server) : emptyForm), [server, open]);
  const set = <K extends keyof ServerForm>(k: K, v: ServerForm[K]) => setF((p) => ({ ...p, [k]: v }));
  const m = useMutation({
    mutationFn: () => (server ? put<Server>(`/api/v1/servers/${server.id}`, toPayload(f)) : post<Server>("/api/v1/servers", toPayload(f))),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["servers"] });
      toast.success(server ? "已保存" : "已添加服务器");
      onClose();
    },
    onError: (e) => toast.fromError(e),
  });
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={server ? "编辑服务器" : "添加服务器"}
      description="添加后生成安装命令，在这台机器上执行一次即可接入。只做监控不建入站也可以。"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={() => m.mutate()} loading={m.isPending}>保存</Button>
        </>
      }
    >
      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="名称"><Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="hk-1" /></Field>
        <Field label="地区" hint="两位代码，如 HK/JP/US"><Input value={f.region} onChange={(e) => set("region", e.target.value.toUpperCase())} maxLength={4} /></Field>
        <Field label="公网地址" hint="留空则使用 agent 上报的 IP" className="sm:col-span-2"><Input value={f.public_host} onChange={(e) => set("public_host", e.target.value)} placeholder="1.2.3.4 或 hk.example.com" /></Field>
        <Field label="月流量配额 (GB)" hint="0 为不限"><Input type="number" min={0} step="0.1" value={f.quota_gb} onChange={(e) => set("quota_gb", e.target.value)} /></Field>
        <Field label="重置日" hint="1–28 固定那天；29/30/31 都是每月最后一天。不选则按近 30 天滚动。"><ResetDayInput value={f.quota_reset_day} onChange={(v) => set("quota_reset_day", v)} /></Field>
        <Field label="流量怎么算" hint="照服务商的算法选。改这一项或重置日，本期的校正会作废。">
          <Select value={f.quota_billing} onChange={(e) => set("quota_billing", e.target.value as ServerForm["quota_billing"])}>
            <option value="dual">双向：入站 + 出站</option>
            <option value="out">只算出站</option>
          </Select>
        </Field>
        <Field label="出口 IP 偏好" hint="目标同时有 IPv4 和 IPv6 时用哪个">
          <Select value={f.ip_pref} onChange={(e) => set("ip_pref", e.target.value as ServerForm["ip_pref"])}>
            <option value="ipv4">优先 IPv4</option>
            <option value="ipv6">优先 IPv6</option>
            <option value="ipv4_only">只用 IPv4（这台机器没有可用的 IPv6）</option>
          </Select>
        </Field>
        <Field label="标签" hint="逗号分隔"><Input value={f.tags} onChange={(e) => set("tags", e.target.value)} /></Field>
        <div className="flex flex-col gap-3 sm:col-span-2">
          <Switch checked={f.quota_stop} onChange={(v) => set("quota_stop", v)} label="配额用完就停掉这台机器上的入站，下次重置自动恢复（超量要另外付费的机器打开；不开则只提醒）" />
          <Switch checked={f.ingress_ack} onChange={(v) => set("ingress_ack", v)} label="这台机器上另有防火墙，节点端口由我自己放行（不再提示）" />
          <Switch checked={f.strict_source} onChange={(v) => set("strict_source", v)} label="作为落地机时，只接受入口机 IP 发来的中转（机器经过地址转换、看不到真实来源时不要开）" />
          <Switch checked={f.udp_over_tcp} onChange={(v) => set("udp_over_tcp", v)} label="作为 Shadowsocks 落地机时，中转来的 UDP 并进 TCP 连接（这台机器的 UDP 端口映射丢包时再开；不开则 UDP 走 UDP）" />
          <Switch checked={f.enabled} onChange={(v) => set("enabled", v)} label="启用（关闭后这台机器上的入站全部停止）" />
        </div>
        <Field label="备注" className="sm:col-span-2"><Textarea value={f.notes} onChange={(e) => set("notes", e.target.value)} rows={2} /></Field>
      </div>
    </Dialog>
  );
}

function AgentStatusBadge({ s }: { s: Server["agent_status"] }) {
  return <Badge variant={s === "online" ? "success" : s === "offline" ? "destructive" : "secondary"}>{STATUS_LABELS[s]}</Badge>;
}

// The order here is the order everywhere servers are listed: the overview, this page, the daily report.
function OrderDialog({ open, onClose, servers }: { open: boolean; onClose: () => void; servers: Server[] }) {
  const qc = useQueryClient();
  const toast = useToast();
  const reorder = useMutation({
    mutationFn: (ids: number[]) => post<Server[]>("/api/v1/servers/reorder", { ids }),
    onSuccess: (list) => { qc.setQueryData(["servers"], list); qc.invalidateQueries({ queryKey: ["dashboard"] }); },
    onError: (e) => toast.fromError(e),
  });
  const move = (i: number, d: -1 | 1) => {
    const ids = servers.map((s) => s.id);
    [ids[i], ids[i + d]] = [ids[i + d], ids[i]];
    reorder.mutate(ids);
  };
  return (
    <Dialog open={open} onClose={onClose} title="调整顺序" description="总览、服务器页和 Telegram 日报都按这个顺序排。新添加的服务器排在最后。">
      <ul className="divide-y divide-border/60">
        {servers.map((s, i) => (
          <li key={s.id} className="flex items-center justify-between gap-3 py-2">
            <div className="min-w-0">
              <p className="truncate text-sm font-medium">{s.name}</p>
              <p className="truncate text-xs text-muted-foreground">{[s.region, s.public_host || s.agent?.public_ipv4, s.node_count > 0 ? null : "只做监控"].filter(Boolean).join(" · ")}</p>
            </div>
            <div className="flex shrink-0 gap-0.5">
              <Button size="sm" variant="ghost" aria-label={`上移 ${s.name}`} disabled={i === 0 || reorder.isPending} onClick={() => move(i, -1)}><ArrowUp className="h-3.5 w-3.5" /></Button>
              <Button size="sm" variant="ghost" aria-label={`下移 ${s.name}`} disabled={i === servers.length - 1 || reorder.isPending} onClick={() => move(i, 1)}><ArrowDown className="h-3.5 w-3.5" /></Button>
            </div>
          </li>
        ))}
      </ul>
    </Dialog>
  );
}

// ---------- list ----------
export function ServersPage() {
  const q = useQuery({ queryKey: ["servers"], queryFn: () => get<Server[]>("/api/v1/servers"), refetchInterval: 15000 });
  const toast = useToast();
  const qc = useQueryClient();
  const [create, setCreate] = React.useState(false);
  const [ordering, setOrdering] = React.useState(false);
  const checkAgentUpdates = useMutation({
    mutationFn: () => post<{ message: string }>("/api/v1/agents/update"),
    onSuccess: (r) => { toast.success(r.message); qc.invalidateQueries({ queryKey: ["servers"] }); },
    onError: (e) => toast.fromError(e),
  });
  const stale = (q.data ?? []).filter((s) => s.agent_update?.outdated).length;
  return (
    <div>
      <PageHeader title="服务器" description="状态、负载和流量；在线情况变化会通过 Telegram 通知" actions={
        <>
          {stale > 0 && (
            <Button variant="outline" onClick={() => checkAgentUpdates.mutate()} loading={checkAgentUpdates.isPending} title="检查与控制端提供的 agent 版本是否一致；有差异时会随心跳自动更新">
              <RefreshCw className="h-4 w-4" /> 检查 agent 更新（{stale} 台待同步）
            </Button>
          )}
          {(q.data?.length ?? 0) > 1 && <Button variant="outline" onClick={() => setOrdering(true)}><ArrowUpDown className="h-4 w-4" /> 调整顺序</Button>}
          <Button onClick={() => setCreate(true)}><Plus className="h-4 w-4" /> 添加服务器</Button>
        </>
      } />
      {q.isLoading ? (
        <Spinner />
      ) : !q.data?.length ? (
        <Empty title="还没有服务器" description="添加一台机器，然后用生成的命令安装 agent。" action={<Button onClick={() => setCreate(true)}>添加服务器</Button>} />
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{q.data.map((s) => <ServerCard key={s.id} s={s} />)}</div>
      )}
      <ServerDialog open={create} onClose={() => setCreate(false)} />
      <OrderDialog open={ordering} onClose={() => setOrdering(false)} servers={q.data ?? []} />
    </div>
  );
}

// ---------- detail ----------
interface Revision {
  revision: number;
  hash: string;
  status: string;
  error: string;
  created_at: string;
  summary?: { nodes: unknown[] };
}

export function ServerDetailPage() {
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["servers", id], queryFn: () => get<Server>(`/api/v1/servers/${id}`), refetchInterval: 10000 });
  const [trafficDays, setTrafficDays] = React.useState(30);
  const [trafficSelection, setTrafficSelection] = React.useState({ serverID: id, nodeID: "server" });
  const nodes = useQuery({ queryKey: ["nodes", { server_id: id }], queryFn: () => get<Node[]>(`/api/v1/nodes?server_id=${id}&members=1`), refetchInterval: 60000 });
  const trafficNode = trafficSelection.serverID === id
    ? nodes.data?.find((n) => String(n.id) === trafficSelection.nodeID)
    : undefined;
  const trafficSubject = trafficNode ? String(trafficNode.id) : "server";
  const traffic = useQuery({
    queryKey: ["servers", id, "traffic", trafficSubject, trafficDays],
    queryFn: () => get<Series>(trafficNode
      ? `/api/v1/nodes/${trafficNode.id}/traffic?days=${trafficDays}`
      : `/api/v1/servers/${id}/traffic?days=${trafficDays}`),
    refetchInterval: 30000,
  });
  const samples = useQuery({ queryKey: ["servers", id, "samples"], queryFn: () => get<{ ts: string; rx_rate: number; tx_rate: number }[]>(`/api/v1/servers/${id}/samples?hours=24`), refetchInterval: 60000 });
  const desired = useQuery({ queryKey: ["servers", id, "desired"], queryFn: () => get<Revision[]>(`/api/v1/servers/${id}/desired?limit=5`) });
  const [edit, setEdit] = React.useState(false);
  const [calibrate, setCalibrate] = React.useState(false);
  const [enroll, setEnroll] = React.useState<{ token: string; install_command: string; expires_at: string } | null>(null);
  const [confirmDel, setConfirmDel] = React.useState(false);
  const [confirmReset, setConfirmReset] = React.useState(false);
  const [searchParams, setSearchParams] = useSearchParams();
  type ServerTab = "nodes" | "diag" | "revisions";
  const selectedTab = searchParams.get("tab");
  const tab: ServerTab = ["nodes", "diag", "revisions"].includes(selectedTab ?? "") ? selectedTab as ServerTab : "nodes";
  const setTab = (value: ServerTab) => setSearchParams((previous) => { const next = new URLSearchParams(previous); next.set("tab", value); return next; }, { replace: true });
  const [maintenanceOpen, setMaintenanceOpen] = React.useState(false);
  const [maintenanceBusy, setMaintenanceBusy] = React.useState(false);

  const enrollM = useMutation({ mutationFn: () => post<{ token: string; install_command: string; expires_at: string }>(`/api/v1/servers/${id}/enroll-token`), onSuccess: setEnroll, onError: (e) => toast.fromError(e) });
  const republish = useMutation({ mutationFn: () => post(`/api/v1/servers/${id}/republish`), onSuccess: () => { toast.success("已重试，等待服务器同步"); qc.setQueryData<Server>(["servers", id], (old) => old?.desired ? { ...old, desired: { ...old.desired, in_sync: false, status: "pending", error: "" }, agent: old.agent ? { ...old.agent, apply_error: "" } : old.agent } : old); qc.invalidateQueries({ queryKey: ["servers", id] }); }, onError: (e) => toast.fromError(e) });
  const checkAgentUpdate = useMutation({
    mutationFn: () => post<{ queued?: boolean; manual?: boolean; message: string; agent_update?: { command?: string } }>(`/api/v1/servers/${id}/update-agent`),
    onSuccess: async (r) => {
      qc.invalidateQueries({ queryKey: ["servers", id] });
      if (r.manual && r.agent_update?.command) {
        await copyText(r.agent_update.command);
        toast.success(r.message + "（命令已复制）");
        return;
      }
      toast.success(r.message);
    },
    onError: (e) => toast.fromError(e),
  });
  const onServerDeleted = React.useCallback(() => { toast.success("服务器记录已删除"); void qc.invalidateQueries({ queryKey: ["servers"] }); void qc.invalidateQueries({ queryKey: ["nodes"] }); void qc.invalidateQueries({ queryKey: ["subscriptions"] }); nav("/servers"); }, [qc, nav]);
  const resetTok = useMutation({ mutationFn: () => post(`/api/v1/servers/${id}/reset-token`), onSuccess: () => { toast.success("已吊销 agent 令牌，需重新注册"); setConfirmReset(false); qc.invalidateQueries({ queryKey: ["servers", id] }); } });

  if (q.isLoading) return <Spinner />;
  const s = q.data;
  if (!s) return <Empty title="服务器不存在" />;
  const m = s.metrics;
  const d = s.diagnostics;

  return (
    <div>
      <PageHeader
        back={{ to: "/servers", label: "服务器" }}
        title={s.name}
        description={`${s.public_host || s.agent?.public_ipv4 || "—"} ${s.region ? `· ${s.region}` : ""} ${m?.hostname ? `· ${m.hostname}` : ""}`}
        actions={
          <>
            <AgentStatusBadge s={s.agent_status} />
            {s.agent_status === "pending" && <Button size="sm" onClick={() => enrollM.mutate()} loading={enrollM.isPending}><KeyRound className="h-4 w-4" /> 生成安装命令</Button>}
            <Button size="sm" variant="outline" onClick={() => setEdit(true)}><Pencil className="h-4 w-4" /> 编辑</Button>
            <ServerActions items={[
              { label: "agent 维护", icon: <Wrench className="h-4 w-4" />, onClick: () => setMaintenanceOpen(true) },
              ...(s.agent && !s.diagnostics?.maintenance ? [{ label: s.agent_update?.supported ? "检查 agent 更新" : "复制 agent 更新命令", icon: <Copy className="h-4 w-4" />, onClick: () => checkAgentUpdate.mutate(), disabled: checkAgentUpdate.isPending || maintenanceBusy }] : []),
              { label: "删除服务器记录", icon: <Trash2 className="h-4 w-4" />, onClick: () => setConfirmDel(true), disabled: maintenanceBusy, destructive: true },
            ]} />
          </>
        }
      />

      <MaintenancePanel key={s.id} server={{ id: s.id, name: s.name }} open={maintenanceOpen} onOpen={() => setMaintenanceOpen(true)} onClose={() => setMaintenanceOpen(false)} onBusyChange={setMaintenanceBusy} deleteOpen={confirmDel} onDeleteClose={() => setConfirmDel(false)} onDeleted={onServerDeleted} />

      {s.agent && <div className="mb-4 rounded-md border p-3 text-sm">安全状态：{s.agent_status === "pending" ? "尚未接入 agent" : !s.diagnostics?.security_version ? "旧版 agent，尚未启用新版安全策略" : !s.diagnostics.security_policy ? "本机安全策略加载失败，程序与配置变更已关闭" : s.diagnostics.security_paused ? "本机已暂停配置变更" : "更新文件校验与本机安全策略已启用"}</div>}
      {s.quota_stopped && <div className="mb-4 rounded-md border border-rose-500/40 bg-rose-500/5 p-3 text-sm text-rose-700 dark:text-rose-300">本期配额用完，这台机器上的入站已停{s.usage?.next_reset ? `，${fmtPeriodDay(s.usage.next_reset)}重置后自动恢复` : "，用量回到配额以内后自动恢复"}。想现在就恢复：调大配额、校正本期已用，或在「编辑」里关掉“配额用完就停”。</div>}
      {s.diagnostics?.metering_error && <div className="mb-4 rounded-md border border-red-500/40 p-3 text-sm text-destructive">节点流量采集异常：{s.diagnostics.metering_error}。当前用量可能未更新。</div>}
      {!maintenanceBusy && <ConfigStatusNotice server={s} retrying={republish.isPending} disabled={maintenanceBusy} onRetry={() => republish.mutate()} onDetails={() => setTab("diag")} />}
      {s.agent && s.agent_update && !s.agent_update.supported && (
        <div className="mb-4 rounded-md border border-amber-500/40 bg-amber-500/5 p-3 text-sm">
          请先在此 VPS 通过独立可信渠道配置验证器、签名根和本地安装器，再执行「更多」中的迁移命令。完成后只接受受信签名版本；未完成前不会自动更新。
        </div>
      )}
      {s.agent_update?.outdated && !s.diagnostics?.maintenance && (
        <div className="mb-4 rounded-md border border-sky-500/40 bg-sky-500/5 p-3 text-sm">
          agent 与控制端提供的版本不同，将随心跳自动同步。
        </div>
      )}

      <div className="mt-6">
        <Tabs value={tab} onChange={setTab} items={[{ value: "nodes", label: "概览" }, { value: "diag", label: "诊断" }, { value: "revisions", label: "配置版本" }]} />
      </div>

      {tab === "nodes" && <>
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Card className="p-4"><p className="flex items-center gap-1 text-xs text-muted-foreground"><ArrowUp className="h-3 w-3" />实时上行</p><p className="mt-1 text-2xl font-semibold tabular-nums">{m ? fmtRate(m.net_tx_rate) : "-"}</p></Card>
        <Card className="p-4"><p className="flex items-center gap-1 text-xs text-muted-foreground"><ArrowDown className="h-3 w-3" />实时下行</p><p className="mt-1 text-2xl font-semibold tabular-nums">{m ? fmtRate(m.net_rx_rate) : "-"}</p></Card>
        <Card className="p-4"><p className="text-xs text-muted-foreground">CPU / 负载</p><p className="mt-1 text-xl font-semibold">{m ? `${m.cpu_percent.toFixed(0)}%` : "-"}</p><p className="text-xs text-muted-foreground">load {m?.load1?.toFixed(2) ?? "-"} / {m?.load5?.toFixed(2) ?? "-"}</p></Card>
        <Card className="p-4"><p className="text-xs text-muted-foreground">内存</p><p className="mt-1 text-xl font-semibold">{m?.mem_total ? `${((m.mem_used / m.mem_total) * 100).toFixed(0)}%` : "-"}</p><p className="text-xs text-muted-foreground">{fmtBytes(m?.mem_used)} / {fmtBytes(m?.mem_total)}</p></Card>
        <Card className="col-span-2 p-4 lg:col-span-1"><p className="text-xs text-muted-foreground">磁盘</p><p className="mt-1 text-xl font-semibold">{m?.disk_total ? `${((m.disk_used / m.disk_total) * 100).toFixed(0)}%` : "-"}</p><p className="text-xs text-muted-foreground">{fmtBytes(m?.disk_used)} / {fmtBytes(m?.disk_total)}</p></Card>
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader>
            <CardTitle>服务器流量</CardTitle>
            <div className="grid gap-2 sm:grid-cols-[minmax(0,2fr)_minmax(0,1fr)]">
              <Select aria-label="流量统计对象" value={trafficSubject} onChange={(e) => setTrafficSelection({ serverID: id, nodeID: e.target.value })}>
                <option value="server">整台服务器</option>
                {(nodes.data ?? []).filter((n) => !n.attach_node_id).map((n) => <option key={n.id} value={String(n.id)}>入站 {n.name} · {PROTOCOL_LABELS[n.protocol] ?? n.protocol} :{n.listen_port}</option>)}
              </Select>
              <Select aria-label="服务器流量时间范围" value={trafficDays} onChange={(e) => setTrafficDays(Number(e.target.value))}>
                {[7, 30, 90].map((days) => <option key={days} value={days}>近 {days} 天</option>)}
              </Select>
            </div>
          </CardHeader>
          <CardContent>
            {traffic.isLoading ? <p className="text-sm text-muted-foreground">正在加载流量…</p>
              : traffic.isError ? <p className="text-destructive">流量加载失败</p>
              : traffic.data?.has_data ? <><TrafficIO inbound={traffic.data.total_up} outbound={traffic.data.total_down} /><TrafficBars points={traffic.data.points} /></>
              : <p className="text-sm text-muted-foreground">暂无用量记录</p>}
            <p className="text-xs text-muted-foreground">{trafficNode
              ? "当前只显示经过所选入站的流量。仅汇总已采集记录。"
              : "服务器按网卡收发计量，包含 SSH、系统更新等流量，与节点合计不必相等。仅汇总已采集记录。"}</p>
            {nodes.isError && <p className="mt-2 text-xs text-destructive">节点列表加载失败，暂时只能查看整台服务器。</p>}
          </CardContent>
        </Card>
        <Card className="order-first lg:order-none">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle>{s.usage?.next_reset ? "本期流量" : "近 30 天流量"}</CardTitle>
            {s.usage?.next_reset && <Button size="sm" variant="outline" onClick={() => setCalibrate(true)}><Gauge className="h-4 w-4" /> 校正</Button>}
          </CardHeader>
          <CardContent>
            {s.usage && (
              <>
                <p className="text-3xl font-semibold tabular-nums">{fmtBytes(s.usage.billed)}{s.quota_bytes > 0 && <span className="ml-2 text-sm font-normal text-muted-foreground">/ {fmtBytes(s.quota_bytes, 0)} · {s.usage.percent.toFixed(1)}%</span>}</p>
                <Progress className="mt-3" value={s.quota_bytes > 0 ? s.usage.percent : 0} />
                <dl className="mt-4 space-y-1.5 text-xs text-muted-foreground">
                  <div className="flex justify-between gap-3"><dt>怎么算</dt><dd>{BILLING_LABELS[s.usage.billing] ?? s.usage.billing}{s.quota_bytes > 0 ? "" : " · 未设配额"}</dd></div>
                  {s.usage.next_reset
                    ? <div className="flex justify-between gap-3"><dt>本期</dt><dd className="text-right">{fmtPeriodDay(s.usage.period_start)} – {fmtPeriodDay(s.usage.next_reset)} · {fmtResetIn(s.usage.next_reset)}</dd></div>
                    : <div className="flex justify-between gap-3"><dt>周期</dt><dd>没有重置日，按近 30 天滚动</dd></div>}
                  {s.usage.adjust !== 0 && <div className="flex justify-between gap-3"><dt>其中校正</dt><dd className="text-right tabular-nums">{s.usage.adjust > 0 ? "+" : "−"}{fmtBytes(Math.abs(s.usage.adjust))} · 面板自己计到 {fmtBytes(s.usage.measured)}</dd></div>}
                </dl>
                <TrafficIO className="mt-4" inbound={s.usage.inbound ?? s.usage.up} outbound={s.usage.outbound ?? s.usage.down} />
                {s.usage.adjust !== 0 && <p className="mt-1.5 text-xs text-muted-foreground">上面三格是面板自己计到的，不含校正。</p>}
              </>
            )}
            <dl className="mt-4 space-y-1.5 text-xs text-muted-foreground">
              <div className="flex justify-between"><dt>agent 版本</dt><dd>{s.agent?.version || "-"}</dd></div>
              <div className="flex justify-between"><dt>最后心跳</dt><dd>{fmtAgo(s.agent?.last_seen_at)}</dd></div>
              <div className="flex justify-between"><dt>运行时间</dt><dd>{m ? fmtDuration(m.uptime_sec) : "-"}</dd></div>
              <div className="flex justify-between"><dt>配置版本</dt><dd>{s.desired ? `rev ${s.desired.revision} ${s.desired.in_sync ? "✓" : "…"}` : "-"}</dd></div>
              <div className="flex justify-between"><dt>内核</dt><dd>{m?.kernel || "-"} {m?.arch}</dd></div>
            </dl>
          </CardContent>
        </Card>
      </div>

      {samples.data && samples.data.length > 1 && (
        <Card className="mt-4">
          <CardHeader><CardTitle>近 24 小时速率</CardTitle></CardHeader>
          <CardContent><RateArea points={samples.data} /></CardContent>
        </Card>
      )}
      </>}

      {tab === "nodes" && <ServerProxyUsage nodes={nodes.data ?? []} />}

      {tab === "diag" && (
        <div className="mt-4 grid gap-4 lg:grid-cols-2">
          <Card>
            <CardHeader><CardTitle className="flex items-center gap-2"><ShieldCheck className="h-4 w-4" /> 主机健康</CardTitle></CardHeader>
            <CardContent>
              {!d ? <p className="text-sm text-muted-foreground">等待 agent 上报</p> : (
                <ul className="grid grid-cols-2 gap-2 text-sm">
                  <Diag ok={d.bbr} label={`拥塞控制 ${d.congestion_ctl || "?"}`} neutral={s.node_count === 0} />
                  <Diag ok={d.time_sync} label="时间同步" />
                  <Diag ok={Math.abs(d.clock_skew_ms) < 2000} label={`时钟偏差 ${d.clock_skew_ms} ms`} />
                  <Diag ok={d.ipv4_reachable} label="IPv4 出网" />
                  <Diag ok={d.ipv6_reachable} label="IPv6 出网" warnOnly />
                  <Diag ok={d.nftables} label="nftables 计量" />
                  <Diag ok={d.systemd} label="systemd" />
                  <Diag ok={d.oom_events === 0} label={`OOM 事件 ${d.oom_events}`} />
                </ul>
              )}
              {d?.warnings?.length ? <ul className="mt-3 space-y-1 text-xs text-amber-600 dark:text-amber-400">{d.warnings.map((w, i) => <li key={i}>• {w}</li>)}</ul> : null}
              {d?.certs?.length ? (
                <div className="mt-4">
                  <p className="mb-1 text-xs font-medium text-muted-foreground">证书</p>
                  {d.certs.map((c) => <p key={c.domain} className="text-xs">{c.domain} · {c.mode} · 到期 {fmtDate(c.not_after, false)}</p>)}
                </div>
              ) : null}
            </CardContent>
          </Card>
          <Card>
            <CardHeader><CardTitle>内核进程</CardTitle></CardHeader>
            <CardContent>
              {!d?.cores?.length ? <p className="text-sm text-muted-foreground">无</p> : (
                <div className="space-y-3">
                  {d.cores.map((c) => (
                    <div key={c.name} className="rounded-md border p-3 text-sm">
                      <div className="flex items-center justify-between">
                        <span className="font-medium">{c.name} <span className="text-xs text-muted-foreground">{c.version}</span></span>
                        <Badge variant={!c.wanted ? "secondary" : c.active ? "success" : "destructive"}>{!c.wanted ? "未启用" : c.active ? "运行中" : "未运行"}</Badge>
                      </div>
                      <p className="mt-1 text-xs text-muted-foreground">
                        {c.installed ? "已安装" : "未安装"} · 内存 {fmtBytes(c.rss_bytes)} · 重启 {c.nrestarts} 次{c.instances ? ` · ${c.instances} 个实例` : ""}
                      </p>
                      {c.last_error && <p className="mt-1 break-all text-xs text-red-500">{c.last_error}</p>}
                    </div>
                  ))}
                </div>
              )}
              {d?.recent_errors?.length ? (
                <div className="mt-4">
                  <p className="mb-1 text-xs font-medium text-muted-foreground">sing-box 最近告警</p>
                  <Pre className="max-h-48">{d.recent_errors.join("\n")}</Pre>
                </div>
              ) : null}
              {d && d.connlog_lag > 0 && <p className="mt-3 text-xs text-muted-foreground">连接日志待上传：{d.connlog_lag} 条</p>}
            </CardContent>
          </Card>
        </div>
      )}

      {tab === "revisions" && (
        <Card className="mt-4">
          <CardContent className="pt-4">
            {!desired.data?.length ? <p className="text-sm text-muted-foreground">尚无配置版本</p> : (
              <Table>
                <thead><tr className="border-b"><Th>版本</Th><Th>状态</Th><Th>节点数</Th><Th>时间</Th><Th>错误</Th></tr></thead>
                <tbody>
                  {desired.data.map((r) => (
                    <Tr key={r.revision}>
                      <Td>rev {r.revision}</Td>
                      <Td><Badge variant={r.status === "applied" ? "success" : r.status === "failed" ? "destructive" : "secondary"}>{r.status}</Badge></Td>
                      <Td>{r.summary?.nodes?.length ?? "-"}</Td>
                      <Td className="text-muted-foreground">{fmtDate(r.created_at)}</Td>
                      <Td className="max-w-md break-words text-xs text-red-500">{r.error}</Td>
                    </Tr>
                  ))}
                </tbody>
              </Table>
            )}
            <div className="mt-4 flex flex-wrap gap-2">
              <Button size="sm" variant="outline" onClick={() => enrollM.mutate()} loading={enrollM.isPending}><KeyRound className="h-4 w-4" /> 重新生成安装命令</Button>
              {s.agent && (
                <Button size="sm" variant="outline" onClick={async () => {
                  const origin = window.location.origin;
                  const cmd = `curl -fsSL ${origin}/install-agent.sh | sudo bash -s -- --update --server ${origin}`;
                  await copyText(cmd);
                  toast.success("已复制更新命令，在该 VPS 上执行即可，不用重新注册");
                }}><RefreshCw className="h-4 w-4" /> 复制更新 agent 命令</Button>
              )}
              <Button size="sm" variant="outline" className="text-red-500" onClick={() => setConfirmReset(true)}>吊销 agent 令牌</Button>
            </div>
          </CardContent>
        </Card>
      )}

      <ServerDialog open={edit} onClose={() => setEdit(false)} server={s} />
      <CalibrateDialog open={calibrate} onClose={() => setCalibrate(false)} server={s} />
      <Dialog open={!!enroll} onClose={() => setEnroll(null)} title="安装 agent" description="先从独立可信发行渠道安装验证器、安装脚本和本机策略，再以 root 执行以下命令。令牌 15 分钟有效，仅可使用一次。">
        {enroll && (
          <div className="space-y-3">
            <Pre className="whitespace-pre-wrap break-all">{enroll.install_command}</Pre>
            <Button onClick={async () => { await copyText(enroll.install_command); toast.success("已复制"); }}><Copy className="h-4 w-4" /> 复制命令</Button>
            <p className="text-xs text-muted-foreground">脚本只补装机器上缺少的依赖（nft；没有时间同步服务时装 chrony），下载 ctlvps-agent，注册并以 systemd 常驻。只做监控、不建入站的机器，agent 不改动主机的内核参数和时间同步。安装完成后此页面会显示「在线」。</p>
          </div>
        )}
      </Dialog>
      <Confirm open={confirmReset} onClose={() => setConfirmReset(false)} onConfirm={() => resetTok.mutate()} destructive title="吊销 agent 令牌？" description="agent 将无法继续通信，需要重新生成安装命令并注册。" />
    </div>
  );
}

// The panel only counts from the day a server joins it. Entering what the
// host says has been used brings the running period in line with the bill.
function CalibrateDialog({ open, onClose, server }: { open: boolean; onClose: () => void; server: Server }) {
  const qc = useQueryClient();
  const toast = useToast();
  const u = server.usage;
  const [gb, setGb] = React.useState("");
  React.useEffect(() => setGb(u ? bytesToGb(u.billed) : ""), [open]);
  const done = (text: string) => (s: Server) => { qc.setQueryData(["servers", String(server.id)], s); qc.invalidateQueries({ queryKey: ["servers"] }); qc.invalidateQueries({ queryKey: ["dashboard"] }); toast.success(text); onClose(); };
  const save = useMutation({ mutationFn: () => put<Server>(`/api/v1/servers/${server.id}/usage`, { used_bytes: gbToBytes(gb) }), onSuccess: done("已校正"), onError: (e) => toast.fromError(e) });
  const clear = useMutation({ mutationFn: () => del<Server>(`/api/v1/servers/${server.id}/usage`), onSuccess: done("已撤销校正"), onError: (e) => toast.fromError(e) });
  const valid = gb.trim() !== "" && Number.isFinite(Number(gb)) && Number(gb) >= 0;
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="校正本期已用流量"
      description="填服务商后台（或原来的探针）现在显示的本期已用流量。面板从这个数接着往下计，到下次重置自动归零。"
      footer={
        <>
          {u && u.adjust !== 0 && <Button variant="outline" className="mr-auto" onClick={() => clear.mutate()} loading={clear.isPending}>撤销校正</Button>}
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={() => save.mutate()} loading={save.isPending} disabled={!valid}>保存</Button>
        </>
      }
    >
      <Field label="本期已用 (GB)" hint={`这台服务器的算法是「${BILLING_LABELS[u?.billing ?? "dual"]}」，填同一种算法下的数。面板自己计到的是 ${fmtBytes(u?.measured)}。`}>
        <Input type="number" min={0} step="0.01" value={gb} onChange={(e) => setGb(e.target.value)} autoFocus />
      </Field>
    </Dialog>
  );
}

/** `neutral`: shown for information on a server the panel only watches, not judged. */
function Diag({ ok, label, warnOnly, neutral }: { ok: boolean; label: string; warnOnly?: boolean; neutral?: boolean }) {
  return (
    <li className="flex items-center gap-2">
      <span className={`h-2 w-2 rounded-full ${ok ? "bg-emerald-500" : neutral ? "bg-muted-foreground/40" : warnOnly ? "bg-amber-500" : "bg-red-500"}`} />
      {label}
    </li>
  );
}

// What this machine carried for each user in the last 30 days, by inbound.
function ServerProxyUsage({ nodes }: { nodes: Node[] }) {
  const inbounds = nodes.filter((n) => !n.attach_node_id && !n.share_id);
  const members = nodes.filter((n) => n.attach_node_id && !n.revoked);
  const byUser = new Map<string, { name: string; id: number; total: number; lines: number }>();
  for (const m of members) {
    const key = String(m.share_id);
    const row = byUser.get(key) ?? { name: m.share_name ?? "—", id: m.share_id ?? 0, total: 0, lines: 0 };
    row.total += m.traffic?.total ?? 0;
    row.lines += 1;
    byUser.set(key, row);
  }
  const users = [...byUser.values()].sort((a, b) => b.total - a.total);
  return (
    <div className="mt-4 grid gap-4 lg:grid-cols-2">
      <Card>
        <CardHeader><CardTitle>入站</CardTitle></CardHeader>
        <CardContent>
          {!inbounds.length ? <p className="text-sm text-muted-foreground">这台机器只做监控，没有入站。要让它代理流量，到<Link to="/nodes" className="text-primary hover:underline">「节点」</Link>里新建入站。</p> : (
            <Table>
              <thead><tr className="border-b"><Th>名称</Th><Th>协议</Th><Th>端口</Th><Th>凭据</Th><Th className="text-right">近 30 天</Th></tr></thead>
              <tbody>
                {inbounds.map((n) => (
                  <Tr key={n.id}>
                    <Td className="font-medium">{n.name}{!n.enabled && <Badge variant="secondary" className="ml-2">已停用</Badge>}</Td>
                    <Td className="text-muted-foreground">{PROTOCOL_LABELS[n.protocol] ?? n.protocol}</Td>
                    <Td className="tabular-nums">{n.listen_port}</Td>
                    <Td className="tabular-nums">{members.filter((m) => m.attach_node_id === n.id).length}</Td>
                    <Td className="text-right tabular-nums">{n.traffic?.has_data ? fmtBytes(n.traffic.total) : "—"}</Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </CardContent>
      </Card>
      <Card>
        <CardHeader><CardTitle>各用户在这台机器上的用量（近 30 天）</CardTitle></CardHeader>
        <CardContent>
          {!users.length ? <p className="text-sm text-muted-foreground">还没有用户用到这台机器</p> : (
            <Table>
              <thead><tr className="border-b"><Th>用户</Th><Th>线路数</Th><Th className="text-right">用量</Th></tr></thead>
              <tbody>
                {users.map((u) => (
                  <Tr key={u.id}>
                    <Td className="font-medium"><Link to={`/users/${u.id}`} className="hover:underline">{u.name}</Link></Td>
                    <Td className="tabular-nums">{u.lines}</Td>
                    <Td className="text-right tabular-nums">{u.total > 0 ? fmtBytes(u.total) : "—"}</Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
