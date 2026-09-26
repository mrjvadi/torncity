import { useEffect, useState } from 'react';
import { Chart } from '../components/Chart.tsx';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Card, ErrorBox, Grid, Loading, Money, PageHeader, Ref, Status, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { foldSeries } from '../lib/chart.ts';
import { useLive } from '../lib/live.tsx';
import type { EconomySeries, Rec, Series, SwitchesView } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';

// The dashboard: the world's figures now (pushed live, or polled), how the
// money and the players moved over the chosen days, and what happened last.

const num = (r: Rec | null | undefined, k: string): number => {
  const v = r?.[k];
  return typeof v === 'number' ? v : 0;
};

export function Overview() {
  const { t, n, nc, pct, val } = useI18n();
  const [days, setDays] = useState(30);
  const live = useLive();
  const kpiLoad = useLoad<Rec>('/api/kpis');
  const switchesLoad = useLoad<SwitchesView>('/api/switches');
  const econ = useLoad<EconomySeries>(`/api/series/economy?days=${days}`);
  const newPlayers = useLoad<Series>(`/api/series/players.new?days=${days}`);

  // Without the live feed the figures are read again every minute.
  useEffect(() => {
    if (live.status === 'connected') return;
    const id = window.setInterval(kpiLoad.reload, 60_000);
    return () => window.clearInterval(id);
  }, [live.status, kpiLoad.reload]);

  const k = live.state.kpis ?? kpiLoad.data;
  const e = econ.data;
  const supply = e?.supply ?? [];
  const joined = newPlayers.data?.lines[0]?.values ?? [];
  const priceIdx = (e?.price_index_bps ?? []).filter((v) => v > 0);
  const health = typeof k?.health === 'string' ? k.health : null;
  const telegramPlay = switchesLoad.data?.switches.find((s) => s.key === 'telegram_play')?.effective ?? null;

  return (
    <>
      <PageHeader
        title={t('overview.title')}
        subtitle={t('overview.subtitle')}
        actions={<RangePicker days={days} setDays={setDays} />}
        badge={health ? <Status value={health} /> : undefined}
      />
      {kpiLoad.error && !k ? (
        <ErrorBox error={kpiLoad.error} retry={kpiLoad.reload} />
      ) : !k ? (
        <Loading rows={2} />
      ) : (
        <StatGrid>
          <Stat label={t('kpi.money_supply')} value={<Money v={num(k, 'money_supply')} compact />} trend={supply}
            before={supply.length > 1 ? supply[0] : undefined} href="#/economy" />
          <Stat label={t('kpi.players')} value={n(num(k, 'players'))} hint={t('kpi.new_24h', { n: num(k, 'new_24h') })} trend={joined} href="#/players" />
          <Stat label={t('kpi.active_15m')} value={n(num(k, 'active_15m'))} hint={t('kpi.active_24h', { n: num(k, 'active_24h') })} />
          <Stat label={t('kpi.price_index')} value={priceIdx.length ? pct(priceIdx[priceIdx.length - 1] ?? 0) : '—'} trend={priceIdx}
            before={priceIdx.length > 1 ? priceIdx[0] : undefined} good="down" href="#/economy" />
          <Stat label={t('kpi.companies')} value={n(num(k, 'companies'))} href="#/companies" />
          <Stat label={t('kpi.open_flags')} value={n(num(k, 'open_flags'))} tone={num(k, 'open_flags') > 0 ? 'warn' : undefined} href="#/watch" />
          <Stat label={t('kpi.held_payments')} value={n(num(k, 'held_payments'))} tone={num(k, 'held_payments') > 0 ? 'warn' : undefined} href="#/watch/holds" />
          <Stat label={t('kpi.moderated')} value={n(num(k, 'moderated'))} href="#/watch/moderation" />
          <Stat label={t('kpi.jailed')} value={n(num(k, 'jailed'))} href="#/justice/jail" />
          <Stat label={t('kpi.hospitalised')} value={n(num(k, 'hospitalised'))} href="#/health" />
          <Stat label={t('kpi.wars')} value={n(num(k, 'wars'))} tone={num(k, 'wars') > 0 ? 'warn' : undefined} href="#/wars" />
          <Stat label={t('kpi.backlog')} value={nc(num(k, 'outbox_pending') + num(k, 'actions_overdue'))}
            tone={num(k, 'actions_stuck') + num(k, 'actions_failed') > 0 ? 'bad' : undefined}
            hint={t('kpi.failed_actions', { n: num(k, 'actions_failed') })} href="#/system" />
          <Stat label={t('kpi.telegram_play')} value={telegramPlay ? <Status value={telegramPlay} /> : '—'}
            tone={telegramPlay && telegramPlay !== 'on' ? 'warn' : undefined} href="#/system/switches" />
        </StatGrid>
      )}
      <Grid>
        <Card title={t('overview.supply_over_time')}>
          {econ.error ? (
            <ErrorBox error={econ.error} retry={econ.reload} />
          ) : !e ? (
            <Loading rows={4} />
          ) : (
            <Chart title={t('overview.supply_over_time')} days={e.days} kind="area" unit="money"
              series={[{ key: 'supply', label: t('economy.supply'), values: e.supply }]} />
          )}
        </Card>
        <Card title={t('overview.faucets_drains')}>
          {!e ? (
            <Loading rows={4} />
          ) : (
            <Chart title={t('overview.faucets_drains')} days={e.days} kind="bar" unit="money"
              series={[
                { key: 'minted', label: t('overview.minted'), values: e.minted },
                { key: 'burned', label: t('overview.burned'), values: e.burned },
              ]} />
          )}
        </Card>
      </Grid>
      <Grid>
        <SeriesChart name="players.new" title={t('overview.new_players')} days={days} kind="bar" />
        <SeriesChart name="trade.volume" title={t('overview.trade_volume')} days={days} kind="bar" stacked />
      </Grid>
      {e && e.faucets.length > 0 && (
        <Card title={t('overview.faucets_by_reason')}>
          <Chart title={t('overview.faucets_by_reason')} days={e.days} kind="bar" stacked unit="money"
            series={foldSeries(e.faucets, 7, 'other').map((l) => ({ key: l.key, label: l.key === 'other' ? t('chart.other') : (val(l.key) ?? l.key), values: l.values }))} />
        </Card>
      )}
      <Grid>
        <LiveFeed />
        <DataView view="audit" title={t('overview.recent_changes')} pageSize={10} live={['audit']}
          hide={['target_type', 'target', 'new_value', 'old_value', 'id']} />
      </Grid>
    </>
  );
}

// LiveFeed lists what the live feed brought since the page opened.
function LiveFeed() {
  const { t } = useI18n();
  const { status, state, seen } = useLive();
  useEffect(() => {
    seen();
  }, [state.feed.length, seen]);
  const events = state.feed.filter((e) => e.type !== 'kpis');
  return (
    <Card title={t('overview.live')} subtitle={t(`live.${status}` as Key)}>
      {status === 'off' ? (
        <p className="muted">{t('overview.live_off')}</p>
      ) : events.length === 0 ? (
        <p className="muted">{t('overview.live_waiting')}</p>
      ) : (
        <ol className="feed">
          {events.map((e, i) => (
            <li key={`${e.at}-${i}`} className={`feed-${e.type}`}>
              <span className="feed-type">{t(`feed.${e.type}` as Key)}</span>
              <span className="feed-body">
                {e.type === 'audit' && (
                  <>
                    <bdi dir="ltr" className="code">{String(e.data.action ?? '')}</bdi> · <bdi>{String(e.data.actor ?? '')}</bdi>
                  </>
                )}
                {e.type === 'flag' && (
                  <>
                    <Ref kind="player" code={String(e.data.player ?? '')} /> · <bdi dir="ltr">{String(e.data.rule ?? '')}</bdi>
                  </>
                )}
                {e.type === 'hold' && (
                  <>
                    <Ref kind="player" code={String(e.data.payer ?? '')} /> → <Ref kind="player" code={String(e.data.payee ?? '')} /> ·{' '}
                    <Money v={Number(e.data.amount ?? 0)} />
                  </>
                )}
                {e.type === 'change' && (
                  <>
                    <bdi dir="ltr" className="code">{String(e.data.route ?? '')}</bdi> · <bdi>{String(e.data.operator ?? '')}</bdi>
                  </>
                )}
                {e.type === 'health' && <Status value={String(e.data.state ?? '')} />}
              </span>
              <When iso={e.at} rel />
            </li>
          ))}
        </ol>
      )}
    </Card>
  );
}
