import type { ProfileKind } from "@/lib/types";

/** A proxy app a profile can be handed to with one tap. */
export interface ClientApp {
  id: string;
  name: string;
  platforms: string;
  kind: ProfileKind;
  /** What a user of this app has to know that the others do not. */
  note?: string;
  /** Builds the app's import link from the profile address; name is what the profile is called in the app. */
  link: (url: string, name: string) => string;
}

// A user's address is either /s/<token> or the short /r/<code>; a format is a
// path segment on the first and a query on the second. Shadowrocket names a
// profile after the last segment of its address and gets the named form,
// <address>/<format>/<name>, so the profile carries that name instead of the
// user's code.
export function profileURL(base: string, kind: ProfileKind | "raw" | "uri", name?: string): string {
  const u = new URL(base);
  const path = u.pathname.replace(/\/+$/, "");
  if (name) u.pathname = `${path}/${kind}/${encodeURIComponent(name)}`;
  else if (path.startsWith("/r/")) u.searchParams.set("format", kind);
  else u.pathname = `${path}/${kind}`;
  return u.toString();
}

export const CLIENT_APPS: ClientApp[] = [
  { id: "clash", name: "Clash", platforms: "Clash Verge · FlClash · Clash Meta（Windows / macOS / Android / Linux）", kind: "mihomo",
    link: (url, name) => `clash://install-config?url=${encodeURIComponent(profileURL(url, "mihomo"))}&name=${encodeURIComponent(name)}` },
  { id: "shadowrocket", name: "Shadowrocket", platforms: "小火箭（iPhone / iPad）", kind: "shadowrocket",
    note: "换线路在小火箭首页点。它默认不会自己更新配置：到「设置 → 自动更新」里打开配置的“自动后台更新”，线路和规则有变化时才会跟上。",
    link: (url, name) => `shadowrocket://config/add/${profileURL(url, "shadowrocket", name)}` },
  { id: "stash", name: "Stash", platforms: "iPhone / iPad / Mac", kind: "mihomo",
    link: (url, name) => `stash://install-config?url=${encodeURIComponent(profileURL(url, "mihomo"))}&name=${encodeURIComponent(name)}` },
];
