// A server's own page as a probe: the numbers that move every second, and
// what was measured over the last hour, day or month.
import * as React from "react";
import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { ArrowDown, ArrowUp } from "lucide-react";
import { get } from "@/lib/api";
import type { Server } from "@/lib/types";
import { useLive, useProbeTargets, type ProbeTarget } from "@/lib/live";
import { cn, fmtBytes, fmtLoss, fmtMs, fmtRate } from "@/lib/utils";
import { Card, CardContent, CardHeader, CardTitle, Table, Tabs, Td, Th, Tr } from "@/components/ui";
import { ProbeRows, Spark, ratio, targetColors, useVitals } from "@/components/probe";
import { TimeChart, byteAxis, capacityAxis, type ChartSeries, type TimePoint } from "@/components/charts";

function Tile({ label, value, sub, trail, className }: { label: React.ReactNode; value: string; sub?: React.ReactNode; trail?: number[]; className?: string }) {
  return (
    <Card className={cn("flex flex-col p-4", className)}>
      <p className="flex items-center gap-1 text-xs text-muted-foreground">{label}</p>
      <p className="mt-1 truncate text-2xl font-semibold tabular-nums leading-tight">{value}</p>
      {sub && <p className="mt-0.5 truncate text-xs tabular-nums text-muted-foreground">{sub}</p>}
      <div className="mt-auto h-8 pt-2 text-primary/80">{trail && trail.length > 1 && <Spark values={trail} />}</div>
    </Card>
  );
}

/** What the server is doing this second. */
export function LiveTiles({ s }: { s: Server }) {
  const v = useVitals(s);
  const live = useLive(s.id);
  const trail = v?.live ? live?.trail : undefined;
  const pct = (n: number | null) => (n == null ? "-" : `${n.toFixed(0)}%`);
  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
      <Tile label={<><ArrowUp className="h-3 w-3" />上行</>} value={v ? fmtRate(v.tx) : "-"} trail={trail?.map((t) => t.tx)} />
      <Tile label={<><ArrowDown className="h-3 w-3" />下行</>} value={v ? fmtRate(v.rx) : "-"} trail={trail?.map((t) => t.rx)} />
      <Tile label="CPU" value={v ? pct(v.cpu) : "-"} sub={v && `负载 ${v.load.map((l) => l.toFixed(2)).join(" ")}`} trail={trail?.map((t) => t.c)} />
      <Tile label="内存" value={v ? pct(ratio(v.memUsed, v.memTotal)) : "-"} sub={v && `${fmtBytes(v.memUsed)} / ${fmtBytes(v.memTotal)}${v.swapTotal > 0 ? ` · 交换 ${fmtBytes(v.swapUsed)}` : ""}`} />
      <Tile label="硬盘" value={v ? pct(ratio(v.diskUsed, v.diskTotal)) : "-"} sub={v && `${fmtBytes(v.diskUsed)} / ${fmtBytes(v.diskTotal)}`} />
      <Tile label="连接" value={v ? String(v.tcp) : "-"} sub={v && `TCP · 另有 ${v.udp} 个 UDP`} />
    </div>
  );
}

/** Every probe target as this server sees it now. */
export function LatencyNow({ id }: { id: number }) {
  const targets = useProbeTargets();
  return (
    <Card>
      <CardHeader>
        <CardTitle>延迟和丢包</CardTitle>
        <p className="text-xs text-muted-foreground">从这台机器到各目标的 TCP 握手耗时。数字是最近一次，百分比和条是近 30 分钟：一条一分钟，越高越慢，颜色是那一分钟的丢包。</p>
      </CardHeader>
      <CardContent>
        {targets.length ? <ProbeRows serverId={id} all /> : <p className="text-sm text-muted-foreground">还没有探测目标。到「服务器」页右上角的「延迟监测」里添加。</p>}
      </CardContent>
    </Card>
  );
}

// ---------- history ----------

const RANGES = [{ value: "1h", label: "1 小时" }, { value: "6h", label: "6 小时" }, { value: "24h", label: "24 小时" }, { value: "7d", label: "7 天" }, { value: "30d", label: "30 天" }] as const;
type Range = (typeof RANGES)[number]["value"];

interface Span {
  from: number;
  to: number;
  step: number;
}
interface MetricPoint {
  ts: number;
  cpu: number;
  cpu_max: number;
  mem_used: number;
  mem_total: number;
  swap_used: number;
  disk_used: number;
  disk_total: number;
  load1: number;
  rx_rate: number;
  tx_rate: number;
  rx_max: number;
  tx_max: number;
  tcp: number;
  udp: number;
}
interface LatencyPoint {
  ts: number;
  sent: number;
  lost: number;
  avg: number;
  min: number;
  max: number;
}
type LatencySeries = ProbeTarget & LatencyPoint & { points: LatencyPoint[] };

function ChartCard({ title, note, children, className }: { title: string; note?: string; children: React.ReactNode; className?: string }) {
  return (
    <Card className={className}>
      <CardHeader className="pb-3">
        <CardTitle>{title}</CardTitle>
        {note && <p className="text-xs text-muted-foreground">{note}</p>}
      </CardHeader>
      <CardContent>{children}</CardContent>
    </Card>
  );
}

const BLUE = "var(--viz-blue)";
const ORANGE = "var(--viz-orange)";
const QUIET = "hsl(var(--muted-foreground) / 0.55)";
const MAX_LINES = 6;

/** What was measured over a span the reader picks; every chart below follows the one choice. */
export function ServerMonitor({ id }: { id: number }) {
  const [range, setRange] = React.useState<Range>("1h");
  const every = range === "1h" || range === "6h" ? 60_000 : 300_000;
  const history = useQuery({ queryKey: ["servers", String(id), "history", range], queryFn: () => get<Span & { points: MetricPoint[] }>(`/api/v1/servers/${id}/history?range=${range}`), refetchInterval: every, placeholderData: keepPreviousData });
  const latency = useQuery({ queryKey: ["servers", String(id), "latency", range], queryFn: () => get<Span & { targets: LatencySeries[] }>(`/api/v1/servers/${id}/latency?range=${range}`), refetchInterval: every, placeholderData: keepPreviousData });
  const h = history.data;
  const l = latency.data;
  const points = (h?.points ?? []) as unknown as TimePoint[];
  const top = (key: keyof MetricPoint) => Math.max(0, ...(h?.points ?? []).map((p) => p[key]));
  const span = h ? { from: h.from, to: h.to, step: h.step } : undefined;
  const percent = (v: number) => `${v.toFixed(v < 10 ? 1 : 0)}%`;
  const count = (v: number) => String(Math.round(v));

  const targets = l?.targets ?? [];
  const colors = targetColors(targets);
  const drawn = targets.filter((t) => colors.has(t.id)).slice(0, MAX_LINES);
  const lines: ChartSeries[] = drawn.map((t) => ({ key: `t${t.id}`, name: t.name, color: colors.get(t.id)! }));
  const byTime = new Map<number, TimePoint>();
  for (const t of drawn) {
    for (const p of t.points) {
      const row = byTime.get(p.ts) ?? ({ ts: p.ts } as TimePoint);
      row[`t${t.id}`] = p.sent > p.lost ? p.avg / 1000 : null;
      row[`l${t.id}`] = p.sent > 0 ? (p.lost / p.sent) * 100 : null;
      byTime.set(p.ts, row);
    }
  }
  const latencyPoints = [...byTime.values()];
  const worst = Math.max(0, ...latencyPoints.flatMap((p) => drawn.map((t) => p[`l${t.id}`] ?? 0)));
  // Round trips sit far from zero and move by a few percent: the axis runs
  // from just under the quickest to just over the slowest, in round steps.
  const trips = latencyPoints.flatMap((p) => drawn.map((t) => p[`t${t.id}`]).filter((v): v is number => v != null));
  const quickest = trips.length ? Math.min(...trips) * 0.92 : 0;
  const slowest = trips.length ? Math.max(...trips) : 100;
  const stride = [5, 10, 20, 25, 50, 100, 200, 500].find((n) => (slowest - quickest) / n <= 5) ?? 1000;
  const tripTicks: number[] = [];
  for (let v = Math.floor(quickest / stride) * stride; tripTicks.length === 0 || tripTicks[tripTicks.length - 1] < slowest; v += stride) tripTicks.push(v);

  return (
    <div className="mt-4">
      <Tabs value={range} onChange={setRange} items={[...RANGES]} />
      <div className={cn("mt-4 grid gap-4 transition-opacity lg:grid-cols-2", (history.isPlaceholderData || latency.isPlaceholderData) && "opacity-60")}>
        <ChartCard title="延迟" note="到各探测目标的 TCP 握手耗时，每个点是那段时间的平均值；线断开的地方是全部丢失或没有探测。" className="lg:col-span-2">
          {!l ? <p className="text-sm text-muted-foreground">正在加载…</p> : !targets.length ? <p className="text-sm text-muted-foreground">还没有探测目标。到「服务器」页右上角的「延迟监测」里添加。</p> : (
            <>
              <TimeChart points={latencyPoints} series={lines} from={l.from} to={l.to} step={l.step} format={(v) => `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`} axis={{ ticks: tripTicks, domain: [tripTicks[0], tripTicks[tripTicks.length - 1]], format: (v) => `${Math.round(v)} ms` }} height={220} />
              <p className="mb-1 mt-4 text-xs font-medium text-muted-foreground">丢包率</p>
              <TimeChart points={latencyPoints} series={lines.map((s) => ({ ...s, key: `l${s.key.slice(1)}` }))} from={l.from} to={l.to} step={l.step} format={percent} axis={{ domain: [0, Math.max(5, Math.ceil(worst / 5) * 5)], format: (v) => `${v}%` }} height={110} legend={false} />
              {targets.length > drawn.length && <p className="mt-2 text-xs text-muted-foreground">图上只画前 {drawn.length} 个目标，下表是全部。</p>}
              <Table className="mt-4">
                <thead><tr className="border-b"><Th>目标</Th><Th className="text-right">平均</Th><Th className="text-right">最快</Th><Th className="text-right">最慢</Th><Th className="text-right">丢包</Th><Th className="text-right">探测次数</Th></tr></thead>
                <tbody>
                  {targets.map((t) => (
                    <Tr key={t.id}>
                      <Td><span className="flex items-center gap-2"><span className="h-0.5 w-3.5 shrink-0 rounded-full" style={{ background: colors.get(t.id) ?? "transparent" }} />{t.name}<span className="hidden text-xs text-muted-foreground sm:inline">{t.host}:{t.port}</span></span></Td>
                      <Td className="text-right tabular-nums">{fmtMs(t.avg)}</Td>
                      <Td className="text-right tabular-nums">{fmtMs(t.min)}</Td>
                      <Td className="text-right tabular-nums">{fmtMs(t.max)}</Td>
                      <Td className="text-right tabular-nums">{fmtLoss(t.sent > 0 ? (t.lost / t.sent) * 100 : null)}</Td>
                      <Td className="text-right tabular-nums text-muted-foreground">{t.sent || "—"}</Td>
                    </Tr>
                  ))}
                </tbody>
              </Table>
            </>
          )}
        </ChartCard>

        {!span ? <p className="text-sm text-muted-foreground lg:col-span-2">正在加载…</p> : !points.length ? (
          <p className="rounded-xl border border-dashed p-6 text-center text-sm text-muted-foreground lg:col-span-2">这段时间还没有记录。曲线从这台机器的 agent 升级到带实时通道的版本那一刻开始积累。</p>
        ) : (
          <>
            <ChartCard title="网速" note="每个点是那段时间的平均速率。">
              <TimeChart points={points} series={[{ key: "rx_rate", name: "入站", color: BLUE }, { key: "tx_rate", name: "出站", color: ORANGE }]} {...span} format={(v) => fmtRate(v)} axis={byteAxis(Math.max(top("rx_rate"), top("tx_rate")), "/s")} />
            </ChartCard>
            <ChartCard title="CPU" note="平均值，以及那段时间里最高的一次读数。">
              <TimeChart points={points} series={[{ key: "cpu", name: "平均", color: BLUE, area: true }, { key: "cpu_max", name: "峰值", color: QUIET }]} {...span} format={percent} axis={{ domain: [0, 100], ticks: [0, 25, 50, 75, 100], format: (v) => `${v}%` }} />
            </ChartCard>
            <ChartCard title="内存">
              <TimeChart points={points} series={[{ key: "mem_used", name: "已用", color: BLUE, area: top("swap_used") === 0 }, ...(top("swap_used") > 0 ? [{ key: "swap_used", name: "交换", color: ORANGE }] : [])]} {...span} format={(v) => fmtBytes(v)} axis={capacityAxis(top("mem_total"))} />
            </ChartCard>
            <ChartCard title="负载" note="1 分钟平均负载。">
              <TimeChart points={points} series={[{ key: "load1", name: "负载", color: BLUE, area: true }]} {...span} format={(v) => v.toFixed(2)} />
            </ChartCard>
            <ChartCard title="连接数">
              <TimeChart points={points} series={[{ key: "tcp", name: "TCP", color: BLUE }, { key: "udp", name: "UDP", color: ORANGE }]} {...span} format={count} />
            </ChartCard>
            <ChartCard title="硬盘">
              <TimeChart points={points} series={[{ key: "disk_used", name: "已用", color: BLUE, area: true }]} {...span} format={(v) => fmtBytes(v)} axis={capacityAxis(top("disk_total"))} />
            </ChartCard>
          </>
        )}
      </div>
    </div>
  );
}
