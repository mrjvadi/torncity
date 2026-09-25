import { useCallback, useEffect, useState, type ReactNode } from 'react';
import { ErrorBox, Loading } from './components/ui.tsx';
import { useI18n, type Key } from './i18n/index.tsx';
import { currentSession, logout, setUnauthorizedHandler, type Session } from './lib/api.ts';
import { applyTheme, initialTheme, nextTheme, type Theme } from './lib/theme.ts';
import { Cities, City } from './pages/Cities.tsx';
import { Companies, Company } from './pages/Companies.tsx';
import { Content, Economy } from './pages/Economy.tsx';
import { Elections, Governance } from './pages/Governance.tsx';
import { Login } from './pages/Login.tsx';
import { Overview } from './pages/Overview.tsx';
import { Player, Players } from './pages/Players.tsx';
import { Audit, Messages, Watch } from './pages/Watch.tsx';
import { match, usePath } from './router.ts';

interface NavItem {
  id: string;
  label: Key;
  icon: string;
  primary?: boolean;
}

// Icons are text glyphs: no icon font, nothing fetched.
const NAV: NavItem[] = [
  { id: 'overview', label: 'nav.overview', icon: '◎', primary: true },
  { id: 'players', label: 'nav.players', icon: '☺', primary: true },
  { id: 'cities', label: 'nav.cities', icon: '⌂', primary: true },
  { id: 'watch', label: 'nav.watch', icon: '⚑', primary: true },
  { id: 'companies', label: 'nav.companies', icon: '▣' },
  { id: 'governance', label: 'nav.governance', icon: '⚖' },
  { id: 'elections', label: 'nav.elections', icon: '☑' },
  { id: 'economy', label: 'nav.economy', icon: '∑' },
  { id: 'content', label: 'nav.content', icon: '▤' },
  { id: 'messages', label: 'nav.messages', icon: '✉' },
  { id: 'audit', label: 'nav.audit', icon: '☰' },
];

function Page({ path }: { path: string }) {
  const [page, arg] = match(path);
  switch (page) {
    case 'players':
      return arg ? <Player code={arg} /> : <Players />;
    case 'cities':
      return arg ? <City code={arg} /> : <Cities />;
    case 'companies':
      return arg ? <Company code={arg} /> : <Companies />;
    case 'governance':
      return <Governance />;
    case 'elections':
      return <Elections />;
    case 'economy':
      return <Economy />;
    case 'content':
      return <Content />;
    case 'watch':
      return <Watch />;
    case 'messages':
      return <Messages />;
    case 'audit':
      return <Audit />;
    default:
      return <Overview />;
  }
}

function Toolbar({ theme, setTheme, extra }: { theme: Theme; setTheme: (t: Theme) => void; extra?: ReactNode }) {
  const { t, lang, setLang } = useI18n();
  return (
    <div className="toolbar">
      <button type="button" className="btn small" onClick={() => setLang(lang === 'fa' ? 'en' : 'fa')} lang={lang === 'fa' ? 'en' : 'fa'}>
        {t('nav.language')}
      </button>
      <button type="button" className="btn small" onClick={() => setTheme(nextTheme(theme))} aria-label={t('nav.theme')}>
        {theme === 'dark' ? '☾' : theme === 'light' ? '☀' : '◐'} {t(`theme.${theme}`)}
      </button>
      {extra}
    </div>
  );
}

export function App() {
  const { t } = useI18n();
  const path = usePath();
  const [session, setSession] = useState<Session | null | undefined>(undefined);
  const [bootError, setBootError] = useState<unknown>(null);
  const [theme, setThemeState] = useState<Theme>(initialTheme);
  const [more, setMore] = useState(false);

  const setTheme = useCallback((th: Theme) => {
    applyTheme(th);
    setThemeState(th);
  }, []);

  const boot = useCallback(() => {
    setBootError(null);
    currentSession().then(setSession, setBootError);
  }, []);

  useEffect(() => {
    applyTheme(theme);
    setUnauthorizedHandler(() => setSession(null));
    boot();
  }, [boot]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => setMore(false), [path]);

  if (bootError) return <ErrorBox error={bootError} retry={boot} />;
  if (session === undefined) return <Loading />;
  if (session === null) return <Login onSignedIn={setSession} toolbar={<Toolbar theme={theme} setTheme={setTheme} />} />;

  const [page] = match(path);
  const signOut = async () => {
    try {
      await logout();
    } finally {
      setSession(null);
    }
  };
  const link = (n: NavItem) => (
    <a key={n.id} href={`#/${n.id}`} className={page === n.id ? 'active' : undefined} aria-current={page === n.id ? 'page' : undefined}>
      <span className="icon" aria-hidden="true">
        {n.icon}
      </span>
      <span>{t(n.label)}</span>
    </a>
  );

  return (
    <div className="shell">
      <a className="skip" href="#main" onClick={(e) => { e.preventDefault(); document.getElementById('main')?.focus(); }}>
        {t('nav.skip')}
      </a>
      <aside className="sidebar">
        <div className="brand">
          <span className="logo" aria-hidden="true">
            T
          </span>
          <div>
            <strong>{t('app.brand')}</strong>
            <div className="muted small">{t('app.subtitle')}</div>
          </div>
        </div>
        <nav aria-label={t('nav.main')}>{NAV.map(link)}</nav>
      </aside>
      <div className="main-col">
        <header className="topbar">
          <span className="muted small">
            <bdi dir="ltr">{session.username}</bdi>
          </span>
          <Toolbar
            theme={theme}
            setTheme={setTheme}
            extra={
              <button type="button" className="btn small" onClick={signOut}>
                {t('nav.logout')}
              </button>
            }
          />
        </header>
        <main id="main" tabIndex={-1}>
          <Page path={path} />
        </main>
      </div>
      <nav className="bottomnav" aria-label={t('nav.main')}>
        {NAV.filter((n) => n.primary).map(link)}
        <button type="button" className={more ? 'active' : undefined} aria-expanded={more} onClick={() => setMore((m) => !m)}>
          <span className="icon" aria-hidden="true">
            ⋯
          </span>
          <span>{t('nav.more')}</span>
        </button>
      </nav>
      {more && (
        <div className="more-sheet" role="dialog" aria-label={t('nav.more')}>
          {NAV.filter((n) => !n.primary).map(link)}
        </div>
      )}
    </div>
  );
}
