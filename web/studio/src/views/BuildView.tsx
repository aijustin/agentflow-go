import { useEffect } from 'react';
import { useSearchParams } from 'react-router-dom';
import { useGraph, useRunSteps } from '../api/hooks';
import { useCanvasStore } from '../store/canvas';
import { ComposeBar } from '../components/ComposeBar';
import { PartsPalette } from '../components/PartsPalette';
import { GraphCanvas } from '../components/GraphCanvas';
import { NodeInspector } from '../components/NodeInspector';
import { TrialRunPanel } from '../components/TrialRunPanel';
import { StatusBadge } from '../components/StatusBadge';

export function BuildView() {
  const { data: graph, isLoading } = useGraph();
  const doc = useCanvasStore((s) => s.doc);
  const setDoc = useCanvasStore((s) => s.setDoc);
  const overlayRunID = useCanvasStore((s) => s.overlayRunID);
  const setOverlayRun = useCanvasStore((s) => s.setOverlayRun);
  const clearNodeRunState = useCanvasStore((s) => s.clearNodeRunState);
  const setNodeRunState = useCanvasStore((s) => s.setNodeRunState);
  const [params, setParams] = useSearchParams();

  // Initialize the draft from the live scenario graph once both exist.
  useEffect(() => {
    if (graph && !doc) setDoc(graph, false);
  }, [graph, doc, setDoc]);

  // The ?run=<id> search param drives the execution overlay on the canvas.
  const paramRun = params.get('run');
  useEffect(() => {
    setOverlayRun(paramRun);
  }, [paramRun, setOverlayRun]);

  const { data: overlaySteps } = useRunSteps(overlayRunID ?? undefined);

  useEffect(() => {
    if (!overlayRunID || !overlaySteps) return;
    clearNodeRunState();
    for (const step of overlaySteps.steps) {
      setNodeRunState(step.node_id, 'done');
    }
    if (overlaySteps.current_node_id) {
      setNodeRunState(
        overlaySteps.current_node_id,
        overlaySteps.status === 'failed' ? 'failed' : 'running',
      );
    }
  }, [overlayRunID, overlaySteps, clearNodeRunState, setNodeRunState]);

  if (isLoading) {
    return <div className="flex h-full items-center justify-center text-[13px] text-muted">加载场景中…</div>;
  }

  return (
    <div className="flex h-full flex-col">
      <ComposeBar />
      {overlayRunID && (
        <div className="flex items-center gap-3 border-b border-live/30 bg-live/5 px-4 py-1.5">
          <span className="size-1.5 rounded-full bg-live" />
          <span className="font-mono text-[11px] text-fg-1">
            正在叠加显示 run <span className="text-live">{overlayRunID}</span> 的执行状态
          </span>
          {overlaySteps && <StatusBadge status={overlaySteps.status} />}
          <button
            onClick={() => setParams({})}
            className="ml-auto rounded border border-line-strong px-2 py-0.5 font-mono text-[10.5px] text-fg-1 hover:bg-ink-2"
          >
            退出叠加
          </button>
        </div>
      )}
      <div className="flex min-h-0 flex-1">
        <PartsPalette />
        <div className="relative min-w-0 flex-1">
          <GraphCanvas />
        </div>
        <NodeInspector />
      </div>
      <TrialRunPanel />
    </div>
  );
}
