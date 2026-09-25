import { useState, type FormEvent } from 'react';
import { Action, Field, TextArea } from '../components/Action.tsx';
import { Async, Card, Code, Money, PageTitle, Table } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post, q } from '../lib/api.ts';
import type { AuditLine, FlagLine, HoldLine } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';

export function Watch() {
  const { t, n, at } = useI18n();
  const [status, setStatus] = useState<'open' | 'cleared'>('open');
  const flags = useLoad<FlagLine[]>(`/api/watch/flags?status=${status}`);
  const holds = useLoad<HoldLine[]>('/api/watch/holds');
  const settle = (h: HoldLine, release: boolean) => (
    <Action
      label={release ? t('watch.release') : t('watch.return')}
      danger={!release}
      run={(reason, key) => post(`/api/watch/holds/${h.no}/${release ? 'release' : 'return'}`, { reason }, key)}
      done={() => {
        holds.reload();
        return t('toast.done');
      }}
    >
      <p>
        #{n(h.no)} · <Code>{h.payer}</Code> → <Code>{h.payee}</Code> · <Money v={h.amount} />
      </p>
    </Action>
  );
  return (
    <>
      <PageTitle>{t('watch.title')}</PageTitle>
      <Card
        title={t('watch.flags')}
        actions={
          <div className="segmented" role="group" aria-label={t('watch.flags')}>
            {(['open', 'cleared'] as const).map((s) => (
              <button key={s} type="button" className={`btn small ${status === s ? 'active' : ''}`} aria-pressed={status === s} onClick={() => setStatus(s)}>
                {s === 'open' ? t('watch.open') : t('watch.cleared')}
              </button>
            ))}
          </div>
        }
      >
        <Async load={flags}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(f) => String(f.no)}
              cols={[
                { label: t('watch.no'), cell: (f) => n(f.no), num: true },
                { label: t('watch.rule'), cell: (f) => <Code>{f.rule}</Code> },
                { label: t('watch.player'), cell: (f) => <a href={`#/players/${encodeURIComponent(f.player)}`}><Code>{f.player}</Code></a> },
                { label: t('watch.other'), cell: (f) => (f.other ? <a href={`#/players/${encodeURIComponent(f.other)}`}><Code>{f.other}</Code></a> : '—') },
                { label: t('watch.score'), cell: (f) => n(f.score), num: true },
                { label: t('watch.hits'), cell: (f) => n(f.hits), num: true },
                {
                  label: t('watch.evidence'),
                  cell: (f) => (
                    <bdi dir="ltr" className="small">
                      {Object.entries(f.evidence ?? {})
                        .map(([k, v]) => `${k}=${v}`)
                        .join(' ')}
                    </bdi>
                  ),
                },
                { label: t('watch.updated'), cell: (f) => at(f.updated_at) },
                {
                  label: status === 'open' ? t('watch.clear') : t('watch.note'),
                  cell: (f) =>
                    f.status === 'open' ? (
                      <Action
                        label={t('watch.clear')}
                        run={(reason, key) => post(`/api/watch/flags/${f.no}/clear`, { reason }, key)}
                        done={() => {
                          flags.reload();
                          return t('toast.done');
                        }}
                      >
                        <p>
                          #{n(f.no)} · <Code>{f.rule}</Code> · <Code>{f.player}</Code>
                        </p>
                      </Action>
                    ) : (
                      <bdi>{`${f.cleared_by ?? ''}: ${f.note ?? ''}`}</bdi>
                    ),
                },
              ]}
            />
          )}
        </Async>
      </Card>
      <Card title={t('watch.holds')}>
        <Async load={holds}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(h) => String(h.no)}
              cols={[
                { label: t('watch.no'), cell: (h) => n(h.no), num: true },
                { label: t('watch.payer'), cell: (h) => <Code>{h.payer}</Code> },
                { label: t('watch.payee'), cell: (h) => <Code>{h.payee}</Code> },
                { label: t('watch.method'), cell: (h) => <Code>{h.method}</Code> },
                { label: t('watch.amount'), cell: (h) => <Money v={h.amount} />, num: true },
                { label: t('watch.since'), cell: (h) => at(h.created_at) },
                {
                  label: '',
                  cell: (h) => (
                    <div className="inline">
                      {settle(h, true)}
                      {settle(h, false)}
                    </div>
                  ),
                },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}

const MAX_MESSAGE = 3000;

export function Messages() {
  const { t } = useI18n();
  const [text, setText] = useState('');
  const [textEN, setTextEN] = useState('');
  const [btext, setBText] = useState('');
  const [btextEN, setBTextEN] = useState('');
  const [only, setOnly] = useState('');
  const tooLong = (a: string, b: string) => [...a].length > MAX_MESSAGE || [...b].length > MAX_MESSAGE;
  const check = (a: string, b: string) => (!a.trim() ? t('common.invalid') : tooLong(a, b) ? t('messages.too_long') : null);
  return (
    <>
      <PageTitle>{t('messages.title')}</PageTitle>
      <Card title={t('messages.announce')}>
        <p className="muted">{t('messages.announce_lead')}</p>
        <TextArea label={t('messages.text')} value={text} onChange={setText} dir="rtl" />
        <TextArea label={t('messages.text_en')} value={textEN} onChange={setTextEN} dir="ltr" />
        <Action<{ cities: number }>
          label={t('messages.send')}
          danger
          check={() => check(text, textEN)}
          run={(reason, key) => post('/api/announce', { text: text.trim(), text_en: textEN.trim(), reason }, key)}
          done={(r) => t('messages.sent_groups', { n: r.cities })}
        >
          <blockquote className="preview">{text}</blockquote>
        </Action>
      </Card>
      <Card title={t('messages.broadcast')}>
        <p className="muted">{t('messages.broadcast_lead')}</p>
        <TextArea label={t('messages.text')} value={btext} onChange={setBText} dir="rtl" />
        <TextArea label={t('messages.text_en')} value={btextEN} onChange={setBTextEN} dir="ltr" />
        <Field label={t('messages.only')} value={only} onChange={setOnly} dir="ltr" hint={t('messages.only_hint')} />
        <Action<{ players: number }>
          label={t('messages.send')}
          danger={!only.trim()}
          check={() => check(btext, btextEN)}
          run={(reason, key) =>
            post('/api/broadcast', { text: btext.trim(), text_en: btextEN.trim(), only: only.trim(), reason }, key)
          }
          done={(r) => t('messages.sent_players', { n: r.players })}
        >
          <blockquote className="preview">{btext}</blockquote>
          {only.trim() && (
            <p>
              {t('messages.only')}: <Code>{only.trim()}</Code>
            </p>
          )}
        </Action>
      </Card>
    </>
  );
}

export function Audit() {
  const { t, at } = useI18n();
  const [prefix, setPrefix] = useState('');
  const [shown, setShown] = useState('');
  const load = useLoad<AuditLine[]>(`/api/audit${q({ action: shown, limit: 100 })}`);
  const submit = (e: FormEvent) => {
    e.preventDefault();
    setShown(prefix.trim());
  };
  return (
    <>
      <PageTitle>{t('audit.title')}</PageTitle>
      <form className="inline" onSubmit={submit}>
        <Field label={t('audit.filter')} value={prefix} onChange={setPrefix} dir="ltr" placeholder="economy." />
        <button type="submit" className="btn primary">
          {t('common.show')}
        </button>
      </form>
      <Card>
        <Async load={load}>
          {(rows) => (
            <Table
              rows={rows}
              rowKey={(r) => String(r.id)}
              cols={[
                { label: t('audit.at'), cell: (r) => at(r.at) },
                { label: t('audit.actor'), cell: (r) => <Code>{r.actor}</Code> },
                { label: t('audit.action'), cell: (r) => <Code>{r.action}</Code> },
                { label: t('audit.reason'), cell: (r) => <bdi>{r.reason}</bdi> },
                {
                  label: t('audit.target'),
                  cell: (r) => (
                    <bdi dir="ltr" className="small">
                      {JSON.stringify(r.new_value)}
                    </bdi>
                  ),
                },
              ]}
            />
          )}
        </Async>
      </Card>
    </>
  );
}
