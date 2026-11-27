import { Link, useNavigate } from "react-router-dom";
import { useMigrations } from "../../features/databases/hooks";
import { Badge, Button, EmptyState, Loading, PageHeader, Table, td } from "../../components/ui";
import { fmtDate, fmtDuration } from "../../lib/format";
import type { Migration, MigrationStatus } from "../../lib/types";
import { StateError, StatusBadge, canEdit, useDb } from "./shared";

type Filter = "all" | "draft" | "review" | "success" | "failed";
const MATCH: Record<Filter, (s: MigrationStatus) => boolean> = {
  all: () => true,
  draft: (s) => s === "draft",
  review: (s) => s === "pending_review" || s === "approved",
  success: (s) => s === "success",
  failed: (s) => s === "failed",
};

export default function MigrationList() {
  const db = useDb();
  const nav = useNavigate();
  const q = useMigrations(db.id);
  const editable = canEdit(db.level);

  const add = editable
    ? <Button variant="primary" onClick={() => nav(`/databases/${db.id}/migrations/new`)}>Migration baru</Button> : undefined;

  return (
    <div>
      <PageHeader title="Riwayat migration" action={add} />
      <p className="mb-4 text-sm text-slate-500">
        Riwayat bersifat linear: nomor versi hanya diberikan pada migration yang berhasil di-commit, berurutan tanpa lompatan.
        Migration dengan <b>Batch size &gt; 0</b> diberi tanda <b>Batch</b>; buka detailnya untuk melihat progres per potongan.
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title="Belum ada migration"
          hint={editable ? "Buat migration pertama untuk mengubah struktur database ini." : "Belum ada perubahan yang tercatat."} action={add} />
      )}
      {q.data && q.data.length > 0 && (
        <Table columns={["Versi", "Nama", "Status", "Dibuat oleh", "Dijalankan", "Durasi", ...(editable ? ["Aksi"] : [])]}>
          {q.data.map((m) => (
            <tr key={m.id}>
              <td className={`${td} font-mono`}>{m.version ? `v${m.version}` : "—"}</td>
              <td className={td}>
                <span className="flex flex-wrap items-center gap-2">
                  <Link to={`/databases/${db.id}/migrations/${m.id}`} className="font-medium underline-offset-2 hover:underline">{m.name}</Link>
                  {m.batch_size > 0 && <Badge tone="dark">Batch {m.batch_size.toLocaleString("id-ID")}</Badge>}
                </span>
              </td>
              <td className={td}><StatusBadge status={m.status} /></td>
              <td className={td}>{m.created_by_name}<span className="block text-xs text-slate-400">{fmtDate(m.created_at)}</span></td>
              <td className={td}>
                {m.executed_at ? <>{m.executed_by_name}<span className="block text-xs text-slate-400">{fmtDate(m.executed_at)}</span></> : "—"}
              </td>
              <td className={td}>{fmtDuration(m.duration_ms)}</td>
              {editable && (
                <td className={`${td} whitespace-nowrap`}>
                  {m.status === "draft"
                    ? <Link to={`/databases/${db.id}/migrations/${m.id}/edit`} className="font-medium text-primary underline-offset-2 hover:underline">Edit</Link>
                    : <span className="text-slate-400">—</span>}
                </td>
              )}
            </tr>
          ))}
        </Table>
      )}
    </div>
  );
}
