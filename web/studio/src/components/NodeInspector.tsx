import { useEffect, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { apiPost } from '../api/client';
import type { CodegenResult, GraphNode, ImportStudioResult, SaveStudioResult, ValidateStudioResult } from '../api/types';
import { currentView, useCanvasStore } from '../store/canvas';
import { prettyJSON } from '../lib/format';

export function NodeInspector() {
  const doc = useCanvasStore((s) => s.doc);
  const subgraph = useCanvasStore((s) => s.subgraph);
  const selectedID = useCanvasStore((s) => s.selectedNodeID);
  const mutate = useCanvasStore((s) => s.mutate);

  const view = currentView({ doc, subgraph });
  const node = view?.nodes.find((n) => n.id === selectedID);

  return (
    <aside className="flex w-72 shrink-0 flex-col border-l border-line bg-ink-1">
      <div className="border-b border-line px-3 py-2.5">
        <span className="label-micro">{node ? '节点' : '场景'}</span>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {node ? <NodeEditor key={node.id} node={node} subgraph={subgraph} mutate={mutate} /> : <ScenarioActions />}
      </div>
    </aside>
  );
}

function NodeEditor({
  node,
  subgraph,
  mutate,
}: {
  node: GraphNode;
  subgraph: string | null;
  mutate: (fn: (doc: import('../api/types').ScenarioGraph) => void) => void;
}) {
  const [inputText, setInputText] = useState(prettyJSON(node.input));
  const [inputError, setInputError] = useState('');

  useEffect(() => {
    setInputText(prettyJSON(node.input));
    setInputError('');
  }, [node.id]);

  const patch = (fn: (n: GraphNode) => void) =>
    mutate((draft) => {
      const view = subgraph ? draft.workflows?.[subgraph] : draft.workflow;
      const target = view?.nodes.find((n) => n.id === node.id);
      if (target) fn(target);
    });

  const commitInput = () => {
    if (!inputText.trim()) {
      patch((n) => delete n.input);
      setInputError('');
      return;
    }
    try {
      const parsed = JSON.parse(inputText);
      patch((n) => {
        n.input = parsed;
      });
      setInputError('');
    } catch {
      setInputError('不是合法 JSON');
    }
  };

  return (
    <div className="flex flex-col gap-3 rise-in">
      <Field label="ID">
        <div className="font-mono text-[12.5px] text-fg-0">{node.id}</div>
      </Field>
      <Field label="Kind / Ref">
        <div className="font-mono text-[12px] text-fg-1">
          {node.kind}
          {node.ref ? ` → ${node.ref}` : ''}
        </div>
      </Field>
      <Field label="Input（JSON）">
        <textarea
          value={inputText}
          onChange={(e) => setInputText(e.target.value)}
          onBlur={commitInput}
          rows={6}
          spellCheck={false}
          placeholder='{"set":{"key":"value"}}'
          className="w-full rounded border border-line-strong bg-ink-2 p-2 font-mono text-[11.5px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
        />
        {inputError && <p className="mt-1 font-mono text-[10.5px] text-fail">{inputError}</p>}
      </Field>
      <Field label="Condition">
        <input
          defaultValue={node.condition ?? ''}
          onBlur={(e) => patch((n) => (n.condition = e.target.value.trim() || undefined))}
          placeholder="eq(steps.a.x, 1)"
          className="w-full rounded border border-line-strong bg-ink-2 px-2 py-1.5 font-mono text-[11.5px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
        />
      </Field>
      <Field label="Depends on（逗号分隔）">
        <input
          defaultValue={(node.depends_on ?? []).join(', ')}
          onBlur={(e) =>
            patch((n) => {
              const deps = e.target.value.split(',').map((s) => s.trim()).filter(Boolean);
              n.depends_on = deps.length > 0 ? deps : undefined;
            })
          }
          placeholder="node_a, node_b"
          className="w-full rounded border border-line-strong bg-ink-2 px-2 py-1.5 font-mono text-[11.5px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
        />
      </Field>
      <label className="flex items-center gap-2 text-[12px] text-fg-1">
        <input
          type="checkbox"
          defaultChecked={!!node.interrupt}
          onChange={(e) => patch((n) => (n.interrupt = e.target.checked || undefined))}
          className="accent-[#ffb224]"
        />
        执行后暂停（HITL interrupt）
      </label>
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <div className="label-micro mb-1">{label}</div>
      {children}
    </div>
  );
}

function ScenarioActions() {
  const doc = useCanvasStore((s) => s.doc);
  const scenarioDraft = useCanvasStore((s) => s.scenarioDraft);
  const dirty = useCanvasStore((s) => s.dirty);
  const undo = useCanvasStore((s) => s.undo);
  const redo = useCanvasStore((s) => s.redo);
  const canUndo = useCanvasStore((s) => s.past.length > 0);
  const canRedo = useCanvasStore((s) => s.future.length > 0);
  const setDoc = useCanvasStore((s) => s.setDoc);
  const queryClient = useQueryClient();
  const [message, setMessage] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);
  const [export_, setExport] = useState<{ language: string; code: string } | null>(null);

  if (!doc) return null;

  const run = async (action: () => Promise<{ kind: 'ok' | 'err'; text: string }>) => {
    setMessage(null);
    setMessage(await action());
  };

  const save = () =>
    run(async () => {
      try {
        const payload = scenarioDraft ? { graph: doc, scenario: scenarioDraft } : doc;
        const result = await apiPost<SaveStudioResult>('studio/save', payload);
        setDoc(result.graph ?? doc, false);
        void queryClient.invalidateQueries({ queryKey: ['graph'] });
        void queryClient.invalidateQueries({ queryKey: ['parts'] });
        return { kind: 'ok', text: '已保存（live 场景已更新）' };
      } catch (err) {
        return { kind: 'err', text: err instanceof Error ? err.message : '保存失败' };
      }
    });

  const validate = () =>
    run(async () => {
      const payload = scenarioDraft ? { graph: doc, scenario: scenarioDraft } : doc;
      const result = await apiPost<ValidateStudioResult>('studio/validate', payload);
      return result.valid
        ? { kind: 'ok' as const, text: '校验通过' }
        : { kind: 'err' as const, text: result.error || '校验失败' };
    });

  const codegen = (language: 'go' | 'yaml') => async () => {
    try {
      const payload = scenarioDraft ? { graph: doc, scenario: scenarioDraft } : doc;
      const result = await apiPost<CodegenResult>(language === 'go' ? 'studio/codegen' : 'studio/yaml', payload);
      setExport(result);
    } catch (err) {
      setMessage({ kind: 'err', text: err instanceof Error ? err.message : '导出失败' });
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center gap-2">
        <span className={`size-1.5 rounded-full ${dirty ? 'bg-signal' : 'bg-ok'}`} />
        <span className="text-[12px] text-fg-1">{dirty ? '未保存草稿' : '与 live 场景一致'}</span>
        <div className="ml-auto flex gap-1">
          <button disabled={!canUndo} onClick={undo} className="rounded border border-line-strong px-2 py-1 font-mono text-[11px] text-fg-1 hover:bg-ink-2 disabled:opacity-40">
            ↩ 撤销
          </button>
          <button disabled={!canRedo} onClick={redo} className="rounded border border-line-strong px-2 py-1 font-mono text-[11px] text-fg-1 hover:bg-ink-2 disabled:opacity-40">
            ↪ 重做
          </button>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <ActionButton onClick={validate}>校验</ActionButton>
        <ActionButton onClick={save} primary>
          保存到 live
        </ActionButton>
        <ActionButton onClick={codegen('yaml')}>导出 YAML</ActionButton>
        <ActionButton onClick={codegen('go')}>导出 Go</ActionButton>
      </div>

      <ImportYAML />

      {message && (
        <p className={`font-mono text-[11px] ${message.kind === 'ok' ? 'text-ok' : 'text-fail'}`}>{message.text}</p>
      )}

      {export_ && (
        <div className="rise-in">
          <div className="label-micro mb-1">{export_.language} 导出</div>
          <pre className="max-h-72 overflow-auto rounded border border-line bg-ink-2 p-2 font-mono text-[10.5px] leading-relaxed text-fg-1">
            {export_.code}
          </pre>
        </div>
      )}
    </div>
  );
}

function ActionButton({
  children,
  onClick,
  primary,
}: {
  children: React.ReactNode;
  onClick: () => void;
  primary?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      className={`rounded-md border px-2 py-1.5 text-[12px] transition-colors ${
        primary
          ? 'border-signal-dim bg-signal/15 text-signal hover:bg-signal/25'
          : 'border-line-strong text-fg-1 hover:bg-ink-2 hover:text-fg-0'
      }`}
    >
      {children}
    </button>
  );
}

function ImportYAML() {
  const doc = useCanvasStore((s) => s.doc);
  const setDoc = useCanvasStore((s) => s.setDoc);
  const [open, setOpen] = useState(false);
  const [yaml, setYaml] = useState('');
  const [message, setMessage] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const importYAML = async () => {
    if (!yaml.trim()) return;
    setMessage(null);
    try {
      const result = await apiPost<ImportStudioResult>('studio/import-yaml', {
        yaml,
        layout_graph: doc ?? undefined,
      });
      setDoc(result.graph, true);
      setMessage({ kind: 'ok', text: `已导入 ${result.scenario_name}（未保存草稿）` });
      setOpen(false);
      setYaml('');
    } catch (err) {
      setMessage({ kind: 'err', text: err instanceof Error ? err.message : '导入失败' });
    }
  };

  return (
    <div className="rounded border border-line">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-[12px] text-fg-1 hover:text-fg-0"
      >
        <span className="font-mono text-[9px]">{open ? '▾' : '▸'}</span>
        导入 YAML
      </button>
      {open && (
        <div className="border-t border-line p-2.5 rise-in">
          <textarea
            value={yaml}
            onChange={(e) => setYaml(e.target.value)}
            rows={8}
            spellCheck={false}
            placeholder="粘贴 scenario YAML…"
            className="w-full rounded border border-line-strong bg-ink-2 p-2 font-mono text-[11px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
          />
          <button
            onClick={importYAML}
            disabled={!yaml.trim()}
            className="mt-2 w-full rounded-md border border-signal-dim bg-signal/15 px-2 py-1.5 text-[12px] text-signal hover:bg-signal/25 disabled:opacity-40"
          >
            导入到画布
          </button>
        </div>
      )}
      {message && (
        <p className={`px-2.5 pb-2 font-mono text-[10.5px] ${message.kind === 'ok' ? 'text-ok' : 'text-fail'}`}>
          {message.text}
        </p>
      )}
    </div>
  );
}
