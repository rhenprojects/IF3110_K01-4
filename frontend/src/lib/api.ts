export class ApiError extends Error {
  status: number; //[error] 0 = request tidak sampai ke server
  field?: string; //[error] nama field yang gagal validasi (400)
  constructor(status: number, message: string, field?: string) {
    super(message);
    this.status = status;
    this.field = field;
  }
}

//[csrf] token dibaca dari cookie csrf_token dan dikirim lewat header X-CSRF-Token (lihat docs/api-contract.md)
function csrfToken(): string {
  const m = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]+)/);
  return m ? decodeURIComponent(m[1]) : "";
}

const SAFE = new Set(["GET", "HEAD", "OPTIONS"]);

//[401] endpoint auth boleh menjawab 401 tanpa berarti sesi habis (login salah, belum login)
const AUTH_PATHS = new Set(["/users/login", "/users/profile", "/users/logout"]);

let onUnauthorized: (() => void) | null = null;
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn;
}

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!SAFE.has(method)) headers.set("X-CSRF-Token", csrfToken());

  let res: Response;
  try {
    res = await fetch(`/api/v1${path}`, { ...init, method, headers, credentials: "same-origin" });
  } catch {
    throw new ApiError(0, "Tidak bisa menghubungi server");
  }

  if (!res.ok) {
    let msg = res.statusText;
    let field: string | undefined;
    try {
      const body = await res.json();
      msg = body.error ?? body.message ?? msg;
      field = body.field;
    } catch {
      //[error] body bukan JSON, pakai statusText
    }
    if (res.status === 401 && !AUTH_PATHS.has(path)) onUnauthorized?.();
    throw new ApiError(res.status, msg, field);
  }
  return res.status === 204 ? (undefined as T) : ((await res.json()) as T);
}
