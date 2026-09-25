import { useState } from 'react';
import { DataView } from '../components/DataView.tsx';
import { RangePicker, SeriesChart } from '../components/Stat.tsx';
import { Grid, PageHeader, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { href, type Route } from '../router.ts';

const TABS: [string, Key][] = [
  ['market', 'markets.tab_market'],
  ['auctions', 'markets.tab_auctions'],
  ['gold', 'markets.tab_gold'],
  ['stocks', 'markets.tab_stocks'],
  ['companies', 'markets.tab_companies'],
];

export function MarketsPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[1] ?? 'market';
  const [days, setDays] = useState(30);
  return (
    <>
      <PageHeader title={t('markets.title')} subtitle={t('markets.subtitle')} crumbs={[{ label: t('nav.markets') }]}
        actions={<RangePicker days={days} setDays={setDays} />} />
      <Tabs active={tab} hrefOf={(id) => href(['markets', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'market' && (
        <>
          <SeriesChart name="trade.volume" title={t('overview.trade_volume')} days={days} kind="bar" stacked />
          <DataView view="market.items" />
          <DataView view="market.book" pageSize={50} />
          <DataView view="market.orders" defaultFilters={{ status: 'open' }} />
          <DataView view="market.trades" />
        </>
      )}
      {tab === 'auctions' && (
        <>
          <DataView view="auctions" rowActions={(r) => <a className="btn small" href={href(['markets', 'auctions'], { auction: String(r.no) })}>{t('markets.bids')}</a>} />
          {route.query.get('auction') ? <DataView view="auction.bids" scope={{ auction: route.query.get('auction') ?? undefined }} /> : <p className="muted">{t('markets.bids_hint')}</p>}
        </>
      )}
      {tab === 'gold' && (
        <>
          <Grid>
            <SeriesChart name="gold.price" title={t('markets.gold_price')} days={days} kind="line" />
            <SeriesChart name="gold.volume" title={t('markets.gold_volume')} days={days} kind="bar" />
          </Grid>
          <DataView view="gold.prices" />
          <DataView view="gold.trades" />
          <DataView view="gold.holdings" />
        </>
      )}
      {tab === 'stocks' && (
        <>
          <SeriesChart name="shares.volume" title={t('markets.shares_volume')} days={days} kind="bar" />
          <DataView view="stocks" />
          <DataView view="share.trades" />
          <DataView view="share.orders" defaultFilters={{ status: 'open' }} />
          <DataView view="dividends" />
          <DataView view="portfolio" />
        </>
      )}
      {tab === 'companies' && (
        <>
          <DataView view="company.listings" defaultFilters={{ status: 'open' }} />
          <DataView view="shops" />
          <DataView view="procurements" />
        </>
      )}
    </>
  );
}
