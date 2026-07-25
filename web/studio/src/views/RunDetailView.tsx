import { useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router';
import { useQuery } from '@tanstack/react-query';
import { apiGet, apiPost } from '../api/client';
import type { EventRecord, RunResult, RunSnapshot } from '../api/types';
import { useRunCheckpoints, useRunSteps, useRunThread } from '../api/hooks';
import { QueryError } from '../components/QueryError';
import { StatusBadge } from '../components/StatusBadge';
import { formatTime, prettyJSON } from '../lib/format';

type Tab = 'trace' | 'steps' | 'checkpoints';

export function RunDetailView() {
  const { runID } = useParams<{ runID: string }>();
  const navigate = useNavigate();
  const [tab, setTab] = useState<Tab>('trace');
  const { data: steps } = useRunSteps(runID);

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-3 border-b border-line bg-ink-1 px-4 py-2.5">
        <Link to="/runs" className="font-mono text-[11px] text-muted hover:text-fg-0">
          ← 运行
        </Link>
        <span className="font-mono text-[12.5px] text-fg-0">{runID}</span>
        {steps && <StatusBadge status={steps.status} />}
        <button
          onClick={() => navigate(`/?run=${runID}`)}
          className="rounded border border-live/40 bg-live/10 px-2.5 py-1 text-[11.5px] text-live hover:bg-live/20"
        >
          在画布查看轨迹
        </button>
        <div className="ml-4 flex gap-1">
          {(
            [
              ['trace', 'Trace'],
              ['steps', 'Steps'],
              ['checkpoints', 'Checkpoints'],
            ] as [Tab, string][]
          ).map(([key, label]) => (
            <button
              key={key}
              onClick={() => setTab(key)}
              className={`rounded px-2 py-1 text-[12px] transition-colors ${
                tab === key ? 'bg-ink-3 text-fg-0' : 'text-fg-1 hover:text-fg-0'
              }`}
            >
              {label}
            </button>
          ))}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-hidden">
        {tab === 'trace' && <TraceTab runID={runID!} />}
        {tab === 'steps' && <StepsTab runID={runID!} />}
        {tab === 'checkpoints' && <CheckpointsTab runID={runID!} />}
      </div>
    </div>
  );
}

function TraceTab({ runID }: { runID: string }) {
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['events', runID],
    queryFn: () => apiGet<{ events: EventRecord[] }>(`runs/${runID}/events?limit=500&preset=product_ui`),
  });
  const [expandedID, setExpandedID] = useState<number | null>(null);
  const events = data?.events ?? [];

  if (isLoading) return <p className="p-4 text-[13px] text-muted">加载事件中…</p>;
  if (isError) return <QueryError error={error} label="Trace 加载失败" />;

  // Build a span tree: roots ordered by sequence, children nested by parent_span_id.
  const byParent = new Map<string | undefined, EventRecord[]>();
  for (const record of events) {
    const parent = record.event.parent_span_id;
    if (!byParent.has(parent)) byParent.set(parent, []);
    byParent.get(parent)!.push(record);
  }
  const roots = [...(byParent.get(undefined) ?? []), ...events.filter((e) => e.event.parent_span_id && !events.some((p) => p.event.span_id === e.event.parent_span_id))];

  const renderNode = (record: EventRecord, depth: number): React.ReactNode => {
    const children = record.event.span_id ? (byParent.get(record.event.span_id) ?? []) : [];
    const expanded = expandedID === record.id;
    const hasPayload = record.event.payload != null && prettyJSON(record.event.payload) !== '';
    return (
      <div key={record.id}>
        <button
          onClick={() => hasPayload && setExpandedID(expanded ? null : record.id)}
          className={`flex w-full items-baseline gap-2 border-b border-line/50 px-4 py-1.5 text-left hover:bg-ink-1 ${hasPayload ? 'cursor-pointer' : 'cursor-default'}`}
          style={{ paddingLeft: `${16 + depth * 20}px` }}
        >
          <span className="font-mono text-[10.5px] text-muted">{formatTime(record.event.timestamp)}</span>
          <span className="text-[12px] text-fg-0">{record.event.display_label || record.event.type}</span>
          <span className="font-mono text-[10.5px] text-muted">{record.event.type}</span>
          {hasPayload && <span className="ml-auto font-mono text-[10px] text-muted">{expanded ? '▾' : '▸'}</span>}
        </button>
        {expanded && hasPayload && (
          <pre
            className="overflow-x-auto border-b border-line bg-ink-2 px-4 py-2 font-mono text-[10.5px] leading-relaxed text-fg-1"
            style={{ marginLeft: `${16 + depth * 20}px` }}
          >
            {prettyJSON(record.event.payload)}
          </pre>
        )}
        {children.map((child) => renderNode(child, depth + 1))}
      </div>
    );
  };

  return <div className="h-full overflow-y-auto">{roots.map((r) => renderNode(r, 0))}</div>;
}

function StepsTab({ runID }: { runID: string }) {
  const { data: steps, isLoading, isError, error } = useRunSteps(runID);
  const [resumeMsg, setResumeMsg] = useState('');

  if (isLoading) return <p className="p-4 text-[13px] text-muted">加载 steps…</p>;
  if (isError) return <QueryError error={error} label="Steps 加载失败" />;
  if (!steps) return <p className="p-4 text-[13px] text-muted">暂无 step 数据</p>;

  const resumeFrom = async (nodeID: string) => {
    try {
      const result = await apiPost<RunResult>(`runs/${runID}/resume-from-step`, { node_id: nodeID });
      setResumeMsg(`已从 ${nodeID} 恢复：${result.status}`);
    } catch (err) {
      setResumeMsg(err instanceof Error ? err.message : '恢复失败');
    }
  };

  return (
    <div className="h-full overflow-y-auto p-4">
      {resumeMsg && <p className="mb-2 font-mono text-[11px] text-signal">{resumeMsg}</p>}
      {steps.steps.length === 0 && <p className="text-[13px] text-muted">暂无 step 输出</p>}
      {steps.steps.map((step) => (
        <div key={step.node_id} className="mb-2 rounded border border-line bg-ink-1">
          <div className="flex items-center gap-2 border-b border-line px-3 py-1.5">
            <span className="font-mono text-[12px] text-fg-0">{step.node_id}</span>
            <button
              onClick={() => resumeFrom(step.node_id)}
              className="ml-auto rounded border border-line-strong px-2 py-0.5 font-mono text-[10.5px] text-fg-1 hover:bg-ink-2"
            >
              从此恢复
            </button>
          </div>
          <pre className="max-h-48 overflow-auto px-3 py-2 font-mono text-[11px] leading-relaxed text-fg-1">
            {prettyJSON(step.output.inline ?? step.output.blob ?? null) || '（无输出）'}
          </pre>
        </div>
      ))}
    </div>
  );
}

function CheckpointsTab({ runID }: { runID: string }) {
  const { data, isLoading, isError, error } = useRunCheckpoints(runID);
  const { data: thread } = useRunThread(runID);
  const [selected, setSelected] = useState<number | null>(null);
  const [snapshot, setSnapshot] = useState<RunSnapshot | null>(null);
  const [message, setMessage] = useState('');

  if (isLoading) return <p className="p-4 text-[13px] text-muted">加载 checkpoints…</p>;
  if (isError) return <QueryError error={error} label="Checkpoints 加载失败" />;
  const checkpoints = data?.checkpoints ?? [];

  const loadVersion = async (version: number) => {
    setSelected(version);
    try {
      setSnapshot(await apiGet<RunSnapshot>(`runs/${runID}/checkpoints/${version}`));
    } catch (err) {
      setSnapshot(null);
      setMessage(err instanceof Error ? err.message : `Checkpoint v${version} 加载失败`);
    }
  };

  const action = async (kind: 'resume' | 'fork') => {
    if (selected == null) return;
    try {
      if (kind === 'resume') {
        const result = await apiPost<RunResult>(`runs/${runID}/resume-from-checkpoint`, { version: selected });
        setMessage(`已从 v${selected} 恢复：${result.status}`);
      } else {
        const result = await apiPost<{ run_id: string }>(`runs/${runID}/fork`, { version: selected });
        setMessage(`已分叉出新 run：${result.run_id}`);
      }
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '操作失败');
    }
  };

  return (
    <div className="flex h-full">
      <div className="w-72 shrink-0 overflow-y-auto border-r border-line">
        {checkpoints.length === 0 && <p className="p-4 text-[13px] text-muted">暂无 checkpoint</p>}
        {checkpoints.map((cp) => (
          <button
            key={cp.version}
            onClick={() => loadVersion(cp.version)}
            className={`flex w-full items-center gap-2 border-b border-line px-4 py-2.5 text-left hover:bg-ink-1 ${
              selected === cp.version ? 'bg-ink-2' : ''
            }`}
          >
            <span className="font-mono text-[12px] text-signal">v{cp.version}</span>
            <StatusBadge status={cp.status} />
            <span className="ml-auto font-mono text-[10.5px] text-muted">{cp.step_count} steps</span>
          </button>
        ))}
        {thread && thread.runs.length > 1 && (
          <div className="p-3">
            <div className="label-micro mb-1.5">Thread lineage</div>
            {thread.runs.map((run) => (
              <div key={run.run_id} className="flex items-center gap-2 py-1 font-mono text-[11px] text-fg-1">
                <Link to={`/runs/${run.run_id}`} className="text-live hover:underline">
                  {run.run_id.slice(0, 14)}
                </Link>
                {run.parent_run_id && <span className="text-muted">← {run.parent_run_id.slice(0, 10)}</span>}
              </div>
            ))}
          </div>
        )}
      </div>
      <div className="min-w-0 flex-1 overflow-y-auto p-4">
        {selected == null ? (
          <p className="text-[13px] text-muted">选择左侧的版本查看快照，可恢复或分叉</p>
        ) : (
          <div className="rise-in">
            <div className="mb-3 flex items-center gap-2">
              <span className="text-[13px] text-fg-0">Checkpoint v{selected}</span>
              <button onClick={() => action('resume')} className="rounded border border-signal-dim bg-signal/15 px-2.5 py-1 text-[11.5px] text-signal hover:bg-signal/25">
                从此版本恢复
              </button>
              <button onClick={() => action('fork')} className="rounded border border-line-strong px-2.5 py-1 text-[11.5px] text-fg-1 hover:bg-ink-2">
                分叉为新 run
              </button>
            </div>
            {message && <p className="mb-2 font-mono text-[11px] text-signal">{message}</p>}
            {snapshot && (
              <pre className="max-h-[70vh] overflow-auto rounded border border-line bg-ink-1 p-3 font-mono text-[11px] leading-relaxed text-fg-1">
                {prettyJSON(snapshot)}
              </pre>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
