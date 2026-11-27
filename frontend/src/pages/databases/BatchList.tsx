import { useState } from "react";
import { Link } from "react-router-dom";
import { useBatches } from "../../features/databases/hooks";
import { DataList, type Column } from "../../components/DataList";
import { FilterChips } from "../../components/Filters";
import { Button, EmptyState, Loading, PageHeader } from "../../components/ui";
import { fmtDate } from "../../lib/format";
import type { BatchStatus, MigrationBatch } from "../../lib/types";
import { BatchStatusBadge, StateError, useDb } from "./shared";

type Filter = "all" | "active" | "success" | "attention";

//[batch] pengelompokan status untuk filter; "perlu tindakan" = batch yang berhenti sebelum semua item sukses
const GROUP: Record<Exclude<Filter, "all">, BatchStatus[]> = {
  active: ["queued", "running"],
  success: ["success"],
  attention: ["partial", "failed", "cancelled"],
};

const duration = (b: MigrationBatch) =>
  fmtDate(b.finished_at) === "—" ? "—" : `${Math.max(1, Math.round((Date.parse(b.finished_at!) - Date.parse(b.started_at ?? b.created_at)) / 1000))} dtk`;

export default function BatchList() {
  const db = useDb();
  const q = useBatches(db.id);
  const [filter, setFilter] = useState<Filter>("all");

  const all = q.data ?? [];
  const count = (f: Exclude<Filter, "all">) => all.filter((b) => GROUP[f].includes(b.status)).length;
  const rows = filter === "all" ? all : all.filter((b) => GROUP[filter].includes(b.status));

  const columns: Column<MigrationBatch>[] = [
    {
      header: "Dibuat", role: "title",
      cell: (b) => (
        <>
          <Link to={`/databases/${db.id}/migrations/batches/${b.id}`} className="font-mono text-sm text-primary underline-offset-2 hover:underline">{b.id.slice(0, 8)}</Link>
          <span className="block text-xs font-normal text-slate-500">{fmtDate(b.created_at)}</span>
        </>
      ),
    },
    { header: "Status", role: "status", cell: (b) => <BatchStatusBadge status={b.status} /> },
    { header: "Progres", cell: (b) => `${b.processed}/${b.total}` },
    { header: "Gagal", cell: (b) => <span className={b.failed > 0 ? "font-medium text-danger" : ""}>{b.failed}</span> },
    { header: "Oleh", cell: (b) => b.created_by_name ?? b.created_by },
    { header: "Durasi", cell: duration },
  ];

  return (
    <div>
      <PageHeader title="Riwayat batch" />
      <p className="mb-4 max-w-3xl text-sm text-slate-500">
        Satu batch menjalankan beberapa draft migration secara berurutan. Progres disimpan per item, jadi batch bisa dilanjutkan atau diulang
        tanpa mengulang migration yang sudah berhasil.
      </p>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Belum ada batch" hint="Pilih beberapa draft migration di halaman Migrations lalu jalankan sebagai batch." />}
      {all.length > 0 && (
        <>
          <div className="mb-4">
            <FilterChips<Filter> label="Filter status batch" value={filter} onChange={setFilter} options={[
              { value: "all", label: "Semua", count: all.length },
              { value: "active", label: "Aktif", count: count("active") },
              { value: "success", label: "Berhasil", count: count("success") },
              { value: "attention", label: "Perlu tindakan", count: count("attention") },
            ]} />
          </div>
          {rows.length === 0
            ? <EmptyState title="Tidak ada batch dengan status ini" action={<Button onClick={() => setFilter("all")}>Tampilkan semua</Button>} />
            : <DataList label="Riwayat batch" rows={rows} columns={columns} rowKey={(b) => b.id} />}
        </>
      )}
    </div>
  );
}
