// The arithmetic behind the charts, kept apart so it can be tested without
// a browser.

export interface Scale {
  min: number;
  max: number;
  ticks: number[];
}

// niceScale covers lo..hi with about `count` round steps (1, 2, 2.5 or 5
// times a power of ten), always including zero; for counts, whole steps.
export function niceScale(lo: number, hi: number, count: number, integer = false): Scale {
  lo = Math.min(lo, 0);
  hi = Math.max(hi, 0);
  if (hi === lo) hi = lo + 1;
  const raw = (hi - lo) / Math.max(count, 1);
  const pow = 10 ** Math.floor(Math.log10(raw));
  let step = [1, 2, 2.5, 5, 10].map((m) => m * pow).find((s) => s >= raw) ?? 10 * pow;
  if (integer) step = Math.max(1, Math.ceil(step));
  const min = Math.floor(lo / step) * step;
  const max = Math.ceil(hi / step) * step;
  const ticks: number[] = [];
  for (let v = min; v <= max + step / 2; v += step) ticks.push(Math.round(v * 1e6) / 1e6);
  return { min, max, ticks };
}

// stackSeries turns values into [base, top] pairs, each layer on the one
// before it; positive and negative values stack apart.
export function stackSeries(series: number[][]): [number, number][][] {
  const len = Math.max(0, ...series.map((s) => s.length));
  const pos = new Array<number>(len).fill(0);
  const neg = new Array<number>(len).fill(0);
  return series.map((s) =>
    Array.from({ length: len }, (_, i) => {
      const v = s[i] ?? 0;
      if (v >= 0) {
        const base = pos[i] ?? 0;
        pos[i] = base + v;
        return [base, base + v] as [number, number];
      }
      const base = neg[i] ?? 0;
      neg[i] = base + v;
      return [base, base + v] as [number, number];
    }),
  );
}

export interface Line {
  key: string;
  values: number[];
  total?: number;
}

// foldSeries keeps the `keep` largest lines and folds the rest into one
// named `other` — a ninth colour is never invented.
export function foldSeries<T extends Line>(lines: T[], keep: number, other: string): Line[] {
  if (lines.length <= keep + 1) return lines;
  const total = (l: Line) => l.total ?? l.values.reduce((a, b) => a + Math.abs(b), 0);
  const sorted = [...lines].sort((a, b) => total(b) - total(a));
  const head = sorted.slice(0, keep);
  const rest = sorted.slice(keep);
  const len = Math.max(...rest.map((l) => l.values.length));
  const values = Array.from({ length: len }, (_, i) => rest.reduce((a, l) => a + (l.values[i] ?? 0), 0));
  return [...head, { key: other, values, total: values.reduce((a, b) => a + b, 0) }];
}

// sum adds a list.
export function sum(values: number[]): number {
  return values.reduce((a, b) => a + b, 0);
}
