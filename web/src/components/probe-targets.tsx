// Which addresses every server times connections to, and how often.
import * as React from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Plus, Trash2 } from "lucide-react";
import { get, put } from "@/lib/api";
import { loadLive, type Carrier } from "@/lib/live";
import { CARRIER_LABELS } from "@/lib/utils";
import { Button, Dialog, Field, Input, Select, Switch } from "@/components/ui";
import { useToast } from "@/components/toast";

interface Target {
  name: string;
  host: string;
  port: number;
  carrier: Carrier;
  on_card: boolean;
}
interface Settings {
  interval: number;
  targets: Target[];
}

// Province-level TCP ping nodes of the three carriers, published for probes:
// {province}-{carrier}-v4.ip.zstaticcdn.com:80.
const PROVINCES: [string, string][] = [
  ["bj", "北京"], ["sh", "上海"], ["gd", "广东"], ["sc", "四川"], ["js", "江苏"], ["zj", "浙江"], ["tj", "天津"], ["cq", "重庆"],
  ["he", "河北"], ["sx", "山西"], ["ln", "辽宁"], ["jl", "吉林"], ["hl", "黑龙江"], ["ah", "安徽"], ["fj", "福建"], ["jx", "江西"],
  ["sd", "山东"], ["ha", "河南"], ["hb", "湖北"], ["hn", "湖南"], ["hi", "海南"], ["gz", "贵州"], ["yn", "云南"], ["sn", "陕西"],
  ["gs", "甘肃"], ["qh", "青海"], ["nm", "内蒙古"], ["gx", "广西"], ["xz", "西藏"], ["nx", "宁夏"], ["xj", "新疆"],
];
const CARRIERS: Carrier[] = ["ct", "cu", "cm"];
const MAX = 24;

export function ProbeTargetsDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const toast = useToast();
  const q = useQuery({ queryKey: ["probe-targets"], queryFn: () => get<Settings>("/api/v1/probe-targets"), enabled: open });
  const [f, setF] = React.useState<Settings>({ interval: 10, targets: [] });
  const [province, setProvince] = React.useState("sc");
  const [custom, setCustom] = React.useState({ name: "", host: "", port: "80" });
  React.useEffect(() => {
    if (open && q.data) setF({ interval: q.data.interval, targets: q.data.targets.map(({ name, host, port, carrier, on_card }) => ({ name, host, port, carrier, on_card })) });
  }, [open, q.data]);
  const save = useMutation({
    mutationFn: () => put<Settings>("/api/v1/probe-targets", f),
    onSuccess: () => {
      void q.refetch();
      void loadLive();
      toast.success("已保存，各台机器几秒内开始按新的目标探测");
      onClose();
    },
    onError: (e) => toast.fromError(e),
  });
  const has = (host: string, port: number) => f.targets.some((t) => t.host === host.toLowerCase() && t.port === port);
  const add = (list: Target[]) => setF((p) => ({ ...p, targets: [...p.targets, ...list.filter((t) => !p.targets.some((x) => x.host === t.host && x.port === t.port))].slice(0, MAX) }));
  const change = (i: number, patch: Partial<Target>) => setF((p) => ({ ...p, targets: p.targets.map((t, j) => (j === i ? { ...t, ...patch } : t)) }));
  const provinceName = PROVINCES.find(([code]) => code === province)?.[1] ?? "";
  const provinceTargets = CARRIERS.map((c) => ({ name: provinceName + CARRIER_LABELS[c], host: `${province}-${c}-v4.ip.zstaticcdn.com`, port: 80, carrier: c, on_card: true }));
  const customPort = Number(custom.port);
  const customOK = custom.name.trim() !== "" && custom.host.trim() !== "" && Number.isInteger(customPort) && customPort >= 1 && customPort <= 65535 && !has(custom.host.trim(), customPort);
  return (
    <Dialog
      open={open}
      onClose={onClose}
      size="md"
      title="延迟监测"
      description="每台服务器都会定时对下面的目标做 TCP 握手，记下耗时和有没有超时（950 毫秒内没接通算丢包）。只建立连接，不发任何数据。"
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button onClick={() => save.mutate()} loading={save.isPending}>保存</Button>
        </>
      }
    >
      <div className="space-y-5">
        <div>
          <p className="mb-2 text-sm font-medium">探测目标（{f.targets.length} / {MAX}）</p>
          {!f.targets.length ? <p className="rounded-xl border border-dashed p-4 text-center text-sm text-muted-foreground">还没有目标。在下面按省份加三网，或者自己填一个地址。</p> : (
            <ul className="divide-y divide-border/60">
              {f.targets.map((t, i) => (
                <li key={`${t.host}:${t.port}`} className="flex flex-wrap items-center gap-x-3 gap-y-1.5 py-2">
                  <Input aria-label="名称" className="h-9 w-28" value={t.name} onChange={(e) => change(i, { name: e.target.value })} />
                  <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={`${t.host}:${t.port}`}>{t.host}:{t.port}</span>
                  <Switch size="sm" checked={t.on_card} onChange={(v) => change(i, { on_card: v })} label="卡片上显示" />
                  <Button size="sm" variant="ghost" aria-label={`删除 ${t.name}`} onClick={() => setF((p) => ({ ...p, targets: p.targets.filter((_, j) => j !== i) }))}><Trash2 className="h-3.5 w-3.5" /></Button>
                </li>
              ))}
            </ul>
          )}
          <p className="mt-1 text-xs text-muted-foreground">删掉一个目标，它已经记下的延迟曲线也一起删掉。关掉“卡片上显示”的目标只出现在每台服务器自己的页面里。</p>
        </div>

        <div className="grid gap-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-end">
          <Field label="按省份加三网" hint="电信、联通、移动各一个，用的是 zstaticcdn 公开的省级 TCP Ping 节点。">
            <Select value={province} onChange={(e) => setProvince(e.target.value)}>
              {PROVINCES.map(([code, name]) => <option key={code} value={code}>{name}</option>)}
            </Select>
          </Field>
          <Button variant="outline" className="sm:mb-6" disabled={provinceTargets.every((t) => has(t.host, t.port)) || f.targets.length >= MAX} onClick={() => add(provinceTargets)}><Plus className="h-4 w-4" /> 添加{provinceName}三网</Button>
        </div>

        <div className="grid gap-3 sm:grid-cols-[7rem_minmax(0,1fr)_5.5rem_auto] sm:items-end">
          <Field label="自定义：名称"><Input value={custom.name} onChange={(e) => setCustom({ ...custom, name: e.target.value })} placeholder="家里宽带" /></Field>
          <Field label="地址"><Input value={custom.host} onChange={(e) => setCustom({ ...custom, host: e.target.value })} placeholder="公网域名或 IP" /></Field>
          <Field label="端口"><Input type="number" min={1} max={65535} value={custom.port} onChange={(e) => setCustom({ ...custom, port: e.target.value })} /></Field>
          <Button variant="outline" disabled={!customOK || f.targets.length >= MAX} onClick={() => { add([{ name: custom.name.trim(), host: custom.host.trim().toLowerCase(), port: customPort, carrier: "", on_card: true }]); setCustom({ name: "", host: "", port: "80" }); }}><Plus className="h-4 w-4" /> 添加</Button>
        </div>

        <Field label="每个目标多久探测一次" hint="间隔越短，丢包率越细、卡片上的数字跳得越勤，占的流量也越多：三个目标、10 秒一次，每台机器每月约 0.3 GB。">
          <Select value={String(f.interval)} onChange={(e) => setF({ ...f, interval: Number(e.target.value) })}>
            {[5, 10, 30, 60].map((n) => <option key={n} value={n}>{n} 秒</option>)}
          </Select>
        </Field>
      </div>
    </Dialog>
  );
}
