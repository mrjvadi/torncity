import type { ReactNode } from 'react';
import { useI18n } from '../i18n/index.tsx';
import { foldSeries } from '../lib/chart.ts';
import { delta } from '../lib/format.ts';
import { q } from '../lib/api.ts';
import type { Series } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { Chart, Sparkline, type ChartUnit } from './Chart.tsx';
import { Card, ErrorBox, Loading } from './ui.tsx';

// Stat is a KPI tile: a figure, what it is, and how it moved.
export function Stat({ label, value, trend, before, good, href, hint, tone }: {
  label: string;
  value: ReactNode;
  trend?: number[];
  // before is the figure a period ago, for the change shown under it.
  before?: number;
  now?: number;
  good?: 'up' | 'down';
  href?: string;
  hint?: string;
  tone?: 'bad' | 'warn';
}) {
  const { lang, t } = useI18n();
  const nowV = trend && trend.length ? trend[trend.length - 1] : undefined;
  const d = nowV !== undefined && before !== undefined ? delta(lang, nowV, before) : null;
  const dir = nowV !== undefined && before !== undefined ? Math.sign(nowV - before) : 0;
  const cls = dir === 0 || !good ? 'flat' : (dir > 0) === (good === 'up') ? 'up' : 'down';
  const body = (
    <>
      <div className="stat-label">{label}</div>
      <div className="stat-value">{value}</div>
      <div className="stat-foot">
        {d && (
          <span className={`stat-delta ${cls}`} title={t('stat.vs_before')}>
            {dir > 0 ? '▲' : dir < 0 ? '▼' : '•'} <bdi>{d}</bdi>
          </span>
        )}
        {hint && <span className="muted small">{hint}</span>}
        {trend && <Sparkline values={trend} label={label} />}
      </div>
    </>
  );
  return href ? (
    <a className={`stat ${tone ?? ''}`} href={href}>
      {body}
    </a>
  ) : (
    <div className={`stat ${tone ?? ''}`}>{body}</div>
  );
}

export function StatGrid({ children }: { children: ReactNode }) {
  return <div className="stat-grid">{children}</div>;
}

export const DAY_RANGES = [7, 30, 90, 365];

// RangePicker chooses how many days a chart covers.
export function RangePicker({ days, setDays }: { days: number; setDays: (d: number) => void }) {
  const { t, n } = useI18n();
  return (
    <div className="chips" role="group" aria-label={t('chart.range')}>
      {DAY_RANGES.map((d) => (
        <button key={d} type="button" className="chip" aria-pressed={days === d} onClick={() => setDays(d)}>
          {t('chart.days', { n: d })}
          <span className="sr-only">{n(d)}</span>
        </button>
      ))}
    </div>
  );
}

// SeriesChart loads one server series and charts it.
export function SeriesChart({ name, title, days, kind, stacked, scope, keep, height }: {
  name: string;
  title: string;
  days: number;
  kind: 'line' | 'area' | 'bar';
  stacked?: boolean;
  scope?: Record<string, string>;
  keep?: number;
  height?: number;
}) {
  const { t, val } = useI18n();
  const load = useLoad<Series>(`/api/series/${encodeURIComponent(name)}${q({ days, ...(scope ?? {}) })}`);
  const s = load.data;
  return (
    <Card title={title}>
      {load.error ? (
        <ErrorBox error={load.error} retry={load.reload} />
      ) : !s ? (
        <Loading rows={4} />
      ) : s.lines.length === 0 ? (
        <p className="muted empty-chart">{t('chart.no_data')}</p>
      ) : (
        <Chart
          title={title}
          days={s.days}
          kind={kind}
          stacked={stacked}
          unit={(s.unit === 'money' ? 'money' : s.unit === 'bps' ? 'bps' : 'int') as ChartUnit}
          height={height}
          series={foldSeries(s.lines, keep ?? 7, 'other').map((l) => ({
            key: l.key,
            label: l.key === 'other' ? t('chart.other') : (val(l.key) ?? l.key),
            values: l.values,
          }))}
        />
      )}
    </Card>
  );
}
