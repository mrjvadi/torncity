import { useState, type ReactNode } from 'react';
import { Action, Field, Select } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { SeriesChart, Stat, StatGrid } from '../components/Stat.tsx';
import { Async, Card, Code, Grid, KV, Money, PageHeader, Ref, Tabs, When } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { CityDetail, Rec, ViewRow } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseChatId } from '../lib/validate.ts';
import { href, type Route } from '../router.ts';
import { PolicyView } from './Governance.tsx';

export function CitiesPage({ route }: { route: Route }) {
  const { t } = useI18n();
  const code = route.segments[1];
  if (code) return <City code={code.toLowerCase()} tab={route.segments[2] ?? 'summary'} />;
  return (
    <>
      <PageHeader title={t('cities.title')} subtitle={t('cities.subtitle')} crumbs={[{ label: t('nav.cities') }]} />
      <DataView view="cities" pageSize={50} />
    </>
  );
}

const TABS: [string, Key][] = [
  ['summary', 'city.tab_summary'],
  ['budget', 'city.tab_budget'],
  ['policy', 'city.tab_policy'],
  ['economy', 'city.tab_economy'],
  ['property', 'city.tab_property'],
  ['people', 'city.tab_people'],
  ['justice', 'city.tab_justice'],
  ['war', 'city.tab_war'],
  ['groups', 'city.tab_groups'],
];

const s = (r: Rec | undefined, k: string) => (r && r[k] !== null && r[k] !== undefined ? String(r[k]) : '');
const nOf = (r: Rec | undefined, k: string) => (r && typeof r[k] === 'number' ? (r[k] as number) : null);

function City({ code, tab }: { code: string; tab: string }) {
  const { t, n, pct } = useI18n();
  const load = useLoad<{ city: Rec }>(`/api/dossier/cities/${encodeURIComponent(code)}`);
  const detail = useLoad<CityDetail>(tab === 'summary' || tab === 'budget' ? `/api/cities/${encodeURIComponent(code)}` : null);
  const c = load.data?.city;
  const scope = { city: code };
  return (
    <>
      <PageHeader
        crumbs={[{ label: t('nav.cities'), to: '#/cities' }, { label: <Code>{code}</Code> }]}
        title={c ? <bdi>{s(c, 'name')}</bdi> : <Code>{code}</Code>}
        subtitle={c && (
          <span className="meta-line">
            <Code>{code}</Code>
            <span>{t('cities.country')}: <Ref kind="country" code={s(c, 'country')} /></span>
            {s(c, 'controller') && s(c, 'controller') !== s(c, 'country') && <span>{t('city.controlled_by')}: <Ref kind="country" code={s(c, 'controller')} /></span>}
          </span>
        )}
      />
      <Tabs active={tab} hrefOf={(id) => href(['cities', code, id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'summary' && (
        <Async load={load} rows={4}>
          {() => (
            <>
              <StatGrid>
                <Stat label={t('cities.treasury')} value={<Money v={nOf(c, 'treasury')} compact />} />
                <Stat label={t('city.population')} value={n(nOf(c, 'population'))} />
                <Stat label={t('cities.residents')} value={n(nOf(c, 'residents'))} href={href(['cities', code, 'people'])} />
                <Stat label={t('cities.present')} value={n(nOf(c, 'present'))} hint={t('city.active_now', { n: nOf(c, 'active_now') ?? 0 })} />
                <Stat label={t('city.companies')} value={n(nOf(c, 'companies'))} href={href(['cities', code, 'economy'])} />
                <Stat label={t('city.damage')} value={pct(nOf(c, 'damage_bps'))} tone={(nOf(c, 'damage_bps') ?? 0) > 0 ? 'warn' : undefined} />
              </StatGrid>
              <Grid>
                <Card title={t('city.facts')}>
                  <KV rows={[
                    [t('city.tax_rate'), pct(nOf(c, 'tax_rate_bps'))],
                    [t('city.cost_of_living'), <Money key="c" v={nOf(c, 'cost_of_living')} />],
                    [t('city.properties'), n(nOf(c, 'properties'))],
                    [t('city.factions'), n(nOf(c, 'factions'))],
                    [t('city.jailed'), n(nOf(c, 'jailed'))],
                    [t('city.hospitalised'), n(nOf(c, 'hospitalised'))],
                    [t('city.groups'), n(nOf(c, 'groups'))],
                    [t('city.period'), `${n(nOf(c, 'period_no'))}`],
                    [t('city.next_period'), <When key="np" iso={s(c, 'next_period_at')} rel />],
                    [t('city.facilities'), Array.isArray(c?.facilities) ? (c?.facilities as string[]).map((f) => <Code key={f}>{f} </Code>) : '—'],
                  ]} />
                </Card>
                <Allocation detail={detail} />
              </Grid>
            </>
          )}
        </Async>
      )}
      {tab === 'budget' && (
        <>
          <SeriesChart name="city.budget" title={t('city.budget_chart')} days={90} kind="line" scope={scope} />
          <Allocation detail={detail} />
          <DataView view="city.budgets" scope={scope} hide={['city']} />
          <DataView view="accounts" scope={scope} hide={['owner']} />
          <DataView view="ledger" scope={scope} hide={['owner', 'account']} />
        </>
      )}
      {tab === 'policy' && (
        <>
          <PolicyView kind="city" code={code} />
          <DataView view="seats" scope={scope} />
          <DataView view="policy.changes" scope={scope} hide={['place_kind', 'place']} />
          <DataView view="proposals" scope={scope} hide={['place_kind', 'place', 'body', 'value_json']} />
          <DataView view="elections" scope={scope} hide={['place_kind', 'place']} />
        </>
      )}
      {tab === 'economy' && (
        <>
          <SeriesChart name="city.demand" title={t('city.demand_chart')} days={90} kind="bar" scope={scope} />
          <DataView view="city.demand" scope={scope} hide={['city']} />
          <DataView view="company.periods" scope={scope} hide={['city', 'ended_at', 'settled_at']} />
          <DataView view="companies" scope={scope} hide={['city', 'manager', 'price_bps', 'rating_bps', 'total_shares']} />
          <DataView view="shops" scope={scope} hide={['city']} />
          <DataView view="shop.sales" scope={scope} hide={['city']} />
          <DataView view="market.book" scope={scope} hide={['city']} />
          <DataView view="market.trades" scope={scope} hide={['city']} />
          <DataView view="company.listings" scope={scope} hide={['city']} />
          <DataView view="auctions" scope={scope} hide={['city']} />
        </>
      )}
      {tab === 'property' && (
        <>
          <DataView view="properties" scope={scope} hide={['city']} />
          <DataView view="leases" scope={scope} hide={['city']} />
          <DataView view="property.listings" scope={scope} hide={['city']} />
          <DataView view="property.charges" scope={scope} hide={['city']} />
        </>
      )}
      {tab === 'people' && (
        <>
          <DataView view="players" title={t('city.residents_list')} scope={{ city: code }} hide={['residence', 'telegram_id', 'language']} />
          <DataView view="players" title={t('city.present_list')} scope={{ here: code }} hide={['city', 'telegram_id', 'language']} />
          <DataView view="factions" scope={scope} hide={['city']} />
          <DataView view="travels" scope={scope} />
          <DataView view="walks" scope={scope} />
        </>
      )}
      {tab === 'justice' && (
        <>
          <DataView view="crimes" scope={scope} hide={['city']} />
          <DataView view="jail" scope={scope} hide={['city']} defaultFilters={{ status: 'serving' }} />
          <DataView view="crime.reports" scope={scope} hide={['city']} />
          <DataView view="hospital.stays" scope={scope} hide={['city']} />
          <DataView view="clinics" scope={scope} hide={['city']} />
        </>
      )}
      {tab === 'war' && (
        <>
          <DataView view="city.control" scope={scope} />
          <DataView view="forces" scope={scope} />
          <DataView view="war.operations" scope={scope} hide={['report']} />
          <DataView view="military.moves" scope={scope} />
        </>
      )}
      {tab === 'groups' && <Groups code={code} />}
    </>
  );
}

function Allocation({ detail }: { detail: ReturnType<typeof useLoad<CityDetail>> }) {
  const { t, pct } = useI18n();
  return (
    <Card title={t('city.allocation')}>
      <Async load={detail}>
        {(c) => (
          <>
            <KV rows={Object.entries(c.allocation_bps ?? {})
              .sort(([a], [b]) => a.localeCompare(b))
              .map(([k, v]): [string, ReactNode] => [k, pct(v)])} />
            <h3>{t('city.last_budget')}</h3>
            {c.last_budget === null ? (
              <p className="muted">{t('city.no_budget')}</p>
            ) : (
              <KV rows={[
                [t('overview.total'), <Money key="lb" v={c.last_budget} />],
                ...(c.last_budget_lines ?? []).map((l): [string, ReactNode] => [l.reason, <Money key={l.reason} v={l.amount} />]),
              ]} />
            )}
          </>
        )}
      </Async>
    </Card>
  );
}

function Groups({ code }: { code: string }) {
  const { t } = useI18n();
  const [tick, setTick] = useState(0);
  return (
    <DataView
      key={tick}
      view="city.groups"
      scope={{ city: code }}
      hide={['city']}
      actions={<LinkGroup city={code} onDone={() => setTick((x) => x + 1)} />}
      rowActions={(g: ViewRow) => (
        <Action small danger label={t('city.unlink')}
          run={(reason, key) => post(`/api/cities/${encodeURIComponent(code)}/groups/unlink`, { chat_id: Number(g.chat_id), reason }, key)}
          done={() => {
            setTick((x) => x + 1);
            return t('toast.done');
          }}>
          <p>
            <Code>{String(g.chat_id)}</Code>
          </p>
        </Action>
      )}
    />
  );
}

function LinkGroup({ city, onDone }: { city: string; onDone: () => void }) {
  const { t } = useI18n();
  const bots = useLoad<string[]>('/api/bots');
  const [chat, setChat] = useState('');
  const [bot, setBot] = useState('');
  const [lang, setLang] = useState('fa');
  const botList = bots.data ?? [];
  const chosen = bot || botList[0] || '';
  return (
    <Action small label={t('city.link')} icon="plus"
      check={() => (parseChatId(chat) === null || !chosen ? t('common.invalid') : null)}
      run={(reason, key) => post(`/api/cities/${encodeURIComponent(city)}/groups/link`, { chat_id: parseChatId(chat), bot: chosen, language: lang, reason }, key)}
      done={() => {
        onDone();
        return t('toast.done');
      }}>
      <Field label={t('city.chat_id')} value={chat} onChange={setChat} dir="ltr" inputMode="numeric" placeholder="-100…" />
      <Select label={t('city.bot')} value={chosen} onChange={setBot} options={botList.map((b) => [b, b])} />
      <Select label={t('city.language')} value={lang} onChange={setLang} options={[['fa', 'فارسی'], ['en', 'English']]} />
    </Action>
  );
}
