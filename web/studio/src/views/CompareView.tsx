import { useState } from 'react';
import { useCompare, useRuns } from '../api/hooks';
import { StatusBadge } from '../components/StatusBadge';
import { prettyJSON, shortID } from '../lib/format';

export function CompareView() {
  const { data } = useRuns();
  const runs = data?.runs ?? [];
  const [runA, setRunA] = useState('');
  const [runB, setRunB] = useState('');
  const { data: result, isLoading, error } = useCompare(runA || undefined, runB || undefined);

  const picker = (value: string, set: (v: string) => void, label: string) => (
    <label className="flex items-center gap-2">
      <span className="label-micro">{label}</span>
      <select
        value={value}
        onChange={(e) => set(e.target.value)}
        className="h-8 rounded border border-line-strong bg-ink-2 px-2 font-mono text-[11.5px] text-fg-0 focus:border-signal focus:outline-none"
      >
        <option value="">选择 run…</option>
        {runs.map((run) => (
          <option key={run.run_id} value={run.run_id}>
            {shortID(run.run_id, 18)} · {run.status}
          </option>
        ))}
      </select>
    </label>
  );

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-6 border-b border-line bg-ink-1 px-4 py-2.5">
        <span className="label-micro">运行对比</span>
        {picker(runA, setRunA, 'Run A')}
        {picker(runB, setRunB, 'Run B')}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {!runA || !runB ? (
          <div className="flex h-full items-center justify-center text-[13px] text-muted">
            选择两个 run 查看 step 级差异
          </div>
        ) : isLoading ? (
          <p className="text-[13px] text-muted">对比中…</p>
        ) : error ? (
          <p className="font-mono text-[12px] text-fail">{error instanceof Error ? error.message : '对比失败'}</p>
        ) : result ? (
          <div>
            <div className="mb-3 flex items-center gap-3 font-mono text-[11.5px] text-fg-1">
              <span>
                A <StatusBadge status={result.status_a} />
              </span>
              <span>
                B <StatusBadge status={result.status_b} />
              </span>
              <span className="text-muted">
                仅 A：{result.steps_only_a.length} · 仅 B：{result.steps_only_b.length} · 共享：{result.shared_steps.length}
              </span>
            </div>
            {result.steps_only_a.map((id) => (
              <DiffRow key={`a-${id}`} nodeID={id} marker="仅 A" tone="text-live" />
            ))}
            {result.steps_only_b.map((id) => (
              <DiffRow key={`b-${id}`} nodeID={id} marker="仅 B" tone="text-kind-skill" />
            ))}
            {result.shared_steps.map((step) => (
              <div key={step.node_id} className="mb-2 rounded border border-line bg-ink-1">
                <div className="flex items-center gap-2 border-b border-line px-3 py-1.5">
                  <span className="font-mono text-[12px] text-fg-0">{step.node_id}</span>
                  <span className={`ml-auto font-mono text-[10.5px] ${step.same ? 'text-ok' : 'text-warn'}`}>
                    {step.same ? '输出一致' : '输出不一致'}
                  </span>
                </div>
                {!step.same && (
                  <div className="grid grid-cols-2 gap-px bg-line">
                    <pre className="max-h-40 overflow-auto bg-ink-1 p-2 font-mono text-[10.5px] text-fg-1">
                      {prettyJSON(step.output_a?.inline ?? null) || '（空）'}
                    </pre>
                    <pre className="max-h-40 overflow-auto bg-ink-1 p-2 font-mono text-[10.5px] text-fg-1">
                      {prettyJSON(step.output_b?.inline ?? null) || '（空）'}
                    </pre>
                  </div>
                )}
              </div>
            ))}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function DiffRow({ nodeID, marker, tone }: { nodeID: string; marker: string; tone: string }) {
  return (
    <div className="mb-2 flex items-center gap-2 rounded border border-line bg-ink-1 px-3 py-2">
      <span className="font-mono text-[12px] text-fg-0">{nodeID}</span>
      <span className={`ml-auto font-mono text-[10.5px] ${tone}`}>{marker}</span>
    </div>
  );
}
