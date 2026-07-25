import { memo } from 'react';
import { Handle, Position, type NodeProps, type Node } from '@xyflow/react';
import type { GraphNode } from '../api/types';
import type { NodeRunState } from '../store/canvas';

export type WorkflowFlowNode = Node<{ gn: GraphNode; runState?: NodeRunState }, 'wf'>;

const kindLabels: Record<string, string> = {
  agent: 'AGENT',
  tool: 'TOOL',
  skill: 'SKILL',
  transform: 'XFORM',
  human_gate: 'GATE',
  parallel_group: 'PAR',
  loop: 'LOOP',
  subgraph: 'SUB',
};

function WorkflowNode({ data, selected }: NodeProps<WorkflowFlowNode>) {
  const { gn, runState } = data;
  const kindColor = `var(--color-kind-${gn.kind}, var(--color-kind-transform))`;
  const stateRing =
    runState === 'done'
      ? 'border-ok'
      : runState === 'failed'
        ? 'border-fail'
        : runState === 'running'
          ? 'border-live node-running'
          : selected
            ? 'border-signal'
            : 'border-line-strong hover:border-fg-2';

  return (
    <div className={`w-[180px] rounded-md border bg-ink-2 shadow-lg shadow-black/30 transition-colors ${stateRing}`}>
      <Handle type="target" position={Position.Left} className="!size-2" />
      <div className="flex items-center gap-2 border-b border-line px-2.5 py-1.5">
        <span
          className="rounded px-1 py-px font-mono text-[9px] font-semibold tracking-wider"
          style={{ color: kindColor, background: `color-mix(in srgb, ${kindColor} 14%, transparent)` }}
        >
          {kindLabels[gn.kind] ?? gn.kind.toUpperCase()}
        </span>
        {gn.interrupt && <span className="font-mono text-[9px] text-warn">⏸ HITL</span>}
      </div>
      <div className="px-2.5 py-2">
        <div className="truncate font-mono text-[12px] font-medium text-fg-0">{gn.id}</div>
        {gn.ref && <div className="mt-0.5 truncate text-[11px] text-muted">→ {gn.ref}</div>}
      </div>
      <Handle type="source" position={Position.Right} className="!size-2" />
    </div>
  );
}

export default memo(WorkflowNode);
