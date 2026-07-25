interface QueryErrorProps {
  error: unknown;
  label?: string;
}

export function QueryError({ error, label = '加载失败' }: QueryErrorProps) {
  const detail = error instanceof Error ? error.message : label;
  return (
    <div role="alert" className="m-4 rounded border border-fail/40 bg-fail/10 p-3 text-[12px] text-fail">
      <p>{label}</p>
      {detail !== label && <p className="mt-1 font-mono text-[11px]">{detail}</p>}
    </div>
  );
}
