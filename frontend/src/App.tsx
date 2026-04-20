import { LocationProvider, Router, Route } from 'preact-iso';
import { Sidebar } from './components/Sidebar/Sidebar';
import { Home } from './routes/Home';
import { RunPlaceholder } from './routes/RunPlaceholder';

export function App() {
  return (
    <LocationProvider>
      <div class="flex h-screen bg-neutral-950 text-neutral-100">
        <Sidebar />
        <main class="flex-1 overflow-y-auto">
          <Router>
            <Route path="/" component={Home} />
            <Route path="/repos/:fp/runs/:runId" component={RunPlaceholder} />
            <Route default component={Home} />
          </Router>
        </main>
      </div>
    </LocationProvider>
  );
}
