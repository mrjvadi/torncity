// The live feed's protocol handling, kept apart from React and from the
// Centrifugo client so it can be tested: where to connect, what a
// publication on panel:ops is, and how the console's live state changes
// with each one.

export const OPS_CHANNEL = 'panel:ops';

export type LiveType = 'audit' | 'flag' | 'hold' | 'change' | 'kpis' | 'health';

export interface LiveEvent {
  type: LiveType;
  at: string;
  data: Record<string, unknown>;
}

const TYPES: ReadonlySet<string> = new Set(['audit', 'flag', 'hold', 'change', 'kpis', 'health']);

// parseEvent reads one publication's data, or null for anything that is not
// a well-formed event.
export function parseEvent(data: unknown): LiveEvent | null {
  if (typeof data !== 'object' || data === null) return null;
  const d = data as Record<string, unknown>;
  if (typeof d.type !== 'string' || !TYPES.has(d.type) || typeof d.at !== 'string') return null;
  if (typeof d.data !== 'object' || d.data === null || Array.isArray(d.data)) return null;
  return { type: d.type as LiveType, at: d.at, data: d.data as Record<string, unknown> };
}

export interface LiveState {
  kpis: Record<string, unknown> | null;
  health: string | null;
  // feed is the most recent events, newest first, at most FEED_SIZE.
  feed: LiveEvent[];
  unseen: number;
  // counters of events by type, so a list can reload when its kind changes.
  ticks: Record<string, number>;
}

export const FEED_SIZE = 50;

export const initialLive: LiveState = { kpis: null, health: null, feed: [], unseen: 0, ticks: {} };

// reduceLive applies one event to the live state.
export function reduceLive(s: LiveState, e: LiveEvent): LiveState {
  const ticks = { ...s.ticks, [e.type]: (s.ticks[e.type] ?? 0) + 1 };
  switch (e.type) {
    case 'kpis': {
      const health = typeof e.data.health === 'string' ? e.data.health : s.health;
      return { ...s, kpis: e.data, health, ticks };
    }
    case 'health':
      return {
        ...s,
        health: typeof e.data.state === 'string' ? e.data.state : s.health,
        feed: [e, ...s.feed].slice(0, FEED_SIZE),
        unseen: s.unseen + 1,
        ticks,
      };
    default:
      return { ...s, feed: [e, ...s.feed].slice(0, FEED_SIZE), unseen: s.unseen + 1, ticks };
  }
}

// wsURL is where the browser connects: an absolute ws(s) URL as given, a
// path on the page's own origin otherwise.
export function wsURL(url: string, loc: { protocol: string; host: string }): string {
  if (/^wss?:\/\//.test(url)) return url;
  const scheme = loc.protocol === 'https:' ? 'wss' : 'ws';
  return `${scheme}://${loc.host}${url.startsWith('/') ? url : `/${url}`}`;
}

// backoff is the wait before reconnect attempt n (from 0): doubling from
// one second, capped, with jitter in [0.5, 1).
export function backoff(n: number, rand: () => number = Math.random): number {
  const base = Math.min(1000 * 2 ** n, 30_000);
  return Math.round(base * (0.5 + rand() / 2));
}
