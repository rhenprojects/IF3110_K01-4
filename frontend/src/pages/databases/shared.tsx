import { Link, useOutletContext } from "react-router-dom";
import { Badge, Button } from "../../components/ui";
import { ApiError } from "../../lib/api";
import { errorMessage, isConnectionError } from "../../lib/errors";
import type { AccessLevel, DatabaseItem, MigrationStatus } from "../../lib/types";

//[context] database yang sedang dibuka, disediakan DatabaseLayout lewat <Outlet context>
export const useDb = () => useOutletContext<DatabaseItem>();

export const canEdit = (level: AccessLevel) => level === "edit";

export function LevelBadge({ level }: { level: AccessLevel }) {
  return <Badge tone={level === "edit" ? "dark" : "neutral"}>{level === "edit" ? "View + Edit" : "View Only"}</Badge>;
}

const STATUS: Record<MigrationStatus, { tone: "amber" | "green" | "red"; label: string }> = {
  draft: { tone: "amber", label: "Draft" },
  success: { tone: "green", label: "Berhasil" },
  failed: { tone: "red", label: "Gagal (rollback)" },
};
export function StatusBadge({ status }: { status: MigrationStatus }) {
  return <Badge tone={STATUS[status].tone}>{STATUS[status].label}</Badge>;
}

//[error] satu tampilan untuk 403 / 404 / 502 / 504 agar semua halaman database konsisten
export function StateError({ error, onRetry, backLink }: { error: unknown; onRetry?: () => void; backLink?: boolean }) {
  const status = error instanceof ApiError ? error.status : 0;
  let msg = errorMessage(error);
  if (status === 403) msg = "Kamu tidak punya akses ke database ini.";
  if (status === 404) msg = "Data yang diminta tidak ditemukan.";
  return (
    <div role="alert" className="space-y-3 rounded border border-red-200 bg-red-50 p-4 text-sm text-red-700">
      <p>{msg}</p>
      {onRetry && (isConnectionError(error) || status === 0) && <Button onClick={onRetry}>Coba lagi</Button>}
      {backLink && <p><Link to="/databases" className="underline">← Kembali ke daftar database</Link></p>}
    </div>
  );
}
