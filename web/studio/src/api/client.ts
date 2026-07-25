// API client. The dashboard is served under a mount prefix (e.g.
// /observability/), and the SPA uses hash routing, so the API base resolves
// relative to the page path — the same trick the legacy UI used.

const apiBase = (() => {
  const path = window.location.pathname;
  const base = path.endsWith('/') ? path : `${path}/`;
  return `${base}api/`;
})();

export function apiURL(suffix: string): string {
  return `${apiBase}${suffix}`;
}

export class ApiRequestError extends Error {
  status: number;
  code?: string;

  constructor(status: number, message: string, code?: string) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

async function parseError(resp: Response): Promise<ApiRequestError> {
  let message = `${resp.status} ${resp.statusText}`;
  let code: string | undefined;
  try {
    const body = await resp.json();
    if (typeof body?.error === 'string') {
      message = body.error;
    } else if (body?.error?.message) {
      message = body.error.message;
      code = body.error.code;
    }
    code = code ?? body?.error_code;
  } catch {
    // keep the status-line message
  }
  return new ApiRequestError(resp.status, message, code);
}

export async function apiGet<T>(suffix: string): Promise<T> {
  const resp = await fetch(apiURL(suffix));
  if (!resp.ok) throw await parseError(resp);
  return (await resp.json()) as T;
}

export async function apiPost<T>(suffix: string, body: unknown): Promise<T> {
  const resp = await fetch(apiURL(suffix), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!resp.ok) throw await parseError(resp);
  return (await resp.json()) as T;
}
