import { useState } from 'react';
import { Select } from '../components/Action.tsx';
import { Async, Card, Money, PageTitle } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import type { Flow, Overview as O } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';

function Flows({ rows, total }: { rows: Flow[] | null; total?: number }) {
  const { t } = useI18n();
  const list = rows ?? [];
  const sum = total ?? list.reduce((s, f) => s + f.amount, 0);
  const max = Math.max(1, ...list.map((f) => Math.abs(f.amount)));
  return (
    <ul className="bars">
      {list.map((f) => (
        <li key={f.reason}>
          <span className="bar-label">
            <bdi dir="ltr">{f.reason}</bdi>
          </span>
          <span className="bar-track" aria-hidden="true">
            <span className="bar" style={{ inlineSize: `${(Math.abs(f.amount) / max) * 100}%` }} />
          </span>
          <Money v={f.amount} />
        </li>
      ))}
      <li className="bar-total">
        <span className="bar-label">{t('overview.total')}</span>
        <span />
        <Money v={sum} />
      </li>
    </ul>
  );
}

export function Overview() {
  const { t, n, pct } = useI18n();
  const [days, setDays] = useState('7');
  const load = useLoad<O>(`/api/overview?days=${days}`);
  const index = (v: number) => (v === 0 ? t('overview.no_trades') : pct(v));
  return (
    <>
      <PageTitle
        actions={
          <div className="inline">
            <Select
              label={t('overview.window')}
              value={days}
              onChange={setDays}
              options={['1', '7', '30', '90'].map((d) => [d, `${n(Number(d))} ${t('common.days')}`])}
            />
            <button type="button" className="btn" onClick={load.reload}>
              {t('common.refresh')}
            </button>
          </div>
        }
      >
        {t('overview.title')}
      </PageTitle>
      <Async load={load}>
        {(o) => {
          const inflow = (o.faucets ?? []).reduce((s, f) => s + f.amount, 0);
          const outflow = (o.drains ?? []).reduce((s, f) => s + f.amount, 0);
          const stats: [string, number, string?][] = [
            [t('overview.players'), o.counts.players],
            [t('overview.active_15m'), o.counts.active_15m],
            [t('overview.active_24h'), o.counts.active_24h],
            [t('overview.companies'), o.counts.companies, '#/companies'],
            [t('overview.cities'), o.counts.cities, '#/cities'],
            [t('overview.open_flags'), o.counts.open_flags, '#/watch'],
            [t('overview.held_payments'), o.counts.held_payments, '#/watch'],
          ];
          return (
            <>
              <div className="stats">
                {stats.map(([label, v, href]) => {
                  const body = (
                    <>
                      <span className="stat-value">{n(v)}</span>
                      <span className="stat-label">{label}</span>
                    </>
                  );
                  return href ? (
                    <a key={label} className="stat" href={href}>
                      {body}
                    </a>
                  ) : (
                    <div key={label} className="stat">
                      {body}
                    </div>
                  );
                })}
                <div className="stat">
                  <span className="stat-value">{index(o.price_index_bps)}</span>
                  <span className="stat-label">
                    {t('overview.price_index')} · {t('overview.prior_index')}: {index(o.prior_index_bps)}
                  </span>
                </div>
                <div className="stat">
                  <span className={`stat-value ${inflow - outflow < 0 ? 'neg' : ''}`}>{n(inflow - outflow)}</span>
                  <span className="stat-label">{t('overview.net')}</span>
                </div>
              </div>
              <div className="grid-2">
                <Card title={t('overview.faucets')}>
                  <Flows rows={o.faucets} />
                </Card>
                <Card title={t('overview.drains')}>
                  <Flows rows={o.drains} />
                </Card>
              </div>
              <Card title={t('overview.supply')}>
                <Flows rows={o.supply} total={o.total} />
              </Card>
            </>
          );
        }}
      </Async>
    </>
  );
}
