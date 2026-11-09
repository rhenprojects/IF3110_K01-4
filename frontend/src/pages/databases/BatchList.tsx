import { Link } from "react-router-dom";
import { useBatches } from "../../features/databases/hooks";
import { EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import { BatchStatusBadge, StateError, useDb } from "./shared";

export default function BatchList() {
  const db = useDb();
  const q = useBatches(db.id);
  return (
    <div>
      <PageHeader title="Riwayat batch" />
      <p className="mb-4 text-sm text-slate-500">
        Satu batch menjalankan beberapa draft migration secara berurutan. Progres disimpan per item sehingga batch bisa dilanjutkan atau diulang tanpa mengulang migration yang sudah berhasil.
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Belum ada batch" hint="Pilih beberapa draft migration di halaman Migrations lalu jalankan sebagai batch." />}
      {q.data && q.data.length > 0 && (
        <Table columns={["Dibuat", "Status", "Progres", "Gagal", "Oleh", "Durasi"]}>
          {q.data.map((b) => (
            <tr key={b.id}>
              <td className={td}>
                <Link to={`/databases/${db.id}/migrations/batches/${b.id}`} className="font-mono text-xs underline-offset-2 hover:underline">{b.id.slice(0, 8)}</Link>
                <span className="block text-xs text-slate-400">{fmtDate(b.created_at)}</span>
              </td>
              <td className={td}><BatchStatusBadge status={b.status} /></td>
              <td className={td}>{b.processed}/{b.total}</td>
              <td className={td}>{b.failed}</td>
              <td className={td}>{b.created_by_name ?? b.created_by}</td>
              <td className={td}>{fmtDate(b.finished_at) === "—" ? "—" : `${Math.max(1, Math.round((Date.parse(b.finished_at!) - Date.parse(b.started_at ?? b.created_at)) / 1000))} dtk`}</td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}
