import { useState, type FormEvent } from 'react';
import { Field } from '../components/Action.tsx';
import { useToast } from '../components/Toast.tsx';
import { Async, Badge, Card, Code, Grid, KV, PageHeader, Table, When, useErrorText } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { postSelf, setCsrf, type Session } from '../lib/api.ts';
import type { Me, SessionLine } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';

// An operator's own account: password, two-factor sign-in, sessions. Each
// change asks for the current password (and code when enrolled) again, and
// signs this browser in afresh, since it ends every other session.

export function AccountPage({ username }: { username: string }) {
  const { t } = useI18n();
  const me = useLoad<Me>('/api/me');
  const sessions = useLoad<SessionLine[]>('/api/me/sessions');
  const reload = () => {
    me.reload();
    sessions.reload();
  };
  return (
    <>
      <PageHeader title={t('account.title')} subtitle={<bdi dir="ltr">{username}</bdi>} crumbs={[{ label: t('nav.account') }]} />
      <Async load={me}>
        {(m) => (
          <Grid>
            <Card title={t('account.profile')}>
              <KV rows={[
                [t('login.username'), <Code key="u">{m.username}</Code>],
                [t('account.two_factor'), m.totp_enabled ? <Badge key="t" tone="ok">{t('account.on')}</Badge> : <Badge key="t" tone="warn">{t('account.off')}</Badge>],
                [t('account.last_login'), <When key="l" iso={m.last_login_at} />],
                [t('account.created'), <When key="c" iso={m.created_at} />],
                [t('account.session_ends'), <When key="s" iso={m.session_expires_at} rel />],
              ]} />
            </Card>
            <Password me={m} onDone={reload} />
            <TwoFactor me={m} onDone={reload} />
          </Grid>
        )}
      </Async>
      <Card title={t('account.sessions')}>
        <Async load={sessions}>
          {(list) => (
            <Table rows={list} rowKey={(s) => s.id} cols={[
              { label: t('account.device'), cell: (s) => <span className="small"><bdi dir="ltr">{s.user_agent || '—'}</bdi>{s.current && <> <Badge tone="info">{t('account.this_one')}</Badge></>}</span> },
              { label: t('account.ip'), cell: (s) => <Code>{s.client_ip}</Code> },
              { label: t('account.started'), cell: (s) => <When iso={s.created_at} /> },
              { label: t('account.seen'), cell: (s) => <When iso={s.last_seen_at} rel /> },
              { label: '', cell: (s) => (s.current ? null : <Revoke id={s.id} onDone={sessions.reload} />) },
            ]} />
          )}
        </Async>
      </Card>
    </>
  );
}

function useSelf() {
  const toast = useToast();
  const errorText = useErrorText();
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const run = async <T,>(fn: () => Promise<T>, ok: string): Promise<T | null> => {
    setBusy(true);
    setProblem(null);
    try {
      const r = await fn();
      toast('ok', ok);
      return r;
    } catch (e) {
      setProblem(errorText(e));
      return null;
    } finally {
      setBusy(false);
    }
  };
  return { busy, problem, run };
}

function Password({ me, onDone }: { me: Me; onDone: () => void }) {
  const { t } = useI18n();
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [again, setAgain] = useState('');
  const [code, setCode] = useState('');
  const { busy, problem, run } = useSelf();
  const [local, setLocal] = useState<string | null>(null);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if ([...next].length < me.password_min_length) return setLocal(t('account.too_short', { n: me.password_min_length }));
    if (next !== again) return setLocal(t('account.mismatch'));
    setLocal(null);
    const s = await run(() => postSelf<Session>('/api/me/password', { current, new: next, code }), t('account.password_changed'));
    if (s) {
      setCsrf(s.csrf);
      setCurrent('');
      setNext('');
      setAgain('');
      setCode('');
      onDone();
    }
  };
  return (
    <Card title={t('account.password')}>
      <form onSubmit={submit} noValidate>
        <Field label={t('account.current_password')} type="password" value={current} onChange={setCurrent} autoComplete="current-password" dir="ltr" />
        <Field label={t('account.new_password')} type="password" value={next} onChange={setNext} autoComplete="new-password" dir="ltr"
          hint={t('account.too_short', { n: me.password_min_length })} />
        <Field label={t('account.repeat_password')} type="password" value={again} onChange={setAgain} autoComplete="new-password" dir="ltr" />
        {me.totp_enabled && <Field label={t('login.code')} value={code} onChange={setCode} inputMode="numeric" autoComplete="one-time-code" dir="ltr" />}
        {(local ?? problem) && <p className="error" role="alert">{local ?? problem}</p>}
        <button type="submit" className="btn primary" disabled={busy}>{t('account.change_password')}</button>
      </form>
    </Card>
  );
}

function TwoFactor({ me, onDone }: { me: Me; onDone: () => void }) {
  const { t } = useI18n();
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [pending, setPending] = useState<{ secret: string; uri: string } | null>(null);
  const { busy, problem, run } = useSelf();
  const begin = async (e: FormEvent) => {
    e.preventDefault();
    const r = await run(() => postSelf<{ secret: string; uri: string }>('/api/me/totp/begin', { password }), t('account.scan'));
    if (r) setPending(r);
  };
  const enable = async (e: FormEvent) => {
    e.preventDefault();
    if (!pending) return;
    const s = await run(() => postSelf<Session>('/api/me/totp/enable', { password, secret: pending.secret, code }), t('account.totp_on'));
    if (s) {
      setCsrf(s.csrf);
      setPending(null);
      setPassword('');
      setCode('');
      onDone();
    }
  };
  const disable = async (e: FormEvent) => {
    e.preventDefault();
    const s = await run(() => postSelf<Session>('/api/me/totp/disable', { password, code }), t('account.totp_off'));
    if (s) {
      setCsrf(s.csrf);
      setPassword('');
      setCode('');
      onDone();
    }
  };
  return (
    <Card title={t('account.two_factor')}>
      {me.totp_enabled ? (
        <form onSubmit={disable} noValidate>
          <p className="muted">{t('account.totp_disable_lead')}</p>
          <Field label={t('account.current_password')} type="password" value={password} onChange={setPassword} autoComplete="current-password" dir="ltr" />
          <Field label={t('login.code')} value={code} onChange={setCode} inputMode="numeric" autoComplete="one-time-code" dir="ltr" />
          {problem && <p className="error" role="alert">{problem}</p>}
          <button type="submit" className="btn danger" disabled={busy}>{t('account.disable_totp')}</button>
        </form>
      ) : pending ? (
        <form onSubmit={enable} noValidate>
          <p>{t('account.scan_lead')}</p>
          <p className="secret"><Code>{pending.secret.replace(/(.{4})/g, '$1 ').trim()}</Code></p>
          <details>
            <summary>{t('account.uri')}</summary>
            <p className="small"><bdi dir="ltr" className="code wrap">{pending.uri}</bdi></p>
          </details>
          <Field label={t('login.code')} value={code} onChange={setCode} inputMode="numeric" autoComplete="one-time-code" dir="ltr" />
          {problem && <p className="error" role="alert">{problem}</p>}
          <div className="inline">
            <button type="button" className="btn" onClick={() => setPending(null)}>{t('common.cancel')}</button>
            <button type="submit" className="btn primary" disabled={busy}>{t('account.enable_totp')}</button>
          </div>
        </form>
      ) : (
        <form onSubmit={begin} noValidate>
          <p className="muted">{t('account.totp_lead')}</p>
          <Field label={t('account.current_password')} type="password" value={password} onChange={setPassword} autoComplete="current-password" dir="ltr" />
          {problem && <p className="error" role="alert">{problem}</p>}
          <button type="submit" className="btn primary" disabled={busy}>{t('account.begin_totp')}</button>
        </form>
      )}
    </Card>
  );
}

function Revoke({ id, onDone }: { id: string; onDone: () => void }) {
  const { t } = useI18n();
  const { busy, run } = useSelf();
  return (
    <button type="button" className="btn small danger" disabled={busy}
      onClick={async () => {
        if (await run(() => postSelf(`/api/me/sessions/${encodeURIComponent(id)}/revoke`, {}), t('toast.done'))) onDone();
      }}>
      {t('account.revoke')}
    </button>
  );
}
