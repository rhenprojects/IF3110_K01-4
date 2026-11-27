import { useState } from "react";
import { Link } from "react-router-dom";
import { useDecideChangeRequest, usePendingReviews } from "../features/databases/hooks";
import { Banner, BannerStat } from "../components/Banner";
import { DataList, type Column } from "../components/DataList";
import { InboxIcon } from "../components/icons";
import { Button, EmptyState, ErrorBox, Field, Loading, Modal, generalError, inputCls } from "../components/ui";
import { useToast } from "../components/toast";
import { fmtDate } from "../lib/format";
import type { ChangeRequest, Reviewer } from "../lib/types";
import { KIND_LABEL, KindBadge, StateError } from "./databases/shared";

type Decision = "approve" | "reject";

const COPY: Record<Decision, { title: string; cta: string; note: string; hint?: string }> = {
  approve: {
    title: "Setujui pengajuan",
    cta: "Setujui",
    note: "Migration baru dijalankan setelah Team Leader semua group yang punya akses menyetujui.",
  },
  reject: {
    title: "Tolak pengajuan",
    cta: "Tolak",
    note: "Satu penolakan membatalkan pengajuan ini. Pengaju harus membuat pengajuan baru.",
    hint: "Jelaskan alasannya agar pengaju bisa memperbaiki.",
  },
};

//[review] progres persetujuan: berapa group yang sudah menyetujui dari seluruh group yang punya akses
function ApprovalProgress({ reviewers }: { reviewers: Reviewer[] }) {
  const done = reviewers.filter((r) => r.decision === "approve").length;
  const total = reviewers.length;
  return (
    <div className="min-w-32">
      <span>{done} dari {total} group setuju</span>
      <div aria-hidden className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-slate-200">
        <div className="h-full rounded-full bg-primary" style={{ width: `${total > 0 ? (done / total) * 100 : 0}%` }} />
      </div>
    </div>
  );
}

//[review] inbox Team Leader: pengajuan di semua database yang menunggu keputusan user ini
export default function Reviews() {
  const q = usePendingReviews();
  const decide = useDecideChangeRequest();
  const toast = useToast();
  const [target, setTarget] = useState<{ cr: ChangeRequest; decision: Decision }>();
  const [comment, setComment] = useState("");

  function open(cr: ChangeRequest, decision: Decision) {
    decide.reset();
    setComment("");
    setTarget({ cr, decision });
  }

  async function submit() {
    if (!target) return;
    try {
      await decide.mutateAsync({ dbId: target.cr.connection_id, rid: target.cr.id, decision: target.decision, comment: comment.trim() });
      toast.success(target.decision === "approve" ? "Pengajuan disetujui" : "Pengajuan ditolak");
      setTarget(undefined);
    } catch {
      //[error] ditampilkan lewat decide.error
    }
  }

  const columns: Column<ChangeRequest>[] = [
    {
      header: "Migration", role: "title",
      cell: (c) => <Link to={`/databases/${c.connection_id}/migrations/${c.migration_id}`} className="text-primary underline-offset-2 hover:underline">{c.migration_name}</Link>,
    },
    { header: "Jenis", role: "status", cell: (c) => <KindBadge kind={c.kind} /> },
    { header: "Database", cell: (c) => c.connection_name },
    {
      header: "Diajukan oleh",
      cell: (c) => <>{c.requested_by_name}<span className="block text-xs text-slate-500">{fmtDate(c.created_at)}</span></>,
    },
    { header: "Persetujuan", cell: (c) => <ApprovalProgress reviewers={c.reviewers} /> },
    {
      header: "", role: "actions", nowrap: true,
      cell: (c) => (
        <>
          <Button variant="primary" onClick={() => open(c, "approve")}>Setujui</Button>
          <Button variant="danger" onClick={() => open(c, "reject")}>Tolak</Button>
        </>
      ),
    },
  ];

  const copy = target && COPY[target.decision];
  return (
    <div>
      <Banner title="Review" icon={<InboxIcon />}
        caption="Pengajuan migration yang menunggu persetujuanmu sebagai Team Leader. Migration berjalan setelah semua group menyetujui; satu penolakan membatalkan pengajuan.">
        {q.data && <BannerStat value={q.data.length} label="menunggu keputusanmu" />}
      </Banner>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && (
        <EmptyState title="Tidak ada pengajuan yang menunggu keputusanmu"
          hint="Pengajuan di database yang diakses group-mu sebagai Team Leader akan muncul di sini." />
      )}
      {q.data && q.data.length > 0 && (
        <DataList label="Pengajuan menunggu keputusan" rows={q.data} columns={columns} rowKey={(c) => c.id} />
      )}

      {target && copy && (
        <Modal title={copy.title} onClose={() => setTarget(undefined)}>
          <div className="space-y-4">
            <div className="rounded-xl bg-slate-100 p-3 text-sm">
              <p className="font-medium">{target.cr.migration_name}</p>
              <p className="text-slate-600">{KIND_LABEL[target.cr.kind]} di {target.cr.connection_name}</p>
            </div>
            <p className="text-sm text-slate-600">{copy.note}</p>
            <Field label="Komentar (opsional)" hint={copy.hint}>
              <textarea className={inputCls} rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />
            </Field>
            <ErrorBox message={generalError(decide.error, [])} />
            <div className="flex flex-wrap justify-end gap-2">
              <Button onClick={() => setTarget(undefined)}>Batal</Button>
              <Button variant={target.decision === "approve" ? "primary" : "danger"} onClick={submit} disabled={decide.isPending}>
                {copy.cta}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}
