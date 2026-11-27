import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  useCancelBatchMigration, useChangeRequests, useDispatchChangeRequest, useJob, useJobs, useMigration, useMigrationExecutions,
  useResumeBatchMigration, useSubmitChangeRequest,
} from "../../features/databases/hooks";
import { Badge, Button, ConfirmDialog, ErrorBox, Loading, Table, generalError, td } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration, isDestructive } from "../../lib/format";
import type { ChangeRequest, JobKind, MigrationExecution, MigrationJob } from "../../lib/types";
import { JobStatusBadge, KIND_LABEL, StateError, StatusBadge, canEdit, useDb } from "./shared";

//[queue] status job terbaru migration ini; dipolling sampai success/failed
function JobPanel({ job }: { job: MigrationJob }) {
  const text: Record<MigrationJob["status"], string> = {
    queued: job.attempts > 0 ? `Menunggu retry (attempt ${job.attempts + 1} dari ${job.max_attempts}).` : "Menunggu worker mengambil job ini.",
    running: `Sedang dijalankan worker (attempt ${job.attempts} dari ${job.max_attempts}).`,
    uncertain: "Worker berhenti saat commit. Hasilnya sedang diverifikasi; migration lain di database ini ditahan sampai pasti.",
    success: "Job selesai dan di-commit.",
    failed: "Job gagal. Seluruh perubahan di-rollback.",
  };
  return (
    <div role="status" aria-live="polite" className="flex flex-wrap items-center gap-3 rounded border bg-slate-50 p-3 text-sm">
      <span className="font-medium">Job {KIND_LABEL[job.kind]}</span>
      <JobStatusBadge status={job.status} />
      <span className="text-slate-600">{text[job.status]}</span>
    </div>
  );
}

//[review] status persetujuan per group: tiap Team Leader group yang punya akses harus menyetujui, satu penolakan cukup untuk menolak
function ReviewPanel({ cr, onDispatch, dispatching }: { cr: ChangeRequest; onDispatch?: () => void; dispatching: boolean }) {
  const status = {
    pending_review: { tone: "amber", label: "Menunggu persetujuan" },
    approved: { tone: "green", label: "Disetujui" },
    rejected: { tone: "red", label: "Ditolak" },
  } as const;
  const decision = {
    pending: { tone: "amber", label: "Menunggu" },
    approve: { tone: "green", label: "Setuju" },
    reject: { tone: "red", label: "Tolak" },
  } as const;
  return (
    <section className="space-y-3 rounded border p-3">
      <div className="flex flex-wrap items-center gap-3 text-sm">
        <h3 className="font-medium">Review {KIND_LABEL[cr.kind]}</h3>
        <Badge tone={status[cr.status].tone}>{status[cr.status].label}</Badge>
        <span className="text-slate-500">diajukan {cr.requested_by_name} · {fmtDate(cr.created_at)}</span>
        {onDispatch && <Button onClick={onDispatch} disabled={dispatching}>Masukkan ke queue</Button>}
      </div>
      {onDispatch && <p className="text-sm text-slate-600">Sudah disetujui semua, tetapi belum masuk queue. Coba masukkan ke queue lagi.</p>}
      <Table columns={["Group", "Team Leader", "Keputusan", "Komentar"]}>
        {cr.reviewers.map((r, i) => (
          <tr key={i} className="align-top">
            <td className={td}>{r.group_name ?? "—"}</td>
            <td className={td}>{r.leader_name ?? "—"}</td>
            <td className={td}><Badge tone={decision[r.decision].tone}>{decision[r.decision].label}</Badge></td>
            <td className={td}>{r.comment || "—"}</td>
          </tr>
        ))}
      </Table>
    </section>
  );
}

function ExecutionHistory({ items }: { items: MigrationExecution[] }) {
  if (items.length === 0) return null;
  return (
    <section>
      <h3 className="mb-2 text-sm font-medium">Riwayat attempt</h3>
      <Table columns={["#", "Jenis", "Status", "Waktu", "Durasi", "Error"]}>
        {items.map((x) => (
          <tr key={x.id} className="align-top">
            <td className={`${td} font-mono`}>{x.attempt}</td>
            <td className={td}>{KIND_LABEL[x.kind]}{x.recovered && <span className="ml-1 text-xs text-slate-500">(dipulihkan)</span>}</td>
            <td className={td}><JobStatusBadge status={x.status} /></td>
            <td className={`${td} whitespace-nowrap`}>{fmtDate(x.started_at)}</td>
            <td className={td}>{fmtDuration(x.duration_ms)}</td>
            <td className={`${td} font-mono text-xs text-red-700`}>{x.sqlstate && `[${x.sqlstate}] `}{x.error}</td>
          </tr>
        ))}
      </Table>
    </section>
  );
}

export default function MigrationDetail() {
  const db = useDb();
  const { mid = "" } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const q = useMigration(db.id, mid);
  const qc = useQueryClient();
  const submit = useSubmitChangeRequest(db.id);
  const dispatch = useDispatchChangeRequest(db.id);
  const crs = useChangeRequests(db.id);
  const jobs = useJobs(db.id);
  const history = useMigrationExecutions(db.id, mid);
  const cancelBatch = useCancelBatchMigration(db.id);
  const resumeBatch = useResumeBatchMigration(db.id);
  const [confirm, setConfirm] = useState<JobKind | null>(null);

  //[review] pengajuan terbaru untuk migration ini
  const cr = crs.data?.filter((c) => c.migration_id === mid).sort((a, z) => z.created_at.localeCompare(a.created_at))[0];
  //[review] keputusan terakhir mengubah status migration dan membuat job di server, jadi muat ulang saat pengajuan berubah
  useEffect(() => {
    qc.invalidateQueries({ queryKey: ["db", db.id], predicate: (x) => x.queryKey[2] === "migrations" || x.queryKey[2] === "jobs" });
  }, [qc, db.id, cr?.status, cr?.job_id]);

  //[queue] job terbaru migration ini; dipolling sampai selesai
  const latest = jobs.data?.find((j) => j.migration_id === mid);
  const job = useJob(db.id, latest?.id).data;

  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  const m = q.data;
  const editable = canEdit(db.level);
  const waiting = cr?.status === "pending_review" || (cr?.status === "approved" && !cr.job_id);
  const busy = waiting || (!!job && (job.status === "queued" || job.status === "running" || job.status === "uncertain"));
  //[rollback] rollback sukses tidak mengubah baris migration, jadi keadaan "dibatalkan" dibaca dari riwayat eksekusi
  const lastOk = [...(history.data ?? [])].sort((a, z) => z.id - a.id).find((x) => x.status === "success");
  const rolledBack = m.status === "success" && lastOk?.kind === "rollback";
  const canRollback = m.status === "success" && !m.irreversible && !rolledBack && m.batch_size === 0;
  const canForward = (m.status === "failed" || rolledBack) && m.batch_size === 0;

  async function send(kind: JobKind) {
    try {
      await submit.mutateAsync({ mid: m.id, kind });
      toast.success("Pengajuan dikirim ke Team Leader");
    } catch {
      //[error] ditampilkan lewat submit.error
    } finally {
      setConfirm(null);
    }
  }

  const execMsg = generalError(submit.error, []) ?? generalError(dispatch.error, []);
  const meta: [string, string][] = [
    ["Dibuat oleh", `${m.created_by_name} · ${fmtDate(m.created_at)}`],
    ["Dijalankan oleh", m.executed_at ? `${m.executed_by_name} · ${fmtDate(m.executed_at)}` : "Belum dijalankan"],
    ["Durasi", fmtDuration(m.duration_ms)],
  ];

  return (
    <div className="space-y-5">
      <Link to={`/databases/${db.id}/migrations`} className="text-sm text-slate-500 hover:underline">← Riwayat migration</Link>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 break-words text-lg font-semibold">
          {m.version && <span className="font-mono text-slate-400">v{m.version}</span>}{m.name} <StatusBadge status={m.status} />
        </h2>
        {editable && !busy && (
          <div className="flex flex-wrap gap-2">
            {m.status === "draft" && <Button onClick={() => nav(`/databases/${db.id}/migrations/${m.id}/edit`)}>Edit</Button>}
            {m.status === "draft" && <Button variant="primary" onClick={() => setConfirm("up")}>Ajukan review</Button>}
            {canRollback && <Button onClick={() => setConfirm("rollback")}>Rollback</Button>}
            {canForward && <Button variant="primary" onClick={() => setConfirm("forward")}>Forward</Button>}
            {m.status === "failed" && (
              <Button onClick={() => nav(`/databases/${db.id}/migrations/new`, { state: { name: m.name, sql: m.sql } })}>Salin ke migration baru</Button>
            )}
          </div>
        )}
      </div>

      <ErrorBox message={execMsg} />
      {cr && (
        <ReviewPanel cr={cr} dispatching={dispatch.isPending}
          onDispatch={editable && cr.status === "approved" && !cr.job_id ? () => dispatch.mutate(cr.id) : undefined} />
      )}
      {job && <JobPanel job={job} />}

      {m.batch_size > 0 && (
        <section className="space-y-2 rounded border bg-slate-50 p-3 text-sm">
          <div className="flex flex-wrap items-center gap-3">
            <h3 className="font-medium">Batch per potongan row</h3>
            <Badge tone={m.batch_status === "success" ? "green" : m.batch_status.includes("reverted") ? "red" : "amber"}>
              {m.batch_status || "belum berjalan"}
            </Badge>
            <span className="text-slate-600">{m.batch_rows} row · {m.batch_chunks} chunk · cursor {m.batch_cursor}</span>
          </div>
          <p className="text-xs text-slate-500">Batch size {m.batch_size}. Checkpoint disimpan di database target pada transaction yang sama dengan potongan.</p>
          {m.batch_cancel_requested && (
            <p className="text-xs text-amber-700">Permintaan cancel diterima; worker berhenti di batas potongan lalu menjalankan SQL down terbalik.</p>
          )}
          <div className="flex flex-wrap gap-2">
            {(m.batch_status === "running" || m.batch_status === "partial") && (
              <Button variant="danger" onClick={() => cancelBatch.mutate(m.id)} disabled={cancelBatch.isPending || m.batch_cancel_requested}>
                Batalkan & revert
              </Button>
            )}
            {m.status === "partial" && !m.batch_cancel_requested && (
              <Button variant="primary" onClick={() => resumeBatch.mutate(m.id)} disabled={resumeBatch.isPending}>
                Lanjutkan dari checkpoint
              </Button>
            )}
          </div>
        </section>
      )}

      <ErrorBox message={generalError(cancelBatch.error, []) ?? generalError(resumeBatch.error, [])} />

      <dl className="grid gap-3 text-sm sm:grid-cols-3">
        {meta.map(([k, v]) => (
          <div key={k}><dt className="text-xs uppercase tracking-wide text-slate-500">{k}</dt><dd>{v}</dd></div>
        ))}
      </dl>

      {m.status === "failed" && (
        <div role="alert" className="rounded border border-red-200 bg-red-50 p-4 text-sm text-red-800">
          <p className="font-medium">Gagal dijalankan. Seluruh perubahan di-rollback.</p>
          <pre className="mt-2 whitespace-pre-wrap font-mono text-xs">{m.error}</pre>
          <p className="mt-2 text-xs">Jalankan ulang lewat Forward (perlu persetujuan lagi), atau salin SQL-nya ke migration baru.</p>
        </div>
      )}
      {m.status === "success" && (
        <p className="rounded border border-green-200 bg-green-50 p-3 text-sm text-green-800">Di-commit sebagai versi {m.version}. Migration yang berhasil tidak dapat diubah.</p>
      )}

      <section>
        <h3 className="mb-2 text-sm font-medium">SQL up</h3>
        <pre className="max-h-96 overflow-auto rounded border bg-slate-900 p-4 text-sm text-slate-100"><code>{m.sql}</code></pre>
      </section>
      <section>
        <h3 className="mb-2 text-sm font-medium">SQL down (rollback)</h3>
        {m.irreversible ? (
          <p className="rounded border border-amber-200 bg-amber-50 p-3 text-sm text-amber-800">
            Irreversible: {m.irreversible_reason || "alasan belum diisi"}
          </p>
        ) : m.sql_down ? (
          <pre className="max-h-96 overflow-auto rounded border bg-slate-900 p-4 text-sm text-slate-100"><code>{m.sql_down}</code></pre>
        ) : (
          <p className="text-sm text-slate-500">Belum diisi. Isi SQL down atau tandai irreversible sebelum menjalankan migration.</p>
        )}
      </section>

      <ExecutionHistory items={history.data ?? []} />

      {confirm && (
        <ConfirmDialog title={`Ajukan ${KIND_LABEL[confirm]}`} confirmLabel="Ajukan" busy={submit.isPending}
          message={`${{
            up: "Migration diajukan ke Team Leader setiap group yang punya akses ke database ini. Setelah semuanya setuju, migration masuk queue dan dijalankan dalam satu transaction; satu penolakan membatalkan pengajuan.",
            rollback: "SQL down dijalankan dalam satu transaction setelah semua Team Leader setuju. Hanya migration aktif terakhir yang bisa di-rollback.",
            forward: "SQL up dijalankan ulang setelah semua Team Leader setuju.",
          }[confirm]}${confirm !== "rollback" && isDestructive(m.sql) ? " Peringatan: SQL ini berisi perintah destruktif (DROP / TRUNCATE / DELETE)." : ""}`}
          onConfirm={() => send(confirm)} onClose={() => setConfirm(null)} />
      )}
    </div>
  );
}