import { Route, Routes } from 'react-router';
import { TopBar } from './components/TopBar';
import { BuildView } from './views/BuildView';
import { RunsView } from './views/RunsView';
import { RunDetailView } from './views/RunDetailView';
import { CompareView } from './views/CompareView';

export default function App() {
  return (
    <div className="flex h-full flex-col bg-ink-0 text-fg-0">
      <TopBar />
      <main className="min-h-0 flex-1">
        <Routes>
          <Route path="/" element={<BuildView />} />
          <Route path="/runs" element={<RunsView />} />
          <Route path="/runs/:runID" element={<RunDetailView />} />
          <Route path="/compare" element={<CompareView />} />
        </Routes>
      </main>
    </div>
  );
}
