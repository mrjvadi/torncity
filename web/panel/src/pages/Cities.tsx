import { useState, type ReactNode } from 'react';
import { Action, Field, Select } from '../components/Action.tsx';
import { Async, Card, Code, KV, Money, PageTitle, Table } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { CityDetail, CityLine, Seat } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseChatId } from '../lib/validate.ts';
import { go } from '../router.ts';

export function Cities() {
  const { t, n } = useI18n();
  const load = useLoad<CityLine[]>('/api/cities');
  return (
    <>
      <PageTitle>{t('cities.title')}</PageTitle>
      <Card>
        <Async load={load}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(r) => r.code}
              onRow={(r) => go(`/cities/${encodeURIComponent(r.code)}`)}
              cols={[
                { label: t('cities.code'), cell: (r) => <Code>{r.code}</Code> },
                { label: t('cities.name'), cell: (r) => <bdi>{r.name}</bdi> },
                { label: t('cities.country'), cell: (r) => <Code>{r.country || '—'}</Code> },
                { label: t('cities.treasury'), cell: (r) => <Money v={r.treasury} />, num: true },
                { label: t('cities.residents'), cell: (r) => n(r.residents), num: true },
                { label: t('cities.present'), cell: (r) => n(r.present), num: true },
                { label: t('cities.groups'), cell: (r) => n(r.groups), num: true },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}

export function SeatTable({ rows }: { rows: Seat[] | null }) {
  const { t, n, at } = useI18n();
  return (
    <Table
      rows={rows ?? []}
      rowKey={(s) => `${s.jurisdiction_kind}/${s.jurisdiction_code}/${s.office}/${s.seat}`}
      cols={[
        { label: t('gov.kind'), cell: (s) => <Code>{`${s.jurisdiction_kind}:${s.jurisdiction_code}`}</Code> },
        { label: t('gov.office'), cell: (s) => <Code>{s.office}</Code> },
        { label: t('gov.seat'), cell: (s) => n(s.seat), num: true },
        { label: t('gov.holder'), cell: (s) => (s.holder ? <bdi>{s.holder}</bdi> : <span className="muted">{t('gov.vacant')}</span>) },
        { label: t('gov.acquired_by'), cell: (s) => s.acquired_by || '—' },
        { label: t('gov.since'), cell: (s) => at(s.since) },
        { label: t('gov.term_ends'), cell: (s) => at(s.term_ends_at) },
      ]}
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
    <Action
      label={t('city.link')}
      check={() => (parseChatId(chat) === null || !chosen ? t('common.invalid') : null)}
      run={(reason, key) =>
        post(`/api/cities/${encodeURIComponent(city)}/groups/link`, { chat_id: parseChatId(chat), bot: chosen, language: lang, reason }, key)
      }
      done={() => {
        onDone();
        return t('toast.done');
      }}
    >
      <Field label={t('city.chat_id')} value={chat} onChange={setChat} dir="ltr" inputMode="numeric" placeholder="-100…" />
      <Select label={t('city.bot')} value={chosen} onChange={setBot} options={botList.map((b) => [b, b])} />
      <Select label={t('city.language')} value={lang} onChange={setLang} options={[['fa', 'فارسی'], ['en', 'English']]} />
    </Action>
  );
}

export function City({ code }: { code: string }) {
  const { t, n, pct, at } = useI18n();
  const load = useLoad<CityDetail>(`/api/cities/${encodeURIComponent(code)}`);
  return (
    <>
      <PageTitle actions={<a className="btn" href="#/cities">{t('common.back')}</a>}>
        <Code>{code}</Code>
      </PageTitle>
      <Async load={load}>
        {(c) => (
          <>
            <div className="grid-2">
              <Card title={c.name}>
                <KV
                  rows={[
                    [t('cities.country'), <Code key="c">{c.country || '—'}</Code>],
                    [t('cities.treasury'), <Money key="t" v={c.treasury} />],
                    [t('cities.residents'), n(c.residents)],
                    [t('cities.present'), n(c.present)],
                    [t('city.companies'), n(c.companies)],
                    [t('city.properties'), n(c.properties)],
                    [t('city.damage'), pct(c.damage_bps)],
                  ]}
                />
              </Card>
              <Card title={t('city.allocation')}>
                <KV
                  rows={Object.entries(c.allocation_bps ?? {})
                    .sort(([a], [b]) => a.localeCompare(b))
                    .map(([k, v]): [string, ReactNode] => [k, pct(v)])}
                />
                <h3>{t('city.last_budget')}</h3>
                {c.last_budget === null ? (
                  <p className="muted">{t('city.no_budget')}</p>
                ) : (
                  <KV
                    rows={[
                      [t('overview.total'), <Money key="lb" v={c.last_budget} />],
                      ...(c.last_budget_lines ?? []).map((l): [string, ReactNode] => [l.reason, <Money key={l.reason} v={l.amount} />]),
                    ]}
                  />
                )}
              </Card>
            </div>
            <Card title={t('city.groups')} actions={<LinkGroup city={c.code} onDone={load.reload} />}>
              <Table
                rows={c.groups ?? []}
                rowKey={(g) => String(g.chat_id)}
                cols={[
                  { label: t('city.chat_id'), cell: (g) => <Code>{g.chat_id}</Code> },
                  { label: t('city.language'), cell: (g) => g.language },
                  { label: t('city.linked_by'), cell: (g) => <bdi>{g.linked_by}</bdi> },
                  { label: t('gov.since'), cell: (g) => at(g.linked_at) },
                  {
                    label: t('city.unlink'),
                    cell: (g) => (
                      <Action
                        label={t('city.unlink')}
                        danger
                        run={(reason, key) =>
                          post(`/api/cities/${encodeURIComponent(c.code)}/groups/unlink`, { chat_id: g.chat_id, reason }, key)
                        }
                        done={() => {
                          load.reload();
                          return t('toast.done');
                        }}
                      >
                        <p>
                          <Code>{g.chat_id}</Code>
                        </p>
                      </Action>
                    ),
                  },
                ]}
              />
            </Card>
            <Card title={t('city.seats')}>
              <SeatTable rows={c.seats} />
            </Card>
          </>
        )}
      </Async>
    </>
  );
}
