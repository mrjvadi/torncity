import { useEffect, useState } from 'react';

// The panel routes by the URL's fragment (#/players/AB12CDE/ledger?tx=…),
// so the server only ever serves one page and a reload lands where the
// operator was. A route is its path segments and its query.

export interface Route {
  segments: string[];
  query: URLSearchParams;
  path: string;
}

export function parseRoute(hash: string): Route {
  const h = hash.replace(/^#/, '');
  const [p = '', qs = ''] = h.split('?', 2);
  const path = p.startsWith('/') ? p : '/overview';
  const segments = path
    .split('/')
    .filter(Boolean)
    .map((s) => {
      try {
        return decodeURIComponent(s);
      } catch {
        return s;
      }
    });
  if (segments.length === 0) segments.push('overview');
  return { segments, query: new URLSearchParams(qs), path };
}

export function currentRoute(): Route {
  return parseRoute(window.location.hash);
}

export function go(path: string): void {
  window.location.hash = path;
}

// href builds a fragment link from segments (each encoded) and a query.
export function href(segments: (string | number)[], query?: Record<string, string | number | undefined>): string {
  const p = '#/' + segments.map((s) => encodeURIComponent(String(s))).join('/');
  if (!query) return p;
  const u = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) if (v !== undefined && v !== '') u.set(k, String(v));
  const s = u.toString();
  return s ? `${p}?${s}` : p;
}

export function useRoute(): Route {
  const [route, setRoute] = useState(currentRoute);
  useEffect(() => {
    let lastPath = currentRoute().path;
    const on = () => {
      const r = currentRoute();
      setRoute(r);
      if (r.path !== lastPath) window.scrollTo(0, 0);
      lastPath = r.path;
    };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return route;
}

// match splits a path into its first segment and the rest (kept for the
// pages written against it).
export function match(path: string): [string, string | undefined] {
  const r = parseRoute(path);
  return [r.segments[0] ?? 'overview', r.segments[1]];
}
