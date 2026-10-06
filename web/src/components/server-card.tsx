import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp } from "lucide-react";
import type { Server } from "@/lib/types";
import { cn, fmtBytes, fmtDuration, fmtRate } from "@/lib/utils";
import { Card } from "@/components/ui";

function Meter({ label, percent, detail }: { label: string; percent: number | null; detail?: string }) {
  const p = percent == null ? 0 : Math.max(0, Math.min(100, percent));
  const tone = p >= 90 ? "bg-rose-500" : p >= 75 ? "bg-amber-500" : "bg-emerald-500";
  return (
    <div>
      <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
        <span className="text-muted-foreground">{label}</span>
        <span className="tabular-nums">{percent == null ? "—" : `${p.toFixed(0)}%`}{detail && <span className="ml-1.5 text-muted-foreground">{detail}</span>}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-muted"><div className={cn("h-full rounded-full transition-all", tone)} style={{ width: `${p}%` }} /></div>
    </div>
  );
}

const ratio = (used?: number, total?: number) => (total ? ((used ?? 0) / total) * 100 : null);

/** One server as a probe tile: is it up, how loaded, how much traffic is left. */
export function ServerCard({ s }: { s: Server }) {
  const m = s.agent_status === "online" ? s.metrics : undefined;
  const dot = s.agent_status === "online" ? "bg-emerald-500" : s.agent_status === "offline" ? "bg-rose-500" : "bg-muted-foreground/40";
  const trouble = s.agent_status === "online" && ((s.desired && !s.desired.in_sync) || !!s.agent?.apply_error);
  const used = s.usage?.total ?? (s.usage ? s.usage.up + s.usage.down : 0);
  return (
    <Link to={`/servers/${s.id}`} className="block min-w-0">
      <Card className={cn("h-full p-4 transition-colors hover:bg-accent/30", s.agent_status === "offline" && "border-rose-500/40")}>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="flex items-center gap-2 font-medium"><span className={cn("h-2 w-2 shrink-0 rounded-full", dot, s.agent_status === "online" && "shadow-[0_0_0_3px_rgba(16,185,129,0.18)]")} /><span className="break-words">{s.name}</span></p>
            <p className="mt-0.5 break-words pl-4 text-xs text-muted-foreground">{[s.region, s.public_host || s.agent?.public_ipv4].filter(Boolean).join(" · ") || "等待接入"}</p>
          </div>
          <div className="shrink-0 text-right text-xs tabular-nums text-muted-foreground">
            {s.agent_status === "online" && m ? (
              <>
                <p className="inline-flex items-center gap-0.5"><ArrowUp className="h-3 w-3" />{fmtRate(m.net_tx_rate)}</p>
                <p className="flex items-center justify-end gap-0.5"><ArrowDown className="h-3 w-3" />{fmtRate(m.net_rx_rate)}</p>
              </>
            ) : <p className={s.agent_status === "offline" ? "font-medium text-rose-500" : ""}>{s.agent_status === "offline" ? "离线" : "待接入"}</p>}
          </div>
        </div>
        <div className="mt-3 grid gap-2.5">
          <Meter label="CPU" percent={m ? m.cpu_percent : null} detail={m ? `负载 ${m.load1.toFixed(2)}` : undefined} />
          <Meter label="内存" percent={m ? ratio(m.mem_used, m.mem_total) : null} detail={m ? fmtBytes(m.mem_total, 0) : undefined} />
          <Meter label="硬盘" percent={m ? ratio(m.disk_used, m.disk_total) : null} detail={m ? fmtBytes(m.disk_total, 0) : undefined} />
          <Meter label="本期流量" percent={s.quota_bytes > 0 ? s.usage?.percent ?? 0 : null} detail={s.quota_bytes > 0 ? `${fmtBytes(s.usage?.billed)} / ${fmtBytes(s.quota_bytes, 0)}` : fmtBytes(used)} />
        </div>
        <p className="mt-3 flex flex-wrap justify-between gap-x-3 text-xs text-muted-foreground">
          <span>{m ? `运行 ${fmtDuration(m.uptime_sec)}` : "—"}</span>
          <span>{m ? `${m.tcp_conns} TCP · ${m.udp_conns} UDP` : ""}</span>
        </p>
        {trouble && <p className="mt-2 break-words text-xs text-amber-600 dark:text-amber-400">{s.agent?.apply_error || "等待同步配置"}</p>}
      </Card>
    </Link>
  );
}
