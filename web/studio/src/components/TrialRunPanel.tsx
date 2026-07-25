import { useEffect, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { apiGet, apiPost, apiURL } from '../api/client';
import type { EventRecord, RunResult, RunStatus, RunStepsResult } from '../api/types';
import { useCanvasStore } from '../store/canvas';
import { StatusBadge } from './StatusBadge';

type TrialPhase = 'idle' | 'starting' | 'streaming' | 'done';

export function TrialRunPanel() {
  const doc = useCanvasStore((s) => s.doc);
  const scenarioDraft = useCanvasStore((s) => s.scenarioDraft);
  const setNodeRunState = useCanvasStore((s) => s.setNodeRunState);
  const clearNodeRunState = useCanvasStore((s) => s.clearNodeRunState);
  const [, setParams] = useSearchParams();
  const [open, setOpen] = useState(false);
  const [prompt, setPrompt] = useState('');
  const [phase, setPhase] = useState<TrialPhase>('idle');
  const [run, setRun] = useState<RunResult | null>(null);
  const [steps, setSteps] = useState<RunStepsResult | null>(null);
  const [log, setLog] = useState<string[]>([]);
  const [error, setError] = useState('');
  const sourceRef = useRef<EventSource | null>(null);

  const stopStream = () => {
    sourceRef.current?.close();
    sourceRef.current = null;
  };

  useEffect(() => stopStream, []);

  const start = async () => {
    if (!doc || phase === 'starting' || phase === 'streaming') return;
    stopStream();
    setParams({}); // a trial run replaces any run overlay on the canvas
    clearNodeRunState();
    setLog([]);
    setSteps(null);
    setError('');
    setRun(null);
    setOpen(true);
    setPhase('starting');
    try {
      const result = await apiPost<RunResult>('studio/run', {
        graph: doc,
        scenario: scenarioDraft ?? undefined,
        prompt: prompt.trim(),
      });
      setRun(result);
      if (result.status !== 'running' && result.status !== 'paused') {
        await hydrateResult(result.run_id);
        setPhase('done');
        return;
      }
      subscribe(result.run_id);
    } catch (err) {
      setError(err instanceof Error ? err.message : '试跑启动失败');
      setPhase('done');
    }
  };

  const subscribe = (runID: string) => {
    setPhase('streaming');
    const source = new EventSource(apiURL(`runs/${runID}/stream?preset=product_ui`));
    sourceRef.current = source;
    source.addEventListener('runtime_event', (raw) => {
      const record = JSON.parse((raw as MessageEvent).data) as EventRecord;
      const type = record.event.type;
      const payload = (record.event.payload ?? {}) as Record<string, unknown>;
      const nodeID = (payload.node_id ?? payload.node ?? payload.step) as string | undefined;
      if (nodeID) {
        if (type === 'StepStarted') setNodeRunState(nodeID, 'running');
        if (type === 'StepCompleted') setNodeRunState(nodeID, 'done');
        if (type === 'StepFailed') setNodeRunState(nodeID, 'failed');
      }
      setLog((prev) => [...prev.slice(-199), eventLine(record)]);
      if (type === 'RunCompleted' || type === 'RunFailed' || type === 'RunCancelled') {
        const status: RunStatus = type === 'RunCompleted' ? 'completed' : type === 'RunFailed' ? 'failed' : 'cancelled';
        void finishRun(runID, status);
      }
      if (type === 'RunPaused') {
        void finishRun(runID, 'paused');
      }
    });
    source.onerror = () => {
      stopStream();
      setError('运行事件流已中断，请在 Runs 页面查看最终状态');
      setPhase('done');
    };
  };

  const hydrateResult = async (runID: string) => {
    const [stepResult, eventResult] = await Promise.all([
      apiGet<RunStepsResult>(`runs/${runID}/steps`),
      apiGet<{ events: EventRecord[] }>(`runs/${runID}/events?preset=product_ui`),
    ]);
    setSteps(stepResult);
    setLog(eventResult.events.slice(-200).map(eventLine));
    clearNodeRunState();
    for (const step of stepResult.steps) setNodeRunState(step.node_id, 'done');
    if (stepResult.current_node_id) {
      setNodeRunState(stepResult.current_node_id, stepResult.status === 'failed' ? 'failed' : 'running');
    }
  };

  const finishRun = async (runID: string, status: RunStatus) => {
    stopStream();
    setRun((prev) => (prev ? { ...prev, status } : prev));
    try {
      await hydrateResult(runID);
    } catch (err) {
      setError(err instanceof Error ? `试跑结果加载失败：${err.message}` : '试跑结果加载失败');
    } finally {
      setPhase('done');
    }
  };

  const hitlResume = async (decision: 'approve' | 'deny') => {
    if (!run) return;
    try {
      const result = await apiPost<RunResult>(`runs/${run.run_id}/hitl/resume`, { decision });
      setRun(result);
      if (result.status === 'running' || result.status === 'paused') subscribe(run.run_id);
      else await finishRun(run.run_id, result.status);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'HITL 恢复失败');
    }
  };

  return (
    <div className="shrink-0 border-t border-line bg-ink-1">
      <div className="flex items-center gap-3 px-4 py-2">
        <button onClick={() => setOpen((v) => !v)} className="label-micro hover:text-fg-1">
          {open ? '▾' : '▸'} 试跑
        </button>
        <input
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && start()}
          placeholder="试跑输入（可选 prompt）"
          className="h-7 w-72 rounded border border-line-strong bg-ink-2 px-2 text-[12px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
        />
        <button
          onClick={start}
          disabled={!doc || phase === 'starting' || phase === 'streaming'}
          className="h-7 rounded border border-signal-dim bg-signal/15 px-3 text-[12px] text-signal hover:bg-signal/25 disabled:opacity-40"
        >
          {phase === 'starting' || phase === 'streaming' ? '运行中…' : '▶ 试跑当前图'}
        </button>
        {run && (
          <span className="flex items-center gap-2">
            <StatusBadge status={run.status} />
            <span className="font-mono text-[11px] text-muted">{run.run_id}</span>
          </span>
        )}
        {run?.status === 'paused' && (
          <span className="flex gap-1.5">
            <button onClick={() => hitlResume('approve')} className="rounded border border-ok/40 bg-ok/10 px-2.5 py-1 text-[11px] text-ok hover:bg-ok/20">
              批准继续
            </button>
            <button onClick={() => hitlResume('deny')} className="rounded border border-fail/40 bg-fail/10 px-2.5 py-1 text-[11px] text-fail hover:bg-fail/20">
              拒绝
            </button>
          </span>
        )}
        {error && <span className="font-mono text-[11px] text-fail" role="alert">{error}</span>}
      </div>
      {open && (
        <div className="max-h-56 overflow-y-auto border-t border-line px-4 py-2" aria-live="polite">
          {phase !== 'idle' && log.length === 0 && !run?.output && run?.structured_output == null && !steps && (
            <div className="font-mono text-[11px] text-muted">
              {phase === 'done' ? '运行已结束，未返回可展示的输出。' : '等待运行事件与输出…'}
            </div>
          )}
          {log.map((line, i) => (
            <div key={i} className="font-mono text-[11px] leading-relaxed text-fg-1">
              {line}
            </div>
          ))}
          {run?.output && (
            <pre className="mt-2 rounded border border-line bg-ink-2 p-2 font-mono text-[11px] text-fg-0">{run.output}</pre>
          )}
          {run?.structured_output != null && (
            <pre className="mt-2 rounded border border-line bg-ink-2 p-2 font-mono text-[11px] text-fg-0">
              {JSON.stringify(run.structured_output, null, 2)}
            </pre>
          )}
          {steps && steps.steps.length > 0 && (
            <div className="mt-2 space-y-1.5">
              {steps.steps.map((step) => (
                <div key={step.node_id} className="rounded border border-line bg-ink-2 p-2">
                  <div className="label-micro mb-1">{step.node_id}</div>
                  <pre className="overflow-x-auto font-mono text-[11px] text-fg-1">{JSON.stringify(step.output, null, 2)}</pre>
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function eventLine(record: EventRecord): string {
  const payload = (record.event.payload ?? {}) as Record<string, unknown>;
  const nodeID = (payload.node_id ?? payload.node ?? payload.step) as string | undefined;
  const label = record.event.display_label || record.event.type;
  return `${new Date(record.event.timestamp).toLocaleTimeString('zh-CN', { hour12: false })}  ${label}${nodeID ? `  ·  ${nodeID}` : ''}`;
}
