import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useExecuteMigration, useJob, useJobs, useMigration, useMigrationExecutions } from "../../features/databases/hooks";
import { Button, ConfirmDialog, ErrorBox, Loading, Table, generalError, td } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration, isDestructive } from "../../lib/format";
import { isConnectionError } from "../../lib/errors";
import type { MigrationExecution, MigrationJob } from "../../lib/types";
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
  const exec = useExecuteMigration(db.id);
  const jobs = useJobs(db.id);
  const history = useMigrationExecutions(db.id, mid);
  const [confirm, setConfirm] = useState(false);
  const [jobId, setJobId] = useState<string>();

  //[queue] job dari tombol Jalankan, atau job terbaru migration ini (mis. setelah reload halaman)
  const latest = jobs.data?.find((j) => j.migration_id === mid);
  const job = useJob(db.id, jobId ?? latest?.id).data;

  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  const m = q.data;
  const editable = canEdit(db.level);
  const busy = !!job && (job.status === "queued" || job.status === "running" || job.status === "uncertain");

  async function run() {
    try {
      const j = await exec.mutateAsync(m.id);
      setJobId(j.id);
      toast.success("Migration masuk queue");
    } catch {
      //[error] ditampilkan lewat exec.error
    } finally {
      setConfirm(false);
    }
  }

  const execMsg = generalError(exec.error, []);
  const meta: [string, string][] = [
    ["Dibuat oleh", `${m.created_by_name} · ${fmtDate(m.created_at)}`],
    ["Dijalankan oleh", m.executed_at ? `${m.executed_by_name} · ${fmtDate(m.executed_at)}` : "Belum dijalankan"],
    ["Durasi", fmtDuration(m.duration_ms)],
  ];

  return (
    <div className="space-y-5">
      <Link to={`/databases/${db.id}/migrations`} className="text-sm text-slate-500 hover:underline">← Riwayat migration</Link>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h2 className="flex items-center gap-3 text-lg font-semibold">
          {m.version && <span className="font-mono text-slate-400">v{m.version}</span>}{m.name} <StatusBadge status={m.status} />
        </h2>
        {editable && m.status === "draft" && !busy && (
          <div className="flex gap-2">
            <Button onClick={() => nav(`/databases/${db.id}/migrations/${m.id}/edit`)}>Edit</Button>
            <Button variant="primary" onClick={() => setConfirm(true)}>Jalankan</Button>
          </div>
        )}
        {editable && m.status === "failed" && (
          <Button onClick={() => nav(`/databases/${db.id}/migrations/new`, { state: { name: m.name, sql: m.sql } })}>Salin ke migration baru</Button>
        )}
      </div>

      <ErrorBox message={execMsg && (isConnectionError(exec.error) ? `${execMsg} Migration tetap berstatus draft.` : execMsg)} />
      {job && <JobPanel job={job} />}

      <dl className="grid gap-3 text-sm sm:grid-cols-3">
        {meta.map(([k, v]) => (
          <div key={k}><dt className="text-xs uppercase tracking-wide text-slate-500">{k}</dt><dd>{v}</dd></div>
        ))}
      </dl>

      {m.status === "failed" && (
        <div role="alert" className="rounded border border-red-200 bg-red-50 p-4 text-sm text-red-800">
          <p className="font-medium">Gagal dijalankan. Seluruh perubahan di-rollback.</p>
          <pre className="mt-2 whitespace-pre-wrap font-mono text-xs">{m.error}</pre>
          <p className="mt-2 text-xs">Migration yang gagal tidak bisa dijalankan ulang. Salin SQL-nya ke migration baru untuk mencoba lagi.</p>
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
        <ConfirmDialog title="Jalankan migration" confirmLabel="Jalankan" busy={exec.isPending}
          message={`SQL akan dijalankan dalam satu transaction: COMMIT jika semua berhasil, ROLLBACK jika ada yang gagal.${
            isDestructive(m.sql) ? " Peringatan: SQL ini berisi perintah destruktif (DROP / TRUNCATE / DELETE)." : ""}`}
          onConfirm={run} onClose={() => setConfirm(false)} />
      )}
    </div>
  );
}
