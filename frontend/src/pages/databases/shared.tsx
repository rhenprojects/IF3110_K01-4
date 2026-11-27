import { Link, useOutletContext } from "react-router-dom";
import { Badge, Button } from "../../components/ui";
import { ApiError } from "../../lib/api";
import { errorMessage, isConnectionError } from "../../lib/errors";
import type { AccessLevel, BatchItemStatus, BatchStatus, DatabaseItem, JobKind, JobStatus, MigrationStatus } from "../../lib/types";

//[context] database yang sedang dibuka, disediakan DatabaseLayout lewat <Outlet context>
export const useDb = () => useOutletContext<DatabaseItem>();

export const canEdit = (level: AccessLevel) => level === "edit";

//[nav] tautan kembali di halaman detail
export function BackLink({ to, children }: { to: string; children: string }) {
  return <Link to={to} className="inline-flex text-sm font-medium text-primary hover:underline">← {children}</Link>;
}

export function LevelBadge({ level }: { level: AccessLevel }) {
  return <Badge tone={level === "edit" ? "dark" : "neutral"}>{level === "edit" ? "View + Edit" : "View Only"}</Badge>;
}

const STATUS: Record<MigrationStatus, { tone: "neutral" | "amber" | "green" | "red"; label: string }> = {
  draft: { tone: "neutral", label: "Draft" },
  pending_review: { tone: "amber", label: "Menunggu review" },
  approved: { tone: "green", label: "Disetujui" },
  success: { tone: "green", label: "Berhasil" },
  failed: { tone: "red", label: "Gagal (rollback)" },
  partial: { tone: "amber", label: "Sebagian / checkpoint" },
};
export function StatusBadge({ status }: { status: MigrationStatus }) {
  return <Badge tone={STATUS[status].tone}>{STATUS[status].label}</Badge>;
}

const BATCH: Record<BatchStatus, { tone: "neutral" | "dark" | "green" | "red" | "amber"; label: string }> = {
  queued: { tone: "neutral", label: "Menunggu" },
  running: { tone: "dark", label: "Berjalan" },
  success: { tone: "green", label: "Berhasil" },
  partial: { tone: "amber", label: "Sebagian gagal" },
  failed: { tone: "red", label: "Gagal" },
  cancelled: { tone: "amber", label: "Dibatalkan" },
};
export function BatchStatusBadge({ status }: { status: BatchStatus }) {
  return <Badge tone={BATCH[status].tone}>{BATCH[status].label}</Badge>;
}

const JOB: Record<JobStatus, { tone: "neutral" | "dark" | "green" | "red" | "amber"; label: string }> = {
  queued: { tone: "neutral", label: "Di queue" },
  running: { tone: "dark", label: "Berjalan" },
  success: { tone: "green", label: "Berhasil" },
  failed: { tone: "red", label: "Gagal" },
  uncertain: { tone: "amber", label: "Belum pasti" },
};
export function JobStatusBadge({ status }: { status: JobStatus }) {
  return <Badge tone={JOB[status].tone}>{JOB[status].label}</Badge>;
}

export const KIND_LABEL: Record<JobKind, string> = { up: "Up", rollback: "Rollback", forward: "Forward", revert: "Revert batch" };

//[badge] jenis eksekusi: rollback diberi warna peringatan agar mudah dibedakan di daftar
const KIND_TONE: Record<JobKind, "neutral" | "amber" | "dark"> = { up: "neutral", rollback: "amber", forward: "dark", revert: "amber" };
export function KindBadge({ kind }: { kind: JobKind }) {
  return <Badge tone={KIND_TONE[kind]}>{KIND_LABEL[kind]}</Badge>;
}

const BATCH_ITEM: Record<BatchItemStatus, { tone: "neutral" | "dark" | "green" | "red" | "amber"; label: string }> = {
  pending: { tone: "neutral", label: "Menunggu" },
  running: { tone: "dark", label: "Berjalan" },
  success: { tone: "green", label: "Berhasil" },
  failed: { tone: "red", label: "Gagal" },
  skipped: { tone: "amber", label: "Dilewati" },
};
export function BatchItemBadge({ status }: { status: BatchItemStatus }) {
  return <Badge tone={BATCH_ITEM[status].tone}>{BATCH_ITEM[status].label}</Badge>;
}

//[error] satu tampilan untuk 403 / 404 / 502 / 504 agar semua halaman database konsisten
export function StateError({ error, onRetry, backLink }: { error: unknown; onRetry?: () => void; backLink?: boolean }) {
  const status = error instanceof ApiError ? error.status : 0;
  let msg = errorMessage(error);
  if (status === 403) msg = "Kamu tidak punya akses ke database ini.";
  if (status === 404) msg = "Data yang diminta tidak ditemukan.";
  return (
    <div role="alert" className="space-y-3 rounded-2xl bg-danger-soft p-4 text-sm text-danger">
      <p>{msg}</p>
      {onRetry && (isConnectionError(error) || status === 0) && <Button onClick={onRetry}>Coba lagi</Button>}
      {backLink && <p><Link to="/databases" className="font-medium underline">← Kembali ke daftar database</Link></p>}
    </div>
  );
}
