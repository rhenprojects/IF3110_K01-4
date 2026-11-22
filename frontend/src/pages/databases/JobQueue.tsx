import { Link } from "react-router-dom";
import { useJobs } from "../../features/databases/hooks";
import { EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import { JobStatusBadge, KIND_LABEL, StateError, useDb } from "./shared";

//[queue] monitoring sederhana: job terbaru di database ini, auto-refresh selama masih ada yang belum selesai
export default function JobQueue() {
  const db = useDb();
  const q = useJobs(db.id);
  const active = q.data?.filter((j) => j.status !== "success" && j.status !== "failed").length ?? 0;
  return (
    <div>
      <PageHeader title="Queue eksekusi" />
      <p className="mb-4 text-sm text-slate-500">
        Setiap migration yang dijalankan masuk queue lalu diambil worker. Satu database hanya menjalankan satu migration pada satu
        waktu (dikunci dengan lock database); kegagalan sementara seperti koneksi putus di-retry otomatis dengan backoff.
        {active > 0 && ` ${active} job belum selesai, halaman ini diperbarui otomatis.`}
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Queue kosong" hint="Jalankan migration dari halaman detail migration." />}
      {q.data && q.data.length > 0 && (
        <Table columns={["Migration", "Jenis", "Status", "Attempt", "Worker", "Dibuat", "Error"]}>
          {q.data.map((j) => (
            <tr key={j.id} className="align-top">
              <td className={td}>
                <Link to={`/databases/${db.id}/migrations/${j.migration_id}`} className="underline-offset-2 hover:underline">
                  {j.migration_name ?? j.migration_id.slice(0, 8)}
                </Link>
                <span className="block font-mono text-xs text-slate-400">{j.id.slice(0, 8)}</span>
              </td>
              <td className={td}>{KIND_LABEL[j.kind]}</td>
              <td className={td}><JobStatusBadge status={j.status} /></td>
              <td className={td}>{j.attempts}/{j.max_attempts}</td>
              <td className={`${td} font-mono text-xs`}>{j.worker_id || "—"}</td>
              <td className={`${td} whitespace-nowrap`}>{fmtDate(j.created_at)}</td>
              <td className={`${td} max-w-xs break-words text-xs text-red-700`}>{j.error}</td>
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}
