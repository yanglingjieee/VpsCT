import { Area, AreaChart, Bar, BarChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis, Legend } from "recharts";
import type { TrafficPoint } from "@/lib/types";
import { fmtBytes } from "@/lib/utils";

const UNITS = ["B", "KB", "MB", "GB", "TB", "PB"];

/**
 * Round ticks for an axis of byte counts. Sizes are written 1024-based, so the
 * steps are round in those units (10 GB, 20 GB…), not in powers of ten of bytes.
 */
function byteAxis(max: number, suffix = "") {
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

export function RateArea({ points, height = 200 }: { points: { ts: string; rx_rate: number; tx_rate: number }[]; height?: number }) {
  const data = points.map((p) => {
    const d = new Date(p.ts);
    return { t: `${String(d.getHours()).padStart(2, "0")}:${String(d.getMinutes()).padStart(2, "0")}`, 入站: p.rx_rate, 出站: p.tx_rate };
  });
  const axis = byteAxis(Math.max(0, ...points.map((p) => Math.max(p.rx_rate, p.tx_rate))), "/s");
  return (
    <ResponsiveContainer width="100%" height={height}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: 0, bottom: 0 }}>
        <defs>
          <linearGradient id="rx" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="hsl(var(--primary))" stopOpacity={0.5} />
            <stop offset="100%" stopColor="hsl(var(--primary))" stopOpacity={0} />
          </linearGradient>
        </defs>
        <CartesianGrid strokeDasharray="3 3" className="stroke-border" vertical={false} />
        <XAxis dataKey="t" tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }} tickLine={false} axisLine={false} minTickGap={32} />
        <YAxis ticks={axis.ticks} domain={axis.domain} tickFormatter={axis.format} tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }} tickLine={false} axisLine={false} width={80} />
        <Tooltip formatter={(v: number) => fmtBytes(v) + "/s"} contentStyle={{ background: "hsl(var(--card))", border: "1px solid hsl(var(--border))", borderRadius: 14, fontSize: 12, boxShadow: "0 8px 24px -8px rgba(16,24,40,.2)" }} />
        <Area type="monotone" dataKey="入站" stroke="hsl(var(--primary))" fill="url(#rx)" strokeWidth={2} />
        <Area type="monotone" dataKey="出站" stroke="#7c6cf0" fill="transparent" strokeWidth={2} />
      </AreaChart>
    </ResponsiveContainer>
  );
}
