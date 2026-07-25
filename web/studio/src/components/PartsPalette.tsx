import { useState } from 'react';
import { useParts } from '../api/hooks';
import { useCanvasStore } from '../store/canvas';
import type { StudioPart } from '../api/types';

const groups = [
  { key: 'agents', label: 'Agents', kind: 'agent' },
  { key: 'tools', label: 'Tools', kind: 'tool' },
  { key: 'skills', label: 'Skills', kind: 'skill' },
  { key: 'subgraphs', label: '子图', kind: 'subgraph' },
] as const;

export function PartsPalette() {
  const { data: parts, isLoading, error } = useParts();
  const mutate = useCanvasStore((s) => s.mutate);
  const subgraph = useCanvasStore((s) => s.subgraph);
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});

  const addNode = (kind: string, part: StudioPart) => {
    const id = `${part.name}`.replace(/[^a-zA-Z0-9_-]/g, '_');
    mutate((draft) => {
      const view = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
      if (!view) {
        if (subgraph) return;
        // Autonomous base: composing creates the main workflow.
        draft.workflow = { nodes: [], edges: [] };
        if (!draft.mode || draft.mode === 'autonomous') draft.mode = 'fixed_workflow';
      }
      const target = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
      if (!target) return;
      let unique = id;
      let n = 2;
      while (target.nodes.some((node) => node.id === unique)) unique = `${id}_${n++}`;
      target.nodes.push({ id: unique, kind, ref: part.name });
    });
  };

  return (
    <aside className="flex w-56 shrink-0 flex-col border-r border-line bg-ink-1">
      <div className="border-b border-line px-3 py-2.5">
        <span className="label-micro">零件箱</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {isLoading && <p className="p-2 text-[12px] text-muted">加载中…</p>}
        {error && <p className="p-2 text-[12px] text-fail">零件加载失败</p>}
        {groups.map((group) => {
          const items = (parts?.[group.key] ?? []) as StudioPart[];
          const isCollapsed = collapsed[group.key];
          return (
            <div key={group.key} className="mb-1">
              <button
                onClick={() => setCollapsed((c) => ({ ...c, [group.key]: !c[group.key] }))}
                className="flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-[11px] text-fg-1 hover:bg-ink-2"
              >
                <span className="font-mono text-[9px]">{isCollapsed ? '▸' : '▾'}</span>
                <span className="label-micro">{group.label}</span>
                <span className="ml-auto font-mono text-[10px] text-muted">{items.length}</span>
              </button>
              {!isCollapsed &&
                items.map((part) => (
                  <button
                    key={part.name}
                    onClick={() => addNode(group.kind, part)}
                    title={part.description || part.name}
                    className="group mb-0.5 flex w-full items-center gap-2 rounded border border-transparent px-2 py-1.5 text-left hover:border-line-strong hover:bg-ink-2"
                  >
                    <span
                      className="size-1.5 shrink-0 rounded-full"
                      style={{ background: `var(--color-kind-${group.kind})` }}
                    />
                    <span className="min-w-0">
                      <span className="block truncate font-mono text-[11.5px] text-fg-0">{part.name}</span>
                      {part.description && (
                        <span className="block truncate text-[10.5px] text-muted">{part.description}</span>
                      )}
                    </span>
                    <span className="ml-auto hidden font-mono text-[10px] text-signal group-hover:block">+</span>
                  </button>
                ))}
            </div>
          );
        })}
      </div>
      <div className="border-t border-line px-3 py-2">
        <button
          onClick={() => addNode('transform', { name: '' })}
          className="w-full rounded border border-line-strong px-2 py-1 text-[11px] text-fg-1 hover:bg-ink-2 hover:text-fg-0"
        >
          + transform 节点
        </button>
      </div>
    </aside>
  );
}
