import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useBatch, useBatchItems, useCancelBatch, useRetryBatch } from "../../features/databases/hooks";
import { Button, ConfirmDialog, ErrorBox, Loading, Table, generalError, td } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration } from "../../lib/format";
import { StateError, BatchStatusBadge, canEdit, useDb } from "./shared";
import type { BatchItemStatus } from "../../lib/types";

const ITEM: Record<BatchItemStatus, string> = {
  pending: "Menunggu",
  running: "Berjalan",
  success: "Berhasil",
  failed: "Gagal",
  skipped: "Dilewati",
};

export default function BatchDetail() {
  const db = useDb();
  const { bid = "" } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const qc = useQueryClient();
  const q = useBatch(db.id, bid);
  const items = useBatchItems(db.id, bid);
  const cancel = useCancelBatch(db.id);
  const retry = useRetryBatch(db.id);
  const [confirm, setConfirm] = useState(false);

  const status = q.data?.status;
  const terminal = status === "success" || status === "partial" || status === "failed" || status === "cancelled";
  //[batch] saat batch selesai, riwayat migration ikut berubah status/versi
  useEffect(() => {
    if (terminal) qc.invalidateQueries({ queryKey: ["db", db.id, "migrations"] });
  }, [terminal, db.id, qc]);

  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  const b = q.data;
  const editable = canEdit(db.level);
  const pct = b.total > 0 ? Math.round(((b.processed + b.failed) / b.total) * 100) : 0;

  async function doCancel() {
    try {
      await cancel.mutateAsync(b.id);
      toast.success("Permintaan batal dikirim. Migration yang sedang berjalan akan diselesaikan dulu.");
    } catch {
      //[error] ditampilkan lewat cancel.error
    } finally {
      setConfirm(false);
    }
  }

  async function doRetry() {
    try {
      const next = await retry.mutateAsync(b.id);
      toast.success("Batch baru dibuat untuk item yang belum dijalankan.");
      nav(`/databases/${db.id}/migrations/batches/${next.id}`);
    } catch {
      //[error] ditampilkan lewat retry.error
    }
  }

  return (
    <div className="space-y-5">
      <Link to={`/databases/${db.id}/migrations/batches`} className="text-sm text-slate-500 hover:underline">← Riwayat batch</Link>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-3 text-lg font-semibold">
          <span className="font-mono text-slate-400">{b.id.slice(0, 8)}</span> Batch <BatchStatusBadge status={b.status} />
        </h2>
        {editable && !terminal && (
          <Button variant="danger" onClick={() => setConfirm(true)} disabled={b.cancel_requested}>
            {b.cancel_requested ? "Pembatalan diminta…" : "Batalkan"}
          </Button>
        )}
        {editable && terminal && (b.failed > 0 || b.processed < b.total) && (
          <Button onClick={doRetry} disabled={retry.isPending}>Ulangi item tersisa</Button>
        )}
      </div>

      <ErrorBox message={generalError(cancel.error, []) || generalError(retry.error, [])} />

      <dl className="grid gap-3 text-sm sm:grid-cols-4">
        {([
          ["Dibuat oleh", `${b.created_by_name ?? b.created_by} · ${fmtDate(b.created_at)}`],
          ["Mulai", b.started_at ? fmtDate(b.started_at) : "—"],
          ["Selesai", b.finished_at ? fmtDate(b.finished_at) : "—"],
          ["Dibatalkan oleh", b.cancelled_by_name ?? b.cancelled_by ?? "—"],
        ] as [string, string][]).map(([k, v]) => (
          <div key={k}><dt className="text-xs uppercase tracking-wide text-slate-500">{k}</dt><dd>{v}</dd></div>
        ))}
      </dl>

      <div>
        <div className="mb-1 flex justify-between text-sm">
          <span>{b.processed} dari {b.total} selesai{b.failed > 0 ? `, ${b.failed} gagal` : ""}</span>
          <span className="text-slate-500">{pct}%</span>
        </div>
        <div className="h-2 overflow-hidden rounded bg-slate-200">
          <div className="h-full bg-slate-900 transition-all" style={{ width: `${pct}%` }} />
        </div>
      </div>

      {b.status === "failed" && b.error && (
        <div role="alert" className="rounded border border-red-200 bg-red-50 p-4 text-sm text-red-800">
          <p className="font-medium">Batch berhenti karena error.</p>
          <pre className="mt-2 whitespace-pre-wrap font-mono text-xs">{b.error}</pre>
        </div>
      )}
      {(b.status === "partial" || b.status === "cancelled") && (
        <p className="rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
          {b.status === "cancelled" ? "Batch dibatalkan di batas item." : "Sebagian migration gagal."} Perubahan yang sudah di-commit tetap tersimpan; tidak ada rollback otomatis.
        </p>
      )}

      <section>
        <h3 className="mb-2 text-sm font-medium">Item</h3>
        {items.isLoading && <Loading />}
        {items.error && <StateError error={items.error} onRetry={() => items.refetch()} />}
        {items.data && items.data.length > 0 && (
          <Table columns={["#", "Migration", "Percobaan", "Status", "Eksekusi", "Durasi", "Error"]}>
            {items.data.map((it) => (
              <tr key={it.id}>
                <td className={`${td} font-mono text-xs`}>{it.id}</td>
                <td className={td}>
                  <Link to={`/databases/${db.id}/migrations/${it.migration_id}`} className="font-medium underline-offset-2 hover:underline">
                    {it.migration_name ?? it.migration_id.slice(0, 8)}
                  </Link>
                </td>
                <td className={td}>{it.attempt}</td>
                <td className={td}>{ITEM[it.status]}{it.compensation_status === "retained" && <span className="block text-xs text-slate-400">perubahan dipertahankan</span>}</td>
                <td className={`${td} font-mono text-xs`}>{it.execution_id.slice(0, 8)}</td>
                <td className={td}>{fmtDuration(it.duration_ms)}</td>
                <td className={`${td} max-w-xs`}><span className="text-xs text-red-700">{it.error}</span></td>
              </tr>
            ))}
          </Table>
        )}
      </section>

      {confirm && (
        <ConfirmDialog title="Batalkan batch" confirmLabel="Batalkan" busy={cancel.isPending}
          message="Batch berhenti setelah item yang sedang berjalan selesai. Migration yang sudah di-commit tidak di-rollback."
          onConfirm={doCancel} onClose={() => setConfirm(false)} />
      )}
    </div>
  );
}
