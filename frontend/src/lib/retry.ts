import { ApiError } from "./api";

//[retry] error 4xx tidak diulang (401/403/404/409 bukan error sementara); jaringan putus atau 5xx diulang sekali
export function shouldRetry(count: number, e: unknown): boolean {
  if (count >= 1) return false;
  return !(e instanceof ApiError) || e.status === 0 || e.status >= 500;
}
