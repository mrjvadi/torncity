import { useState, type FormEvent } from 'react';
import { Action, Field, Select } from '../components/Action.tsx';
import { Async, Badge, Card, Code, PageTitle, Table } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post, q } from '../lib/api.ts';
import type { ElectionLine, Lever, PolicyPlace, Seat } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { SeatTable } from './Cities.tsx';

type Kind = 'city' | 'country';

// PlacePicker names a jurisdiction: a city or a country, and its code.
function PlacePicker({ kind, code, setKind, setCode }: {
  kind: Kind;
  code: string;
  setKind: (k: Kind) => void;
  setCode: (c: string) => void;
}) {
  const { t } = useI18n();
  return (
    <>
      <Select
        label={t('gov.kind')}
        value={kind}
        onChange={(v) => setKind(v === 'country' ? 'country' : 'city')}
        options={[
          ['city', t('gov.city')],
          ['country', t('gov.country')],
        ]}
      />
      <Field label={t('gov.code')} value={code} onChange={setCode} dir="ltr" />
    </>
  );
}

function SeatChange({ appoint, onDone }: { appoint: boolean; onDone: () => void }) {
  const { t } = useI18n();
  const [office, setOffice] = useState('mayor');
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  const [seat, setSeat] = useState('1');
  const [player, setPlayer] = useState('');
  return (
    <Action<Seat>
      label={appoint ? t('gov.appoint') : t('gov.vacate')}
      danger={!appoint}
      check={() => (!office.trim() || !code.trim() || !/^\d+$/.test(seat) || (appoint && !player.trim()) ? t('common.invalid') : null)}
      run={(reason, key) =>
        post<Seat>(
          `/api/offices/${appoint ? 'appoint' : 'vacate'}`,
          { office: office.trim(), kind, code: code.trim(), seat: Number(seat), player: appoint ? player.trim() : '', reason },
          key,
        )
      }
      done={() => {
        onDone();
        return t('toast.done');
      }}
    >
      <Field label={t('gov.office')} value={office} onChange={setOffice} dir="ltr" />
      <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
      <Field label={t('gov.seat')} value={seat} onChange={setSeat} dir="ltr" inputMode="numeric" />
      {appoint && <Field label={t('gov.player')} value={player} onChange={setPlayer} dir="ltr" />}
    </Action>
  );
}

function LeverRow({ l }: { l: Lever }) {
  const { t, n, pct, at } = useI18n();
  const fmt = (v: number) => (l.type === 'bps' ? pct(v) : n(v));
  return (
    <div className="lever">
      <div className="lever-head">
        <Code>{l.code}</Code>
        {!l.supported ? (
          <Badge tone="info">{t('gov.unsupported')}</Badge>
        ) : (
          <strong>{fmt(l.value)}</strong>
        )}
      </div>
      {l.supported && (
        <div className="small muted">
          {t('gov.bounds')}: {fmt(l.min)} – {fmt(l.max)} · {t('gov.source')}:{' '}
          {l.source === 'office' ? `${t('gov.source_office')} (${l.set_by}, ${at(l.since)})` : t('gov.source_default')}
          {l.clamped && ` · ${t('gov.clamped')}`}
          {l.pending !== null && ` · ${t('gov.pending')}: ${fmt(l.pending)} (${at(l.pending_at)})`}
          {` · ${t('gov.decided_by')}: `}
          <bdi>{(l.decided_by ?? []).join('، ') || l.held_by}</bdi>
        </div>
      )}
    </div>
  );
}

function Policy() {
  const { t } = useI18n();
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  const [shown, setShown] = useState<string | null>(null);
  const load = useLoad<PolicyPlace[]>(shown);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim()) setShown(`/api/policy${q({ kind, code: code.trim() })}`);
  };
  return (
    <Card title={t('gov.policy')}>
      <form className="inline" onSubmit={submit}>
        <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
        <button type="submit" className="btn primary">
          {t('gov.show')}
        </button>
      </form>
      {shown && (
        <Async load={load}>
          {(places) => (
            <>
              {places.map((p) => (
                <section key={`${p.kind}:${p.code}`} className="policy-place">
                  <h3>
                    <Code>{`${p.kind}:${p.code}`}</Code> <bdi>{p.name}</bdi>
                  </h3>
                  {(p.levers ?? []).length === 0 ? (
                    <p className="muted">{t('common.none')}</p>
                  ) : (
                    (p.levers ?? []).map((l) => <LeverRow key={l.code} l={l} />)
                  )}
                </section>
              ))}
            </>
          )}
        </Async>
      )}
    </Card>
  );
}

export function Governance() {
  const { t } = useI18n();
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  const [filter, setFilter] = useState('');
  const seats = useLoad<Seat[]>(`/api/offices${filter}`);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setFilter(code.trim() ? q({ kind, code: code.trim() }) : '');
  };
  return (
    <>
      <PageTitle>{t('gov.title')}</PageTitle>
      <Card
        title={t('gov.offices')}
        actions={
          <div className="inline">
            <SeatChange appoint onDone={seats.reload} />
            <SeatChange appoint={false} onDone={seats.reload} />
          </div>
        }
      >
        <form className="inline" onSubmit={submit}>
          <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
          <button type="submit" className="btn">
            {code.trim() ? t('gov.show') : t('gov.all_places')}
          </button>
        </form>
        <Async load={seats}>{(rows) => <SeatTable rows={rows} />}</Async>
      </Card>
      <Policy />
    </>
  );
}

export function Elections() {
  const { t, n, at } = useI18n();
  const load = useLoad<ElectionLine[]>('/api/elections');
  const [office, setOffice] = useState('mayor');
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  return (
    <>
      <PageTitle
        actions={
          <Action
            label={t('elections.open')}
            check={() => (!office.trim() || !code.trim() ? t('common.invalid') : null)}
            run={(reason, key) => post('/api/elections/open', { office: office.trim(), kind, code: code.trim(), reason }, key)}
            done={() => {
              load.reload();
              return t('toast.done');
            }}
          >
            <Field label={t('elections.office')} value={office} onChange={setOffice} dir="ltr" />
            <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
          </Action>
        }
      >
        {t('elections.title')}
      </PageTitle>
      <Card>
        <Async load={load}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(e) => String(e.no)}
              cols={[
                { label: t('elections.no'), cell: (e) => n(e.no), num: true },
                { label: t('elections.office'), cell: (e) => <Code>{e.office}</Code> },
                { label: t('elections.place'), cell: (e) => <Code>{`${e.jurisdiction_kind}:${e.jurisdiction_code}`}</Code> },
                { label: t('elections.status'), cell: (e) => <Badge tone={e.status === 'open' ? 'warn' : 'ok'}>{e.status}</Badge> },
                { label: t('elections.candidacy_ends'), cell: (e) => at(e.candidacy_ends_at) },
                { label: t('elections.voting_ends'), cell: (e) => at(e.voting_ends_at) },
                { label: t('elections.candidates'), cell: (e) => n(e.candidates), num: true },
                { label: t('elections.votes'), cell: (e) => n(e.votes_cast), num: true },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}
