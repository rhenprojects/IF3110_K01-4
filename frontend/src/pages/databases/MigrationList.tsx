import { useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useCreateBatch, useMigrations } from "../../features/databases/hooks";
import { DataList, type Column } from "../../components/DataList";
import { FilterChips } from "../../components/Filters";
import { Button, ConfirmDialog, EmptyState, ErrorBox, Loading, PageHeader, generalError } from "../../components/ui";
import { useToast } from "../../components/toast";
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
  const toast = useToast();
  const q = useMigrations(db.id);
  const batch = useCreateBatch(db.id);
  const [picked, setPicked] = useState<string[]>([]);
  const [confirm, setConfirm] = useState(false);
  const [filter, setFilter] = useState<Filter>("all");

  const editable = canEdit(db.level);
  const all = q.data ?? [];
  const drafts = all.filter((m) => m.status === "draft");
  const rows = all.filter((m) => MATCH[filter](m.status));
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

  const columns: Column<Migration>[] = [
    { header: "Versi", cell: (m) => (m.version ? <span className="rounded-md bg-slate-100 px-2 py-0.5 font-mono text-xs">v{m.version}</span> : null) },
    {
      header: "Nama", role: "title",
      cell: (m) => <Link to={`/databases/${db.id}/migrations/${m.id}`} className="text-primary underline-offset-2 hover:underline">{m.name}</Link>,
    },
    { header: "Status", role: "status", cell: (m) => <StatusBadge status={m.status} /> },
    { header: "Dibuat oleh", cell: (m) => <>{m.created_by_name}<span className="block text-xs text-slate-500">{fmtDate(m.created_at)}</span></> },
    {
      header: "Dijalankan",
      cell: (m) => (m.executed_at ? <>{m.executed_by_name}<span className="block text-xs text-slate-500">{fmtDate(m.executed_at)}</span></> : null),
    },
    { header: "Durasi", cell: (m) => fmtDuration(m.duration_ms) },
    ...(editable ? [{
      header: "Batch",
      cell: (m: Migration) => m.status === "draft" ? (
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" className="size-4 accent-primary" checked={picked.includes(m.id)} onChange={() => toggle(m.id)} />
          Pilih
        </label>
      ) : null,
    }] : []),
  ];

  return (
    <div>
      <PageHeader title="Riwayat migration" action={actions} />
      <p className="mb-4 text-sm text-slate-500">
        Riwayat bersifat linear: nomor versi hanya diberikan pada migration yang berhasil di-commit, berurutan tanpa lompatan.
        {editable && drafts.length > 0 && " Pilih beberapa draft untuk dijalankan sekaligus dalam satu batch."}
      </p>
      {all.length > 0 && (
        <div className="mb-4">
          <FilterChips label="Filter status" value={filter} onChange={setFilter} options={[
            { value: "all", label: "Semua", count: all.length },
            { value: "draft", label: "Draft", count: all.filter((m) => MATCH.draft(m.status)).length },
            { value: "review", label: "Review", count: all.filter((m) => MATCH.review(m.status)).length },
            { value: "success", label: "Berhasil", count: all.filter((m) => MATCH.success(m.status)).length },
            { value: "failed", label: "Gagal", count: all.filter((m) => MATCH.failed(m.status)).length },
          ]} />
        </div>
      )}
      <ErrorBox message={generalError(batch.error, [])} />
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title="Belum ada migration"
          hint={editable ? "Buat migration pertama untuk mengubah struktur database ini." : "Belum ada perubahan yang tercatat."} action={add} />
      )}
      {all.length > 0 && rows.length === 0 && <EmptyState title="Tidak ada migration dengan status ini" />}
      {rows.length > 0 && <DataList label="Riwayat migration" rows={rows} columns={columns} rowKey={(m) => m.id} />}

      {confirm && (
        <ConfirmDialog title="Jalankan batch" confirmLabel="Jalankan" busy={batch.isPending}
          message={`${picked.length} draft migration akan dijalankan berurutan. Setiap migration memakai transaction sendiri; yang gagal tidak menghentikan migration berikutnya, tetapi batch berhenti bila database tidak terjangkau atau dibatalkan.`}
          onConfirm={run} onClose={() => setConfirm(false)} />
      )}
    </div>
  );
}
