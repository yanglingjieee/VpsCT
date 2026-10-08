import { Area, Bar, BarChart, CartesianGrid, ComposedChart, Line, ResponsiveContainer, Tooltip, XAxis, YAxis, Legend } from "recharts";
import type { TrafficPoint } from "@/lib/types";
import { fmtBytes } from "@/lib/utils";

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/**
 * Round ticks for an axis of byte counts. Sizes are written 1024-based, so the
 * steps are round in those units (10 GB, 20 GB…), not in powers of ten of bytes.
 */
export function byteAxis(max: number, suffix = "") {
  if (!(max > 0)) return { ticks: undefined, domain: undefined, format: (v: number) => fmtBytes(v, 0) + suffix };
  const raw = max / 4;
  let k = Math.min(UNITS.length - 1, Math.max(0, Math.floor(Math.log(raw) / Math.log(1024))));
  let nice = [1, 2, 5, 10, 20, 50, 100, 200, 500].find((n) => n >= raw / 1024 ** k);
  if (!nice) {
    nice = 1;
    k = Math.min(UNITS.length - 1, k + 1);
  }
  const step = nice * 1024 ** k;
  const count = Math.max(1, Math.ceil(max / step));
  return {
    ticks: Array.from({ length: count + 1 }, (_, i) => i * step),
    domain: [0, count * step] as [number, number],
    format: (v: number) => (v === 0 ? "0" : `${Math.round(v / 1024 ** k)} ${UNITS[k]}${suffix}`),
  };
}

function dayLabel(s: string) {
  const d = new Date(s);
  return `${d.getMonth() + 1}/${d.getDate()}`;
}

/** `fill`: take the height of the card it is in (at least `height`), for a card stretched by its neighbour. */
export function TrafficBars({ points, height = 220, fill }: { points: TrafficPoint[]; height?: number; fill?: boolean }) {
  const data = points.map((p) => ({ day: dayLabel(p.bucket), 入站: p.up, 出站: p.down }));
  const axis = byteAxis(Math.max(0, ...points.map((p) => p.up + p.down)));
  const chart = (
    <ResponsiveContainer width="100%" height={fill ? "100%" : height}>
      <BarChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }} barCategoryGap="30%">
        <CartesianGrid strokeDasharray="3 3" className="stroke-border" vertical={false} />
        <XAxis dataKey="day" tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }} tickLine={false} axisLine={false} interval="preserveStartEnd" minTickGap={24} />
        <YAxis ticks={axis.ticks} domain={axis.domain} tickFormatter={axis.format} tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }} tickLine={false} axisLine={false} width={64} />
        <Tooltip
          formatter={(v: number) => fmtBytes(v)}
          contentStyle={{ background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: 14, fontSize: 12, boxShadow: "0 8px 24px -8px rgba(16,24,40,.2)" }}
          cursor={{ fill: "hsl(var(--foreground) / 0.04)" }}
        />
        <Legend wrapperStyle={{ fontSize: 12 }} iconType="circle" iconSize={8} formatter={(v: string) => <span className="text-muted-foreground">{v}</span>} />
        <Bar dataKey="入站" stackId="a" fill="hsl(var(--primary))" radius={[0, 0, 0, 0]} />
        <Bar dataKey="出站" stackId="a" fill="hsl(var(--primary) / 0.4)" radius={[4, 4, 0, 0]} />
      </BarChart>
    </ResponsiveContainer>
  );
  if (!fill) return chart;
  return (
    <div className="relative flex-1" style={{ minHeight: height }}>
      <div className="absolute inset-0">{chart}</div>
    </div>
  );
}

// ---------- measurements over time ----------

/** An axis for how much of a fixed size is used: quarters of the whole, up to the whole. */
export function capacityAxis(total: number) {
  if (!(total > 0)) return { format: (v: number) => fmtBytes(v, 0) };
  return { ticks: [0, 0.25, 0.5, 0.75, 1].map((f) => total * f), domain: [0, total] as [number, number], format: (v: number) => (v === 0 ? "0" : fmtBytes(v, v >= 1024 ** 3 && v % 1024 ** 3 !== 0 ? 1 : 0)) };
}

export interface ChartSeries {
  key: string;
  name: string;
  color: string;
  /** A wash under the line, for a chart with one series. */
  area?: boolean;
}

export type TimePoint = { ts: number } & Record<string, number | null>;

const pad2 = (n: number) => String(n).padStart(2, "0");

/** Round moments of the local clock across a span: every ten minutes, every four hours, every day. */
function timeTicks(from: number, to: number): number[] {
  const every = [300, 600, 900, 1800, 3600, 7200, 14400, 21600, 43200, 86400, 2 * 86400, 5 * 86400, 7 * 86400].find((n) => (to - from) / n <= 7) ?? 30 * 86400;
  const zone = new Date(to * 1000).getTimezoneOffset() * 60;
  const ticks: number[] = [];
  for (let t = Math.ceil((from - zone) / every) * every + zone; t <= to; t += every) ticks.push(t);
  return ticks;
}

/**
 * Lines over a span of time. Points are buckets `step` seconds wide; a bucket
 * nothing was measured in breaks the line instead of being drawn across.
 */
export function TimeChart({ points, series, from, to, step, format, axis, height = 180, legend = true }: {
  points: TimePoint[];
  series: ChartSeries[];
  /** The span drawn, in unix seconds. */
  from: number;
  to: number;
  step: number;
  /** A value written for the tooltip and, without `axis`, the axis. */
  format: (v: number) => string;
  axis?: { ticks?: number[]; domain?: [number | "auto", number | "auto"]; format: (v: number) => string };
  height?: number;
  /** Name the series above the plot; off for a chart that repeats the series of the one above it. */
  legend?: boolean;
}) {
  const have = new Map(points.map((p) => [p.ts, p]));
  const data: TimePoint[] = [];
  for (let ts = Math.ceil(from / step) * step; ts <= to; ts += step) data.push(have.get(ts) ?? ({ ts } as TimePoint));
  const long = to - from > 26 * 3600;
  const clock = (ts: number) => {
    const d = new Date(ts * 1000);
    return long ? `${d.getMonth() + 1}/${d.getDate()}` : `${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
  };
  const moment = (ts: number) => {
    const d = new Date(ts * 1000);
    return `${d.getMonth() + 1} 月 ${d.getDate()} 日 ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
  };
  const tick = { fontSize: 11, fill: "hsl(var(--muted-foreground))" };
  return (
    <div>
      {legend && series.length > 1 && (
        <ul className="mb-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
          {series.map((s) => <li key={s.key} className="flex items-center gap-1.5"><span className="h-0.5 w-3.5 rounded-full" style={{ background: s.color }} />{s.name}</li>)}
        </ul>
      )}
      <ResponsiveContainer width="100%" height={height}>
        <ComposedChart data={data} margin={{ top: 6, right: 8, left: 0, bottom: 0 }}>
          <CartesianGrid stroke="hsl(var(--viz-grid))" vertical={false} />
          <XAxis dataKey="ts" type="number" domain={[from, to]} ticks={timeTicks(from, to)} tickFormatter={clock} tick={tick} tickLine={false} axisLine={false} />
          <YAxis ticks={axis?.ticks} domain={axis?.domain ?? [0, "auto"]} tickFormatter={axis?.format ?? format} tick={tick} tickLine={false} axisLine={false} width={76} />
          <Tooltip
            isAnimationActive={false}
            cursor={{ stroke: "hsl(var(--muted-foreground) / 0.5)", strokeWidth: 1 }}
            content={({ active, payload, label }) => {
              const rows = (payload ?? []).filter((p) => typeof p.value === "number");
              if (!active || !rows.length) return null;
              return (
                <div className="rounded-xl border border-border bg-card px-3 py-2 text-xs shadow-lg">
                  <p className="mb-1 text-muted-foreground">{moment(Number(label))}</p>
                  {series.map((s) => {
                    const row = rows.find((r) => r.dataKey === s.key);
                    if (!row) return null;
                    return (
                      <p key={s.key} className="flex items-center gap-2">
                        <span className="h-0.5 w-3 shrink-0 rounded-full" style={{ background: s.color }} />
                        <span className="font-semibold tabular-nums">{format(row.value as number)}</span>
                        <span className="text-muted-foreground">{s.name}</span>
                      </p>
                    );
                  })}
                </div>
              );
            }}
          />
          {series.map((s) => (s.area
            ? <Area key={s.key} type="linear" dataKey={s.key} name={s.name} stroke={s.color} fill={s.color} fillOpacity={0.1} strokeWidth={2} dot={false} activeDot={{ r: 4, stroke: "hsl(var(--card))", strokeWidth: 2 }} connectNulls={false} isAnimationActive={false} />
            : <Line key={s.key} type="linear" dataKey={s.key} name={s.name} stroke={s.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" dot={false} activeDot={{ r: 4, stroke: "hsl(var(--card))", strokeWidth: 2 }} connectNulls={false} isAnimationActive={false} />))}
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
