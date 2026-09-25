import { Action } from '../components/Action.tsx';
import { Async, Badge, Card, Code, KV, Money, PageTitle, Table } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { CompanyDetail, CompanyLine } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { go } from '../router.ts';
import { Licences } from './Players.tsx';

export function Companies() {
  const { t, n } = useI18n();
  const load = useLoad<CompanyLine[]>('/api/companies?limit=200');
  return (
    <>
      <PageTitle>{t('companies.title')}</PageTitle>
      <Card>
        <Async load={load}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(r) => r.code}
              onRow={(r) => go(`/companies/${encodeURIComponent(r.code)}`)}
              cols={[
                { label: t('companies.code'), cell: (r) => <Code>{r.code}</Code> },
                { label: t('companies.name'), cell: (r) => <bdi>{r.name}</bdi> },
                { label: t('companies.kind'), cell: (r) => <Code>{r.kind}</Code> },
                { label: t('companies.city'), cell: (r) => <Code>{r.city}</Code> },
                { label: t('companies.status'), cell: (r) => <Badge tone={r.status === 'active' ? 'ok' : 'bad'}>{r.status}</Badge> },
                { label: t('companies.owner'), cell: (r) => <Code>{r.owner}</Code> },
                { label: t('companies.treasury'), cell: (r) => <Money v={r.treasury} />, num: true },
                { label: t('companies.debt'), cell: (r) => <Money v={r.debt} />, num: true },
                { label: t('companies.staff'), cell: (r) => n(r.staff), num: true },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}

export function Company({ code }: { code: string }) {
  const { t, n, pct, at } = useI18n();
  const load = useLoad<CompanyDetail>(`/api/companies/${encodeURIComponent(code)}`);
  const licence = (grant: boolean) => (
    <Action
      label={grant ? t('company.grant_licence') : t('company.revoke_licence')}
      danger={!grant}
      run={(reason, key) => post(`/api/companies/${encodeURIComponent(code)}/defence/${grant ? 'grant' : 'revoke'}`, { reason }, key)}
      done={() => {
        load.reload();
        return t('toast.done');
      }}
    >
      <p>
        <Code>{code}</Code>
      </p>
    </Action>
  );
  return (
    <>
      <PageTitle actions={<a className="btn" href="#/companies">{t('common.back')}</a>}>
        <Code>{code}</Code>
      </PageTitle>
      <Async load={load}>
        {(c) => (
          <>
            <Card title={c.name}>
              <KV
                rows={[
                  [t('companies.kind'), <Code key="k">{c.kind}</Code>],
                  [t('companies.city'), <Code key="c">{c.city}</Code>],
                  [t('companies.status'), <Badge key="s" tone={c.status === 'active' ? 'ok' : 'bad'}>{c.status}</Badge>],
                  [t('companies.owner'), <a key="o" href={`#/players/${encodeURIComponent(c.owner)}`}><Code>{c.owner}</Code></a>],
                  [t('company.manager'), c.manager ? <Code key="m">{c.manager}</Code> : '—'],
                  [t('company.founded'), at(c.founded_at)],
                  [t('company.closed'), c.closed_at ? `${at(c.closed_at)} (${c.close_reason})` : '—'],
                  [t('companies.treasury'), <Money key="t" v={c.treasury} />],
                  [t('companies.debt'), <Money key="d" v={c.debt} />],
                  [t('company.reserved'), <Money key="r" v={c.reserved_wages} />],
                  [t('company.arrears'), n(c.arrears)],
                  [t('company.price'), pct(c.price_bps)],
                  [t('company.rating'), pct(c.rating_bps)],
                ]}
              />
            </Card>
            <Card
              title={t('company.licences')}
              actions={
                <div className="inline">
                  {licence(true)}
                  {licence(false)}
                </div>
              }
            >
              <Licences rows={c.licences} />
            </Card>
            <div className="grid-2">
              <Card title={`${t('company.shares')} (${n(c.total_shares)})`}>
                <Table
                  rows={c.shares ?? []}
                  rowKey={(s) => s.player}
                  cols={[
                    { label: t('watch.player'), cell: (s) => <Code>{s.player}</Code> },
                    { label: t('company.shares'), cell: (s) => n(s.shares), num: true },
                  ]}
                />
              </Card>
              <Card title={t('company.staff_list')}>
                <Table
                  rows={c.staff_list ?? []}
                  rowKey={(s) => s.player}
                  cols={[
                    { label: t('watch.player'), cell: (s) => <Code>{s.player}</Code> },
                    { label: t('company.career'), cell: (s) => <Code>{s.career}</Code> },
                    { label: t('company.tier'), cell: (s) => n(s.tier), num: true },
                    { label: t('company.wage'), cell: (s) => <Money v={s.wage} />, num: true },
                    { label: t('company.shifts'), cell: (s) => n(s.shifts), num: true },
                    { label: t('company.working'), cell: (s) => (s.working ? t('common.yes') : t('common.no')) },
                  ]}
                />
              </Card>
            </div>
            <Card title={t('company.periods')}>
              <Table
                rows={c.periods ?? []}
                rowKey={(p) => String(p.no)}
                cols={[
                  { label: '#', cell: (p) => n(p.no), num: true },
                  { label: t('company.revenue'), cell: (p) => <Money v={p.revenue} />, num: true },
                  { label: t('company.wages'), cell: (p) => <Money v={p.wages} />, num: true },
                  { label: t('company.upkeep'), cell: (p) => <Money v={p.upkeep} />, num: true },
                  { label: t('company.balance'), cell: (p) => <Money v={p.balance} />, num: true },
                  { label: t('company.insolvent'), cell: (p) => (p.insolvent ? <Badge tone="bad">{t('company.insolvent')}</Badge> : '—') },
                ]}
              />
            </Card>
          </>
        )}
      </Async>
    </>
  );
}
