import { LocationProvider, Router, Route } from 'preact-iso';
import { Sidebar } from './components/Sidebar/Sidebar';
import { MainTopBar } from './components/MainTopBar';
import { Home } from './routes/Home';
import { RunRoute } from './routes/RunRoute';
import { IterRoute } from './routes/IterRoute';
import { SettingsRoute } from './routes/SettingsRoute';
import { RepoMetaRoute } from './routes/RepoMetaRoute';
import { ToastStack } from './components/Toast';
import { TweaksPanel } from './components/TweaksPanel';
import {
  mobileNavOpen,
  openMobileNav,
  closeMobileNav,
} from './lib/mobileNav';

export function App() {
  return (
    <LocationProvider>
      {/* Mobile header (hidden above 1100px by the .rv-mobile-only rule) */}
      <header
        class="rv-mobile-only"
        style={{
          position: 'fixed',
          top: 0,
          left: 0,
          right: 0,
          height: 44,
          alignItems: 'center',
          gap: 10,
          padding: '0 12px',
          background: 'var(--bg-card)',
          borderBottom: '1px solid var(--border)',
          zIndex: 30,
          color: 'var(--fg)',
        }}
      >
        <button
          type="button"
          onClick={openMobileNav}
          aria-label="Open sidebar"
          style={{
            width: 28,
            height: 28,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            color: 'var(--fg-muted)',
            borderRadius: 6,
            border: '1px solid var(--border)',
          }}
        >
          <svg
            width="14"
            height="14"
            viewBox="0 0 16 16"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          >
            <path d="M3 4h10M3 8h10M3 12h10" />
          </svg>
        </button>
        <span style={{ fontWeight: 600, fontSize: 13 }}>Ralph Viewer</span>
      </header>

      <div
        class="rv-shell"
        style={{
          display: 'grid',
          gridTemplateColumns: '260px minmax(0, 1fr)',
          gap: 18,
          padding: '18px 18px 18px 0',
          height: '100vh',
          position: 'relative',
          background: 'var(--page)',
        }}
      >
        <div
          class={`rv-sidebar-col ${mobileNavOpen.value ? 'rv-open' : ''}`}
          style={{ height: 'calc(100vh - 36px)', minHeight: 0 }}
        >
          <Sidebar />
        </div>

        {mobileNavOpen.value && (
          <div
            class="rv-scrim rv-mobile-only"
            onClick={closeMobileNav}
          />
        )}

        <main
          class="rv-main-col"
          style={{ minWidth: 0, height: 'calc(100vh - 36px)' }}
        >
          <section
            class="rv-main-card"
            style={{
              background: 'var(--bg-card)',
              borderRadius: 14,
              overflow: 'hidden',
              height: '100%',
              boxShadow: 'var(--shadow-md)',
              minWidth: 0,
              display: 'flex',
              flexDirection: 'column',
            }}
          >
            <MainTopBar />
            <div style={{ flex: 1, overflow: 'auto', minHeight: 0 }}>
              <Router>
                <Route path="/" component={Home} />
                <Route
                  path="/repos/:fp/runs/:runId/iter/:story/:iter"
                  component={IterRoute}
                />
                <Route path="/repos/:fp/runs/:runId" component={RunRoute} />
                <Route path="/repos/:fp/settings" component={SettingsRoute} />
                <Route path="/repos/:fp/meta" component={RepoMetaRoute} />
                <Route default component={Home} />
              </Router>
            </div>
          </section>
        </main>
      </div>
      <ToastStack />
      <TweaksPanel />
    </LocationProvider>
  );
}
