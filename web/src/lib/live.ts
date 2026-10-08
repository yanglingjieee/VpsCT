// What the servers are doing right now. The panel loads the whole picture
// once, then keeps it up to date from the event stream: a reading from each
// server every second and each probe result as it comes back. Components
// subscribe to one server, so a reading redraws that server's card only.
import { useSyncExternalStore } from "react";
import { get } from "@/lib/api";

/** One reading of a host. Rates are bytes per second. */
export interface LiveSample {
  c: number; // CPU percent
  mu: number;
  mt: number;
  su?: number;
  st?: number;
  du: number;
  dt: number;
  l1: number;
  l5: number;
  l15: number;
  rx: number;
  tx: number;
  tc: number;
  uc: number;
  up: number; // seconds since boot
}

export interface LiveHost {
  os?: string;
  kernel?: string;
  arch?: string;
  virt?: string;
  cpu_model?: string;
  cores?: number;
}

export type Carrier = "" | "ct" | "cu" | "cm";

export interface ProbeTarget {
  id: number;
  name: string;
  host: string;
  port: number;
  carrier: Carrier;
  on_card: boolean;
  sort_order: number;
}

/** One minute of one server's probes of one target; times in microseconds. */
export interface ProbeMinute {
  ts: number; // unix seconds
  sent: number;
  lost: number;
  rtt_sum: number;
  rtt_min: number;
  rtt_max: number;
}

export interface LiveProbe {
  target_id: number;
  /** The newest probe: microseconds, -1 when it was lost, 0 before the first. */
  us: number;
  at?: number;
  /** The last half hour by the minute, the running minute last. */
  strip: ProbeMinute[];
}

export interface Trail {
  t: number;
  rx: number;
  tx: number;
  c: number;
}

export interface LiveServer {
  /** The server's worker is sending readings now. */
  connected: boolean;
  at?: number;
  sample?: LiveSample;
  host?: LiveHost;
  trail: Trail[];
  probes: LiveProbe[];
}

interface Snapshot {
  interval_ms: number;
  probe_interval: number;
  targets: ProbeTarget[];
  servers: Record<string, LiveServer>;
}

interface LiveEvent {
  s?: Record<string, LiveSample>;
  p?: { sid: number; tid: number; us: number; m: ProbeMinute }[];
  c?: Record<string, boolean>;
}

const TRAIL = 120;
const STRIP = 31;

let servers = new Map<number, LiveServer>();
let targets: ProbeTarget[] = [];
const watchers = new Map<number, Set<() => void>>();
const targetWatchers = new Set<() => void>();

function changed(id: number) {
  watchers.get(id)?.forEach((fn) => fn());
}

function watch(id: number, fn: () => void) {
  let set = watchers.get(id);
  if (!set) watchers.set(id, (set = new Set()));
  set.add(fn);
  return () => {
    set.delete(fn);
  };
}

/** Load everything the panel holds; called when the event stream opens. */
export async function loadLive() {
  const snap = await get<Snapshot>("/api/v1/live");
  servers = new Map(Object.entries(snap.servers).map(([id, s]) => [Number(id), s]));
  targets = snap.targets;
  watchers.forEach((_, id) => changed(id));
  targetWatchers.forEach((fn) => fn());
}

/** The stream is gone: nothing is known to be current until it is back. */
export function dropLive() {
  servers.forEach((s, id) => {
    if (!s.connected) return;
    servers.set(id, { ...s, connected: false });
    changed(id);
  });
}

export function applyLive(e: LiveEvent) {
  const now = Date.now();
  const touched = new Set<number>();
  const edit = (id: number): LiveServer => {
    const next = { ...(servers.get(id) ?? { connected: false, trail: [], probes: [] }) };
    servers.set(id, next);
    touched.add(id);
    return next;
  };
  for (const [key, on] of Object.entries(e.c ?? {})) edit(Number(key)).connected = on;
  for (const [key, sample] of Object.entries(e.s ?? {})) {
    const s = edit(Number(key));
    s.sample = sample;
    s.at = now;
    s.connected = true;
    s.trail = [...s.trail.slice(-(TRAIL - 1)), { t: now, rx: sample.rx, tx: sample.tx, c: sample.c }];
  }
  for (const p of e.p ?? []) {
    const s = edit(p.sid);
    const old = s.probes.find((x) => x.target_id === p.tid);
    const strip = [...(old?.strip ?? []).filter((m) => m.ts !== p.m.ts), p.m].slice(-STRIP);
    const probe: LiveProbe = { target_id: p.tid, us: p.us, at: now, strip };
    s.probes = old ? s.probes.map((x) => (x === old ? probe : x)) : [...s.probes, probe];
  }
  touched.forEach(changed);
}

/** One server's live state; undefined until its worker has ever reported. */
export function useLive(id: number): LiveServer | undefined {
  return useSyncExternalStore(
    (fn) => watch(id, fn),
    () => servers.get(id),
  );
}

export function useProbeTargets(): ProbeTarget[] {
  return useSyncExternalStore(
    (fn) => {
      targetWatchers.add(fn);
      return () => {
        targetWatchers.delete(fn);
      };
    },
    () => targets,
  );
}

/** What a strip of minutes came to: probes sent and lost, mean round trip in ms. */
export function summarize(strip: ProbeMinute[]) {
  let sent = 0;
  let lost = 0;
  let sum = 0;
  for (const m of strip) {
    sent += m.sent;
    lost += m.lost;
    sum += m.rtt_sum;
  }
  const answered = sent - lost;
  return { sent, lost, loss: sent > 0 ? (lost / sent) * 100 : null, avg: answered > 0 ? sum / answered / 1000 : null };
}
