import { Centrifuge, UnauthorizedError, type Subscription } from 'centrifuge';
import { createContext, useCallback, useContext, useEffect, useReducer, useRef, useState, type ReactNode } from 'react';
import { ApiError, get } from './api.ts';
import { backoff, initialLive, OPS_CHANNEL, parseEvent, reduceLive, wsURL, type LiveEvent, type LiveState } from './realtime.ts';

// The live feed in the page: one Centrifugo connection per signed-in tab,
// subscribed to panel:ops with tokens the panel issues to this session. The
// client reconnects with backoff and asks for fresh tokens before they
// expire. When the server has no live feed (404 realtime_off) the console
// polls instead: lists refresh when asked, the dashboard on a timer.

export type LiveStatus = 'off' | 'connecting' | 'connected' | 'disconnected';

interface Live {
  status: LiveStatus;
  state: LiveState;
  seen: () => void;
}

const Ctx = createContext<Live>({ status: 'off', state: initialLive, seen: () => {} });

type Action = { kind: 'event'; e: LiveEvent } | { kind: 'seen' };

function reducer(s: LiveState, a: Action): LiveState {
  return a.kind === 'seen' ? { ...s, unseen: 0 } : reduceLive(s, a.e);
}

async function token(path: string): Promise<string> {
  try {
    return (await get<{ token: string }>(path)).token;
  } catch (e) {
    if (e instanceof ApiError && (e.status === 401 || e.status === 403)) throw new UnauthorizedError('');
    throw e;
  }
}

export function LiveProvider({ enabled, children, onEvent }: {
  enabled: boolean;
  children: ReactNode;
  onEvent?: (e: LiveEvent) => void;
}) {
  const [status, setStatus] = useState<LiveStatus>('off');
  const [state, dispatch] = useReducer(reducer, initialLive);
  const onEventRef = useRef(onEvent);
  onEventRef.current = onEvent;

  useEffect(() => {
    if (!enabled) return;
    let client: Centrifuge | null = null;
    let sub: Subscription | null = null;
    let stopped = false;
    let attempt = 0;
    let timer = 0;
    const start = async () => {
      let first: { token: string; url: string };
      try {
        first = await get<{ token: string; url: string }>('/api/realtime/token');
      } catch (e) {
        if (e instanceof ApiError && (e.status === 404 || e.status === 401)) {
          setStatus('off');
          return;
        }
        setStatus('disconnected');
        if (!stopped) timer = window.setTimeout(() => void start(), backoff(attempt++));
        return;
      }
      if (stopped) return;
      client = new Centrifuge(wsURL(first.url, window.location), {
        token: first.token,
        getToken: () => token('/api/realtime/token'),
        minReconnectDelay: 1000,
        maxReconnectDelay: 30000,
      });
      client.on('connecting', () => setStatus('connecting'));
      client.on('connected', () => setStatus('connected'));
      client.on('disconnected', () => setStatus('disconnected'));
      sub = client.newSubscription(OPS_CHANNEL, {
        getToken: () => token(`/api/realtime/subscribe?channel=${encodeURIComponent(OPS_CHANNEL)}`),
      });
      sub.on('publication', (ctx) => {
        const e = parseEvent(ctx.data);
        if (!e) return;
        dispatch({ kind: 'event', e });
        onEventRef.current?.(e);
      });
      sub.subscribe();
      client.connect();
    };
    void start();
    return () => {
      stopped = true;
      window.clearTimeout(timer);
      sub?.unsubscribe();
      client?.disconnect();
      setStatus('off');
    };
  }, [enabled]);

  const seen = useCallback(() => dispatch({ kind: 'seen' }), []);
  return <Ctx.Provider value={{ status, state, seen }}>{children}</Ctx.Provider>;
}

export function useLive(): Live {
  return useContext(Ctx);
}

// useLiveReload calls reload (at most once a second) whenever an event of
// one of the types arrives.
export function useLiveReload(types: string[], reload: () => void): void {
  const { state } = useLive();
  const key = types.map((t) => `${t}:${state.ticks[t] ?? 0}`).join(',');
  const first = useRef(true);
  const last = useRef(0);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    if (types.length === 0) return;
    const wait = Math.max(0, 1000 - (Date.now() - last.current));
    const id = window.setTimeout(() => {
      last.current = Date.now();
      reload();
    }, wait);
    return () => window.clearTimeout(id);
  }, [key]); // eslint-disable-line react-hooks/exhaustive-deps
}
