// The pieces a server is drawn with as a probe: what it is doing this second,
// and how far it is from each carrier.
import * as React from "react";
import type { Server } from "@/lib/types";
import { summarize, useLive, useProbeTargets, type LiveProbe, type ProbeMinute, type ProbeTarget } from "@/lib/live";
import { cn, fmtLoss, fmtMs, lossTone } from "@/lib/utils";

/** What a server is doing now, from its live worker or, without one, its last heartbeat. */
export interface Vitals {
  /** Readings are arriving every second. */
  live: boolean;
  cpu: number;
  memUsed: number;
  memTotal: number;
  swapUsed: number;
  swapTotal: number;
  diskUsed: number;
  diskTotal: number;
  /** 1, 5 and, from a live worker, 15 minutes. */
  load: number[];
  rx: number;
  tx: number;
  tcp: number;
  udp: number;
  uptime: number;
}

/** Undefined while the server is not reporting at all. */
export function useVitals(s: Server): Vitals | undefined {
  const live = useLive(s.id);
  if (live?.connected && live.sample) {
    const v = live.sample;
    return { live: true, cpu: v.c, memUsed: v.mu, memTotal: v.mt, swapUsed: v.su ?? 0, swapTotal: v.st ?? 0, diskUsed: v.du, diskTotal: v.dt, load: [v.l1, v.l5, v.l15], rx: v.rx, tx: v.tx, tcp: v.tc, udp: v.uc, uptime: v.up };
  }
  const m = s.agent_status === "online" ? s.metrics : undefined;
  if (!m) return undefined;
  return { live: false, cpu: m.cpu_percent, memUsed: m.mem_used, memTotal: m.mem_total, swapUsed: m.swap_used, swapTotal: m.swap_total, diskUsed: m.disk_used, diskTotal: m.disk_total, load: [m.load1, m.load5], rx: m.net_rx_rate, tx: m.net_tx_rate, tcp: m.tcp_conns, udp: m.udp_conns, uptime: m.uptime_sec };
}

export const ratio = (used?: number, total?: number) => (total ? ((used ?? 0) / total) * 100 : null);

/**
 * The last couple of minutes of one number: a line with a wash under it,
 * ending in a dot at the newest reading. It fills the box it is given and
 * keeps inside it, so the box can sit in a tile's padding.
 */
export function Spark({ values, className }: { values: number[]; className?: string }) {
  const id = React.useId();
  if (values.length < 2) return null;
  const top = Math.max(1, ...values);
  const step = 100 / (values.length - 1);
  // The line stays clear of the top and bottom edges by the dot's radius.
  const y = (v: number) => 26 - (v / top) * 22;
  const line = values.map((v, i) => `${(i * step).toFixed(2)},${y(v).toFixed(2)}`).join(" ");
  return (
    <div aria-hidden className={cn("relative h-full w-full", className)}>
      {/* Narrower than the box by the dot's radius: the line ends at the dot's centre. */}
      <svg viewBox="0 0 100 30" preserveAspectRatio="none" className="absolute inset-y-0 left-0 h-full w-[calc(100%-3px)]">
        <defs>
          <linearGradient id={id} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="currentColor" stopOpacity={0.22} />
            <stop offset="100%" stopColor="currentColor" stopOpacity={0} />
          </linearGradient>
        </defs>
        <polygon points={`0,30 ${line} 100,30`} fill={`url(#${id})`} />
        <polyline points={line} fill="none" stroke="currentColor" strokeWidth={1.5} strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />
      </svg>
      <span className="absolute right-0 h-1.5 w-1.5 -translate-y-1/2 rounded-full bg-current" style={{ top: `${(y(values[values.length - 1]) / 30) * 100}%` }} />
    </div>
  );
}

// Each carrier keeps one colour everywhere; other targets take the rest in order.
const CARRIER_COLOR: Record<string, string> = { ct: "var(--viz-blue)", cu: "var(--viz-orange)", cm: "var(--viz-aqua)" };
const SPARE_COLORS = ["var(--viz-magenta)", "var(--viz-violet)", "var(--viz-yellow)"];

/** The colour of each target's series; a target has none once every colour is taken. */
export function targetColors(targets: ProbeTarget[]): Map<number, string> {
  const out = new Map<number, string>();
  const free = [...Object.values(CARRIER_COLOR), ...SPARE_COLORS];
  const take = (c: string) => free.splice(free.indexOf(c), 1)[0];
  for (const t of targets) {
    const own = CARRIER_COLOR[t.carrier];
    if (own && free.includes(own)) out.set(t.id, take(own));
  }
  for (const t of targets) {
    if (!out.has(t.id) && free.length) out.set(t.id, free.shift()!);
  }
  return out;
}

const TONE_BAR = { none: "bg-muted-foreground/20", good: "bg-emerald-500", warn: "bg-amber-400", serious: "bg-orange-500", bad: "bg-rose-500" } as const;
const TONE_TEXT = { none: "text-muted-foreground", good: "text-muted-foreground", warn: "text-amber-600 dark:text-amber-400", serious: "text-orange-600 dark:text-orange-400", bad: "text-rose-600 dark:text-rose-400" } as const;

const MINUTES = 30;

/**
 * Half an hour of one probe, a bar a minute, the newest on the right. A bar is
 * as tall as the round trip was long and coloured by how much was lost; a
 * minute without probes leaves a gap.
 */
export function LatencyStrip({ strip, className }: { strip: ProbeMinute[]; className?: string }) {
  const end = Math.floor(Date.now() / 60000) * 60;
  const byMinute = new Map(strip.map((m) => [m.ts, m]));
  const slots = Array.from({ length: MINUTES }, (_, i) => byMinute.get(end - (MINUTES - 1 - i) * 60));
  const top = Math.max(1, ...slots.map((m) => (m && m.sent > m.lost ? m.rtt_sum / (m.sent - m.lost) : 0)));
  return (
    <div className={cn("flex h-5 items-end gap-px", className)}>
      {slots.map((m, i) => {
        if (!m || m.sent === 0) return <span key={i} className="h-0.5 min-w-0 flex-1 rounded-full bg-muted-foreground/15" />;
        const answered = m.sent - m.lost;
        const avg = answered > 0 ? m.rtt_sum / answered : 0;
        // A minute holds only a handful of probes: one lost is a blip, most lost is an outage.
        const tone = m.lost === 0 ? "good" : m.lost * 5 <= m.sent ? "warn" : m.lost * 2 <= m.sent ? "serious" : "bad";
        const at = new Date(m.ts * 1000);
        const label = `${String(at.getHours()).padStart(2, "0")}:${String(at.getMinutes()).padStart(2, "0")} · ${answered > 0 ? fmtMs(avg) : "全部丢失"} · 丢 ${m.lost}/${m.sent}`;
        return <span key={i} title={label} className={cn("min-w-0 flex-1 rounded-[1px]", TONE_BAR[tone])} style={{ height: answered > 0 ? `${Math.max(20, (avg / top) * 100)}%` : "100%" }} />;
      })}
    </div>
  );
}

/** One target as one server sees it: the newest round trip, the loss over half an hour, and the strip. */
export function ProbeRow({ target, probe, color }: { target: ProbeTarget; probe?: LiveProbe; color?: string }) {
  const sum = summarize(probe?.strip ?? []);
  const tone = lossTone(sum.loss);
  return (
    <div className="grid grid-cols-[minmax(0,5.5rem)_3.75rem_2.75rem_minmax(0,1fr)] items-center gap-2 text-xs">
      <span className="flex min-w-0 items-center gap-1.5">
        <span className="h-2 w-2 shrink-0 rounded-full" style={{ background: color ?? "hsl(var(--muted-foreground) / 0.4)" }} />
        <span className="truncate">{target.name}</span>
      </span>
      <span className={cn("text-right font-medium tabular-nums", probe?.us === -1 && "text-rose-600 dark:text-rose-400")}>{!probe || probe.us === 0 ? "—" : probe.us < 0 ? "超时" : fmtMs(probe.us)}</span>
      <span className={cn("text-right tabular-nums", TONE_TEXT[tone])} title="近 30 分钟丢包率">{fmtLoss(sum.loss)}</span>
      <LatencyStrip strip={probe?.strip ?? []} />
    </div>
  );
}

/** A server's probe rows: the targets shown on cards, or every target. */
export function ProbeRows({ serverId, all, className }: { serverId: number; all?: boolean; className?: string }) {
  const targets = useProbeTargets();
  const live = useLive(serverId);
  const shown = all ? targets : targets.filter((t) => t.on_card);
  if (!shown.length) return null;
  const colors = targetColors(targets);
  return (
    <div className={cn("space-y-1.5", className)}>
      {shown.map((t) => <ProbeRow key={t.id} target={t} probe={live?.probes.find((p) => p.target_id === t.id)} color={colors.get(t.id)} />)}
    </div>
  );
}
