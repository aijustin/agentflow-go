import type { RunStatus } from '../api/types';

const styles: Record<string, string> = {
  running: 'text-live border-live/40 bg-live/10',
  paused: 'text-warn border-warn/40 bg-warn/10',
  failed: 'text-fail border-fail/40 bg-fail/10',
  completed: 'text-ok border-ok/40 bg-ok/10',
  cancelled: 'text-muted border-line-strong bg-ink-3',
};

const labels: Record<string, string> = {
  running: '运行中',
  paused: '已暂停',
  failed: '失败',
  completed: '已完成',
  cancelled: '已取消',
};

export function StatusBadge({ status }: { status: RunStatus | string }) {
  const cls = styles[status] ?? styles.cancelled;
  return (
    <span className={`inline-flex items-center gap-1.5 rounded border px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${cls}`}>
      {status === 'running' && <span className="size-1.5 rounded-full bg-live animate-pulse" />}
      {labels[status] ?? status}
    </span>
  );
}
