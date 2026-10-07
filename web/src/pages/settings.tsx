import * as React from "react";
import { useSearchParams } from "react-router-dom";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Save, Send, Plus, Trash2, Sun, Moon, Monitor } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import { MaintenancePanel } from "@/components/maintenance";
import { CoreUpgradeDetails } from "@/components/core-upgrade";
import type { AuditEvent, BanRule, AccessLog } from "@/lib/types";
import { fmtBytes, fmtDate, fmtDuration, cn } from "@/lib/utils";
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, Confirm, Dialog, Field, Input, PageHeader, Select, Spinner, Switch, Table, Tabs, Td, Th, Tr, Code } from "@/components/ui";
import { useToast } from "@/components/toast";
import { useTheme } from "@/lib/auth";

type Settings = Record<string, string>;

export function SettingsPage() {
  const [params, setParams] = useSearchParams();
  const selected = params.get("tab") || "general";
  const tab = ["general", "notify", "retention", "cores", "security", "audit", "system"].includes(selected) ? selected : "general";
  const setTab = (value: string) => setParams(p => { p.set("tab", value); return p; });
  return (
    <div>
      <PageHeader title="设置" />
      <Tabs value={tab} onChange={setTab} items={[
        { value: "general", label: "常规" }, { value: "notify", label: "通知" }, { value: "retention", label: "数据保留" }, { value: "cores", label: "内核版本" },
        { value: "security", label: "安全" }, { value: "audit", label: "审计日志" }, { value: "system", label: "系统" },
      ]} />
      <div className="mt-4">
        {tab === "general" && <GeneralTab />}
        {tab === "notify" && <NotifyTab />}
        {tab === "retention" && <RetentionTab />}
        {tab === "cores" && <CoresTab />}
        {tab === "security" && <SecurityTab />}
        {tab === "audit" && <AuditTab />}
        {tab === "system" && <><MaintenancePanel /><SystemTab /></>}
      </div>
    </div>
  );
}

function useSettings() {
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["settings"], queryFn: () => get<Settings>("/api/v1/settings") });
  const [draft, setDraft] = React.useState<Settings>({});
  const value = (k: string) => draft[k] ?? q.data?.[k] ?? "";
  const set = (k: string, v: string) => setDraft((d) => ({ ...d, [k]: v }));
  const save = useMutation({
    mutationFn: () => put<Settings>("/api/v1/settings", draft),
    onSuccess: (r) => { qc.setQueryData(["settings"], r); setDraft({}); toast.success("已保存"); qc.invalidateQueries({ queryKey: ["meta"] }); qc.invalidateQueries({ queryKey: ["core-upgrade"] }); },
    onError: (e) => toast.fromError(e),
  });
  return { q, value, set, save, dirty: Object.keys(draft).length > 0 };
}

function SaveBar({ s }: { s: ReturnType<typeof useSettings> }) {
  return <div className="mt-4 flex justify-end"><Button onClick={() => s.save.mutate()} loading={s.save.isPending} disabled={!s.dirty}><Save className="h-4 w-4" /> 保存</Button></div>;
}

const RESET_ZONES = [
  ["UTC", "UTC"],
  ["Asia/Shanghai", "北京时间（UTC+8）"],
  ["Asia/Tokyo", "东京（UTC+9）"],
  ["Asia/Singapore", "新加坡（UTC+8）"],
  ["Europe/London", "伦敦"],
  ["Europe/Berlin", "柏林"],
  ["America/New_York", "纽约"],
  ["America/Los_Angeles", "洛杉矶"],
] as const;

function GeneralTab() {
  const s = useSettings();
  const { theme, setTheme } = useTheme();
  if (s.q.isLoading) return <Spinner />;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <Card className="p-4 sm:p-5">
        <div className="grid gap-4">
          <Field label="站点名称"><Input value={s.value("site.name")} onChange={(e) => s.set("site.name", e.target.value)} /></Field>
          <Field label="站点外部地址" hint="用户专属链接的前缀；经 Cloudflare 或反向代理时请填写"><Input value={s.value("site.url")} onChange={(e) => s.set("site.url", e.target.value)} placeholder="https://panel.example.com" /></Field>
          <Field label="服务器流量用到多少开始提醒" hint="只看「服务器」的本期已用 ÷ 月配额（每台按自己的算法：双向或只算出站）。到了这个比例：总览出现一条黄条；配了 Telegram 也会推一次。还不停节点。用户的限额不看这个数，用尽才提醒。">
            <Select value={s.value("quota.alert_percent") || "80"} onChange={(e) => s.set("quota.alert_percent", e.target.value)}>
              {(["70", "80", "90", "95"] as const).map((n) => <option key={n} value={n}>用到 {n}% 就提醒</option>)}
              {s.value("quota.alert_percent") && !["70", "80", "90", "95"].includes(s.value("quota.alert_percent")) && (
                <option value={s.value("quota.alert_percent")}>用到 {s.value("quota.alert_percent")}%</option>
              )}
            </Select>
          </Field>
          <Field label="服务器配额用尽以后（100%）" hint="「只提醒」不停机。「停掉节点」会关掉这台服务器上的全部节点，要在服务器页手动再开，不会下个月自动恢复。">
            <Select value={s.value("quota.action") === "stop" || s.value("quota.action") === "disable" ? "disable" : "alert"} onChange={(e) => s.set("quota.action", e.target.value)}>
              <option value="alert">只提醒，节点继续跑</option>
              <option value="disable">停掉该服务器全部节点</option>
            </Select>
          </Field>
          <Field label="重置日按哪个时区算" hint="服务器和用户的流量，都在重置日当天这个时区的 0 点开始新的一期。">
            <Select value={s.value("quota.timezone") || "UTC"} onChange={(e) => s.set("quota.timezone", e.target.value)}>
              {!RESET_ZONES.some(([zone]) => zone === (s.value("quota.timezone") || "UTC")) && <option value={s.value("quota.timezone")}>{s.value("quota.timezone")}</option>}
              {RESET_ZONES.map(([zone, label]) => <option key={zone} value={zone}>{label}</option>)}
            </Select>
          </Field>
          <Field label="agent 离线判定 (秒)"><Input type="number" min={30} value={s.value("agent.offline_after_seconds")} onChange={(e) => s.set("agent.offline_after_seconds", e.target.value)} /></Field>
        </div>
        <SaveBar s={s} />
      </Card>
      <Card className="p-4 sm:p-5">
        <p className="mb-3 text-sm font-medium">外观（仅本浏览器）</p>
        <div className="grid grid-cols-3 gap-2">
          {([["light", "浅色", Sun], ["dark", "深色", Moon], ["system", "跟随系统", Monitor]] as const).map(([v, label, Icon]) => (
            <button key={v} onClick={() => setTheme(v)} className={cn("flex flex-col items-center gap-2 rounded-md border p-3 text-sm transition-colors hover:bg-accent/40", theme === v && "border-primary bg-primary/5")}>
              <Icon className="h-5 w-5" />{label}
            </button>
          ))}
        </div>
      </Card>
    </div>
  );
}

interface TelegramStatus { configured: boolean; bot: string; bot_url: string; error: string; chat_error: string; chat_id: string }
interface TelegramChat { id: string; title: string; type: string }

function NotifyTab() {
  const s = useSettings();
  const toast = useToast();
  const qc = useQueryClient();
  const status = useQuery({ queryKey: ["settings", "telegram"], queryFn: () => get<TelegramStatus>("/api/v1/settings/telegram/status") });
  const [chats, setChats] = React.useState<TelegramChat[] | null>(null);
  const refresh = () => qc.invalidateQueries({ queryKey: ["settings", "telegram"] });
  const save = useMutation({
    mutationFn: (values: Record<string, string>) => put("/api/v1/settings", values),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["settings"] }); refresh(); },
    onError: (e) => toast.fromError(e),
  });
  const test = useMutation({ mutationFn: () => post("/api/v1/settings/telegram/test"), onSuccess: () => toast.success("测试消息已发送，去 Telegram 里看一眼"), onError: (e) => toast.fromError(e, "发送失败") });
  const detect = useMutation({
    mutationFn: () => get<TelegramChat[]>("/api/v1/settings/telegram/chats"),
    onSuccess: (list) => {
      if (!list.length) { toast.error("机器人还没收到过消息", `在 Telegram 里打开 ${status.data?.bot || "你的机器人"}，点 Start 或随便发一句话，再点一次`); return; }
      if (list.length === 1) { save.mutate({ "telegram.chat_id": list[0].id }, { onSuccess: () => toast.success(`已选定：${list[0].title || list[0].id}`) }); return; }
      setChats(list);
    },
    onError: (e) => toast.fromError(e),
  });
  if (s.q.isLoading) return <Spinner />;
  const st = status.data;
  const typeLabel: Record<string, string> = { private: "私聊", group: "群组", supergroup: "群组", channel: "频道" };
  return (
    <Card className="max-w-2xl p-4 sm:p-5">
      <p className="mb-4 text-sm leading-6 text-muted-foreground">会通知：服务器离线和恢复、配置下发失败、服务器流量到 {s.value("quota.alert_percent") || "80"}% 和用完、用户流量用完或到期、证书快到期，以及每日日报。</p>
      <div className="grid gap-4">
        <Field label="Bot Token" hint="在 Telegram 里找 @BotFather 新建机器人，把它给的那段话整段粘进来就行">
          <Input className="mono" value={s.value("telegram.bot_token")} onChange={(e) => s.set("telegram.bot_token", e.target.value)} placeholder="123456789:AA…" />
        </Field>
        {st?.configured && (
          <p className={cn("-mt-2 text-xs", st.error ? "text-destructive" : "text-emerald-600 dark:text-emerald-400")}>
            {st.error || `已连接到机器人 ${st.bot}`}
            {!st.error && st.bot_url && <a className="ml-2 text-primary underline-offset-2 hover:underline" href={st.bot_url} target="_blank" rel="noreferrer">在 Telegram 里打开</a>}
          </p>
        )}
        <Field label="Chat ID" hint="通知发给谁。是一串数字，不是机器人的名字：先在 Telegram 里给机器人发一条消息，再点「自动获取」">
          <div className="flex gap-2">
            <Input className="mono" value={s.value("telegram.chat_id")} onChange={(e) => s.set("telegram.chat_id", e.target.value)} placeholder="123456789" />
            <Button variant="outline" className="shrink-0" onClick={() => detect.mutate()} loading={detect.isPending || save.isPending} disabled={s.dirty || !st?.configured || !!st.error}>自动获取</Button>
          </div>
        </Field>
        {!s.dirty && st?.chat_error && <p className="-mt-2 text-xs text-destructive">{st.chat_error}</p>}
        {chats && (
          <div className="-mt-2 rounded-xl border">
            {chats.map((c) => (
              <button key={c.id} className="flex w-full items-center justify-between gap-3 border-b px-3 py-2 text-left text-sm last:border-0 hover:bg-accent/40"
                onClick={() => save.mutate({ "telegram.chat_id": c.id }, { onSuccess: () => { toast.success(`已选定：${c.title || c.id}`); setChats(null); } })}>
                <span className="min-w-0 break-words">{c.title || c.id}</span>
                <span className="shrink-0 text-xs text-muted-foreground">{typeLabel[c.type] ?? c.type} · {c.id}</span>
              </button>
            ))}
          </div>
        )}
        <div className="flex flex-wrap items-center gap-4">
          <Switch checked={s.value("telegram.daily_report") === "1"} onChange={(v) => s.set("telegram.daily_report", v ? "1" : "0")} label="每日日报（各服务器和各用户的用量）" />
          <Field label="发送时刻 (0-23 时)"><Input type="number" min={0} max={23} className="w-24" value={s.value("telegram.daily_hour")} onChange={(e) => s.set("telegram.daily_hour", e.target.value)} /></Field>
        </div>
      </div>
      <div className="mt-4 flex justify-between">
        <Button variant="outline" onClick={() => test.mutate()} loading={test.isPending} disabled={s.dirty}><Send className="h-4 w-4" /> 发送测试消息</Button>
        <Button onClick={() => s.save.mutate(undefined, { onSuccess: refresh })} loading={s.save.isPending} disabled={!s.dirty}><Save className="h-4 w-4" /> 保存</Button>
      </div>
      {s.dirty && <p className="mt-2 text-right text-xs text-muted-foreground">先保存，再自动获取或测试</p>}
    </Card>
  );
}

function RetentionTab() {
  const s = useSettings();
  if (s.q.isLoading) return <Spinner />;
  const rows: [string, string, string][] = [
    ["traffic.sample_retention_hours", "速率采样（小时）", "每 30s 一条，用于 24h 速率曲线；默认 48"],
    ["traffic.hourly_retention_days", "小时聚合（天）", "默认 14；天聚合永久保留（很小）"],
    ["connlog.retention_days", "连接日志原始事件（天）", "默认 7；日志量大时建议 3"],
    ["connlog.aggregate_retention_days", "连接日志域名日聚合（天）", "默认 90"],
    ["access_log.retention_days", "订阅访问日志（天）", "默认 30"],
  ];
  return (
    <Card className="max-w-2xl p-4 sm:p-5">
      <p className="mb-3 text-sm text-muted-foreground">清理任务每小时执行；缩短保留期会在下一次清理时立刻删除超期数据。连接日志量大时把原始事件降到 3 天，日聚合可留久一点。</p>
      <div className="grid gap-4">
        {rows.map(([k, label, hint]) => <Field key={k} label={label} hint={hint}><Input type="number" min={1} value={s.value(k)} onChange={(e) => s.set(k, e.target.value)} /></Field>)}
      </div>
      <SaveBar s={s} />
    </Card>
  );
}

interface CoreChannel {
  default: string;
  latest: string;
  versions: { version: string; latest?: boolean; prerelease?: boolean; published_at?: string }[];
  source?: string;
  error?: string;
}

function VersionSelect({ value, onChange, channel, loading }: { value: string; onChange: (v: string) => void; channel?: CoreChannel; loading?: boolean }) {
  const opts: { value: string; label: string }[] = [{ value: "", label: channel?.default ? `内置默认（${channel.default}）` : "内置默认" }];
  for (const v of channel?.versions ?? []) {
    const bits = [v.version];
    if (v.latest && !v.prerelease) bits.push("最新稳定");
    if (v.prerelease) bits.push("预发布");
    if (v.published_at) bits.push(v.published_at);
    opts.push({ value: v.version, label: bits.join(" · ") });
  }
  if (value && !opts.some((o) => o.value === value)) opts.push({ value, label: `${value}（当前）` });
  return (
    <Select value={value} onChange={(e) => onChange(e.target.value)} disabled={loading}>
      {loading && opts.length <= 1 ? <option value={value}>正在拉取上游版本…</option> : opts.map((o) => <option key={o.value || "default"} value={o.value}>{o.label}</option>)}
    </Select>
  );
}

function CoresTab() {
  const s = useSettings();
  const cat = useQuery({ queryKey: ["core-versions"], queryFn: () => get<{ singbox: CoreChannel; snell: CoreChannel; mita: CoreChannel }>("/api/v1/settings/core-versions"), staleTime: 60 * 60 * 1000 });
  if (s.q.isLoading) return <Spinner />;
  return (
    <div className="grid items-stretch gap-4 lg:grid-cols-2">
    <Card className="flex min-w-0 flex-col p-4 sm:p-5">
      <h2 className="mb-2 text-sm font-semibold">版本选择</h2>
      <p className="mb-3 text-sm leading-6 text-muted-foreground">下拉从 GitHub / Surge 拉取官方版本（约 6 小时刷新一次）。选中并保存才会下发。升级面板保留已锁定版本；sing-box 选择默认版本并保存后，也会锁定为当次具体版本。</p>
      <div className="grid gap-4">
        <Field label="sing-box" hint={cat.data?.singbox.error ? `上游暂不可用，已用备份列表。${cat.data.singbox.error}` : "VLESS / Hy2 / TUIC 走这个内核。"}>
          <VersionSelect value={s.value("core.singbox_version")} onChange={(v) => s.set("core.singbox_version", v)} channel={cat.data?.singbox} loading={cat.isLoading} />
        </Field>
        <Field label="mita（mieru）" hint="默认锁定 3.37.0；切换其他版本需填写对应官方 SHA-256，保存后升级。"><VersionSelect value={s.value("core.mita_version")} onChange={v=>s.set("core.mita_version",v)} channel={cat.data?.mita} loading={cat.isLoading} /></Field>
        <Field label="snell-server" hint={cat.data?.snell.error ? `未能探测 Surge CDN，已用已知版本。` : "只有 Snell 协议才用。v4 兼容 Surge 4 / 小火箭；v5 要 Surge 5。"}>
          <VersionSelect value={s.value("core.snell_version")} onChange={(v) => s.set("core.snell_version", v)} channel={cat.data?.snell} loading={cat.isLoading} />
        </Field>
      </div>
      <details className="mt-4 rounded-xl border border-border/70 px-3 py-2">
        <summary className="cursor-pointer text-sm font-medium text-muted-foreground">高级：下载校验（SHA-256，可留空）</summary>
        <div className="mt-3 grid gap-3">
          <Field label="sing-box SHA-256" hint="amd64=哈希,arm64=哈希"><Input className="mono" value={s.value("core.singbox_sha256")} onChange={(e) => s.set("core.singbox_sha256", e.target.value)} placeholder="可留空" /></Field>
          <Field label="mita SHA-256" hint="amd64=哈希,arm64=哈希；默认版本留空使用内置官方校验和。"><Input className="mono" value={s.value("core.mita_sha256")} onChange={e=>s.set("core.mita_sha256",e.target.value)} /></Field>
          <Field label="snell-server SHA-256"><Input className="mono" value={s.value("core.snell_sha256")} onChange={(e) => s.set("core.snell_sha256", e.target.value)} placeholder="可留空" /></Field>
        </div>
      </details>
      <div className="mt-auto"><SaveBar s={s} /></div>
    </Card>
    <Card className="min-w-0 p-4 sm:p-5">
      <CoreUpgradeDetails selectedVersion={s.value("core.singbox_version")} onSelect={v => s.set("core.singbox_version", v)} />
    </Card>
    </div>
  );
}

function SecurityTab() {
  const s = useSettings();
  const qc = useQueryClient();
  const toast = useToast();
  const bans = useQuery({ queryKey: ["bans"], queryFn: () => get<BanRule[]>("/api/v1/bans") });
  const access = useQuery({ queryKey: ["access-log"], queryFn: () => get<AccessLog[]>("/api/v1/access-log?limit=100") });
  const [add, setAdd] = React.useState(false);
  const [f, setF] = React.useState({ kind: "ip", value: "", reason: "" });
  const [confirmDel, setConfirmDel] = React.useState<BanRule | null>(null);
  const create = useMutation({ mutationFn: () => post<BanRule>("/api/v1/bans", f), onSuccess: () => { toast.success("已添加封禁"); setAdd(false); setF({ kind: "ip", value: "", reason: "" }); qc.invalidateQueries({ queryKey: ["bans"] }); }, onError: (e) => toast.fromError(e) });
  const remove = useMutation({ mutationFn: (id: number) => del(`/api/v1/bans/${id}`), onSuccess: () => { setConfirmDel(null); qc.invalidateQueries({ queryKey: ["bans"] }); }, onError: (e) => toast.fromError(e) });
  const toggle = useMutation({ mutationFn: (b: BanRule) => put(`/api/v1/bans/${b.id}`, { ...b, enabled: !b.enabled }), onSuccess: () => qc.invalidateQueries({ queryKey: ["bans"] }), onError: (e) => toast.fromError(e) });
  if (s.q.isLoading) return <Spinner />;
  return (
    <div className="grid gap-4 lg:grid-cols-2">
      <div className="space-y-4">
        <Card className="p-4 sm:p-5">
          <Field label="订阅端点限速（每 IP 每分钟次数）" hint="超过返回 429；同一 token 每分钟也受限"><Input type="number" min={1} value={s.value("security.subscription_rate_per_min")} onChange={(e) => s.set("security.subscription_rate_per_min", e.target.value)} /></Field>
          <p className="mt-3 text-xs text-muted-foreground">其他内置防护：登录失败限速、订阅 token 仅存哈希、agent 令牌一次性注册、CSRF 同源 Cookie。管理面板建议放在 Cloudflare 后并仅开放到自己的 IP。</p>
          <SaveBar s={s} />
        </Card>
        <Card>
          <CardHeader className="flex-row items-center justify-between"><CardTitle>封禁规则</CardTitle><Button size="sm" variant="outline" onClick={() => setAdd(true)}><Plus className="h-4 w-4" /> 添加</Button></CardHeader>
          <CardContent>
            {!bans.data?.length ? <p className="text-sm text-muted-foreground">无。可按 IP、CIDR 或 User-Agent 关键字拒绝订阅访问。</p> : (
              <Table>
                <thead><tr className="border-b"><Th>类型</Th><Th>值</Th><Th>原因</Th><Th>启用</Th><Th></Th></tr></thead>
                <tbody>{bans.data.map((b) => <Tr key={b.id}><Td><Badge variant="outline">{b.kind}</Badge></Td><Td className="mono text-xs">{b.value}</Td><Td className="text-xs text-muted-foreground">{b.reason}</Td><Td><Switch checked={b.enabled} onChange={() => toggle.mutate(b)} /></Td><Td className="text-right"><Button size="icon" variant="ghost" className="text-red-500" onClick={() => setConfirmDel(b)}><Trash2 className="h-4 w-4" /></Button></Td></Tr>)}</tbody>
              </Table>
            )}
          </CardContent>
        </Card>
      </div>
      <Card>
        <CardHeader><CardTitle>最近订阅访问</CardTitle></CardHeader>
        <CardContent>
          {access.isLoading ? <Spinner /> : !access.data?.length ? <p className="text-sm text-muted-foreground">无</p> : (
            <div className="max-h-[32rem] overflow-auto">
              <Table>
                <thead><tr className="border-b"><Th>时间</Th><Th>IP</Th><Th>格式</Th><Th>状态</Th><Th></Th></tr></thead>
                <tbody>{access.data.map((l) => (
                  <Tr key={l.id}>
                    <Td className="whitespace-nowrap text-xs">{fmtDate(l.ts)}</Td>
                    <Td className="mono text-xs">{l.ip}</Td>
                    <Td className="text-xs">{l.format}</Td>
                    <Td><Badge variant={l.status === 200 ? "success" : "destructive"}>{l.status}</Badge></Td>
                    <Td className="text-right"><Button size="sm" variant="ghost" title="封禁此 IP" onClick={() => { setF({ kind: "ip", value: l.ip, reason: "来自访问日志" }); setAdd(true); }}>封禁</Button></Td>
                  </Tr>
                ))}</tbody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>
      <Dialog open={add} onClose={() => setAdd(false)} title="添加封禁" footer={<><Button variant="outline" onClick={() => setAdd(false)}>取消</Button><Button onClick={() => create.mutate()} loading={create.isPending}>添加</Button></>}>
        <div className="grid gap-3">
          <Field label="类型"><Select value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}><option value="ip">IP</option><option value="cidr">CIDR</option><option value="ua">User-Agent 包含</option></Select></Field>
          <Field label="值"><Input className="mono" value={f.value} onChange={(e) => setF({ ...f, value: e.target.value })} placeholder={f.kind === "cidr" ? "1.2.3.0/24" : f.kind === "ua" ? "python-requests" : "1.2.3.4"} /></Field>
          <Field label="原因"><Input value={f.reason} onChange={(e) => setF({ ...f, reason: e.target.value })} /></Field>
        </div>
      </Dialog>
      <Confirm open={!!confirmDel} onClose={() => setConfirmDel(null)} onConfirm={() => confirmDel && remove.mutate(confirmDel.id)} destructive title="删除封禁规则？" />
    </div>
  );
}

function AuditTab() {
  const q = useQuery({ queryKey: ["audit"], queryFn: () => get<AuditEvent[]>("/api/v1/audit?limit=200") });
  return (
    <Card>
      <CardContent className="pt-4">
        {q.isLoading ? <Spinner /> : !q.data?.length ? <p className="text-sm text-muted-foreground">无记录</p> : (
          <Table>
            <thead><tr className="border-b"><Th>时间</Th><Th>用户</Th><Th>操作</Th><Th>对象</Th><Th>详情</Th><Th>IP</Th></tr></thead>
            <tbody>{q.data.map((e) => (
              <Tr key={e.id}>
                <Td className="whitespace-nowrap text-xs">{fmtDate(e.ts)}</Td>
                <Td className="text-xs">{e.username || "—"}</Td>
                <Td><Code>{e.action}</Code></Td>
                <Td className="max-w-[16rem] break-all text-xs">{e.target}</Td>
                <Td className="max-w-md text-xs text-muted-foreground">{e.detail ? <pre className="mono whitespace-pre-wrap break-all text-2xs leading-snug">{JSON.stringify(e.detail)}</pre> : ""}</Td>
                <Td className="mono text-xs text-muted-foreground">{e.ip}</Td>
              </Tr>
            ))}</tbody>
          </Table>
        )}
      </CardContent>
    </Card>
  );
}

interface SystemStatus {
  version: string; go: string; started_at: string; uptime_sec: number; goroutines: number; heap_bytes: number; data_dir: string; data_bytes?: number;
  jobs?: Record<string, { interval_sec: number; last_run?: string; error?: string }>;
  connlog?: Record<string, number>;
}

const JOB_LABEL: Record<string, string> = {
  agent_watch: "agent 在线检查",
  backup: "数据库备份",
  daily_report: "Telegram 日报",
  desired_refresh: "期望状态刷新",
  external_sync: "外部订阅同步",
  retention: "过期数据清理",
  share_tick: "用户限额 / 到期",
};

function jobInterval(sec: number): string {
  if (sec < 60) return `${sec} 秒`;
  if (sec % 3600 === 0) return `${sec / 3600} 小时`;
  if (sec % 60 === 0) return `${sec / 60} 分钟`;
  return fmtDuration(sec);
}

function SystemTab() {
  const q = useQuery({ queryKey: ["system"], queryFn: () => get<SystemStatus>("/api/v1/system/status"), refetchInterval: 15000 });
  if (q.isLoading || !q.data) return <Spinner />;
  const d = q.data;
  const jobs = Object.entries(d.jobs ?? {}).sort(([a], [b]) => a.localeCompare(b));
  const facts: [string, React.ReactNode][] = [
    ["版本", d.version],
    ["Go", d.go],
    ["运行时间", `${fmtDuration(d.uptime_sec)}（自 ${fmtDate(d.started_at)}）`],
    ["堆内存", `${fmtBytes(d.heap_bytes)} · ${d.goroutines} goroutines`],
    ["数据目录", <span className="mono text-xs">{d.data_dir}{d.data_bytes ? `（${fmtBytes(d.data_bytes)}）` : ""}</span>],
  ];
  if (d.connlog) facts.push(["连接日志库", `${fmtBytes(d.connlog.db_bytes)} · ${d.connlog.raw_events?.toLocaleString()} 条`]);
  return (
    <Card className="p-5 sm:p-6">
      <div className="grid gap-x-10 gap-y-2 sm:grid-cols-2">
        {facts.map(([k, v]) => (
          <div key={k} className="grid grid-cols-[6.5rem_minmax(0,1fr)] items-baseline gap-3 text-sm">
            <span className="text-muted-foreground">{k}</span>
            <span className="min-w-0 break-all">{v}</span>
          </div>
        ))}
      </div>
      <p className="mt-4 text-xs leading-5 text-muted-foreground">备份在数据目录：<Code>ctlvps.db</Code>、<Code>connlog.db</Code>，每日快照进 <Code>backups/</Code>，留 7 份。</p>
      <h3 className="mb-3 mt-8 text-base font-bold">后台任务</h3>
      {!jobs.length ? <p className="text-sm text-muted-foreground">无</p> : (
        <Table>
          <thead><tr className="border-b"><Th>任务</Th><Th>周期</Th><Th>上次运行</Th><Th>错误</Th></tr></thead>
          <tbody>{jobs.map(([name, j]) => (
            <Tr key={name}>
              <Td className="text-sm">{JOB_LABEL[name] ?? name}</Td>
              <Td className="text-sm text-muted-foreground">{jobInterval(j.interval_sec)}</Td>
              <Td className="text-sm text-muted-foreground">{j.last_run ? fmtDate(j.last_run) : "—"}</Td>
              <Td className="text-sm text-red-500">{j.error || "—"}</Td>
            </Tr>
          ))}</tbody>
        </Table>
      )}
    </Card>
  );
}
