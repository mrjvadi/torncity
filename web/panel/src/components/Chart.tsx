import { useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent } from 'react';
import { useI18n } from '../i18n/index.tsx';
import { niceScale, stackSeries } from '../lib/chart.ts';

// A small SVG chart: lines, areas (stacked or not) or bars (stacked or
// grouped) over days. Colours are the validated categorical slots
// (--series-1…8, in fixed order: a series keeps its colour when others are
// hidden); a legend is always there for two series or more, and toggles
// them; a crosshair and tooltip follow the pointer or the arrow keys; the
// same numbers are one click away as a table. Time runs left to right in
// both languages; in Persian the value axis sits on the right.

export interface ChartSeries {
  key: string;
  label: string;
  values: number[];
}

export type ChartUnit = 'int' | 'money' | 'bps';

export function Chart({ title, days, series, kind, stacked, unit, height }: {
  title: string;
  days: string[];
  series: ChartSeries[];
  kind: 'line' | 'area' | 'bar';
  stacked?: boolean;
  unit: ChartUnit;
  height?: number;
}) {
  const { t, n, nc, pct, dayLabel, dir } = useI18n();
  const wrap = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(640);
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const [hover, setHover] = useState<number | null>(null);
  const id = useId();
  const h = height ?? 240;

  useEffect(() => {
    const el = wrap.current;
    if (!el || typeof ResizeObserver === 'undefined') return;
    const ro = new ResizeObserver((entries) => {
      const w = entries[0]?.contentRect.width;
      if (w && w > 0) setWidth(Math.round(w));
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  const colour = (key: string) => {
    const i = series.findIndex((s) => s.key === key);
    return `var(--series-${(i % 8) + 1})`;
  };
  const shown = series.filter((s) => !hidden.has(s.key));
  const fmt = (v: number) => (unit === 'bps' ? pct(v) : n(v));
  const fmtAxis = (v: number) => (unit === 'bps' ? pct(v) : nc(v));

  const geo = useMemo(() => {
    const rtl = dir === 'rtl';
    const axisW = 56;
    const padL = rtl ? 12 : axisW;
    const padR = rtl ? axisW : 12;
    const padT = 12;
    const padB = 28;
    const plotW = Math.max(width - padL - padR, 40);
    const plotH = Math.max(h - padT - padB, 40);
    const layers = stacked ? stackSeries(shown.map((s) => s.values)) : shown.map((s) => s.values.map((v) => [0, v] as [number, number]));
    let lo = 0;
    let hi = 0;
    for (const layer of layers) for (const [a, b] of layer) {
      lo = Math.min(lo, a, b);
      hi = Math.max(hi, a, b);
    }
    const scale = niceScale(lo, hi, 4, unit === 'int');
    const count = days.length;
    const band = plotW / Math.max(count, 1);
    const x = (i: number) => (kind === 'bar' ? padL + band * i + band / 2 : padL + (count <= 1 ? plotW / 2 : (plotW * i) / (count - 1)));
    const y = (v: number) => padT + plotH - ((v - scale.min) / (scale.max - scale.min || 1)) * plotH;
    return { rtl, padL, padR, padT, padB, plotW, plotH, layers, scale, band, x, y, count };
  }, [width, h, dir, stacked, shown, days.length, kind, unit]);

  const { padL, padT, plotW, plotH, layers, scale, band, x, y, count } = geo;
  const labelEvery = Math.max(1, Math.ceil(count / Math.max(2, Math.floor(plotW / 70))));

  const pick = (e: PointerEvent<SVGSVGElement>) => {
    const rect = e.currentTarget.getBoundingClientRect();
    const px = ((e.clientX - rect.left) / rect.width) * width;
    let best = 0;
    let dist = Infinity;
    for (let i = 0; i < count; i++) {
      const d = Math.abs(x(i) - px);
      if (d < dist) {
        dist = d;
        best = i;
      }
    }
    setHover(best);
  };
  const keys = (e: KeyboardEvent<SVGSVGElement>) => {
    if (e.key === 'ArrowRight') setHover((i) => Math.min((i ?? -1) + 1, count - 1));
    else if (e.key === 'ArrowLeft') setHover((i) => Math.max((i ?? count) - 1, 0));
    else if (e.key === 'Escape') setHover(null);
    else return;
    e.preventDefault();
  };

  const toggle = (key: string) =>
    setHidden((s) => {
      const next = new Set(s);
      if (next.has(key)) next.delete(key);
      else if (series.length - next.size > 1) next.add(key);
      return next;
    });

  const barW = Math.max(2, Math.min(28, band * 0.7 / (stacked ? 1 : Math.max(shown.length, 1))));
  const summary = `${title}: ${shown.map((s) => `${s.label} ${fmt(s.values.reduce((a, b) => a + b, 0))}`).join('، ')}`;

  return (
    <figure className="chart" aria-labelledby={`${id}-cap`}>
      <figcaption id={`${id}-cap`} className="sr-only">
        {title}
      </figcaption>
      {series.length > 1 && (
        <ul className="legend" aria-label={t('chart.legend')}>
          {series.map((s) => (
            <li key={s.key}>
              <button type="button" aria-pressed={!hidden.has(s.key)} onClick={() => toggle(s.key)} className="legend-item">
                <span className="swatch" style={{ background: colour(s.key) }} aria-hidden="true" />
                <span className={hidden.has(s.key) ? 'strike' : undefined}>{s.label}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
      <div className="chart-plot" ref={wrap}>
        <svg
          width="100%"
          height={h}
          viewBox={`0 0 ${width} ${h}`}
          role="img"
          aria-label={summary}
          tabIndex={0}
          onPointerMove={pick}
          onPointerLeave={() => setHover(null)}
          onKeyDown={keys}
          onBlur={() => setHover(null)}
        >
          {scale.ticks.map((v) => (
            <g key={v}>
              <line x1={padL} x2={padL + plotW} y1={y(v)} y2={y(v)} className={v === 0 ? 'axis-zero' : 'grid-line'} />
              <text x={geo.rtl ? padL + plotW + 6 : padL - 6} y={y(v)} className="axis-label" textAnchor={geo.rtl ? 'start' : 'end'} dominantBaseline="middle">
                {fmtAxis(v)}
              </text>
            </g>
          ))}
          {days.map((d, i) =>
            i % labelEvery === 0 || i === count - 1 ? (
              <text key={d} x={x(i)} y={padT + plotH + 18} className="axis-label" textAnchor="middle">
                {dayLabel(d)}
              </text>
            ) : null,
          )}
          {kind === 'bar' &&
            layers.map((layer, si) => {
              const s = shown[si];
              if (!s) return null;
              return (
                <g key={s.key} fill={colour(s.key)}>
                  {layer.map(([a, b], i) => {
                    const top = y(Math.max(a, b));
                    const bottom = y(Math.min(a, b));
                    const off = stacked ? -barW / 2 : -((shown.length * barW) / 2) + si * barW;
                    return b === a ? null : (
                      <rect key={i} x={x(i) + off} y={top} width={Math.max(barW - 1, 1)} height={Math.max(bottom - top, 1)} rx={2} className="bar" />
                    );
                  })}
                </g>
              );
            })}
          {kind !== 'bar' &&
            layers.map((layer, si) => {
              const s = shown[si];
              if (!s) return null;
              const top = layer.map(([, b], i) => `${i === 0 ? 'M' : 'L'}${x(i)},${y(b)}`).join('');
              const base = layer
                .map(([a], i) => [x(i), y(a)] as const)
                .reverse()
                .map(([px, py]) => `L${px},${py}`)
                .join('');
              return (
                <g key={s.key}>
                  {kind === 'area' && <path d={`${top}${base}Z`} fill={colour(s.key)} className="area" />}
                  <path d={top} fill="none" stroke={colour(s.key)} className="line" />
                </g>
              );
            })}
          {hover !== null && hover < count && (
            <g className="crosshair">
              <line x1={x(hover)} x2={x(hover)} y1={padT} y2={padT + plotH} />
              {kind !== 'bar' &&
                layers.map((layer, si) => {
                  const s = shown[si];
                  const p = layer[hover];
                  return s && p ? <circle key={s.key} cx={x(hover)} cy={y(p[1])} r={4} fill={colour(s.key)} className="dot" /> : null;
                })}
            </g>
          )}
        </svg>
        {hover !== null && hover < count && (
          <div
            className="chart-tip"
            role="status"
            style={{
              left: `${Math.min(Math.max((x(hover) / width) * 100, 12), 88)}%`,
            }}
          >
            <strong>{dayLabel(days[hover] ?? '', 'long')}</strong>
            <ul>
              {shown.map((s) => (
                <li key={s.key}>
                  <span className="swatch" style={{ background: colour(s.key) }} aria-hidden="true" />
                  <span>{s.label}</span>
                  <span className="tip-val">{fmt(s.values[hover] ?? 0)}</span>
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
      <details className="chart-table">
        <summary>{t('chart.as_table')}</summary>
        <div className="table-wrap">
          <table className="table compact">
            <thead>
              <tr>
                <th scope="col">{t('chart.day')}</th>
                {shown.map((s) => (
                  <th key={s.key} scope="col" className="num">
                    {s.label}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {days.map((d, i) => (
                <tr key={d}>
                  <td>{dayLabel(d, 'long')}</td>
                  {shown.map((s) => (
                    <td key={s.key} className="num">
                      {fmt(s.values[i] ?? 0)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </details>
    </figure>
  );
}

// Sparkline is a tiny line for a stat tile.
export function Sparkline({ values, label }: { values: number[]; label: string }) {
  if (values.length < 2) return null;
  const lo = Math.min(...values);
  const hi = Math.max(...values);
  const w = 96;
  const h = 28;
  const pts = values
    .map((v, i) => `${(i / (values.length - 1)) * w},${h - 2 - ((v - lo) / (hi - lo || 1)) * (h - 4)}`)
    .join(' ');
  return (
    <svg className="spark" width={w} height={h} viewBox={`0 0 ${w} ${h}`} role="img" aria-label={label}>
      <polyline points={pts} fill="none" stroke="var(--series-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  );
}
