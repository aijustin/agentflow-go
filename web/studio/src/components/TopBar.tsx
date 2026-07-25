import { NavLink } from 'react-router-dom';
import { useGraph } from '../api/hooks';

const views = [
  { to: '/', label: '画布', end: true },
  { to: '/runs', label: '运行' },
  { to: '/compare', label: '对比' },
];

export function TopBar() {
  const { data: graph } = useGraph();

  return (
    <header className="flex h-12 shrink-0 items-center gap-6 border-b border-line bg-ink-1 px-4">
      <div className="flex items-center gap-2.5">
        <span className="flex size-6 items-center justify-center rounded bg-signal/15 font-mono text-[11px] font-semibold text-signal">
          AF
        </span>
        <span className="text-[13px] font-semibold tracking-wide">AgentFlow Studio</span>
      </div>

      <nav className="flex items-center gap-1">
        {views.map((view) => (
          <NavLink
            key={view.to}
            to={view.to}
            end={view.end}
            className={({ isActive }) =>
              `rounded px-2.5 py-1 text-[12.5px] transition-colors ${
                isActive ? 'bg-ink-3 text-fg-0' : 'text-fg-1 hover:text-fg-0'
              }`
            }
          >
            {view.label}
          </NavLink>
        ))}
      </nav>

      <div className="ml-auto flex items-center gap-3">
        {graph && (
          <span className="font-mono text-[11px] text-muted">
            {graph.name} <span className="text-line-strong">·</span> {graph.mode}
          </span>
        )}
      </div>
    </header>
  );
}
