import { useState, type FormEvent } from 'react';
import { Field } from '../components/Action.tsx';
import { useErrorText } from '../components/ui.tsx';
import { useI18n } from '../i18n/index.tsx';
import { login, type Session } from '../lib/api.ts';
import { asciiDigits } from '../lib/format.ts';

export function Login({ onSignedIn, toolbar }: { onSignedIn: (s: Session) => void; toolbar: React.ReactNode }) {
  const { t } = useI18n();
  const errorText = useErrorText();
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [code, setCode] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || !password) {
      setError(t('err.bad_credentials'));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const s = await login(username.trim(), password, asciiDigits(code.trim()));
      setPassword('');
      onSignedIn(s);
    } catch (err) {
      setError(errorText(err));
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="login">
      <div className="login-toolbar">{toolbar}</div>
      <form className="card login-card" onSubmit={submit} noValidate>
        <div className="brand">
          <span className="logo" aria-hidden="true">
            T
          </span>
          <div>
            <strong>{t('app.brand')}</strong>
            <div className="muted small">{t('app.subtitle')}</div>
          </div>
        </div>
        <h1>{t('login.title')}</h1>
        <p className="muted">{t('login.subtitle')}</p>
        <Field label={t('login.username')} value={username} onChange={setUsername} dir="ltr" autoComplete="username" />
        <Field
          label={t('login.password')}
          value={password}
          onChange={setPassword}
          type="password"
          dir="ltr"
          autoComplete="current-password"
        />
        <Field
          label={t('login.code')}
          value={code}
          onChange={setCode}
          dir="ltr"
          inputMode="numeric"
          autoComplete="one-time-code"
          hint={t('login.code_hint')}
        />
        {error && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
        <button type="submit" className="btn primary block" disabled={busy}>
          {busy ? t('login.working') : t('login.submit')}
        </button>
      </form>
    </main>
  );
}
