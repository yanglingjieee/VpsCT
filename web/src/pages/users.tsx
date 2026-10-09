import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ArrowRight, Ban, Copy, ExternalLink, KeyRound, Pause, Pencil, Play, Plus, QrCode, RotateCcw, ScrollText, Trash2 } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import type { Line, Node, Series, Share, ShareStatus } from "@/lib/types";
import { bytesToGb, cn, copyText, fmtAgo, fmtBytes, fmtDate, gbToBytes, parseResetDay, PROTOCOL_LABELS, STATUS_LABELS } from "@/lib/utils";
import { Badge, Button, Card, CardContent, CardHeader, CardTitle, Confirm, Dialog, Empty, Field, Input, PageHeader, Progress, Select, Spinner, Switch, Table, Td, Th, Tr, Textarea } from "@/components/ui";
import { DateTimeInput, ResetDayInput } from "@/components/datetime-picker";
import { useToast } from "@/components/toast";
import { TrafficBars } from "@/components/charts";
import { QR } from "@/components/qr";
import { CLIENT_APPS } from "@/lib/clients";
import { useAuth } from "@/lib/auth";

export function StatusBadge({ s }: { s: ShareStatus }) {
  const v = s === "active" ? "success" : s === "paused" ? "warning" : s === "exhausted" ? "destructive" : "secondary";
  return <Badge variant={v}>{STATUS_LABELS[s] ?? s}</Badge>;
}

const total = (s: Share) => s.used_upload + s.used_download;

export function UserCard({ user }: { user: Share }) {
  const u = user.usage;
  return (
    <Link to={`/users/${user.id}`} className="block min-w-0">
      <Card className="h-full p-4 transition-colors hover:bg-accent/30">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="break-words font-medium">{user.name}</p>
            <p className="break-words text-xs text-muted-foreground">{user.line_count} 条线路 · {user.delivery === "nodes" ? "只给节点" : "一键配置"}</p>
          </div>
          <StatusBadge s={user.status} />
        </div>
        <div className="mt-3">
          <div className="mb-1 flex justify-between text-xs text-muted-foreground">
            <span>本期 {fmtBytes(total(user))}{u.quota > 0 && ` / ${fmtBytes(u.quota)}`}</span>
            <span>{u.quota > 0 ? `${u.percent.toFixed(0)}%` : "不限量"}</span>
          </div>
          <Progress value={u.quota > 0 ? u.percent : 0} />
          <p className="mt-1.5 text-xs text-muted-foreground">
            {u.next_reset ? `${fmtDate(u.next_reset, false)} 重置` : "不重置"}
            {user.expires_at && ` · ${fmtDate(user.expires_at, false)} 到期`}
          </p>
        </div>
      </Card>
    </Link>
  );
}

export function UsersPage() {
  const q = useQuery({ queryKey: ["shares"], queryFn: () => get<Share[]>("/api/v1/shares"), refetchInterval: 30000 });
  const [create, setCreate] = React.useState(false);
  const [filter, setFilter] = React.useState<"" | ShareStatus>("");
  const list = (q.data ?? []).filter((s) => !filter || s.status === filter);
  return (
    <div>
      <PageHeader title="用户" description="每个用户有自己的凭据、用量、限额和专属链接" actions={<Button onClick={() => setCreate(true)}><Plus className="h-4 w-4" /> 新建用户</Button>} />
      {(q.data?.length ?? 0) > 0 && (
        <div className="mb-4 flex flex-wrap gap-1.5">
          {(["", "active", "exhausted", "paused", "expired", "revoked"] as const).map((s) => {
            const n = s === "" ? q.data?.length ?? 0 : q.data?.filter((x) => x.status === s).length ?? 0;
            if (s !== "" && !n) return null;
            return <Badge key={s} variant={filter === s ? "default" : "outline"} className="cursor-pointer" onClick={() => setFilter(s)}>{s === "" ? "全部" : STATUS_LABELS[s]} {n}</Badge>;
          })}
        </div>
      )}
      {q.isLoading ? <Spinner /> : !list.length ? (
        <Empty title="还没有用户" description="新建用户后会在所选线路上生成他自己的凭据，并得到一个专属链接。" action={<Button onClick={() => setCreate(true)}>新建用户</Button>} />
      ) : (
        <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{list.map((s) => <UserCard key={s.id} user={s} />)}</div>
      )}
      <UserDialog open={create} onClose={() => setCreate(false)} />
    </div>
  );
}

// ---------- create / edit ----------
interface Form {
  name: string;
  delivery: "profile" | "nodes";
  line_mode: "all" | "selected";
  line_ids: number[];
  extra_node_ids: number[];
  quota_gb: string;
  reset_day: string;
  expires_at: string;
  connlog_enabled: boolean;
  notes: string;
}

function toLocal(iso: string) {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function UserDialog({ open, onClose, user }: { open: boolean; onClose: () => void; user?: Share }) {
  const qc = useQueryClient();
  const toast = useToast();
  const nav = useNavigate();
  const lines = useQuery({ queryKey: ["lines"], queryFn: () => get<Line[]>("/api/v1/lines"), enabled: open });
  const nodes = useQuery({ queryKey: ["nodes", "plain"], queryFn: () => get<Node[]>("/api/v1/nodes"), enabled: open });
  const [f, setF] = React.useState<Form | null>(null);
  React.useEffect(() => {
    if (!open) { setF(null); return; }
    if (user) {
      setF({
        name: user.name, delivery: user.delivery, line_mode: user.line_mode === "selected" ? "selected" : "all", line_ids: user.line_ids ?? [], extra_node_ids: user.extra_node_ids ?? [],
        quota_gb: bytesToGb(user.quota_bytes), reset_day: String(user.reset_day ?? 0), expires_at: user.expires_at ? toLocal(user.expires_at) : "",
        connlog_enabled: user.connlog_enabled, notes: user.notes,
      });
    } else {
      setF({ name: "", delivery: "profile", line_mode: "all", line_ids: [], extra_node_ids: [], quota_gb: "0", reset_day: "1", expires_at: "", connlog_enabled: false, notes: "" });
    }
  }, [open, user]);
  const set = <K extends keyof Form>(k: K, v: Form[K]) => setF((p) => (p ? { ...p, [k]: v } : p));
  const save = useMutation({
    mutationFn: () => {
      const body = {
        ...f!, user_id: null, targets: [], billing_mode: "dual", line_ids: f!.line_mode === "selected" ? f!.line_ids : [],
        quota_bytes: gbToBytes(f!.quota_gb), reset_day: parseResetDay(f!.reset_day),
        expires_at: f!.expires_at ? new Date(f!.expires_at).toISOString() : "0001-01-01T00:00:00Z",
      };
      return user ? put<Share>(`/api/v1/shares/${user.id}`, body) : post<Share>("/api/v1/shares", body);
    },
    onSuccess: (r) => {
      for (const k of ["shares", "lines", "nodes"]) qc.invalidateQueries({ queryKey: [k] });
      toast.success(user ? "已保存" : "用户已创建");
      onClose();
      if (!user) nav(`/users/${r.id}`);
    },
    onError: (e) => toast.fromError(e),
  });
  const extras = (nodes.data ?? []).filter((n) => n.source === "imported" || n.source === "manual");
  const toggle = (key: "line_ids" | "extra_node_ids", id: number, on: boolean) => f && set(key, on ? [...f[key], id] : f[key].filter((x) => x !== id));
  return (
    <Dialog open={open} onClose={onClose} title={user ? "编辑用户" : "新建用户"} size="md"
      footer={<><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={!f?.name.trim()}>{user ? "保存" : "创建"}</Button></>}>
      {!f ? <Spinner /> : (
        <div className="space-y-5">
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="名称"><Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="sansan" autoFocus /></Field>
            <Field label="交付方式">
              <Select value={f.delivery} onChange={(e) => set("delivery", e.target.value as Form["delivery"])}>
                <option value="profile">一键配置：线路 + 规则，各种客户端通用</option>
                <option value="nodes">只给节点：拼车用，对方自己配置</option>
              </Select>
            </Field>
          </div>

          <section>
            <div className="mb-2 grid gap-3 sm:grid-cols-2">
              <Field label="线路">
                <Select value={f.line_mode} onChange={(e) => set("line_mode", e.target.value as Form["line_mode"])}>
                  <option value="all">全部线路（以后新增的也自动加上）</option>
                  <option value="selected">指定线路</option>
                </Select>
              </Field>
            </div>
            {f.line_mode === "selected" && (
              !lines.data?.length ? <p className="rounded-xl border border-dashed px-4 py-3 text-sm text-muted-foreground">还没有线路，先到「节点」里新建。</p> : (
                <div className="max-h-56 overflow-auto rounded-xl border">
                  {lines.data.map((l) => (
                    <label key={l.id} className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-sm last:border-0">
                      <input type="checkbox" className="accent-primary" checked={f.line_ids.includes(l.id)} onChange={(e) => toggle("line_ids", l.id, e.target.checked)} />
                      <span className="min-w-0 flex-1 break-words">{l.name}</span>
                      <span className="text-xs text-muted-foreground">{l.landing_node_id ? `${l.entry_server} → ${l.landing_server}` : l.entry_server}</span>
                    </label>
                  ))}
                </div>
              )
            )}
          </section>

          {extras.length > 0 && (
            <section>
              <p className="mb-1 text-sm font-medium">附加外部节点</p>
              <p className="mb-2 text-xs text-muted-foreground">不经过自己的服务器，不计入这个用户的用量。</p>
              <div className="max-h-40 overflow-auto rounded-xl border">
                {extras.map((n) => (
                  <label key={n.id} className="flex cursor-pointer items-center gap-2 border-b px-3 py-2 text-sm last:border-0">
                    <input type="checkbox" className="accent-primary" checked={f.extra_node_ids.includes(n.id)} onChange={(e) => toggle("extra_node_ids", n.id, e.target.checked)} />
                    <span className="min-w-0 flex-1 break-words">{n.name}</span><span className="text-xs text-muted-foreground">{PROTOCOL_LABELS[n.protocol] ?? n.protocol}</span>
                  </label>
                ))}
              </div>
            </section>
          )}

          <section className="grid gap-3 sm:grid-cols-3">
            <Field label="每期流量 (GB)" hint="0 为不限"><Input type="number" min={0} step="1" value={f.quota_gb} onChange={(e) => set("quota_gb", e.target.value)} /></Field>
            <Field label="重置日"><ResetDayInput value={f.reset_day} onChange={(v) => set("reset_day", v)} /></Field>
            <Field label="到期时间"><DateTimeInput value={f.expires_at} onChange={(v) => set("expires_at", v)} placeholder="永久有效" /></Field>
          </section>
          <p className="-mt-3 text-xs text-muted-foreground">用量是这个用户在每台服务器上实际走掉的流量之和：中转线路入口机和落地机各算一次。用完后只停他一个人。</p>

          <Switch checked={f.connlog_enabled} onChange={(v) => set("connlog_enabled", v)} label="记录这个用户的连接日志（访问了哪些地址、来自哪个 IP）" />
          <Field label="备注"><Textarea rows={2} value={f.notes} onChange={(e) => set("notes", e.target.value)} /></Field>
        </div>
      )}
    </Dialog>
  );
}

// ---------- detail ----------
function CopyRow({ label, value }: { label?: string; value: string }) {
  const toast = useToast();
  const [qr, setQR] = React.useState(false);
  return (
    <div>
      <div className="flex items-center gap-2">
        {label && <span className="w-40 shrink-0 break-words text-sm">{label}</span>}
        <code className="min-w-0 flex-1 truncate rounded-md bg-muted/50 px-2 py-1.5 text-xs">{value}</code>
        <Button size="sm" variant="ghost" aria-label="二维码" onClick={() => setQR((v) => !v)}><QrCode className="h-4 w-4" /></Button>
        <Button size="sm" variant="ghost" aria-label="复制" onClick={async () => { await copyText(value); toast.success("已复制"); }}><Copy className="h-4 w-4" /></Button>
      </div>
      {qr && <QR text={value} className="mt-2" />}
    </div>
  );
}

function LinkCard({ user }: { user: Share }) {
  // In a client the profile is "the service", so it carries the site's name.
  const siteName = useAuth().meta?.site_name ?? "土豆饼的家";
  const toast = useToast();
  if (!user.link) return <p className="text-sm text-muted-foreground">尚未生成</p>;
  if (user.delivery === "nodes") {
    const links = user.node_links ?? [];
    return (
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">把下面的节点链接发给对方即可，任何客户端都能添加。中转线路也是一条普通节点。</p>
        {!links.length ? <p className="text-sm text-muted-foreground">没有可用的节点</p> : links.map((n) => <CopyRow key={n.name} label={n.name} value={n.uri} />)}
        {links.length > 1 && <Button size="sm" variant="outline" onClick={async () => { await copyText(links.map((n) => n.uri).join("\n")); toast.success("已复制全部节点"); }}><Copy className="h-4 w-4" /> 复制全部</Button>}
        <div className="border-t pt-3">
          <p className="mb-1 text-xs text-muted-foreground">也可以给对方这个页面，他能看到自己的用量和节点：</p>
          <CopyRow value={user.link} />
        </div>
      </div>
    );
  }
  const apps = CLIENT_APPS.filter((a) => user.formats?.includes(a.kind));
  return (
    <div className="space-y-3">
      <p className="text-xs text-muted-foreground">这就是他的专属链接。用浏览器打开是他的个人页：看用量、选客户端一键导入；填进客户端就是订阅地址。</p>
      <CopyRow value={user.link} />
      <div className="flex flex-wrap gap-2">
        <a href={user.link} target="_blank" rel="noreferrer"><Button size="sm" variant="outline"><ExternalLink className="h-4 w-4" /> 打开个人页</Button></a>
        {apps.map((a) => <a key={a.id} href={a.link(user.link!, siteName)}><Button size="sm" variant="outline">导入 {a.name}</Button></a>)}
      </div>
    </div>
  );
}

export function UserDetailPage() {
  const { id } = useParams();
  const nav = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const q = useQuery({ queryKey: ["shares", id], queryFn: () => get<Share>(`/api/v1/shares/${id}`), refetchInterval: 20000 });
  const traffic = useQuery({ queryKey: ["shares", id, "traffic"], queryFn: () => get<Series>(`/api/v1/shares/${id}/traffic?days=30`) });
  const events = useQuery({ queryKey: ["shares", id, "events"], queryFn: () => get<{ id: number; ts: string; kind: string; detail: string }[]>(`/api/v1/shares/${id}/events?limit=30`) });
  const [edit, setEdit] = React.useState(false);
  const [confirm, setConfirm] = React.useState<null | "revoke" | "delete" | "reset" | "reissue">(null);
  const invalidate = () => { for (const k of ["shares", "nodes", "lines"]) qc.invalidateQueries({ queryKey: [k] }); };
  const action = useMutation({
    mutationFn: (a: string) => post(`/api/v1/shares/${id}/${a}`),
    onSuccess: (_r, a) => { toast.success(({ pause: "已暂停", resume: "已恢复", reset: "用量已清零", revoke: "已撤销", reissue: "已重新签发：凭据和专属链接都换了新的" } as Record<string, string>)[a] ?? "完成"); setConfirm(null); invalidate(); },
    onError: (e) => toast.fromError(e),
  });
  const remove = useMutation({ mutationFn: () => del(`/api/v1/shares/${id}`), onSuccess: () => { toast.success("已删除"); invalidate(); nav("/users"); }, onError: (e) => toast.fromError(e) });
  if (q.isLoading) return <Spinner />;
  const s = q.data;
  if (!s) return <Empty title="用户不存在" />;
  const u = s.usage;
  const eventLabels: Record<string, string> = { created: "创建", status: "状态变为", period_reset: "新周期", manual_reset: "手动清零", paused: "暂停", resumed: "恢复", revoked: "撤销", reissued: "重新签发" };
  return (
    <div>
      <PageHeader
        back={{ to: "/users", label: "用户" }}
        title={s.name}
        description={`${s.delivery === "nodes" ? "只给节点" : "一键配置"} · 创建于 ${fmtDate(s.created_at, false)}${s.notes ? ` · ${s.notes}` : ""}`}
        actions={
          <>
            <StatusBadge s={s.status} />
            {s.status !== "revoked" && (
              <>
                {s.status === "paused" ? <Button size="sm" variant="outline" onClick={() => action.mutate("resume")} loading={action.isPending}><Play className="h-4 w-4" /> 恢复</Button>
                  : s.status !== "expired" && <Button size="sm" variant="outline" onClick={() => action.mutate("pause")} loading={action.isPending}><Pause className="h-4 w-4" /> 暂停</Button>}
                <Button size="sm" variant="outline" onClick={() => setEdit(true)}><Pencil className="h-4 w-4" /> 编辑</Button>
                <Button size="sm" variant="outline" onClick={() => setConfirm("reset")}><RotateCcw className="h-4 w-4" /> 清零用量</Button>
                <Button size="sm" variant="outline" onClick={() => setConfirm("reissue")}><KeyRound className="h-4 w-4" /> 重新签发</Button>
                <Button size="sm" variant="ghost" className="text-red-500" onClick={() => setConfirm("revoke")}><Ban className="h-4 w-4" /> 撤销</Button>
              </>
            )}
            {s.status === "revoked" && <Button size="sm" variant="outline" onClick={() => setConfirm("reissue")}><KeyRound className="h-4 w-4" /> 重新签发</Button>}
            <Button size="sm" variant="ghost" className="text-red-500" aria-label="删除" onClick={() => setConfirm("delete")}><Trash2 className="h-4 w-4" /></Button>
          </>
        }
      />
      <div className="grid gap-4 lg:grid-cols-3">
        <Card>
          <CardHeader><CardTitle>本期用量</CardTitle></CardHeader>
          <CardContent>
            <p className="text-2xl font-semibold tabular-nums">{fmtBytes(total(s))}</p>
            <p className="text-xs text-muted-foreground">{u.quota > 0 ? `共 ${fmtBytes(u.quota)} · 剩余 ${fmtBytes(Math.max(0, u.remaining))}` : "不限量"} · 自 {fmtDate(s.period_start, false)}</p>
            <Progress className="mt-3" value={u.quota > 0 ? u.percent : 0} />
            <dl className="mt-4 space-y-1.5 text-xs text-muted-foreground">
              <div className="flex justify-between"><dt>上传 / 下载</dt><dd className="tabular-nums">{fmtBytes(s.used_upload)} / {fmtBytes(s.used_download)}</dd></div>
              <div className="flex justify-between"><dt>下次重置</dt><dd>{u.next_reset ? fmtDate(u.next_reset, false) : "不重置"}</dd></div>
              <div className="flex justify-between"><dt>到期</dt><dd>{s.expires_at ? fmtDate(s.expires_at) : "永久"}</dd></div>
              <div className="flex justify-between"><dt>连接日志</dt><dd>{s.connlog_enabled ? <Link to={`/connlog?share_id=${s.id}`} className="inline-flex items-center gap-1 text-primary hover:underline"><ScrollText className="h-3 w-3" /> 查看</Link> : "未记录"}</dd></div>
            </dl>
          </CardContent>
        </Card>
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle>{s.delivery === "nodes" ? "节点" : "专属链接"}</CardTitle></CardHeader>
          <CardContent>
            <LinkCard user={s} />
            {s.status !== "active" && <p className="mt-3 rounded-md border border-amber-500/40 bg-amber-500/5 p-2 text-xs">当前状态为「{STATUS_LABELS[s.status]}」：他的凭据已在所有服务器上停用，配置里也不再有线路。</p>}
          </CardContent>
        </Card>
      </div>

      <Card className="mt-4">
        <CardHeader><CardTitle>线路用量（本期）</CardTitle></CardHeader>
        <CardContent>
          {!s.lines?.length ? <p className="text-sm text-muted-foreground">没有线路</p> : (
            <Table>
              <thead><tr className="border-b"><Th>线路</Th><Th>经过的服务器</Th><Th>入口机</Th><Th>落地机</Th><Th className="text-right">合计</Th></tr></thead>
              <tbody>
                {s.lines.map((l) => (
                  <Tr key={l.line_id}>
                    <Td className="font-medium">{l.name}{!l.ready && <Badge variant="warning" className="ml-2">未就绪</Badge>}</Td>
                    <Td className="text-muted-foreground"><span className="inline-flex flex-wrap items-center gap-1">{l.entry_server}{l.landing_server && <><ArrowRight className="h-3 w-3" />{l.landing_server}</>}</span></Td>
                    <Td className="tabular-nums">{fmtBytes(l.entry_up + l.entry_down)}</Td>
                    <Td className="tabular-nums">{l.landing_server ? fmtBytes(l.landing_up + l.landing_down) : "—"}</Td>
                    <Td className={cn("text-right tabular-nums", l.total > 0 && "font-medium")}>{fmtBytes(l.total)}</Td>
                  </Tr>
                ))}
              </tbody>
            </Table>
          )}
        </CardContent>
      </Card>

      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle>近 30 天流量</CardTitle></CardHeader>
          <CardContent>{traffic.data?.has_data ? <TrafficBars points={traffic.data.points} /> : <p className="text-sm text-muted-foreground">暂无用量记录</p>}</CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>记录</CardTitle></CardHeader>
          <CardContent>
            {!events.data?.length ? <p className="text-sm text-muted-foreground">无</p> : (
              <ul className="space-y-2 text-xs">
                {events.data.map((e) => <li key={e.id}><span className="text-muted-foreground">{fmtAgo(e.ts)}</span> · <span className="font-medium">{eventLabels[e.kind] ?? e.kind}</span>{e.kind === "status" && e.detail ? ` ${STATUS_LABELS[e.detail] ?? e.detail}` : ""}</li>)}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>
      <UserDialog open={edit} onClose={() => setEdit(false)} user={s} />
      <Confirm open={confirm === "revoke"} onClose={() => setConfirm(null)} onConfirm={() => action.mutate("revoke")} loading={action.isPending} destructive title={`撤销「${s.name}」？`} description="他的凭据从所有服务器上撤下，专属链接失效。之后可以「重新签发」恢复。" />
      <Confirm open={confirm === "reset"} onClose={() => setConfirm(null)} onConfirm={() => action.mutate("reset")} loading={action.isPending} title="清零本期用量？" description="用量归零并开始新的周期；如果是「流量用尽」会恢复正常。" />
      <Confirm open={confirm === "reissue"} onClose={() => setConfirm(null)} onConfirm={() => action.mutate("reissue")} loading={action.isPending} title="重新签发？" description="他的所有凭据和专属链接都换成新的，旧的立即失效。链接泄露时用这个止损；之后要把新链接重新发给他。" />
      <Confirm open={confirm === "delete"} onClose={() => setConfirm(null)} onConfirm={() => remove.mutate()} loading={remove.isPending} destructive title={`删除「${s.name}」？`} description="删除这个用户、他的凭据、专属链接和连接日志，不能恢复。" />
    </div>
  );
}

