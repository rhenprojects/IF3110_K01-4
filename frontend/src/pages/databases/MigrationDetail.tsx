import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useExecuteMigration, useMigration } from "../../features/databases/hooks";
import { Button, ConfirmDialog, ErrorBox, Loading, generalError } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration, isDestructive } from "../../lib/format";
import { isConnectionError } from "../../lib/errors";
import { StateError, StatusBadge, canEdit, useDb } from "./shared";

export default function MigrationDetail() {
  const db = useDb();
  const { mid = "" } = useParams();
  const nav = useNavigate();
  const toast = useToast();
  const q = useMigration(db.id, mid);
  const exec = useExecuteMigration(db.id);
  const [confirm, setConfirm] = useState(false);

  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  const m = q.data;
  const editable = canEdit(db.level);

  async function run() {
    try {
      const r = await exec.mutateAsync(m.id);
      if (r.status === "success") toast.success(`Migration v${r.version} berhasil di-commit`);
      else toast.error("Migration gagal dan di-rollback. Lihat pesan error di halaman ini.");
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
        {editable && m.status === "draft" && (
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
        <h3 className="mb-2 text-sm font-medium">SQL</h3>
        <pre className="max-h-96 overflow-auto rounded border bg-slate-900 p-4 text-sm text-slate-100"><code>{m.sql}</code></pre>
      </section>

      {confirm && (
        <ConfirmDialog title="Jalankan migration" confirmLabel="Jalankan" busy={exec.isPending}
          message={`SQL akan dijalankan dalam satu transaction: COMMIT jika semua berhasil, ROLLBACK jika ada yang gagal.${
            isDestructive(m.sql) ? " Peringatan: SQL ini berisi perintah destruktif (DROP / TRUNCATE / DELETE)." : ""}`}
          onConfirm={run} onClose={() => setConfirm(false)} />
      )}
    </div>
  );
}
