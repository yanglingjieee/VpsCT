import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp } from "lucide-react";
import type { Server } from "@/lib/types";
import { cn, fmtBytes, fmtDuration, fmtRate, fmtResetIn } from "@/lib/utils";
import { Card, Progress } from "@/components/ui";

/** One of the two live rates: with the period's traffic, what a card is read for first. */
function Rate({ icon, label, value }: { icon: ReactNode; label: string; value: string }) {
  return (
    <div className="min-w-0 rounded-xl bg-muted/50 px-3 py-2">
      <p className="flex items-center gap-1 text-xs text-muted-foreground">{icon}{label}</p>
      <p className="mt-0.5 truncate text-lg font-semibold tabular-nums leading-tight">{value}</p>
    </div>
  );
}

/** CPU, memory, disk: kept small, coloured only when they need attention. */
function Load({ label, percent }: { label: string; percent: number | null }) {
  const p = percent == null ? 0 : Math.max(0, Math.min(100, percent));
  const tone = p >= 90 ? "bg-rose-500" : p >= 75 ? "bg-amber-500" : "bg-emerald-500";
  return (
    <div className="min-w-0">
      <p className="flex items-baseline justify-between gap-1 text-xs"><span className="text-muted-foreground">{label}</span><span className="tabular-nums">{percent == null ? "—" : `${p.toFixed(0)}%`}</span></p>
      <div className="mt-1 h-1 overflow-hidden rounded-full bg-muted"><div className={cn("h-full rounded-full transition-all", tone)} style={{ width: `${p}%` }} /></div>
    </div>
  );
}

const ratio = (used?: number, total?: number) => (total ? ((used ?? 0) / total) * 100 : null);

/** One server as a probe tile. Live rates and the period's traffic lead; load is secondary. */
export function ServerCard({ s }: { s: Server }) {
  const online = s.agent_status === "online";
  const m = online ? s.metrics : undefined;
  const dot = online ? "bg-emerald-500" : s.agent_status === "offline" ? "bg-rose-500" : "bg-muted-foreground/40";
  const trouble = online && ((s.desired && !s.desired.in_sync) || !!s.agent?.apply_error);
  const u = s.usage;
  const usageLabel = !u?.next_reset ? "近 30 天流量" : u.billing === "out" ? "本期出站流量" : "本期流量";
  return (
    <Link to={`/servers/${s.id}`} className="block min-w-0">
      <Card className={cn("h-full p-4 transition-colors hover:bg-accent/30", (s.agent_status === "offline" || s.quota_stopped) && "border-rose-500/40")}>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="flex items-center gap-2 font-medium"><span className={cn("h-2 w-2 shrink-0 rounded-full", dot, online && "shadow-[0_0_0_3px_rgba(16,185,129,0.18)]")} /><span className="break-words">{s.name}</span></p>
            <p className="mt-0.5 break-words pl-4 text-xs text-muted-foreground">{[s.region, s.public_host || s.agent?.public_ipv4].filter(Boolean).join(" · ") || "等待接入"}</p>
          </div>
          {!online && <p className={cn("shrink-0 text-xs", s.agent_status === "offline" ? "font-medium text-rose-500" : "text-muted-foreground")}>{s.agent_status === "offline" ? "离线" : "待接入"}</p>}
        </div>

        <div className="mt-3 grid grid-cols-2 gap-2">
          <Rate icon={<ArrowUp className="h-3 w-3" />} label="上行" value={m ? fmtRate(m.net_tx_rate) : "—"} />
          <Rate icon={<ArrowDown className="h-3 w-3" />} label="下行" value={m ? fmtRate(m.net_rx_rate) : "—"} />
        </div>

        <div className="mt-3">
          <p className="flex items-baseline justify-between gap-2 text-xs text-muted-foreground"><span className="min-w-0 truncate">{usageLabel}</span><span className="shrink-0">{fmtResetIn(u?.next_reset)}</span></p>
          <p className="mt-0.5 flex items-baseline justify-between gap-2">
            <span className="min-w-0 truncate"><span className="text-lg font-semibold tabular-nums leading-tight">{fmtBytes(u?.billed ?? 0)}</span><span className="ml-1.5 text-xs text-muted-foreground">{s.quota_bytes > 0 ? `/ ${fmtBytes(s.quota_bytes, 0)}` : "不限量"}</span></span>
            {s.quota_bytes > 0 && <span className="shrink-0 text-sm font-medium tabular-nums">{(u?.percent ?? 0).toFixed(0)}%</span>}
          </p>
          {s.quota_bytes > 0 && <Progress className="mt-1.5" value={u?.percent ?? 0} />}
        </div>

        <div className="mt-3 grid grid-cols-3 gap-3">
          <Load label="CPU" percent={m ? m.cpu_percent : null} />
          <Load label="内存" percent={m ? ratio(m.mem_used, m.mem_total) : null} />
          <Load label="硬盘" percent={m ? ratio(m.disk_used, m.disk_total) : null} />
        </div>
        {m && <p className="mt-2.5 flex flex-wrap justify-between gap-x-3 text-xs text-muted-foreground"><span>运行 {fmtDuration(m.uptime_sec)}</span><span>{m.tcp_conns} TCP · {m.udp_conns} UDP</span></p>}
        {s.quota_stopped && <p className="mt-2 text-xs font-medium text-rose-600 dark:text-rose-400">配额用完，入站已停{u?.next_reset && `，${fmtResetIn(u.next_reset, "恢复")}`}</p>}
        {trouble && <p className="mt-2 break-words text-xs text-amber-600 dark:text-amber-400">{s.agent?.apply_error || "等待同步配置"}</p>}
      </Card>
    </Link>
  );
}
