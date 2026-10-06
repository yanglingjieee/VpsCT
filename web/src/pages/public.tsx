import * as React from "react";
import { Check, Copy, QrCode } from "lucide-react";
import type { PersonalPage, PersonNode } from "@/lib/types";
import { copyText, fmtBytes, fmtDate, STATUS_LABELS } from "@/lib/utils";
import { Button, Card, Progress, Spinner } from "@/components/ui";
import { LogoMark } from "@/components/logo";
import { QR } from "@/components/qr";
import { CLIENT_APPS, profileURL } from "@/lib/clients";

function CopyButton({ text, label = "复制" }: { text: string; label?: string }) {
  const [done, setDone] = React.useState(false);
  return (
    <Button size="sm" variant="outline" onClick={async () => { await copyText(text); setDone(true); window.setTimeout(() => setDone(false), 1500); }}>
      {done ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />} {done ? "已复制" : label}
    </Button>
  );
}

function NodeItem({ node }: { node: PersonNode }) {
  const [qr, setQR] = React.useState(false);
  return (
    <div className="border-b py-3 last:border-0">
      <div className="flex items-center justify-between gap-2">
        <p className="min-w-0 break-words text-sm font-medium">{node.name}</p>
        <div className="flex shrink-0 gap-1.5">
          <Button size="sm" variant="ghost" aria-label="二维码" onClick={() => setQR((v) => !v)}><QrCode className="h-4 w-4" /></Button>
          <CopyButton text={node.uri} />
        </div>
      </div>
      {qr && <QR text={node.uri} className="mt-3" />}
    </div>
  );
}

// The page a user gets when opening their own link in a browser. It lives at
// the link itself, so there is nothing to log in to.
export function PersonalPageView() {
  const [data, setData] = React.useState<PersonalPage | null>(null);
  const [error, setError] = React.useState("");
  const [qr, setQR] = React.useState(false);
  React.useEffect(() => {
    const u = new URL(window.location.href);
    u.searchParams.set("page", "1");
    fetch(u.toString(), { headers: { Accept: "application/json" }, cache: "no-store" })
      .then(async (r) => { if (!r.ok) throw new Error(r.status === 429 ? "打开得太频繁了，稍等一会儿再试" : "这个链接已经失效，请向管理员要一个新的"); return r.json() as Promise<PersonalPage>; })
      .then((d) => { setData(d); document.title = `${d.name} · ${d.site_name}`; })
      .catch((e: Error) => setError(e.message));
  }, []);
  if (error) return <div className="flex min-h-screen items-center justify-center p-6 text-center text-sm text-muted-foreground">{error}</div>;
  if (!data) return <Spinner className="min-h-screen" />;
  const active = data.status === "active";
  const percent = data.quota > 0 ? Math.min(100, (data.used / data.quota) * 100) : 0;
  const apps = CLIENT_APPS.filter((a) => data.formats.includes(a.kind));
  return (
    <div className="mx-auto max-w-xl px-4 py-8">
      <header className="mb-6 flex items-center gap-3">
        <LogoMark size={36} shadow />
        <div className="min-w-0">
          <p className="text-xs text-muted-foreground">{data.site_name}</p>
          <h1 className="break-words text-xl font-semibold">{data.name}</h1>
        </div>
      </header>

      {!active && <Card className="mb-4 border-amber-500/40 bg-amber-500/5 p-4 text-sm">当前状态：{STATUS_LABELS[data.status] ?? data.status}。线路已停用{data.status === "exhausted" && data.next_reset ? `，${fmtDate(data.next_reset, false)} 重置后自动恢复` : "，请联系管理员"}。</Card>}

      <Card className="p-5">
        <p className="text-xs text-muted-foreground">本期已用</p>
        <p className="mt-1 text-3xl font-semibold tabular-nums">{fmtBytes(data.used)}<span className="ml-2 text-sm font-normal text-muted-foreground">{data.quota > 0 ? `/ ${fmtBytes(data.quota)}` : "不限量"}</span></p>
        {data.quota > 0 && <Progress className="mt-3" value={percent} tone={percent >= 95 ? "bad" : percent >= 80 ? "warn" : "ok"} />}
        <p className="mt-3 text-xs text-muted-foreground">
          上传 {fmtBytes(data.upload)} · 下载 {fmtBytes(data.download)}
          {data.next_reset && ` · ${fmtDate(data.next_reset, false)} 重置`}
          {data.expires_at && ` · ${fmtDate(data.expires_at, false)} 到期`}
        </p>
      </Card>

      {data.delivery === "nodes" ? (
        <Card className="mt-4 p-5">
          <div className="mb-1 flex items-center justify-between gap-2">
            <h2 className="font-medium">节点</h2>
            {(data.nodes?.length ?? 0) > 1 && <CopyButton text={data.nodes!.map((n) => n.uri).join("\n")} label="复制全部" />}
          </div>
          <p className="text-xs text-muted-foreground">复制链接或扫码，添加到你自己的客户端里。</p>
          {!data.nodes?.length ? <p className="mt-3 text-sm text-muted-foreground">暂时没有可用的节点</p> : <div className="mt-2">{data.nodes.map((n) => <NodeItem key={n.name} node={n} />)}</div>}
          <div className="mt-4 border-t pt-4">
            <p className="mb-2 text-xs text-muted-foreground">客户端支持订阅的话，也可以填这个地址，节点有变化时会自动更新：</p>
            <div className="flex items-center gap-2"><code className="min-w-0 flex-1 truncate rounded-md bg-muted/50 px-2 py-1.5 text-xs">{profileURL(data.url, "raw")}</code><CopyButton text={profileURL(data.url, "raw")} /></div>
          </div>
        </Card>
      ) : (
        <>
          <Card className="mt-4 p-5">
            <h2 className="font-medium">一键导入</h2>
            <p className="mt-1 text-xs text-muted-foreground">在装好客户端的设备上打开这个页面，点对应的按钮。线路和规则（{data.rules}）会一起配好，以后自动更新。</p>
            <div className="mt-4 grid gap-2">
              {apps.map((a) => (
                <a key={a.id} href={a.link(data.url, data.name)} className="flex items-center justify-between gap-3 rounded-xl border px-4 py-3 transition-colors hover:bg-accent/40">
                  <span className="min-w-0"><span className="block font-medium">{a.name}</span><span className="block break-words text-xs text-muted-foreground">{a.platforms}</span></span>
                  <span className="shrink-0 text-sm font-medium text-primary">导入</span>
                </a>
              ))}
            </div>
            <div className="mt-4 border-t pt-4">
              <p className="mb-2 text-xs text-muted-foreground">按钮没反应，或者用的是别的客户端：把下面的地址填到客户端的「订阅」里。</p>
              <div className="flex items-center gap-2">
                <code className="min-w-0 flex-1 truncate rounded-md bg-muted/50 px-2 py-1.5 text-xs">{data.url}</code>
                <Button size="sm" variant="ghost" aria-label="二维码" onClick={() => setQR((v) => !v)}><QrCode className="h-4 w-4" /></Button>
                <CopyButton text={data.url} />
              </div>
              {qr && <QR text={data.url} className="mt-3" />}
            </div>
          </Card>
          <Card className="mt-4 p-5">
            <h2 className="font-medium">线路</h2>
            {!data.lines.length ? <p className="mt-2 text-sm text-muted-foreground">暂时没有线路</p> : (
              <ul className="mt-2">
                {data.lines.map((l) => (
                  <li key={l.name} className="flex items-center justify-between gap-3 border-b py-2.5 text-sm last:border-0">
                    <span className="min-w-0 break-words">{l.name}</span>
                    <span className="shrink-0 tabular-nums text-muted-foreground">{l.total > 0 ? fmtBytes(l.total) : "—"}</span>
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </>
      )}
      <p className="mt-6 text-center text-xs text-muted-foreground">这个页面的地址就是你的凭据，不要发给别人。</p>
    </div>
  );
}
