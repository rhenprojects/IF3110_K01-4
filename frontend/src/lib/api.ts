//[api] wrapper fetch: cookie, JSON, CSRF, error

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

//[csrf] baca token dari cookie, nama cookie/header samakan dengan backend
function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

const SAFE = new Set(["GET", "HEAD", "OPTIONS"]);

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!SAFE.has(method)) headers.set("X-CSRF-Token", csrfToken());

  const res = await fetch(`/api/v1${path}`, { ...init, method, headers, credentials: "same-origin" });

  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      msg = body.error ?? body.message ?? msg;
    } catch {
    }
    throw new ApiError(res.status, msg);
  }
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}
