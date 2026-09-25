import { useEffect, useState } from 'react';

// The panel routes by the URL's fragment (#/players/AB12), so the server
// only ever serves one page and a reload lands where the operator was.

export function currentPath(): string {
  const h = window.location.hash.replace(/^#/, '');
  return h.startsWith('/') ? h : '/overview';
}

export function go(path: string): void {
  window.location.hash = path;
}

export function usePath(): string {
  const [path, setPath] = useState(currentPath);
  useEffect(() => {
    const on = () => {
      setPath(currentPath());
      window.scrollTo(0, 0);
    };
    window.addEventListener('hashchange', on);
    return () => window.removeEventListener('hashchange', on);
  }, []);
  return path;
}

// match splits a path into its first segment and the rest, decoded.
export function match(path: string): [string, string | undefined] {
  const parts = path.split('/').filter(Boolean);
  const rest = parts[1];
  return [parts[0] ?? 'overview', rest === undefined ? undefined : decodeURIComponent(rest)];
}
