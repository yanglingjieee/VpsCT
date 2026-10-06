import * as React from "react";
import { cn } from "@/lib/utils";

export function QR({ text, className }: { text: string; className?: string }) {
  const [svg, setSvg] = React.useState<string>("");
  React.useEffect(() => {
    let alive = true;
    import("@/lib/qr").then((m) => m.toSVG(text)).then((s) => alive && setSvg(s)).catch(() => alive && setSvg(""));
    return () => { alive = false; };
  }, [text]);
  if (!svg) return null;
  return <div className={cn("mx-auto w-48 rounded-xl bg-white p-2", className)}><img className="h-full w-full" alt="二维码" src={`data:image/svg+xml;charset=utf-8,${encodeURIComponent(svg)}`} /></div>;
}
