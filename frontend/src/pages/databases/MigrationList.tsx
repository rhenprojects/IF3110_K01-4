import { Link, useNavigate } from "react-router-dom";
import { useMigrations } from "../../features/databases/hooks";
import { Button, EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { fmtDate, fmtDuration } from "../../lib/format";
import { StateError, StatusBadge, canEdit, useDb } from "./shared";

export default function MigrationList() {
  const db = useDb();
  const nav = useNavigate();
  const q = useMigrations(db.id);
  const add = canEdit(db.level)
    ? <Button variant="primary" onClick={() => nav(`/databases/${db.id}/migrations/new`)}>Migration baru</Button> : undefined;
  return (
    <div>
      <PageHeader title="Riwayat migration" action={add} />
      <p className="mb-4 text-sm text-slate-500">
        Riwayat bersifat linear: nomor versi hanya diberikan pada migration yang berhasil di-commit, berurutan tanpa lompatan.
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title="Belum ada migration"
          hint={canEdit(db.level) ? "Buat migration pertama untuk mengubah struktur database ini." : "Belum ada perubahan yang tercatat."} action={add} />
      )}
      {q.data && q.data.length > 0 && (
        <Table columns={["Versi", "Nama", "Status", "Dibuat oleh", "Dijalankan", "Durasi"]}>
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
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}
