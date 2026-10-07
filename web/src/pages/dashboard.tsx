import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Activity, AlertTriangle, CalendarDays, Info, Server as ServerIcon, Users2, XCircle } from "lucide-react";
import { get } from "@/lib/api";
import type { Series, Server, Share } from "@/lib/types";
import { cn, fmtBytes, STATUS_LABELS } from "@/lib/utils";
import { Button, Card, CardContent, CardHeader, CardTitle, Empty, PageHeader, Progress, SectionTitle, Spinner, Stat } from "@/components/ui";
import { TrafficBars } from "@/components/charts";
import { ServerCard, sortServers } from "@/components/server-card";

interface Dash {
  counts: { servers: number; servers_online: number; shares: number; shares_active: number; lines: number };
  traffic: { servers_30d: Series; today_up: number; today_down: number; month_up: number; month_down: number };
  alerts: { level: "info" | "warn" | "error"; kind: string; message: string; server_id?: number; share_id?: number }[];
  servers: Server[];
  users: Share[];
}

export function DashboardPage() {
  const q = useQuery({ queryKey: ["dashboard"], queryFn: () => get<Dash>("/api/v1/dashboard"), refetchInterval: 10000 });
  if (q.isLoading || !q.data) return <Spinner />;
  const d = q.data;
  if (!d.servers.length) {
    return <Empty title="先添加一台服务器" description="添加服务器并装上 agent 后，这里会显示它的状态；再到「节点」里建入站和线路，到「用户」里发给人用。" action={<Link to="/servers"><Button>去添加服务器</Button></Link>} />;
  }
  const users = [...d.users].sort((a, b) => b.used_upload + b.used_download - (a.used_upload + a.used_download));
  const peak = Math.max(1, ...users.map((u) => u.used_upload + u.used_download));
  return (
    <div>
      <PageHeader title="总览" />
      {d.alerts.length > 0 && (
        <div className="mb-4 grid gap-2">
          {d.alerts.map((a, i) => {
            const Icon = a.level === "error" ? XCircle : a.level === "warn" ? AlertTriangle : Info;
            const to = a.server_id ? `/servers/${a.server_id}` : a.share_id ? `/users/${a.share_id}` : "";
            const body = <span className="inline-flex min-w-0 items-start gap-2"><Icon className="mt-0.5 h-4 w-4 shrink-0" /><span className="break-words">{a.message}</span></span>;
            return (
              <div key={i} className={cn("rounded-xl border px-3 py-2 text-sm", a.level === "error" ? "border-rose-500/40 bg-rose-500/5 text-rose-700 dark:text-rose-300" : a.level === "warn" ? "border-amber-500/40 bg-amber-500/5 text-amber-700 dark:text-amber-300" : "border-sky-500/40 bg-sky-500/5 text-sky-700 dark:text-sky-300")}>
                {to ? <Link to={to} className="hover:underline">{body}</Link> : body}
              </div>
            );
          })}
        </div>
      )}
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Stat tint="mint" icon={<ServerIcon />} label="服务器在线" value={`${d.counts.servers_online} / ${d.counts.servers}`} />
        <Stat tint="sky" icon={<Activity />} label="今日流量" value={fmtBytes(d.traffic.today_up + d.traffic.today_down)} />
        <Stat tint="lavender" icon={<CalendarDays />} label="近 30 天流量" value={fmtBytes(d.traffic.month_up + d.traffic.month_down)} />
        <Stat tint="peach" icon={<Users2 />} label="用户 / 线路" value={`${d.counts.shares_active} / ${d.counts.lines}`} sub={d.counts.shares > d.counts.shares_active ? `${d.counts.shares - d.counts.shares_active} 个用户未在使用` : undefined} />
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-3">
        <Card className="lg:col-span-2">
          <CardHeader><CardTitle>近 30 天流量（全部服务器）</CardTitle></CardHeader>
          <CardContent>{d.traffic.servers_30d?.has_data ? <TrafficBars points={d.traffic.servers_30d.points} /> : <p className="text-sm text-muted-foreground">暂无用量记录</p>}</CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>用户用量（本期）</CardTitle></CardHeader>
          <CardContent>
            {!users.length ? <p className="text-sm text-muted-foreground">还没有用户，<Link to="/users" className="text-primary hover:underline">去新建</Link></p> : (
              <ul className="space-y-3">
                {users.map((u) => {
                  const used = u.used_upload + u.used_download;
                  return (
                    <li key={u.id}>
                      <Link to={`/users/${u.id}`} className="block rounded-lg hover:bg-accent/40">
                        <div className="mb-1 flex items-baseline justify-between gap-2 text-sm">
                          <span className="min-w-0 break-words">{u.name}{u.status !== "active" && <span className="ml-2 text-xs text-amber-600 dark:text-amber-400">{STATUS_LABELS[u.status]}</span>}</span>
                          <span className="shrink-0 tabular-nums text-muted-foreground">{fmtBytes(used)}{u.quota_bytes > 0 && ` / ${fmtBytes(u.quota_bytes, 0)}`}</span>
                        </div>
                        <Progress value={u.quota_bytes > 0 ? u.usage.percent : (used / peak) * 100} />
                      </Link>
                    </li>
                  );
                })}
              </ul>
            )}
          </CardContent>
        </Card>
      </div>

      <SectionTitle className="mt-8">服务器</SectionTitle>
      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">{sortServers(d.servers).map((s) => <ServerCard key={s.id} s={s} />)}</div>
    </div>
  );
}
