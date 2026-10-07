import * as React from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowDown, ArrowRight, ArrowUp, Pencil, Plus, RefreshCw, RotateCcw, Trash2, Upload } from "lucide-react";
import { del, get, post, put } from "@/lib/api";
import type { ExternalSubscription, Line, LineCandidate, Node, Server } from "@/lib/types";
import { fmtAgo, fmtBytes, PROTOCOL_LABELS } from "@/lib/utils";
import { Badge, Button, Card, Confirm, Dialog, Empty, Field, Input, PageHeader, SectionTitle, Select, Spinner, Switch, Table, Td, Th, Tr, Textarea } from "@/components/ui";
import { useToast } from "@/components/toast";

const sni = (n: Node) => String((n.params as { servername?: string })?.servername ?? "");
const isInbound = (n: Node) => n.source === "deployed" && !n.share_id && !n.attach_node_id;
const isExternal = (n: Node) => n.source === "imported" || n.source === "manual";

function useInvalidate() {
  const qc = useQueryClient();
  return () => { for (const k of ["lines", "nodes", "servers", "shares", "externals"]) qc.invalidateQueries({ queryKey: [k] }); };
}

export function NodesPage() {
  const lines = useQuery({ queryKey: ["lines"], queryFn: () => get<Line[]>("/api/v1/lines") });
  const nodes = useQuery({ queryKey: ["nodes", "all"], queryFn: () => get<Node[]>("/api/v1/nodes?members=1"), refetchInterval: 60000 });
  const servers = useQuery({ queryKey: ["servers"], queryFn: () => get<Server[]>("/api/v1/servers") });
  const externals = useQuery({ queryKey: ["externals"], queryFn: () => get<ExternalSubscription[]>("/api/v1/externals") });
  const [lineDialog, setLineDialog] = React.useState<{ line?: Line } | null>(null);
  const [inboundDialog, setInboundDialog] = React.useState<{ node?: Node } | null>(null);
  const [importOpen, setImportOpen] = React.useState(false);
  const all = nodes.data ?? [];
  const inbounds = all.filter(isInbound);
  return (
    <div>
      <PageHeader title="节点" description="线路是给用户用的；入站是服务器上真正监听的服务；外部节点是别处的节点。"
        actions={<>
          <Button variant="outline" onClick={() => setImportOpen(true)}><Upload className="h-4 w-4" /> 导入外部节点</Button>
          <Button variant="outline" onClick={() => setInboundDialog({})}><Plus className="h-4 w-4" /> 新建入站</Button>
          <Button onClick={() => setLineDialog({})} disabled={!inbounds.length}><Plus className="h-4 w-4" /> 新建线路</Button>
        </>} />
      {lines.isLoading || nodes.isLoading ? <Spinner /> : (
        <>
          <LinesSection lines={lines.data ?? []} hasInbound={inbounds.length > 0} onEdit={(line) => setLineDialog({ line })} onCreate={() => setLineDialog({})} onCreateInbound={() => setInboundDialog({})} />
          <InboundsSection inbounds={inbounds} members={all.filter((n) => n.attach_node_id)} lines={lines.data ?? []} onEdit={(node) => setInboundDialog({ node })} />
          <ExternalSection externals={externals.data ?? []} nodes={all.filter(isExternal)} onImport={() => setImportOpen(true)} />
        </>
      )}
      <LineDialog state={lineDialog} onClose={() => setLineDialog(null)} />
      <InboundDialog state={inboundDialog} servers={servers.data ?? []} onClose={() => setInboundDialog(null)} />
      <ImportDialog open={importOpen} onClose={() => setImportOpen(false)} />
    </div>
  );
}

// ---------- lines ----------
function LinePath({ l }: { l: Line }) {
  return (
    <span className="inline-flex flex-wrap items-center gap-1.5 text-sm">
      <span>{l.entry_server || "—"}</span>
      {l.landing_node_id ? <><ArrowRight className="h-3.5 w-3.5 text-muted-foreground" /><span>{l.landing_server || "—"}</span></> : null}
    </span>
  );
}

// Both machines of a relay carry the traffic, and both are shown.
function LineTraffic({ l }: { l: Line }) {
  if (!l.entry_bytes && !l.landing_bytes) return <span className="text-muted-foreground">—</span>;
  if (!l.landing_node_id) return <>{fmtBytes(l.entry_bytes)}</>;
  return (
    <span className="inline-flex flex-col text-xs leading-5">
      <span><span className="text-muted-foreground">{l.entry_server}</span> {fmtBytes(l.entry_bytes)}</span>
      <span><span className="text-muted-foreground">{l.landing_server}</span> {fmtBytes(l.landing_bytes)}</span>
    </span>
  );
}

function LinesSection({ lines, hasInbound, onEdit, onCreate, onCreateInbound }: { lines: Line[]; hasInbound: boolean; onEdit: (l: Line) => void; onCreate: () => void; onCreateInbound: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const [confirm, setConfirm] = React.useState<Line | null>(null);
  const reorder = useMutation({ mutationFn: (ids: number[]) => post("/api/v1/lines/reorder", { ids }), onSuccess: invalidate, onError: (e) => toast.fromError(e) });
  const toggle = useMutation({
    mutationFn: (l: Line) => put(`/api/v1/lines/${l.id}`, { name: l.name, entry_node_id: l.entry_node_id, landing_node_id: l.landing_node_id ?? null, sort_order: l.sort_order, enabled: !l.enabled }),
    onSuccess: invalidate, onError: (e) => toast.fromError(e),
  });
  const remove = useMutation({ mutationFn: (l: Line) => del(`/api/v1/lines/${l.id}`), onSuccess: () => { toast.success("线路已删除"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  const move = (i: number, d: -1 | 1) => {
    const ids = lines.map((l) => l.id);
    [ids[i], ids[i + d]] = [ids[i + d], ids[i]];
    reorder.mutate(ids);
  };
  return (
    <section>
      <SectionTitle>线路</SectionTitle>
      {!lines.length ? (
        <Empty title="还没有线路" description={hasInbound ? "线路是用户在客户端里看到的一个节点：直连某个入站，或者经入口机中转到落地机。" : "先在一台服务器上新建入站，再用它建线路。"}
          action={hasInbound ? <Button onClick={onCreate}>新建线路</Button> : <Button onClick={onCreateInbound}>新建入站</Button>} />
      ) : (
        <Card className="overflow-hidden p-0">
          <Table>
            <thead><tr className="border-b"><Th className="w-20">顺序</Th><Th>名称</Th><Th>类型</Th><Th>路径</Th><Th>近 30 天流量</Th><Th>用户</Th><Th>状态</Th><Th></Th></tr></thead>
            <tbody>
              {lines.map((l, i) => (
                <Tr key={l.id}>
                  <Td>
                    <div className="flex gap-0.5">
                      <Button size="sm" variant="ghost" aria-label="上移" disabled={i === 0 || reorder.isPending} onClick={() => move(i, -1)}><ArrowUp className="h-3.5 w-3.5" /></Button>
                      <Button size="sm" variant="ghost" aria-label="下移" disabled={i === lines.length - 1 || reorder.isPending} onClick={() => move(i, 1)}><ArrowDown className="h-3.5 w-3.5" /></Button>
                    </div>
                  </Td>
                  <Td className="font-medium">{l.name}</Td>
                  <Td><Badge variant={l.landing_node_id ? "info" : "secondary"}>{l.landing_node_id ? "中转" : "直连"}</Badge></Td>
                  <Td><LinePath l={l} />{l.problem && <p className="text-xs text-destructive">{l.problem}</p>}</Td>
                  <Td className="tabular-nums"><LineTraffic l={l} /></Td>
                  <Td className="tabular-nums">{l.users}</Td>
                  <Td><Switch size="sm" checked={l.enabled} onChange={() => toggle.mutate(l)} aria-label={`启用 ${l.name}`} /></Td>
                  <Td>
                    <div className="flex justify-end gap-1">
                      <Button size="sm" variant="ghost" aria-label="编辑" onClick={() => onEdit(l)}><Pencil className="h-4 w-4" /></Button>
                      <Button size="sm" variant="ghost" aria-label="删除" className="text-red-500" onClick={() => setConfirm(l)}><Trash2 className="h-4 w-4" /></Button>
                    </div>
                  </Td>
                </Tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      <p className="mt-2 text-xs text-muted-foreground">这里的顺序就是用户客户端里的顺序。中转线路由入口机转发到落地机，两台机器都按用户计量，流量各记各的。</p>
      <Confirm open={!!confirm} onClose={() => setConfirm(null)} onConfirm={() => confirm && remove.mutate(confirm)} loading={remove.isPending} destructive title={`删除线路「${confirm?.name ?? ""}」？`} description={`${confirm?.users ?? 0} 个用户会在下次更新配置后失去这条线路；入站本身不受影响。`} />
    </section>
  );
}

function LineDialog({ state, onClose }: { state: { line?: Line } | null; onClose: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const line = state?.line;
  const candidates = useQuery({ queryKey: ["lines", "candidates"], queryFn: () => get<LineCandidate[]>("/api/v1/lines/candidates"), enabled: !!state });
  const [f, setF] = React.useState({ name: "", relay: false, entry: 0, landing: 0 });
  const [named, setNamed] = React.useState(false);
  React.useEffect(() => {
    if (!state) return;
    setF(line ? { name: line.name, relay: !!line.landing_node_id, entry: line.entry_node_id, landing: line.landing_node_id ?? 0 } : { name: "", relay: false, entry: 0, landing: 0 });
    setNamed(!!line);
  }, [state, line]);
  const list = candidates.data ?? [];
  const entry = list.find((c) => c.id === f.entry);
  const landing = list.find((c) => c.id === f.landing);
  const suggested = f.relay ? (entry && landing ? `${landing.server_name}（${entry.server_name}中转）` : "") : entry?.name ?? "";
  const name = named ? f.name : suggested;
  const save = useMutation({
    mutationFn: () => {
      const body = { name, entry_node_id: f.entry, landing_node_id: f.relay ? f.landing : null, sort_order: line?.sort_order ?? 1000, enabled: line?.enabled ?? true };
      return line ? put(`/api/v1/lines/${line.id}`, body) : post("/api/v1/lines", body);
    },
    onSuccess: () => { toast.success(line ? "线路已保存" : "线路已创建，用户下次更新配置时生效"); invalidate(); onClose(); },
    onError: (e) => toast.fromError(e),
  });
  const label = (c: LineCandidate) => `${c.server_name} · ${c.name} · ${PROTOCOL_LABELS[c.protocol] ?? c.protocol} :${c.listen_port}`;
  return (
    <Dialog open={!!state} onClose={onClose} title={line ? "编辑线路" : "新建线路"} size="sm"
      footer={<><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={!name.trim() || !f.entry || (f.relay && (!f.landing || f.landing === f.entry))}>{line ? "保存" : "创建"}</Button></>}>
      <div className="grid gap-4">
        <Field label="类型">
          <Select value={f.relay ? "relay" : "direct"} onChange={(e) => setF((p) => ({ ...p, relay: e.target.value === "relay" }))}>
            <option value="direct">直连：用户连到这台服务器，从它出去</option>
            <option value="relay">中转：用户连到入口机，从落地机出去</option>
          </Select>
        </Field>
        <Field label={f.relay ? "入口（用户连接的入站）" : "入站"}>
          <Select value={String(f.entry)} onChange={(e) => setF((p) => ({ ...p, entry: Number(e.target.value) }))}>
            <option value="0">请选择</option>
            {list.map((c) => <option key={c.id} value={c.id}>{label(c)}</option>)}
          </Select>
        </Field>
        {f.relay && (
          <Field label="落地（流量出去的入站）" hint="落地机的地址和凭据不会出现在用户的配置里，只有入口机能连它。落地入站要用 VLESS Reality 或 Shadowsocks 2022。">
            <Select value={String(f.landing)} onChange={(e) => setF((p) => ({ ...p, landing: Number(e.target.value) }))}>
              <option value="0">请选择</option>
              {list.filter((c) => c.id !== f.entry && c.landing).map((c) => <option key={c.id} value={c.id}>{label(c)}</option>)}
            </Select>
          </Field>
        )}
        <Field label="名称" hint="用户在客户端里看到的名字">
          <Input value={name} onChange={(e) => { setNamed(true); setF((p) => ({ ...p, name: e.target.value })); }} placeholder="🇺🇸 洛杉矶" />
        </Field>
        {line && (line.entry_node_id !== f.entry || (line.landing_node_id ?? 0) !== (f.relay ? f.landing : 0)) && (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/5 p-2 text-xs">换了入站，这条线路上每个用户的凭据都会重新生成，他们需要更新一次配置。</p>
        )}
      </div>
    </Dialog>
  );
}

// ---------- inbounds ----------
function InboundsSection({ inbounds, members, lines, onEdit }: { inbounds: Node[]; members: Node[]; lines: Line[]; onEdit: (n: Node) => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const [confirm, setConfirm] = React.useState<{ node: Node; action: "delete" | "reset" } | null>(null);
  const remove = useMutation({ mutationFn: (n: Node) => del(`/api/v1/nodes/${n.id}`), onSuccess: () => { toast.success("入站已删除"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  const reset = useMutation({ mutationFn: (n: Node) => post(`/api/v1/nodes/${n.id}/regenerate`), onSuccess: () => { toast.success("密钥已更换，用户更新配置后恢复"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  const toggle = useMutation({ mutationFn: (n: Node) => put(`/api/v1/nodes/${n.id}`, { enabled: !n.enabled }), onSuccess: invalidate, onError: (e) => toast.fromError(e) });
  if (!inbounds.length) return null;
  const used = (n: Node) => lines.filter((l) => l.entry_node_id === n.id || l.landing_node_id === n.id).length;
  const target = confirm?.node;
  return (
    <section className="mt-8">
      <SectionTitle>入站</SectionTitle>
      <Card className="overflow-hidden p-0">
        <Table>
          <thead><tr className="border-b"><Th>服务器</Th><Th>名称</Th><Th>协议</Th><Th>端口</Th><Th>伪装域名</Th><Th>线路 / 凭据</Th><Th>近 30 天</Th><Th>启用</Th><Th></Th></tr></thead>
          <tbody>
            {inbounds.map((n) => (
              <Tr key={n.id}>
                <Td>{n.server_name}</Td>
                <Td className="font-medium">{n.name}</Td>
                <Td><Badge variant="outline">{PROTOCOL_LABELS[n.protocol] ?? n.protocol}</Badge></Td>
                <Td className="tabular-nums">{n.listen_port}</Td>
                <Td className="text-muted-foreground">{sni(n) || "—"}</Td>
                <Td className="tabular-nums">{used(n)} / {members.filter((m) => m.attach_node_id === n.id && !m.revoked).length}</Td>
                <Td className="tabular-nums">{n.traffic?.has_data ? fmtBytes(n.traffic.total) : "—"}</Td>
                <Td><Switch size="sm" checked={n.enabled} onChange={() => toggle.mutate(n)} aria-label={`启用 ${n.name}`} /></Td>
                <Td>
                  <div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" aria-label="编辑" onClick={() => onEdit(n)}><Pencil className="h-4 w-4" /></Button>
                    <Button size="sm" variant="ghost" aria-label="更换密钥" onClick={() => setConfirm({ node: n, action: "reset" })}><RotateCcw className="h-4 w-4" /></Button>
                    <Button size="sm" variant="ghost" aria-label="删除" className="text-red-500" onClick={() => setConfirm({ node: n, action: "delete" })}><Trash2 className="h-4 w-4" /></Button>
                  </div>
                </Td>
              </Tr>
            ))}
          </tbody>
        </Table>
      </Card>
      <p className="mt-2 text-xs text-muted-foreground">一个入站是服务器上的一个端口，所有用户共用它，但每人每条线路各有自己的凭据，流量分开计。</p>
      <Confirm open={confirm?.action === "delete"} onClose={() => setConfirm(null)} onConfirm={() => target && remove.mutate(target)} loading={remove.isPending} destructive
        title={`删除入站「${target?.name ?? ""}」？`} description={`服务器会关闭这个端口，用到它的 ${target ? used(target) : 0} 条线路一并删除。`} />
      <Confirm open={confirm?.action === "reset"} onClose={() => setConfirm(null)} onConfirm={() => target && reset.mutate(target)} loading={reset.isPending}
        title={`更换「${target?.name ?? ""}」的密钥？`} description="重新生成握手密钥。每个用户自己的凭据不变，但所有人都要更新一次配置才能继续使用。" />
    </section>
  );
}

// Protocols one port can serve to several users. Snell, mieru and WireGuard
// have a single identity per port and cannot.
const INBOUND_PROTOCOLS = ["vless", "hysteria2", "tuic", "trojan", "anytls", "ss"] as const;
const TLS_PROTOCOLS = ["hysteria2", "tuic", "trojan", "anytls"];
const PROTOCOL_HINTS: Record<string, string> = {
  vless: "TCP。借用一个真实网站的握手，不需要证书。最适合放在入口，也能做落地。",
  hysteria2: "UDP（QUIC）。丢包多的线路上更快，需要证书。",
  tuic: "UDP（QUIC）。需要证书。",
  trojan: "TCP + TLS。需要证书。",
  anytls: "TCP + TLS。需要证书。",
  ss: "TCP + UDP。只靠密钥、没有握手，最轻；适合做落地，直接从国内连容易被识别。",
};

function InboundDialog({ state, servers, onClose }: { state: { node?: Node } | null; servers: Server[]; onClose: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const node = state?.node;
  const blank = { server: 0, protocol: "vless", name: "", port: "443", sni: "", domain: "", cert_mode: "self_signed", cert_id: "", obfs: false };
  const [f, setF] = React.useState(blank);
  React.useEffect(() => {
    if (!state) return;
    setF(node ? { ...blank, server: node.server_id ?? 0, protocol: node.protocol, name: node.name, port: String(node.listen_port), sni: sni(node) } : { ...blank, server: servers[0]?.id ?? 0 });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state, node, servers]);
  const set = <K extends keyof typeof blank>(k: K, v: (typeof blank)[K]) => setF((p) => ({ ...p, [k]: v }));
  const tls = TLS_PROTOCOLS.includes(f.protocol);
  const save = useMutation({
    mutationFn: () => node
      ? put(`/api/v1/nodes/${node.id}`, { name: f.name, sni: f.protocol === "vless" ? f.sni.trim() : "", listen_port: Number(f.port) || 0 })
      : post(`/api/v1/servers/${f.server}/nodes`, {
          protocol: f.protocol, name: f.name, port: Number(f.port) || 0, sni: f.protocol === "vless" ? f.sni.trim() : "",
          domain: tls ? f.domain.trim() : "", cert_mode: tls ? f.cert_mode : "", cert_id: tls && f.cert_mode === "external" ? f.cert_id.trim() : "", obfs: f.protocol === "hysteria2" && f.obfs,
        }),
    onSuccess: () => { toast.success(node ? "已保存，服务器同步后生效" : "入站已创建，服务器同步后生效"); invalidate(); onClose(); },
    onError: (e) => toast.fromError(e),
  });
  const moved = node && (Number(f.port) !== node.listen_port || (f.protocol === "vless" && f.sni.trim().toLowerCase() !== sni(node)));
  return (
    <Dialog open={!!state} onClose={onClose} title={node ? "编辑入站" : "新建入站"} size="sm" description="入站是服务器上的一个端口。密钥自动生成，每个用户在上面各有自己的凭据。"
      footer={<><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={!node && !f.server}>{node ? "保存" : "创建"}</Button></>}>
      <div className="grid gap-4">
        {!node && (
          <div className="grid gap-3 sm:grid-cols-2">
            <Field label="服务器">
              <Select value={String(f.server)} onChange={(e) => set("server", Number(e.target.value))}>
                {servers.map((s) => <option key={s.id} value={s.id}>{s.name}</option>)}
              </Select>
            </Field>
            <Field label="协议">
              <Select value={f.protocol} onChange={(e) => set("protocol", e.target.value)}>
                {INBOUND_PROTOCOLS.map((p) => <option key={p} value={p}>{PROTOCOL_LABELS[p] ?? p}</option>)}
              </Select>
            </Field>
          </div>
        )}
        {!node && <p className="-mt-2 text-xs text-muted-foreground">{PROTOCOL_HINTS[f.protocol]}</p>}
        <div className="grid grid-cols-2 gap-3">
          <Field label="名称" hint="只在面板里显示"><Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="留空自动生成" /></Field>
          <Field label="端口" hint="一台服务器上不能重复"><Input type="number" min={1} max={65535} value={f.port} onChange={(e) => set("port", e.target.value)} /></Field>
        </div>
        {f.protocol === "vless" && (
          <Field label="伪装域名" hint="选一个从这台服务器连过去又快又稳的真实网站：每条新连接都会先和它握一次手。">
            <Input value={f.sni} onChange={(e) => set("sni", e.target.value)} placeholder="www.sony.com" />
          </Field>
        )}
        {!node && tls && (
          <>
            <Field label="证书">
              <Select value={f.cert_mode} onChange={(e) => set("cert_mode", e.target.value)}>
                <option value="self_signed">自签名（客户端不校验证书，能用但不防中间人）</option>
                <option value="acme">自动签发（要有域名指向这台服务器，且 80 端口可达）</option>
                <option value="external">服务器上已有的证书</option>
              </Select>
            </Field>
            {f.cert_mode !== "self_signed" && <Field label="域名" hint="证书上的域名，客户端用它连接"><Input value={f.domain} onChange={(e) => set("domain", e.target.value)} placeholder="node.example.com" /></Field>}
            {f.cert_mode === "external" && <Field label="证书名称" hint="这台服务器本机安全策略里登记的证书名称"><Input value={f.cert_id} onChange={(e) => set("cert_id", e.target.value)} maxLength={64} /></Field>}
          </>
        )}
        {!node && f.protocol === "hysteria2" && <Switch checked={f.obfs} onChange={(v) => set("obfs", v)} label="加一层混淆（Salamander），应对 QUIC 被针对的情况" />}
        {moved && <p className="rounded-md border border-amber-500/40 bg-amber-500/5 p-2 text-xs">端口或伪装域名变了：密钥和每个人的凭据都不变，但所有用户要更新一次配置才能继续使用。</p>}
      </div>
    </Dialog>
  );
}

// ---------- external nodes ----------
function ExternalSection({ externals, nodes, onImport }: { externals: ExternalSubscription[]; nodes: Node[]; onImport: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const [confirm, setConfirm] = React.useState<{ ext?: ExternalSubscription; node?: Node } | null>(null);
  const [rename, setRename] = React.useState<Node | null>(null);
  const sync = useMutation({ mutationFn: (e: ExternalSubscription) => post(`/api/v1/externals/${e.id}/sync`), onSuccess: () => { toast.success("已同步"); invalidate(); }, onError: (e) => toast.fromError(e) });
  const removeExt = useMutation({ mutationFn: (e: ExternalSubscription) => del(`/api/v1/externals/${e.id}`), onSuccess: () => { toast.success("订阅源已删除"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  const removeNode = useMutation({ mutationFn: (n: Node) => del(`/api/v1/nodes/${n.id}`), onSuccess: () => { toast.success("节点已删除"); setConfirm(null); invalidate(); }, onError: (e) => toast.fromError(e) });
  if (!externals.length && !nodes.length) return null;
  return (
    <section className="mt-8">
      <SectionTitle action={<Button size="sm" variant="outline" onClick={onImport}><Upload className="h-4 w-4" /> 导入</Button>}>外部节点</SectionTitle>
      {externals.length > 0 && (
        <div className="mb-3 grid gap-3 md:grid-cols-2">
          {externals.map((e) => (
            <Card key={e.id} className="p-4">
              <div className="flex items-start justify-between gap-2">
                <div className="min-w-0">
                  <p className="break-words font-medium">{e.name}</p>
                  <p className="text-xs text-muted-foreground">{e.node_count} 个节点 · {e.last_sync_at ? `${fmtAgo(e.last_sync_at)}同步` : "尚未同步"}{e.userinfo?.total ? ` · 已用 ${fmtBytes(e.userinfo.upload + e.userinfo.download)} / ${fmtBytes(e.userinfo.total)}` : ""}</p>
                  {e.last_error && <p className="mt-1 break-words text-xs text-destructive">{e.last_error}</p>}
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button size="sm" variant="ghost" aria-label="同步" onClick={() => sync.mutate(e)} loading={sync.isPending && sync.variables?.id === e.id}><RefreshCw className="h-4 w-4" /></Button>
                  <Button size="sm" variant="ghost" aria-label="删除" className="text-red-500" onClick={() => setConfirm({ ext: e })}><Trash2 className="h-4 w-4" /></Button>
                </div>
              </div>
            </Card>
          ))}
        </div>
      )}
      {nodes.length > 0 && (
        <Card className="overflow-hidden p-0">
          <Table>
            <thead><tr className="border-b"><Th>名称</Th><Th>协议</Th><Th>地址</Th><Th>来源</Th><Th></Th></tr></thead>
            <tbody>
              {nodes.map((n) => (
                <Tr key={n.id}>
                  <Td className="font-medium">{n.name}{n.upstream_name && <span className="block text-xs font-normal text-muted-foreground">订阅源里叫 {n.upstream_name}</span>}</Td>
                  <Td><Badge variant="outline">{PROTOCOL_LABELS[n.protocol] ?? n.protocol}</Badge></Td>
                  <Td className="text-muted-foreground">{n.server}:{n.port}</Td>
                  <Td className="text-muted-foreground">{n.external_name || "手动添加"}</Td>
                  <Td><div className="flex justify-end gap-1">
                    <Button size="sm" variant="ghost" aria-label="改名" onClick={() => setRename(n)}><Pencil className="h-4 w-4" /></Button>
                    {!n.external_sub_id && <Button size="sm" variant="ghost" aria-label="删除" className="text-red-500" onClick={() => setConfirm({ node: n })}><Trash2 className="h-4 w-4" /></Button>}
                  </div></Td>
                </Tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
      <p className="mt-2 text-xs text-muted-foreground">外部节点不经过自己的服务器，没法按用户计量和限额；在用户里可以把它们作为附加节点一起发下去。</p>
      <RenameDialog node={rename} onClose={() => setRename(null)} />
      <Confirm open={!!confirm?.ext} onClose={() => setConfirm(null)} onConfirm={() => confirm?.ext && removeExt.mutate(confirm.ext)} loading={removeExt.isPending} destructive title={`删除订阅源「${confirm?.ext?.name ?? ""}」？`} description="它带来的节点会一起删除。" />
      <Confirm open={!!confirm?.node} onClose={() => setConfirm(null)} onConfirm={() => confirm?.node && removeNode.mutate(confirm.node)} loading={removeNode.isPending} destructive title={`删除节点「${confirm?.node?.name ?? ""}」？`} description="用到它的用户会在下次更新配置后失去这个节点。" />
    </section>
  );
}

// The name is the one users see in their client. A subscription's node keeps
// it across syncs; everything else about that node stays the feed's.
function RenameDialog({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const [name, setName] = React.useState("");
  React.useEffect(() => { if (node) setName(node.name); }, [node]);
  const save = useMutation({
    mutationFn: () => put(`/api/v1/nodes/${node!.id}`, { name: name.trim() }),
    onSuccess: () => { toast.success("已改名，用户下次更新配置后生效"); invalidate(); onClose(); },
    onError: (e) => toast.fromError(e),
  });
  const feedName = node?.upstream_name || (node?.external_sub_id ? node.name : "");
  return (
    <Dialog open={!!node} onClose={onClose} title="给外部节点改名" size="sm"
      footer={<><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={!name.trim() || name.trim() === node?.name}>保存</Button></>}>
      <Field label="名称" hint={feedName ? `订阅源里叫「${feedName}」。以后同步，这个节点仍然用你起的名字。` : "用户在客户端里看到的就是这个名字。"}>
        <Input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} autoFocus />
      </Field>
    </Dialog>
  );
}

function ImportDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const toast = useToast();
  const invalidate = useInvalidate();
  const [mode, setMode] = React.useState<"sub" | "links">("sub");
  const [f, setF] = React.useState({ name: "", url: "", text: "" });
  React.useEffect(() => { if (open) setF({ name: "", url: "", text: "" }); }, [open]);
  const save = useMutation({
    mutationFn: async () => {
      if (mode === "links") return post<{ created: unknown[] }>("/api/v1/nodes/import", { text: f.text });
      return post<{ sync_error?: string }>("/api/v1/externals", { name: f.name, url: f.url, user_agent: "", sync_interval_min: 360, enabled: true });
    },
    onSuccess: (r) => {
      const err = (r as { sync_error?: string }).sync_error;
      if (err) toast.error("已添加，但首次同步失败", err);
      else toast.success(mode === "links" ? `已导入 ${(r as { created?: unknown[] }).created?.length ?? 0} 个节点` : "已添加并同步");
      invalidate(); onClose();
    },
    onError: (e) => toast.fromError(e),
  });
  return (
    <Dialog open={open} onClose={onClose} title="导入外部节点" size="sm"
      footer={<><Button variant="outline" onClick={onClose}>取消</Button><Button onClick={() => save.mutate()} loading={save.isPending} disabled={mode === "sub" ? !f.name.trim() || !f.url.trim() : !f.text.trim()}>导入</Button></>}>
      <div className="grid gap-4">
        <Field label="方式">
          <Select value={mode} onChange={(e) => setMode(e.target.value as "sub" | "links")}>
            <option value="sub">订阅地址：定时同步别人给的订阅</option>
            <option value="links">节点链接：粘贴 vless:// ss:// trojan:// 等</option>
          </Select>
        </Field>
        {mode === "sub" ? (
          <>
            <Field label="名称"><Input value={f.name} onChange={(e) => setF((p) => ({ ...p, name: e.target.value }))} placeholder="朋友的线路" /></Field>
            <Field label="订阅地址" hint="每 6 小时同步一次"><Input value={f.url} onChange={(e) => setF((p) => ({ ...p, url: e.target.value }))} placeholder="https://…" /></Field>
          </>
        ) : (
          <Field label="节点链接" hint="一行一个"><Textarea rows={6} value={f.text} onChange={(e) => setF((p) => ({ ...p, text: e.target.value }))} placeholder="vless://…" /></Field>
        )}
      </div>
    </Dialog>
  );
}
