import { useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useBatch, useBatchItems, useCancelBatch, useRetryBatch } from "../../features/databases/hooks";
import { DataList, type Column } from "../../components/DataList";
import { Button, ConfirmDialog, ErrorBox, Loading, generalError } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration } from "../../lib/format";
import type { BatchItem } from "../../lib/types";
import { BackLink, BatchItemBadge, BatchStatusBadge, StateError, canEdit, useDb } from "./shared";

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
  const share = (n: number) => (b.total > 0 ? (n / b.total) * 100 : 0);
  const pct = Math.round(share(b.processed + b.failed));

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

  const columns: Column<BatchItem>[] = [
    { header: "#", cell: (it) => <span className="font-mono text-xs">{it.id}</span> },
    {
      header: "Migration", role: "title",
      cell: (it) => (
        <>
          <Link to={`/databases/${db.id}/migrations/${it.migration_id}`} className="text-primary underline-offset-2 hover:underline">
            {it.migration_name ?? it.migration_id.slice(0, 8)}
          </Link>
          {it.compensation_status === "retained" && <span className="block text-xs font-normal text-slate-500">Perubahan dipertahankan</span>}
        </>
      ),
    },
    { header: "Percobaan", cell: (it) => it.attempt },
    { header: "Status", role: "status", cell: (it) => <BatchItemBadge status={it.status} /> },
    { header: "Eksekusi", cell: (it) => <span className="font-mono text-xs">{it.execution_id.slice(0, 8)}</span> },
    { header: "Durasi", cell: (it) => fmtDuration(it.duration_ms) },
    { header: "Error", wide: true, cell: (it) => it.error ? <span className="break-words text-xs text-danger">{it.error}</span> : null },
  ];

  return (
    <div className="space-y-5">
      <BackLink to={`/databases/${db.id}/migrations/batches`}>Riwayat batch</BackLink>

      <section className={`space-y-5 rounded-2xl border border-t-4 bg-white p-6 shadow-sm ${
        b.status === "failed" ? "border-t-danger" : b.status === "success" ? "border-t-ok" : b.status === "partial" || b.status === "cancelled" ? "border-t-warn" : "border-t-primary"}`}>
        <div className="flex flex-wrap items-center justify-between gap-3">
          <h2 className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 break-words text-xl font-semibold">
            Batch <span className="font-mono text-base font-normal text-slate-500">{b.id.slice(0, 8)}</span>
            <BatchStatusBadge status={b.status} />
          </h2>
          {editable && !terminal && (
            <Button variant="danger" onClick={() => setConfirm(true)} disabled={b.cancel_requested}>
              {b.cancel_requested ? "Pembatalan diminta…" : "Batalkan"}
            </Button>
          )}
          {editable && terminal && (b.failed > 0 || b.processed < b.total) && (
            <Button variant="primary" onClick={doRetry} disabled={retry.isPending}>Ulangi item tersisa</Button>
          )}
        </div>

        <ErrorBox message={generalError(cancel.error, []) || generalError(retry.error, [])} />

        <div>
          <div className="mb-1.5 flex justify-between gap-3 text-sm">
            <span>{b.processed} dari {b.total} selesai{b.failed > 0 ? `, ${b.failed} gagal` : ""}</span>
            <span className="text-slate-500">{pct}%</span>
          </div>
          <div role="progressbar" aria-label="Progres batch" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}
            className="flex h-2.5 overflow-hidden rounded-full bg-slate-200">
            <div className="h-full bg-primary transition-all" style={{ width: `${share(b.processed)}%` }} />
            <div className="h-full bg-danger transition-all" style={{ width: `${share(b.failed)}%` }} />
          </div>
        </div>

        <dl className="grid gap-4 text-sm sm:grid-cols-2 lg:grid-cols-4">
          {([
            ["Dibuat oleh", `${b.created_by_name ?? b.created_by}, ${fmtDate(b.created_at)}`],
            ["Mulai", b.started_at ? fmtDate(b.started_at) : "—"],
            ["Selesai", b.finished_at ? fmtDate(b.finished_at) : "—"],
            ["Dibatalkan oleh", b.cancelled_by_name ?? b.cancelled_by ?? "—"],
          ] as [string, string][]).map(([k, v]) => (
            <div key={k}><dt className="text-xs uppercase tracking-wide text-slate-500">{k}</dt><dd>{v}</dd></div>
          ))}
        </dl>
      </section>

      {b.status === "failed" && b.error && (
        <div role="alert" className="rounded-2xl bg-danger-soft p-4 text-sm text-danger">
          <p className="font-medium">Batch berhenti karena error.</p>
          <pre className="mt-2 whitespace-pre-wrap break-words font-mono text-xs">{b.error}</pre>
        </div>
      )}
      {(b.status === "partial" || b.status === "cancelled") && (
        <p className="rounded-2xl bg-warn-soft p-4 text-sm text-warn">
          {b.status === "cancelled" ? "Batch dibatalkan di batas item." : "Sebagian migration gagal."} Perubahan yang sudah di-commit tetap tersimpan; tidak ada rollback otomatis.
        </p>
      )}

      <section>
        <h3 className="mb-3 font-serif text-lg font-semibold">Item batch</h3>
        {items.isLoading && <Loading />}
        {items.error && <StateError error={items.error} onRetry={() => items.refetch()} />}
        {items.data && items.data.length > 0 && (
          <DataList label="Item batch" rows={items.data} columns={columns} rowKey={(it) => String(it.id)} />
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
