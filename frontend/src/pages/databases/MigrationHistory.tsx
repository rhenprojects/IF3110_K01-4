import { useState } from "react";
import { Link } from "react-router-dom";
import { useHistory, useMigrations } from "../../features/databases/hooks";
import { EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { fmtDate, fmtDuration } from "../../lib/format";
import { JobStatusBadge, KIND_LABEL, StateError, useDb } from "./shared";

//[history] semua attempt eksekusi database ini; "Hide rollback" menyembunyikan eksekusi yang dibatalkan beserta rollback-nya
export default function MigrationHistory() {
  const db = useDb();
  const [hide, setHide] = useState(false);
  const q = useHistory(db.id, hide);
  const migrations = useMigrations(db.id);
  const name = (id: string) => migrations.data?.find((m) => m.id === id)?.name ?? id.slice(0, 8);

  return (
    <div>
      <PageHeader title="Riwayat eksekusi" action={
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={hide} onChange={(e) => setHide(e.target.checked)} />
          Hide rollback
        </label>
      } />
      <p className="mb-4 text-sm text-slate-500">Urutan kronologis, termasuk percobaan yang gagal.</p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Belum ada eksekusi" hint="Eksekusi muncul setelah migration disetujui dan dijalankan." />}
      {q.data && q.data.length > 0 && (
        <Table columns={["Waktu", "Migration", "Jenis", "Status", "Durasi", "Error"]}>
          {q.data.map((x) => (
            <tr key={x.id} className="align-top">
              <td className={`${td} whitespace-nowrap`}>{fmtDate(x.started_at)}</td>
              <td className={td}>
                <Link to={`/databases/${db.id}/migrations/${x.migration_id}`} className="underline-offset-2 hover:underline">{name(x.migration_id)}</Link>
              </td>
              <td className={td}>{KIND_LABEL[x.kind]}</td>
              <td className={td}><JobStatusBadge status={x.status} /></td>
              <td className={td}>{fmtDuration(x.duration_ms)}</td>
              <td className={`${td} font-mono text-xs text-red-700`}>{x.sqlstate && `[${x.sqlstate}] `}{x.error}</td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}