import { useState } from "react";
import { Link } from "react-router-dom";
import { useHistory, useMigrations } from "../../features/databases/hooks";
import { DataList, type Column } from "../../components/DataList";
import { ToggleChip } from "../../components/Filters";
import { EmptyState, Loading, PageHeader } from "../../components/ui";
import { fmtDate, fmtDuration } from "../../lib/format";
import type { MigrationExecution } from "../../lib/types";
import { JobStatusBadge, KindBadge, StateError, useDb } from "./shared";

//[history] semua attempt eksekusi database ini; "Hide rollback" menyembunyikan eksekusi yang dibatalkan beserta rollback-nya
export default function MigrationHistory() {
  const db = useDb();
  const [hide, setHide] = useState(false);
  const q = useHistory(db.id, hide);
  const migrations = useMigrations(db.id);
  const name = (id: string) => migrations.data?.find((m) => m.id === id)?.name ?? id.slice(0, 8);

  const columns: Column<MigrationExecution>[] = [
    { header: "Waktu", nowrap: true, cell: (x) => fmtDate(x.started_at) },
    {
      header: "Migration", role: "title",
      cell: (x) => <Link to={`/databases/${db.id}/migrations/${x.migration_id}`} className="text-primary underline-offset-2 hover:underline">{name(x.migration_id)}</Link>,
    },
    { header: "Jenis", cell: (x) => <KindBadge kind={x.kind} /> },
    { header: "Status", role: "status", cell: (x) => <JobStatusBadge status={x.status} /> },
    { header: "Durasi", cell: (x) => fmtDuration(x.duration_ms) },
    {
      header: "Error", wide: true,
      cell: (x) => x.error ? <span className="font-mono text-xs text-danger">{x.sqlstate && `[${x.sqlstate}] `}{x.error}</span> : null,
    },
  ];

  return (
    <div>
      <PageHeader title="Riwayat eksekusi" action={
        <ToggleChip label="Hide rollback" checked={hide} onChange={setHide}
          hint="Sembunyikan eksekusi yang sudah dibatalkan beserta rollback-nya" />
      } />
      <p className="mb-4 max-w-3xl text-sm text-slate-500">
        Semua percobaan eksekusi di database ini, termasuk yang gagal. Hide rollback hanya menyembunyikan tampilan; riwayatnya tetap tersimpan utuh.
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title={hide ? "Tidak ada eksekusi yang ditampilkan" : "Belum ada eksekusi"}
          hint={hide ? "Matikan Hide rollback untuk melihat semua eksekusi." : "Eksekusi muncul setelah migration disetujui dan dijalankan."} />
      )}
      {q.data && q.data.length > 0 && (
        <DataList label="Riwayat eksekusi" rows={q.data} columns={columns} rowKey={(x) => String(x.id)} />
      )}
    </div>
  );
}
