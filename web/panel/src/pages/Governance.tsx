import { useState, type FormEvent } from 'react';
import { Action, Field, Select } from '../components/Action.tsx';
import { DataView } from '../components/DataView.tsx';
import { Async, Badge, Card, Code, PageHeader, Tabs } from '../components/ui.tsx';
import { useI18n, type Key } from '../i18n/index.tsx';
import { post, q } from '../lib/api.ts';
import type { Lever, PolicyPlace, Seat, ViewRow } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { href, type Route } from '../router.ts';

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
      <Select label={t('gov.kind')} value={kind} onChange={(v) => setKind(v === 'country' ? 'country' : 'city')}
        options={[['city', t('gov.city')], ['country', t('gov.country')]]} />
      <Field label={t('gov.code')} value={code} onChange={setCode} dir="ltr" />
    </>
  );
}

function SeatChange({ appoint, onDone, preset }: { appoint: boolean; onDone: () => void; preset?: ViewRow }) {
  const { t } = useI18n();
  const [office, setOffice] = useState(preset ? String(preset.office) : 'mayor');
  const [kind, setKind] = useState<Kind>(preset?.place_kind === 'country' ? 'country' : 'city');
  const [code, setCode] = useState(preset ? String(preset.place) : '');
  const [seat, setSeat] = useState(preset ? String(preset.seat) : '1');
  const [player, setPlayer] = useState('');
  return (
    <Action<Seat>
      small
      label={appoint ? t('gov.appoint') : t('gov.vacate')}
      danger={!appoint}
      icon={appoint ? 'plus' : undefined}
      check={() => (!office.trim() || !code.trim() || !/^\d+$/.test(seat) || (appoint && !player.trim()) ? t('common.invalid') : null)}
      run={(reason, key) =>
        post<Seat>(`/api/offices/${appoint ? 'appoint' : 'vacate'}`,
          { office: office.trim(), kind, code: code.trim(), seat: Number(seat), player: appoint ? player.trim() : '', reason }, key)
      }
      done={() => {
        onDone();
        return t('toast.done');
      }}
    >
      {!preset && (
        <>
          <Field label={t('gov.office')} value={office} onChange={setOffice} dir="ltr" />
          <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
          <Field label={t('gov.seat')} value={seat} onChange={setSeat} dir="ltr" inputMode="numeric" />
        </>
      )}
      {preset && (
        <p>
          <Code>{`${office}#${seat}`}</Code> · <Code>{`${kind}:${code}`}</Code>
        </p>
      )}
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
        {!l.supported ? <Badge tone="info">{t('gov.unsupported')}</Badge> : <strong>{fmt(l.value)}</strong>}
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

// PolicyView is every lever in force at a place and above it.
export function PolicyView({ kind, code }: { kind: Kind; code: string }) {
  const { t } = useI18n();
  const load = useLoad<PolicyPlace[]>(code ? `/api/policy${q({ kind, code })}` : null);
  return (
    <Card title={t('gov.policy')}>
      <Async load={load}>
        {(places) => (
          <div className="policy">
            {places.map((p) => (
              <section key={`${p.kind}:${p.code}`} className="policy-place">
                <h3>
                  <Code>{`${p.kind}:${p.code}`}</Code> <bdi>{p.name}</bdi>
                </h3>
                {(p.levers ?? []).length === 0 ? <p className="muted">{t('common.none')}</p> : (p.levers ?? []).map((l) => <LeverRow key={l.code} l={l} />)}
              </section>
            ))}
          </div>
        )}
      </Async>
    </Card>
  );
}

function PolicyPicker() {
  const { t } = useI18n();
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  const [shown, setShown] = useState<{ kind: Kind; code: string } | null>(null);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    if (code.trim()) setShown({ kind, code: code.trim() });
  };
  return (
    <>
      <Card title={t('gov.policy_lookup')}>
        <form className="inline" onSubmit={submit}>
          <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
          <button type="submit" className="btn primary">
            {t('gov.show')}
          </button>
        </form>
      </Card>
      {shown && <PolicyView kind={shown.kind} code={shown.code} />}
    </>
  );
}

const TABS: [string, Key][] = [
  ['seats', 'gov.tab_seats'],
  ['policy', 'gov.tab_policy'],
  ['proposals', 'gov.tab_proposals'],
  ['elections', 'gov.tab_elections'],
];

export function GovernancePage({ route }: { route: Route }) {
  const { t } = useI18n();
  const tab = route.segments[0] === 'elections' ? 'elections' : (route.segments[1] ?? 'seats');
  const [tick, setTick] = useState(0);
  const bump = () => setTick((x) => x + 1);
  const election = route.query.get('election') ?? undefined;
  return (
    <>
      <PageHeader title={t('gov.title')} subtitle={t('gov.subtitle')} crumbs={[{ label: t('nav.governance') }]} />
      <Tabs active={tab} hrefOf={(id) => href(['governance', id])} tabs={TABS.map(([id, label]) => ({ id, label: t(label) }))} />
      {tab === 'seats' && (
        <DataView key={tick} view="seats" pageSize={50}
          actions={<><SeatChange appoint onDone={bump} /><SeatChange appoint={false} onDone={bump} /></>}
          rowActions={(r) => (r.state === 'vacant' ? <SeatChange appoint preset={r} onDone={bump} /> : <SeatChange appoint={false} preset={r} onDone={bump} />)} />
      )}
      {tab === 'policy' && (
        <>
          <PolicyPicker />
          <DataView view="policy.values" />
          <DataView view="policy.changes" />
        </>
      )}
      {tab === 'proposals' && (
        <>
          <DataView view="proposals" hide={['body', 'value_json', 'threshold', 'quorum']}
            rowActions={(r) => <a className="btn small" href={href(['governance', 'proposals'], { proposal: String(r.no) })}>{t('gov.votes')}</a>} />
          {route.query.get('proposal') ? (
            <DataView view="proposal.votes" scope={{ proposal: route.query.get('proposal') ?? undefined }} />
          ) : (
            <p className="muted">{t('gov.pick_proposal')}</p>
          )}
        </>
      )}
      {tab === 'elections' && (
        <>
          <DataView key={tick} view="elections" actions={<OpenElection onDone={bump} />}
            rowActions={(r) => <a className="btn small" href={href(['governance', 'elections'], { election: String(r.no) })}>{t('gov.candidates')}</a>} />
          {election ? (
            <DataView view="election.candidates" scope={{ election }} />
          ) : (
            <p className="muted">{t('gov.pick_election')}</p>
          )}
        </>
      )}
    </>
  );
}

function OpenElection({ onDone }: { onDone: () => void }) {
  const { t } = useI18n();
  const [office, setOffice] = useState('mayor');
  const [kind, setKind] = useState<Kind>('city');
  const [code, setCode] = useState('');
  return (
    <Action small label={t('elections.open')} icon="vote"
      check={() => (!office.trim() || !code.trim() ? t('common.invalid') : null)}
      run={(reason, key) => post('/api/elections/open', { office: office.trim(), kind, code: code.trim(), reason }, key)}
      done={() => {
        onDone();
        return t('toast.done');
      }}>
      <Field label={t('elections.office')} value={office} onChange={setOffice} dir="ltr" />
      <PlacePicker kind={kind} code={code} setKind={setKind} setCode={setCode} />
    </Action>
  );
}
