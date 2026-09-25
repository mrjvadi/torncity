import { useCallback, useEffect, useState } from 'react';
import { get } from './api.ts';

export interface Loaded<T> {
  data: T | null;
  error: unknown;
  loading: boolean;
  reload: () => void;
}

// useLoad reads path (null: nothing yet) and reloads it on demand.
export function useLoad<T>(path: string | null): Loaded<T> {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [loading, setLoading] = useState(false);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (path === null) return;
    let live = true;
    setLoading(true);
    setError(null);
    get<T>(path)
      .then((d) => {
        if (live) setData(d);
      })
      .catch((e: unknown) => {
        if (live) setError(e);
      })
      .finally(() => {
        if (live) setLoading(false);
      });
    return () => {
      live = false;
    };
  }, [path, tick]);
  const reload = useCallback(() => setTick((n) => n + 1), []);
  return { data, error, loading, reload };
}
