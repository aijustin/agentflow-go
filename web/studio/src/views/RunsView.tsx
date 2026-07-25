import { useState } from 'react';
import { Link } from 'react-router-dom';
import { useRuns } from '../api/hooks';
import { StatusBadge } from '../components/StatusBadge';
import { formatDateTime, shortID } from '../lib/format';

const filters = [
  { value: '', label: '全部' },
  { value: 'running', label: '运行中' },
  { value: 'paused', label: '已暂停' },
  { value: 'failed', label: '失败' },
  { value: 'completed', label: '已完成' },
];

export function RunsView() {
  const [status, setStatus] = useState('');
  const { data, isLoading } = useRuns(status || undefined);
  const runs = data?.runs ?? [];

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b border-line bg-ink-1 px-4 py-2.5">
        <span className="label-micro">运行记录</span>
        <div className="ml-4 flex gap-1">
          {filters.map((f) => (
            <button
              key={f.value}
              onClick={() => setStatus(f.value)}
              className={`rounded px-2 py-1 text-[12px] transition-colors ${
                status === f.value ? 'bg-ink-3 text-fg-0' : 'text-fg-1 hover:text-fg-0'
              }`}
            >
              {f.label}
            </button>
          ))}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {isLoading && <p className="p-4 text-[13px] text-muted">加载中…</p>}
        {!isLoading && runs.length === 0 && (
          <div className="flex h-full flex-col items-center justify-center gap-2 text-muted">
            <p className="text-[13px]">还没有运行记录</p>
            <p className="font-mono text-[11px]">到「画布」视图试跑一张图，或通过 API 触发 run</p>
          </div>
        )}
        <table className="w-full border-collapse">
          <tbody>
            {runs.map((run) => (
              <tr key={run.run_id} className="border-b border-line transition-colors hover:bg-ink-1">
                <td className="px-4 py-2.5">
                  <Link to={`/runs/${run.run_id}`} className="font-mono text-[12.5px] text-live hover:underline">
                    {shortID(run.run_id, 16)}
                  </Link>
                </td>
                <td className="px-2 py-2.5">
                  <StatusBadge status={run.status} />
                </td>
                <td className="px-2 py-2.5 text-[12px] text-fg-1">{run.scenario_name || '—'}</td>
                <td className="px-2 py-2.5 font-mono text-[11px] text-muted">{run.event_count} events</td>
                <td className="px-2 py-2.5 font-mono text-[11px] text-muted">{run.last_event_type || '—'}</td>
                <td className="px-4 py-2.5 text-right font-mono text-[11px] text-muted">
                  {formatDateTime(run.last_seen_at)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}
