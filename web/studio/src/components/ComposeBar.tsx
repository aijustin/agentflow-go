import { useState } from 'react';
import { apiPost } from '../api/client';
import type { ComposeGraphResult } from '../api/types';
import { useCanvasStore } from '../store/canvas';

type ComposeMode = 'catalog' | 'scenario';

export function ComposeBar() {
  const setDoc = useCanvasStore((s) => s.setDoc);
  const [prompt, setPrompt] = useState('');
  const [mode, setMode] = useState<ComposeMode>('catalog');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<{ kind: 'ok' | 'err'; text: string } | null>(null);

  const compose = async () => {
    if (!prompt.trim() || busy) return;
    setBusy(true);
    setMessage(null);
    try {
      const result = await apiPost<ComposeGraphResult>('studio/compose', {
        prompt: prompt.trim(),
        mode,
      });
      if (!result.valid) {
        setMessage({ kind: 'err', text: result.error || '构图失败' });
        return;
      }
      setDoc(result.graph, true, mode === 'scenario' ? result.scenario ?? null : null);
      const nodes = result.graph.workflow?.nodes.length ?? 0;
      setMessage({ kind: 'ok', text: `已生成 ${nodes} 个节点（未保存草稿）` });
    } catch (err) {
      setMessage({ kind: 'err', text: err instanceof Error ? err.message : '构图请求失败' });
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="border-b border-line bg-ink-1 px-4 py-3">
      <div className="flex items-center gap-3">
        <span className="label-micro shrink-0 text-signal">AI 构图</span>
        <input
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && compose()}
          placeholder="一句话描述要的流程，例如：先检索资料，然后两个 agent 并行评审，最后汇总"
          className="h-9 min-w-0 flex-1 rounded-md border border-line-strong bg-ink-2 px-3 text-[13px] text-fg-0 placeholder:text-muted focus:border-signal focus:outline-none"
        />
        <div className="flex shrink-0 overflow-hidden rounded-md border border-line-strong">
          {(['catalog', 'scenario'] as const).map((m) => (
            <button
              key={m}
              onClick={() => setMode(m)}
              title={m === 'catalog' ? '只编排已有零件' : '可新建 Agent/Skill'}
              className={`px-2.5 py-1.5 font-mono text-[11px] transition-colors ${
                mode === m ? 'bg-ink-4 text-signal' : 'bg-ink-2 text-muted hover:text-fg-1'
              }`}
            >
              {m}
            </button>
          ))}
        </div>
        <button
          onClick={compose}
          disabled={busy || !prompt.trim()}
          className="h-9 shrink-0 rounded-md bg-signal px-4 text-[13px] font-semibold text-ink-0 transition-colors hover:bg-signal-strong disabled:cursor-not-allowed disabled:bg-ink-4 disabled:text-muted"
        >
          {busy ? '生成中…' : '生成'}
        </button>
      </div>
      {message && (
        <p className={`mt-2 font-mono text-[11px] ${message.kind === 'ok' ? 'text-ok' : 'text-fail'}`}>
          {message.text}
        </p>
      )}
    </div>
  );
}
