import { useState, type FormEvent } from 'react';
import { Action, Field } from '../components/Action.tsx';
import { Async, Badge, Card, Code, KV, Money, PageTitle, Table } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post, q } from '../lib/api.ts';
import type { PlayerDetail, PlayerHit } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseAmount } from '../lib/validate.ts';
import { go } from '../router.ts';

export function Players() {
  const { t, at } = useI18n();
  const [text, setText] = useState('');
  const [query, setQuery] = useState('');
  const load = useLoad<PlayerHit[]>(`/api/players${q({ q: query })}`);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setQuery(text.trim());
  };
  return (
    <>
      <PageTitle>{t('players.title')}</PageTitle>
      <form className="search" onSubmit={submit} role="search">
        <label className="sr-only" htmlFor="player-search">
          {t('common.search')}
        </label>
        <input
          id="player-search"
          type="search"
          value={text}
          dir="auto"
          placeholder={t('players.search_placeholder')}
          onChange={(e) => setText(e.target.value)}
        />
        <button type="submit" className="btn primary">
          {t('common.search')}
        </button>
      </form>
      <Card>
        <Async load={load}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(r) => r.code}
              onRow={(r) => go(`/players/${encodeURIComponent(r.code)}`)}
              cols={[
                { label: t('players.code'), cell: (r) => <Code>{r.code}</Code> },
                { label: t('players.name'), cell: (r) => <bdi>{r.name}</bdi> },
                { label: t('players.username'), cell: (r) => (r.username ? <Code>@{r.username}</Code> : '—') },
                { label: t('players.telegram_id'), cell: (r) => <Code>{r.telegram_id}</Code> },
                { label: t('players.city'), cell: (r) => <Code>{r.city || '—'}</Code> },
                { label: t('players.status'), cell: (r) => <Badge tone={r.status === 'active' ? 'ok' : 'bad'}>{r.status}</Badge> },
                { label: t('players.last_active'), cell: (r) => at(r.last_active) },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}

function List({ items }: { items: string[] | null }) {
  const { t } = useI18n();
  if (!items || items.length === 0) return <p className="muted">{t('common.none')}</p>;
  return (
    <ul className="plain">
      {items.map((s) => (
        <li key={s}>
          <bdi dir="ltr">{s}</bdi>
        </li>
      ))}
    </ul>
  );
}

export function Player({ code }: { code: string }) {
  const { t, n, at } = useI18n();
  const load = useLoad<PlayerDetail>(`/api/players/${encodeURIComponent(code)}`);
  const [amount, setAmount] = useState('');
  return (
    <>
      <PageTitle actions={<a className="btn" href="#/players">{t('common.back')}</a>}>
        <Code>{code}</Code>
      </PageTitle>
      <Async load={load}>
        {(p) => (
          <>
            <div className="grid-2">
              <Card
                title={p.name}
                actions={
                  <Action
                    label={t('player.grant')}
                    danger
                    lead={t('economy.grant_warning')}
                    check={() => (parseAmount(amount) === null ? t('common.invalid') : null)}
                    run={(reason, key) => post('/api/economy/grant', { player: p.code, amount: parseAmount(amount), reason }, key)}
                    done={() => {
                      load.reload();
                      return t('toast.done');
                    }}
                  >
                    <Field label={t('economy.grant_amount')} value={amount} onChange={setAmount} inputMode="numeric" dir="ltr" />
                  </Action>
                }
              >
                <KV
                  rows={[
                    [t('players.telegram_id'), <Code key="id">{p.extra.telegram_id}</Code>],
                    [t('players.username'), p.extra.username ? <Code key="u">@{p.extra.username}</Code> : '—'],
                    [t('players.status'), p.extra.status],
                    [t('player.language'), p.extra.language],
                    [t('player.joined'), at(p.created_at)],
                    [t('players.last_active'), at(p.extra.last_active)],
                    [t('player.location'), <Code key="l">{[p.city, p.place].filter(Boolean).join(' / ') || '—'}</Code>],
                    [t('player.state'), t(`player.state_${p.state}`)],
                    [t('player.residence'), <Code key="r">{p.residence || '—'}</Code>],
                    [t('player.job'), <Code key="j">{p.job || '—'}</Code>],
                    [t('player.renting'), p.renting],
                    [t('player.achievements'), n(p.achievements)],
                    [t('player.open_flags'), n(p.open_flags)],
                  ]}
                />
              </Card>
              <Card title={t('player.balances')}>
                <KV rows={(p.balances ?? []).map((b) => [b.reason, <Money key={b.reason} v={b.amount} />])} />
              </Card>
            </div>
            <div className="grid-3">
              <Card title={t('player.companies')}>
                <List items={p.companies} />
              </Card>
              <Card title={t('player.properties')}>
                <List items={p.properties} />
              </Card>
              <Card title={t('player.offices')}>
                <List items={p.offices} />
              </Card>
            </div>
            <Card title={t('player.flags')}>
              <Table
                rows={p.extra.flags ?? []}
                rowKey={(f) => String(f.no)}
                cols={[
                  { label: t('watch.no'), cell: (f) => n(f.no), num: true },
                  { label: t('watch.rule'), cell: (f) => <Code>{f.rule}</Code> },
                  { label: t('watch.other'), cell: (f) => <Code>{f.other || '—'}</Code> },
                  { label: t('watch.score'), cell: (f) => n(f.score), num: true },
                  { label: t('players.status'), cell: (f) => <Badge tone={f.status === 'open' ? 'warn' : 'ok'}>{f.status}</Badge> },
                  { label: t('watch.updated'), cell: (f) => at(f.updated_at) },
                ]}
              />
            </Card>
            <Card title={t('player.licences')}>
              <Licences rows={p.extra.licences} />
            </Card>
            <Card title={t('player.history')}>
              {(p.extra.history ?? []).length === 0 ? (
                <p className="muted">{t('common.none')}</p>
              ) : (
                <ol className="timeline">
                  {(p.extra.history ?? []).map((h, i) => (
                    <li key={`${h.at}-${i}`}>
                      <span className="muted small">{at(h.at)}</span>{' '}
                      <Code>{h.kind}</Code> {h.backfilled && <Badge tone="info">{t('player.backfilled')}</Badge>}
                      <div className="small muted">
                        <bdi dir="ltr">
                          {Object.entries(h.data ?? {})
                            .filter(([, v]) => v !== '' && v !== 0 && v !== null)
                            .map(([k, v]) => `${k}: ${String(v)}`)
                            .join(' · ')}
                        </bdi>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </Card>
          </>
        )}
      </Async>
    </>
  );
}

export function Licences({ rows }: { rows: PlayerDetail['extra']['licences'] }) {
  const { t, n, at } = useI18n();
  return (
    <Table
      rows={rows ?? []}
      rowKey={(l) => String(l.no)}
      cols={[
        { label: t('company.licence_no'), cell: (l) => n(l.no), num: true },
        { label: t('companies.code'), cell: (l) => <Code>{l.company}</Code> },
        { label: t('company.licence_kind'), cell: (l) => <Code>{l.kind}</Code> },
        { label: t('company.licence_basis'), cell: (l) => <Code>{l.basis}</Code> },
        {
          label: t('company.licence_status'),
          cell: (l) => <Badge tone={l.status === 'active' ? 'ok' : l.status === 'revoked' ? 'bad' : 'info'}>{l.status}</Badge>,
        },
        { label: t('gov.since'), cell: (l) => at(l.decided_at) },
      ]}
    />
  );
}
