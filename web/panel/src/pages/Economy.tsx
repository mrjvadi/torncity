import { useState } from 'react';
import { Action, Field } from '../components/Action.tsx';
import { Async, Badge, Card, Code, KV, PageTitle } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { post } from '../lib/api.ts';
import type { ContentStatus, Verification } from '../lib/types.ts';
import { useLoad } from '../lib/useLoad.ts';
import { parseAmount } from '../lib/validate.ts';

export function Economy() {
  const { t, n } = useI18n();
  const [run, setRun] = useState(false);
  const verify = useLoad<Verification>(run ? '/api/economy/verify' : null);
  const [player, setPlayer] = useState('');
  const [amount, setAmount] = useState('');
  return (
    <>
      <PageTitle>{t('economy.title')}</PageTitle>
      <Card
        title={t('economy.grant')}
        actions={
          <Action
            label={t('economy.grant')}
            danger
            lead={t('economy.grant_warning')}
            check={() => (!player.trim() || parseAmount(amount) === null ? t('common.invalid') : null)}
            run={(reason, key) => post('/api/economy/grant', { player: player.trim(), amount: parseAmount(amount), reason }, key)}
            done={() => t('toast.done')}
          >
            <Field label={t('economy.grant_player')} value={player} onChange={setPlayer} dir="ltr" />
            <Field label={t('economy.grant_amount')} value={amount} onChange={setAmount} dir="ltr" inputMode="numeric" />
          </Action>
        }
      >
        <p className="muted">{t('economy.grant_warning')}</p>
      </Card>
      <Card
        title={t('economy.verify')}
        actions={
          <button type="button" className="btn" onClick={() => (run ? verify.reload() : setRun(true))}>
            {t('economy.run_verify')}
          </button>
        }
      >
        {run && (
          <Async load={verify}>
            {(v) => (
              <>
                <p className={v.ok ? 'ok-text' : 'error'} role="status">
                  {v.ok ? t('economy.all_hold') : t('economy.some_fail')}
                </p>
                <KV
                  rows={[
                    [t('economy.accounts'), n(v.accounts)],
                    [t('economy.transactions'), n(v.transactions)],
                    [t('economy.entries'), n(v.entries)],
                    [t('economy.supply'), <Code key="s">{v.money_supply}</Code>],
                  ]}
                />
                <ul className="checks">
                  {v.checks.map((c, i) => (
                    <li key={i} className={c.ok ? 'pass' : 'fail'}>
                      <Badge tone={c.ok ? 'ok' : 'bad'}>{c.ok ? 'PASS' : 'FAIL'}</Badge>{' '}
                      <bdi dir="ltr">{c.text}</bdi>
                      {c.details && (
                        <ul>
                          {c.details.map((d) => (
                            <li key={d}>
                              <bdi dir="ltr">{d}</bdi>
                            </li>
                          ))}
                        </ul>
                      )}
                    </li>
                  ))}
                </ul>
              </>
            )}
          </Async>
        )}
      </Card>
    </>
  );
}

export function Content() {
  const { t, n, at } = useI18n();
  const load = useLoad<ContentStatus>('/api/content');
  return (
    <>
      <PageTitle
        actions={
          <button type="button" className="btn" onClick={load.reload}>
            {t('common.refresh')}
          </button>
        }
      >
        {t('content.title')}
      </PageTitle>
      <Async load={load}>
        {(c) => (
          <>
            <Card title={t('content.version')}>
              {c.version === 0 ? (
                <p className="muted">{t('content.not_loaded')}</p>
              ) : (
                <KV
                  rows={[
                    [t('content.version'), n(c.version)],
                    [t('content.loaded_at'), at(c.loaded_at)],
                    [t('content.loaded_by'), <bdi key="b">{c.loaded_by}</bdi>],
                    [t('content.reason'), <bdi key="r">{c.reason}</bdi>],
                    [t('content.checksum'), <Code key="c">{c.checksum}</Code>],
                  ]}
                />
              )}
            </Card>
            <Card
              title={t('content.local_checksum')}
              actions={
                <Action
                  label={t('content.load')}
                  danger
                  disabled={!!c.local_error || !c.local_checksum || c.matches}
                  lead={
                    <>
                      {t('content.load_lead')} <Code>{c.local_checksum}</Code>
                    </>
                  }
                  run={(reason, key) => post('/api/content/load', { confirm_checksum: c.local_checksum, reason }, key)}
                  done={() => {
                    load.reload();
                    return t('toast.done');
                  }}
                />
              }
            >
              {c.local_error ? (
                <p className="error">
                  {t('content.local_error')}: <bdi dir="ltr">{c.local_error}</bdi>
                </p>
              ) : (
                <>
                  <p className={c.matches ? 'ok-text' : 'warn-text'}>{c.matches ? t('content.matches') : t('content.differs')}</p>
                  <KV rows={[[t('content.local_checksum'), <Code key="l">{c.local_checksum}</Code>]]} />
                </>
              )}
              {(c.warnings ?? []).length > 0 && (
                <>
                  <h3>{t('content.warnings')}</h3>
                  <ul>
                    {(c.warnings ?? []).map((w) => (
                      <li key={w}>
                        <bdi dir="ltr">{w}</bdi>
                      </li>
                    ))}
                  </ul>
                </>
              )}
            </Card>
            {c.counts && (
              <Card title={t('content.counts')}>
                <KV rows={Object.entries(c.counts).map(([k, v]): [string, string] => [k, n(v)])} />
              </Card>
            )}
          </>
        )}
      </Async>
    </>
  );
}
