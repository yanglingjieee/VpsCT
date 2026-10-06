import type { RuleKind } from "@/lib/types";

/** A proxy app a profile can be handed to with one tap. */
export interface ClientApp {
  id: string;
  name: string;
  platforms: string;
  kind: RuleKind;
  /** Builds the app's import link from the profile address. */
  link: (url: string, name: string) => string;
}

// A user's address is either /s/<token> or the short /r/<code>; a format is a
// path segment on the first and a query on the second.
export function profileURL(base: string, kind: RuleKind | "raw" | "uri"): string {
  const u = new URL(base);
  if (u.pathname.startsWith("/r/")) u.searchParams.set("format", kind);
  else u.pathname = u.pathname.replace(/\/+$/, "") + "/" + kind;
  return u.toString();
}

export const CLIENT_APPS: ClientApp[] = [
  { id: "clash", name: "Clash", platforms: "Clash Verge · FlClash · Clash Meta（Windows / macOS / Android / Linux）", kind: "mihomo",
    link: (url, name) => `clash://install-config?url=${encodeURIComponent(profileURL(url, "mihomo"))}&name=${encodeURIComponent(name)}` },
  { id: "shadowrocket", name: "Shadowrocket", platforms: "小火箭（iPhone / iPad）", kind: "shadowrocket",
    link: (url) => `shadowrocket://config/add/${profileURL(url, "shadowrocket")}` },
  { id: "stash", name: "Stash", platforms: "iPhone / iPad / Mac", kind: "mihomo",
    link: (url, name) => `stash://install-config?url=${encodeURIComponent(profileURL(url, "mihomo"))}&name=${encodeURIComponent(name)}` },
  { id: "singbox", name: "sing-box", platforms: "iOS / Android / macOS", kind: "singbox",
    link: (url, name) => `sing-box://import-remote-profile?url=${encodeURIComponent(profileURL(url, "singbox"))}#${encodeURIComponent(name)}` },
  { id: "surge", name: "Surge", platforms: "iPhone / iPad / Mac", kind: "surge",
    link: (url) => `surge:///install-config?url=${encodeURIComponent(profileURL(url, "surge"))}` },
];

export const RULE_KIND_LABELS: Record<RuleKind, string> = {
  mihomo: "Clash / Mihomo",
  shadowrocket: "Shadowrocket",
  surge: "Surge",
  singbox: "sing-box",
};
