import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useCreateBatch, useMigrations } from "../../features/databases/hooks";
import { Button, ConfirmDialog, EmptyState, ErrorBox, Loading, PageHeader, Table, generalError, td } from "../../components/ui";
import { useToast } from "../../components/toast";
import { fmtDate, fmtDuration } from "../../lib/format";
import { StateError, StatusBadge, canEdit, useDb } from "./shared";

export default function MigrationList() {
  const db = useDb();
  const nav = useNavigate();
  const toast = useToast();
  const q = useMigrations(db.id);
  const batch = useCreateBatch(db.id);
  const [picked, setPicked] = useState<string[]>([]);
  const [confirm, setConfirm] = useState(false);

  const editable = canEdit(db.level);
  const drafts = q.data?.filter((m) => m.status === "draft") ?? [];
  const toggle = (id: string) =>
    setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));

  async function run() {
    try {
      const b = await batch.mutateAsync(picked);
      toast.success(`${picked.length} migration dijadwalkan dalam batch.`);
      setPicked([]);
      nav(`/databases/${db.id}/migrations/batches/${b.id}`);
    } catch {
      //[error] ditampilkan lewat batch.error
    } finally {
      setConfirm(false);
    }
  }

  const add = editable
    ? <Button variant="primary" onClick={() => nav(`/databases/${db.id}/migrations/new`)}>Migration baru</Button> : undefined;
  const actions = editable ? (
    <div className="flex flex-wrap gap-2">
      {picked.length > 0 && (
        <Button onClick={() => setConfirm(true)} disabled={batch.isPending}>
          Jalankan terpilih ({picked.length})
        </Button>
      )}
      {add}
    </div>
  ) : add;

  const cols = ["Versi", "Nama", "Status", "Dibuat oleh", "Dijalankan", "Durasi", ...(editable ? ["Batch"] : [])];
  return (
    <div>
      <PageHeader title="Riwayat migration" action={actions} />
      <p className="mb-4 text-sm text-slate-500">
        Riwayat bersifat linear: nomor versi hanya diberikan pada migration yang berhasil di-commit, berurutan tanpa lompatan.
        {editable && drafts.length > 0 && " Pilih beberapa draft untuk dijalankan sekaligus dalam satu batch."}
      </p>
      <ErrorBox message={generalError(batch.error, [])} />
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title="Belum ada migration"
          hint={editable ? "Buat migration pertama untuk mengubah struktur database ini." : "Belum ada perubahan yang tercatat."} action={add} />
      )}
      {q.data && q.data.length > 0 && (
        <Table columns={cols}>
          {q.data.map((m) => (
            <tr key={m.id}>
              <td className={`${td} font-mono`}>{m.version ? `v${m.version}` : "—"}</td>
              <td className={td}><Link to={`/databases/${db.id}/migrations/${m.id}`} className="font-medium underline-offset-2 hover:underline">{m.name}</Link></td>
              <td className={td}><StatusBadge status={m.status} /></td>
              <td className={td}>{m.created_by_name}<span className="block text-xs text-slate-400">{fmtDate(m.created_at)}</span></td>
              <td className={td}>
                {m.executed_at ? <>{m.executed_by_name}<span className="block text-xs text-slate-400">{fmtDate(m.executed_at)}</span></> : "—"}
              </td>
              <td className={td}>{fmtDuration(m.duration_ms)}</td>
              {editable && (
                <td className={td}>
                  {m.status === "draft" ? (
                    <label className="flex items-center gap-2 text-sm">
                      <input type="checkbox" checked={picked.includes(m.id)} onChange={() => toggle(m.id)} />
                      Pilih
                    </label>
                  ) : "—"}
                </td>
              )}
            </tr>
          ))}
        </Table>
      )}

      {confirm && (
        <ConfirmDialog title="Jalankan batch" confirmLabel="Jalankan" busy={batch.isPending}
          message={`${picked.length} draft migration akan dijalankan berurutan. Setiap migration memakai transaction sendiri; yang gagal tidak menghentikan migration berikutnya, tetapi batch berhenti bila database tidak terjangkau atau dibatalkan.`}
          onConfirm={run} onClose={() => setConfirm(false)} />
      )}
    </div>
  );
}
