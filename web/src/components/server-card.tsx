import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { ArrowDown, ArrowUp } from "lucide-react";
import type { Server } from "@/lib/types";
import { useLive } from "@/lib/live";
import { cn, daysUntil, flag, fmtBytes, fmtDuration, fmtOS, fmtPrice, fmtRate, fmtResetIn, fmtVirt } from "@/lib/utils";
import { Card, Progress } from "@/components/ui";
import { ProbeRows, Spark, ratio, useVitals, type Vitals } from "@/components/probe";

/** One of the two live rates: with the period's traffic, what a card is read for first. */
function Rate({ icon, label, value, trail }: { icon: ReactNode; label: string; value: string; trail?: number[] }) {
  return (
    <div className="min-w-0 rounded-xl bg-muted/50 px-3 pb-2.5 pt-2">
      <p className="flex items-center gap-1 text-xs text-muted-foreground">{icon}{label}</p>
      <p className="mt-0.5 truncate text-lg font-semibold tabular-nums leading-tight">{value}</p>
      {/* The last two minutes, under the figure and inside the tile's padding; the room is kept when there is nothing to draw. */}
      <div className="mt-1.5 h-6 text-primary/80">{trail && <Spark values={trail} />}</div>
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
      <div className="mt-1 h-1 overflow-hidden rounded-full bg-muted"><div className={cn("h-full rounded-full transition-[width] duration-700 ease-linear", tone)} style={{ width: `${p}%` }} /></div>
    </div>
  );
}

function Rates({ id, v }: { id: number; v?: Vitals }) {
  const trail = useLive(id)?.trail;
  const drawn = v?.live && trail && trail.length > 1 ? trail : undefined;
  return (
    <div className="mt-3 grid grid-cols-2 gap-2">
      <Rate icon={<ArrowUp className="h-3 w-3" />} label="上行" value={v ? fmtRate(v.tx) : "—"} trail={drawn?.map((t) => t.tx)} />
      <Rate icon={<ArrowDown className="h-3 w-3" />} label="下行" value={v ? fmtRate(v.rx) : "—"} trail={drawn?.map((t) => t.rx)} />
    </div>
  );
}

function Usage({ s }: { s: Server }) {
  const u = s.usage;
  const label = !u?.next_reset ? "近 30 天流量" : u.billing === "out" ? "本期出站流量" : "本期流量";
  return (
    <div className="mt-3">
      <p className="flex items-baseline justify-between gap-2 text-xs text-muted-foreground"><span className="min-w-0 truncate">{label}</span><span className="shrink-0">{fmtResetIn(u?.next_reset)}</span></p>
      <p className="mt-0.5 flex items-baseline justify-between gap-2">
        <span className="min-w-0 truncate"><span className="text-lg font-semibold tabular-nums leading-tight">{fmtBytes(u?.billed ?? 0)}</span><span className="ml-1.5 text-xs text-muted-foreground">{s.quota_bytes > 0 ? `/ ${fmtBytes(s.quota_bytes, 0)}` : "不限量"}</span></span>
        {s.quota_bytes > 0 && <span className="shrink-0 text-sm font-medium tabular-nums">{(u?.percent ?? 0).toFixed(0)}%</span>}
      </p>
      {s.quota_bytes > 0 && <Progress className="mt-1.5" value={u?.percent ?? 0} />}
    </div>
  );
}

function Loads({ v }: { v?: Vitals }) {
  return (
    <div className="mt-3 grid grid-cols-3 gap-3">
      <Load label="CPU" percent={v ? v.cpu : null} />
      <Load label="内存" percent={v ? ratio(v.memUsed, v.memTotal) : null} />
      <Load label="硬盘" percent={v ? ratio(v.diskUsed, v.diskTotal) : null} />
    </div>
  );
}

function Notes({ s }: { s: Server }) {
  const online = s.agent_status === "online";
  const trouble = online && ((s.desired && !s.desired.in_sync) || !!s.agent?.apply_error);
  return (
    <>
      {s.quota_stopped && <p className="mt-2 text-xs font-medium text-rose-600 dark:text-rose-400">配额用完，入站已停{s.usage?.next_reset && `，${fmtResetIn(s.usage.next_reset, "恢复")}`}</p>}
      {trouble && <p className="mt-2 break-words text-xs text-amber-600 dark:text-amber-400">{s.agent?.apply_error || "等待同步配置"}</p>}
    </>
  );
}

/** Whether the server is reporting: by its live worker, or else by its heartbeat. */
function standing(s: Server, v?: Vitals) {
  const online = !!v?.live || s.agent_status === "online";
  return { online, offline: !online && s.agent_status === "offline", dot: online ? "bg-emerald-500" : s.agent_status === "offline" ? "bg-rose-500" : "bg-muted-foreground/40" };
}

/** One server as a tile on the overview. Live rates and the period's traffic lead; load is secondary. */
export function ServerCard({ s }: { s: Server }) {
  const v = useVitals(s);
  const { online, offline, dot } = standing(s, v);
  return (
    <Link to={`/servers/${s.id}`} className="block min-w-0">
      <Card className={cn("h-full p-4 transition-colors hover:bg-accent/30", (offline || s.quota_stopped) && "border-rose-500/40")}>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <p className="flex items-center gap-2 font-medium"><span className={cn("h-2 w-2 shrink-0 rounded-full", dot, online && "shadow-[0_0_0_3px_rgba(16,185,129,0.18)]")} /><span className="break-words">{s.name}</span></p>
            <p className="mt-0.5 break-words pl-4 text-xs text-muted-foreground">{[s.region, s.public_host || s.agent?.public_ipv4].filter(Boolean).join(" · ") || "等待接入"}</p>
          </div>
          {!online && <p className={cn("shrink-0 text-xs", offline ? "font-medium text-rose-500" : "text-muted-foreground")}>{offline ? "离线" : "待接入"}</p>}
        </div>
        <Rates id={s.id} v={v} />
        <Usage s={s} />
        <Loads v={v} />
        {v && <p className="mt-2.5 flex flex-wrap justify-between gap-x-3 text-xs text-muted-foreground"><span>运行 {fmtDuration(v.uptime)}</span><span className="tabular-nums">{v.tcp} TCP · {v.udp} UDP</span></p>}
        <Notes s={s} />
      </Card>
    </Link>
  );
}

function Chip({ children }: { children: ReactNode }) {
  return <span className="rounded-md bg-muted px-1.5 py-0.5 text-2xs font-medium leading-4 text-muted-foreground">{children}</span>;
}

/** Memory the way a plan names it: 1G, 512M, 12G. */
function planMemory(bytes: number): string {
  // The kernel keeps some of what was bought: 418 MB usable is a 512 MB plan.
  const mb = bytes / 1024 ** 2;
  const plan = [128, 256, 384, 512, 768].find((n) => n >= mb * 0.98);
  return plan ? `${plan}M` : `${Math.ceil(mb / 1024 - 0.06)}G`;
}

/** What the server costs and how long it is paid for: "$49.99/年 · 剩 52 天". */
export function Renewal({ s, className }: { s: Server; className?: string }) {
  const left = daysUntil(s.expires_at);
  const cost = fmtPrice(s);
  if (left == null && !cost) return null;
  const when = left == null ? "" : left < 0 ? `到期日已过 ${-left} 天` : left === 0 ? "今天到期" : `剩 ${left} 天`;
  const tone = left == null ? "" : left < 0 ? "font-medium text-rose-600 dark:text-rose-400" : left <= 7 ? "font-medium text-amber-600 dark:text-amber-400" : "";
  return (
    <span className={cn("whitespace-nowrap text-xs text-muted-foreground", className)}>
      {cost}{cost && when && " · "}<span className={tone}>{when}{when && s.auto_renew && left != null && left >= 0 && "，自动续"}</span>
    </span>
  );
}

/** What kind of machine it is, as a row of small labels. */
export function HostChips({ s, memTotal }: { s: Server; memTotal?: number }) {
  const h = useLive(s.id)?.host ?? s.host;
  const mem = memTotal ?? s.metrics?.mem_total;
  const size = [h?.cores ? `${h.cores}C` : "", mem ? planMemory(mem) : ""].filter(Boolean).join(" · ");
  const v4 = s.agent?.public_ipv4;
  const v6 = s.agent?.public_ipv6;
  const stack = v4 && v6 ? "IPv4/6" : v6 ? "IPv6" : v4 ? "IPv4" : "";
  const chips = [fmtOS(h?.os), fmtVirt(h?.virt), size, stack].filter(Boolean);
  if (!chips.length) return null;
  return <span className="flex flex-wrap gap-1">{chips.map((c) => <Chip key={c}>{c}</Chip>)}</span>;
}

/** One server as a probe: what it is, what it is doing this second, and how far it is from each carrier. */
export function ProbeCard({ s }: { s: Server }) {
  const v = useVitals(s);
  const { online, offline, dot } = standing(s, v);
  const pill = online ? "bg-emerald-500/12 text-emerald-700 dark:bg-emerald-400/15 dark:text-emerald-300" : offline ? "bg-rose-500/12 text-rose-700 dark:bg-rose-400/15 dark:text-rose-300" : "bg-secondary text-secondary-foreground";
  return (
    <Link to={`/servers/${s.id}`} className="block min-w-0">
      <Card className={cn("flex h-full flex-col p-4 transition-colors hover:bg-accent/30", (offline || s.quota_stopped) && "border-rose-500/40")}>
        <div className="flex items-start justify-between gap-2">
          <p className="flex min-w-0 items-center gap-2 font-semibold">
            {flag(s.region) && <span className="shrink-0 text-base leading-none">{flag(s.region)}</span>}
            <span className="break-words">{s.name}</span>
          </p>
          <span className={cn("flex shrink-0 items-center gap-1.5 rounded-full px-2 py-0.5 text-2xs font-semibold leading-4", pill)} title={v?.live ? "每秒更新" : online ? "这台机器的 agent 还没有实时通道，约 30 秒更新一次" : undefined}>
            <span className="relative flex h-1.5 w-1.5">
              {v?.live && <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-emerald-500 opacity-60" />}
              <span className={cn("relative inline-flex h-1.5 w-1.5 rounded-full", dot)} />
            </span>
            {online ? (v?.live ? "实时" : "在线") : offline ? "离线" : "待接入"}
          </span>
        </div>
        <div className="mt-2 flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
          <HostChips s={s} memTotal={v?.memTotal} />
          <Renewal s={s} />
        </div>
        <Rates id={s.id} v={v} />
        <Usage s={s} />
        <Loads v={v} />
        {v && (
          <p className="mt-2.5 flex flex-wrap justify-between gap-x-3 text-xs text-muted-foreground">
            <span>负载 <span className="tabular-nums text-foreground/80">{v.load.map((l) => l.toFixed(2)).join(" ")}</span></span>
            <span className="tabular-nums">{v.tcp} TCP · {v.udp} UDP · 运行 {fmtDuration(v.uptime)}</span>
          </p>
        )}
        <Notes s={s} />
        <div className="mt-auto"><ProbeRows serverId={s.id} className="mt-3 border-t border-border/60 pt-3" /></div>
      </Card>
    </Link>
  );
}
