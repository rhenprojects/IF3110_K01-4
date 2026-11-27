import { useState } from "react";
import { Link } from "react-router-dom";
import { useJobs } from "../../features/databases/hooks";
import { DataList, type Column } from "../../components/DataList";
import { FilterChips } from "../../components/Filters";
import { Button, EmptyState, Loading, PageHeader } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import type { MigrationJob } from "../../lib/types";
import { JobStatusBadge, KindBadge, StateError, useDb } from "./shared";

type Filter = "all" | "active" | "success" | "failed";

//[queue] "belum pasti" ikut dihitung aktif karena hasilnya masih diverifikasi dan database ditahan
const isActive = (j: MigrationJob) => j.status !== "success" && j.status !== "failed";

//[queue] monitoring sederhana: job terbaru di database ini, auto-refresh selama masih ada yang belum selesai
export default function JobQueue() {
  const db = useDb();
  const q = useJobs(db.id);
  const [filter, setFilter] = useState<Filter>("all");

  const all = q.data ?? [];
  const active = all.filter(isActive).length;
  const rows = all.filter((j) => filter === "all" || (filter === "active" ? isActive(j) : j.status === filter));

  const columns: Column<MigrationJob>[] = [
    {
      header: "Migration", role: "title",
      cell: (j) => (
        <>
          <Link to={`/databases/${db.id}/migrations/${j.migration_id}`} className="text-primary underline-offset-2 hover:underline">
            {j.migration_name ?? j.migration_id.slice(0, 8)}
          </Link>
          <span className="block font-mono text-xs font-normal text-slate-500">{j.id.slice(0, 8)}</span>
        </>
      ),
    },
    { header: "Jenis", cell: (j) => <KindBadge kind={j.kind} /> },
    { header: "Status", role: "status", cell: (j) => <JobStatusBadge status={j.status} /> },
    { header: "Attempt", cell: (j) => `${j.attempts}/${j.max_attempts}` },
    { header: "Worker", cell: (j) => <span className="font-mono text-xs">{j.worker_id || "—"}</span> },
    { header: "Dibuat", nowrap: true, cell: (j) => fmtDate(j.created_at) },
    { header: "Error", wide: true, cell: (j) => j.error ? <span className="break-words text-xs text-danger">{j.error}</span> : null },
  ];

  return (
    <div>
      <PageHeader title="Queue eksekusi" />
      <p className="mb-4 max-w-3xl text-sm text-slate-500">
        Setiap eksekusi masuk queue lalu diambil worker. Satu database hanya menjalankan satu migration pada satu waktu (dijaga lock database),
        dan kegagalan sementara seperti koneksi putus di-retry otomatis dengan jeda.
        {active > 0 && <span className="font-medium text-primary"> {active} job belum selesai, halaman ini diperbarui otomatis.</span>}
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Queue kosong" hint="Eksekusi muncul di sini setelah migration disetujui dan masuk queue." />}
      {all.length > 0 && (
        <>
          <div className="mb-4">
            <FilterChips<Filter> label="Filter status job" value={filter} onChange={setFilter} options={[
              { value: "all", label: "Semua", count: all.length },
              { value: "active", label: "Aktif", count: active },
              { value: "success", label: "Berhasil", count: all.filter((j) => j.status === "success").length },
              { value: "failed", label: "Gagal", count: all.filter((j) => j.status === "failed").length },
            ]} />
          </div>
          {rows.length === 0
            ? <EmptyState title="Tidak ada job dengan status ini" action={<Button onClick={() => setFilter("all")}>Tampilkan semua</Button>} />
            : <DataList label="Job di queue" rows={rows} columns={columns} rowKey={(j) => j.id} />}
        </>
      )}
    </div>
  );
}
