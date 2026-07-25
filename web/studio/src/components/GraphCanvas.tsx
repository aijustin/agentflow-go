import { useCallback, useMemo } from 'react';
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  type Connection,
  type Edge,
  type EdgeChange,
  type NodeChange,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { currentView, useCanvasStore } from '../store/canvas';
import { layeredLayout } from '../lib/layout';
import WorkflowNode, { type WorkflowFlowNode } from './WorkflowNode';

const nodeTypes = { wf: WorkflowNode };

export function GraphCanvas() {
  const doc = useCanvasStore((s) => s.doc);
  const subgraph = useCanvasStore((s) => s.subgraph);
  const nodeRunState = useCanvasStore((s) => s.nodeRunState);
  const mutate = useCanvasStore((s) => s.mutate);
  const select = useCanvasStore((s) => s.select);
  const drill = useCanvasStore((s) => s.drill);

  const view = currentView({ doc, subgraph });

  const positions = useMemo(
    () => layeredLayout(view?.nodes ?? [], view?.edges ?? [], view?.layout),
    [view],
  );

  const nodes = useMemo<WorkflowFlowNode[]>(
    () =>
      (view?.nodes ?? []).map((gn) => ({
        id: gn.id,
        type: 'wf' as const,
        position: positions[gn.id] ?? { x: 0, y: 0 },
        data: { gn, runState: nodeRunState[gn.id] },
      })),
    [view, positions, nodeRunState],
  );

  const edges = useMemo<Edge[]>(
    () =>
      (view?.edges ?? []).map((ge) => ({
        id: `${ge.from}->${ge.to}${ge.condition ? `:${ge.condition}` : ''}`,
        source: ge.from,
        target: ge.to,
        label: ge.condition,
        labelStyle: { fill: 'var(--color-fg-1)', fontSize: 10, fontFamily: 'var(--font-mono)' },
        labelBgStyle: { fill: 'var(--color-ink-2)', fillOpacity: 0.9 },
      })),
    [view],
  );

  const onNodesChange = useCallback(
    (changes: NodeChange<WorkflowFlowNode>[]) => {
      for (const change of changes) {
        if (change.type === 'position' && change.position && !change.dragging) {
          const { id, position } = change;
          mutate((draft) => {
            const target = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
            if (!target) return;
            target.layout = { ...target.layout, [id]: { x: position.x, y: position.y } };
          });
        }
        if (change.type === 'remove') {
          const id = change.id;
          mutate((draft) => {
            const target = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
            if (!target) return;
            target.nodes = target.nodes.filter((n) => n.id !== id);
            target.edges = target.edges.filter((e) => e.from !== id && e.to !== id);
            for (const n of target.nodes) {
              n.depends_on = n.depends_on?.filter((dep) => dep !== id);
            }
            if (target.layout) delete target.layout[id];
          });
        }
        if (change.type === 'select') {
          select(change.selected ? change.id : null);
        }
      }
    },
    [mutate, select, subgraph],
  );

  const onEdgesChange = useCallback(
    (changes: EdgeChange[]) => {
      for (const change of changes) {
        if (change.type === 'remove') {
          const [from, rest] = change.id.split('->');
          const to = rest?.split(':')[0];
          mutate((draft) => {
            const target = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
            if (!target) return;
            target.edges = target.edges.filter((e) => !(e.from === from && e.to === to));
          });
        }
      }
    },
    [mutate, subgraph],
  );

  const onConnect = useCallback(
    (connection: Connection) => {
      if (!connection.source || !connection.target) return;
      const { source, target } = connection;
      mutate((draft) => {
        const view = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
        if (!view) return;
        if (view.edges.some((e) => e.from === source && e.to === target)) return;
        view.edges.push({ from: source, to: target });
      });
    },
    [mutate, subgraph],
  );

  if (!doc) {
    return (
      <div className="flex h-full items-center justify-center text-[13px] text-muted">加载场景中…</div>
    );
  }
  if (!view) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-[13px] text-muted">
        <p>当前场景没有主 workflow（autonomous 场景）。</p>
        <p className="font-mono text-[11px]">用上方 AI 构图生成一张图，或从零件箱拖入节点。</p>
      </div>
    );
  }

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={nodeTypes}
      onNodesChange={onNodesChange}
      onEdgesChange={onEdgesChange}
      onConnect={onConnect}
      onNodeDoubleClick={(_, node) => {
        if (node.data.gn.kind === 'subgraph' && node.data.gn.ref) drill(node.data.gn.ref);
      }}
      deleteKeyCode={['Backspace', 'Delete']}
      fitView
      proOptions={{ hideAttribution: false }}
      className="dot-grid"
    >
      <Background variant={BackgroundVariant.Dots} gap={22} size={1} color="#232c3a" />
      <Controls position="bottom-left" />
      <MiniMap pannable zoomable position="bottom-right" />
      {subgraph && (
        <button
          onClick={() => drill(null)}
          className="absolute left-3 top-3 z-10 rounded border border-line-strong bg-ink-2 px-2.5 py-1 font-mono text-[11px] text-fg-1 hover:text-fg-0"
        >
          ← 返回主图（{subgraph}）
        </button>
      )}
    </ReactFlow>
  );
}
