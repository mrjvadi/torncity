import { lazy, Suspense, useCallback, useEffect, useState, type ReactNode } from 'react';
import { Icon } from './components/Icon.tsx';
import { SearchPalette } from './components/Search.tsx';
import { useToast } from './components/Toast.tsx';
import { ErrorBox, Loading } from './components/ui.tsx';
import { useI18n, type Key } from './i18n/index.tsx';
import { currentSession, logout, setUnauthorizedHandler, type Session } from './lib/api.ts';
import { LiveProvider, useLive, type LiveStatus } from './lib/live.tsx';
import type { LiveEvent } from './lib/realtime.ts';
import { applyTheme, initialTheme, nextTheme, type Theme } from './lib/theme.ts';
import { Login } from './pages/Login.tsx';
import { useRoute, type Route } from './router.ts';

// The pages load on demand, so the first paint carries only the shell.
const Overview = lazy(() => import('./pages/Overview.tsx').then((m) => ({ default: m.Overview })));
const Players = lazy(() => import('./pages/Players.tsx').then((m) => ({ default: m.PlayersPage })));
const Companies = lazy(() => import('./pages/Companies.tsx').then((m) => ({ default: m.CompaniesPage })));
const Cities = lazy(() => import('./pages/Cities.tsx').then((m) => ({ default: m.CitiesPage })));
const Countries = lazy(() => import('./pages/World.tsx').then((m) => ({ default: m.CountriesPage })));
const Wars = lazy(() => import('./pages/World.tsx').then((m) => ({ default: m.WarsPage })));
const Governance = lazy(() => import('./pages/Governance.tsx').then((m) => ({ default: m.GovernancePage })));
const Economy = lazy(() => import('./pages/Economy.tsx').then((m) => ({ default: m.EconomyPage })));
const Markets = lazy(() => import('./pages/Markets.tsx').then((m) => ({ default: m.MarketsPage })));
const Finance = lazy(() => import('./pages/Finance.tsx').then((m) => ({ default: m.FinancePage })));
const Justice = lazy(() => import('./pages/Justice.tsx').then((m) => ({ default: m.JusticePage })));
const Health = lazy(() => import('./pages/Health.tsx').then((m) => ({ default: m.HealthPage })));
const Society = lazy(() => import('./pages/Society.tsx').then((m) => ({ default: m.SocietyPage })));
const Watch = lazy(() => import('./pages/Watch.tsx').then((m) => ({ default: m.WatchPage })));
const Messages = lazy(() => import('./pages/Watch.tsx').then((m) => ({ default: m.MessagesPage })));
const Audit = lazy(() => import('./pages/Watch.tsx').then((m) => ({ default: m.AuditPage })));
const Content = lazy(() => import('./pages/Content.tsx').then((m) => ({ default: m.ContentPage })));
const System = lazy(() => import('./pages/System.tsx').then((m) => ({ default: m.SystemPage })));
const Account = lazy(() => import('./pages/Account.tsx').then((m) => ({ default: m.AccountPage })));

interface NavItem {
  id: string;
  label: Key;
  icon: string;
}

interface NavGroup {
  label: Key;
  items: NavItem[];
}

const NAV: NavGroup[] = [
  {
    label: 'navgroup.operations',
    items: [
      { id: 'overview', label: 'nav.overview', icon: 'home' },
      { id: 'watch', label: 'nav.watch', icon: 'eye' },
      { id: 'messages', label: 'nav.messages', icon: 'message' },
      { id: 'audit', label: 'nav.audit', icon: 'list' },
    ],
  },
  {
    label: 'navgroup.people',
    items: [
      { id: 'players', label: 'nav.players', icon: 'users' },
      { id: 'companies', label: 'nav.companies', icon: 'briefcase' },
      { id: 'society', label: 'nav.society', icon: 'trophy' },
    ],
  },
  {
    label: 'navgroup.world',
    items: [
      { id: 'cities', label: 'nav.cities', icon: 'city' },
      { id: 'countries', label: 'nav.countries', icon: 'globe' },
      { id: 'wars', label: 'nav.wars', icon: 'sword' },
      { id: 'governance', label: 'nav.governance', icon: 'scale' },
    ],
  },
  {
    label: 'navgroup.money',
    items: [
      { id: 'economy', label: 'nav.economy', icon: 'chart' },
      { id: 'markets', label: 'nav.markets', icon: 'store' },
      { id: 'finance', label: 'nav.finance', icon: 'bank' },
    ],
  },
  {
    label: 'navgroup.life',
    items: [
      { id: 'justice', label: 'nav.justice', icon: 'handcuffs' },
      { id: 'health', label: 'nav.health', icon: 'heart' },
    ],
  },
  {
    label: 'navgroup.system',
    items: [
      { id: 'content', label: 'nav.content', icon: 'file' },
      { id: 'system', label: 'nav.system', icon: 'server' },
      { id: 'account', label: 'nav.account', icon: 'key' },
    ],
  },
];

const PRIMARY = ['overview', 'players', 'watch', 'economy'];

function Page({ route, username }: { route: Route; username: string }) {
  switch (route.segments[0]) {
    case 'players':
      return <Players route={route} />;
    case 'companies':
      return <Companies route={route} />;
    case 'cities':
      return <Cities route={route} />;
    case 'countries':
      return <Countries route={route} />;
    case 'wars':
      return <Wars route={route} />;
    case 'governance':
    case 'elections':
      return <Governance route={route} />;
    case 'economy':
      return <Economy route={route} />;
    case 'markets':
      return <Markets route={route} />;
    case 'finance':
      return <Finance route={route} />;
    case 'justice':
      return <Justice route={route} />;
    case 'health':
      return <Health route={route} />;
    case 'society':
    case 'factions':
      return <Society route={route} />;
    case 'watch':
      return <Watch route={route} />;
    case 'messages':
      return <Messages />;
    case 'audit':
      return <Audit />;
    case 'content':
      return <Content route={route} />;
    case 'system':
      return <System route={route} />;
    case 'account':
      return <Account username={username} />;
    default:
      return <Overview />;
  }
}

function LiveBadge({ status }: { status: LiveStatus }) {
  const { t } = useI18n();
  const { state } = useLive();
  const label = t(`live.${status}` as Key);
  return (
    <span className={`live live-${status}`} role="status" title={label} aria-label={label}>
      <span className="live-dot" aria-hidden="true" />
      <span className="live-text">{label}</span>
      {state.health && state.health !== 'ok' && (
        <a className={`badge ${state.health === 'lagging' ? 'warn' : 'bad'}`} href="#/system">
          {t(`health.${state.health}` as Key)}
        </a>
      )}
    </span>
  );
}

function Toolbar({ theme, setTheme, extra }: { theme: Theme; setTheme: (t: Theme) => void; extra?: ReactNode }) {
  const { t, lang, setLang } = useI18n();
  return (
    <div className="toolbar">
      <button type="button" className="icon-btn" onClick={() => setLang(lang === 'fa' ? 'en' : 'fa')} lang={lang === 'fa' ? 'en' : 'fa'}
        title={t('nav.language')} aria-label={t('nav.language')}>
        <Icon name="language" />
        <span className="toolbar-text">{t('nav.language')}</span>
      </button>
      <button type="button" className="icon-btn" onClick={() => setTheme(nextTheme(theme))} aria-label={`${t('nav.theme')}: ${t(`theme.${theme}`)}`}
        title={`${t('nav.theme')}: ${t(`theme.${theme}`)}`}>
        <Icon name={theme === 'dark' ? 'moon' : theme === 'light' ? 'sun' : 'contrast'} />
      </button>
      {extra}
    </div>
  );
}

function Shell({ session, onSignOut, theme, setTheme }: {
  session: Session;
  onSignOut: () => void;
  theme: Theme;
  setTheme: (t: Theme) => void;
}) {
  const { t } = useI18n();
  const route = useRoute();
  const { status } = useLive();
  const [drawer, setDrawer] = useState(false);
  const [searching, setSearching] = useState(false);
  const page = route.segments[0] ?? 'overview';
  const current = (id: string) => page === id || (id === 'governance' && page === 'elections') || (id === 'society' && page === 'factions');

  useEffect(() => setDrawer(false), [route.path]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      if ((e.key === 'k' && (e.ctrlKey || e.metaKey)) || (e.key === '/' && !typing)) {
        e.preventDefault();
        setSearching(true);
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  const link = (n: NavItem) => (
    <a key={n.id} href={`#/${n.id}`} className={current(n.id) ? 'active' : undefined} aria-current={current(n.id) ? 'page' : undefined}>
      <Icon name={n.icon} />
      <span>{t(n.label)}</span>
    </a>
  );
  const all = NAV.flatMap((g) => g.items);
  const nav = (
    <>
      {NAV.map((g) => (
        <div key={g.label} className="nav-group">
          <div className="nav-group-label">{t(g.label)}</div>
          {g.items.map(link)}
        </div>
      ))}
    </>
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
        <nav aria-label={t('nav.main')}>{nav}</nav>
      </aside>
      <div className="main-col">
        <header className="topbar">
          <button type="button" className="icon-btn only-mobile" onClick={() => setDrawer(true)} aria-label={t('nav.more')}>
            <Icon name="menu" />
          </button>
          <button type="button" className="search-trigger" onClick={() => setSearching(true)} aria-keyshortcuts="Control+K /">
            <Icon name="search" size={16} />
            <span>{t('search.placeholder')}</span>
            <kbd>Ctrl K</kbd>
          </button>
          <LiveBadge status={status} />
          <Toolbar
            theme={theme}
            setTheme={setTheme}
            extra={
              <>
                <a className="icon-btn user" href="#/account" title={t('nav.account')}>
                  <Icon name="user" />
                  <bdi dir="ltr" className="toolbar-text">
                    {session.username}
                  </bdi>
                </a>
                <button type="button" className="icon-btn" onClick={onSignOut} aria-label={t('nav.logout')} title={t('nav.logout')}>
                  <Icon name="logout" />
                </button>
              </>
            }
          />
        </header>
        <main id="main" tabIndex={-1}>
          <Suspense fallback={<Loading rows={6} />}>
            <Page route={route} username={session.username} />
          </Suspense>
        </main>
      </div>
      <nav className="bottomnav" aria-label={t('nav.main')}>
        {all.filter((n) => PRIMARY.includes(n.id)).map(link)}
        <button type="button" className={drawer ? 'active' : undefined} aria-expanded={drawer} onClick={() => setDrawer(true)}>
          <Icon name="menu" />
          <span>{t('nav.more')}</span>
        </button>
      </nav>
      {drawer && (
        <div className="drawer-backdrop" onClick={() => setDrawer(false)}>
          <div className="drawer" role="dialog" aria-modal="true" aria-label={t('nav.main')} onClick={(e) => e.stopPropagation()}>
            <div className="drawer-head">
              <strong>{t('app.brand')}</strong>
              <button type="button" className="icon-btn" onClick={() => setDrawer(false)} aria-label={t('common.close')}>
                <Icon name="x" />
              </button>
            </div>
            <nav aria-label={t('nav.main')}>{nav}</nav>
          </div>
        </div>
      )}
      <SearchPalette open={searching} onClose={() => setSearching(false)} />
    </div>
  );
}

export function App() {
  const { t } = useI18n();
  const toast = useToast();
  const [session, setSession] = useState<Session | null | undefined>(undefined);
  const [bootError, setBootError] = useState<unknown>(null);
  const [theme, setThemeState] = useState<Theme>(initialTheme);

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

  const onEvent = useCallback(
    (e: LiveEvent) => {
      if (e.type === 'flag' && e.data.status === 'open') toast('warn', t('live.new_flag', { player: String(e.data.player ?? '') }));
      else if (e.type === 'hold') toast('warn', t('live.new_hold', { no: Number(e.data.no ?? 0) }));
      else if (e.type === 'health') toast(e.data.state === 'ok' ? 'ok' : 'bad', t(`health.${String(e.data.state)}` as Key));
    },
    [toast, t],
  );

  if (bootError) return <ErrorBox error={bootError} retry={boot} />;
  if (session === undefined) return <Loading />;
  if (session === null) return <Login onSignedIn={setSession} toolbar={<Toolbar theme={theme} setTheme={setTheme} />} />;

  const signOut = async () => {
    try {
      await logout();
    } finally {
      setSession(null);
    }
  };
  return (
    <LiveProvider enabled onEvent={onEvent}>
      <Shell session={session} onSignOut={() => void signOut()} theme={theme} setTheme={setTheme} />
    </LiveProvider>
  );
}
