interface QueryErrorProps {
  error: unknown;
  onRetry: () => void;
  label?: string;
}

export function QueryError({ error, onRetry, label = '加载失败' }: QueryErrorProps) {
  const detail = error instanceof Error ? error.message : label;
  return (
    <div role="alert" className="m-4 rounded border border-fail/40 bg-fail/10 p-3 text-[12px] text-fail">
      <p>{label}</p>
      {detail !== label && <p className="mt-1 font-mono text-[11px]">{detail}</p>}
      <button
        type="button"
        onClick={onRetry}
        className="mt-2 rounded border border-fail/40 px-2.5 py-1 text-[11.5px] hover:bg-fail/10"
      >
        重试
      </button>
    </div>
  );
}
