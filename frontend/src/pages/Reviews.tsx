import { useState } from "react";
import { Link } from "react-router-dom";
import { useDecideChangeRequest, usePendingReviews } from "../features/databases/hooks";
import { Banner, BannerStat } from "../components/Banner";
import { InboxIcon } from "../components/icons";
import { Button, EmptyState, ErrorBox, Field, Loading, Modal, Table, generalError, inputCls, td } from "../components/ui";
import { useToast } from "../components/toast";
import { fmtDate } from "../lib/format";
import type { ChangeRequest } from "../lib/types";
import { KIND_LABEL, StateError } from "./databases/shared";

//[review] inbox Team Leader: pengajuan di semua database yang menunggu keputusan user ini
export default function Reviews() {
  const q = usePendingReviews();
  const decide = useDecideChangeRequest();
  const toast = useToast();
  const [target, setTarget] = useState<{ cr: ChangeRequest; decision: "approve" | "reject" }>();
  const [comment, setComment] = useState("");

  function open(cr: ChangeRequest, decision: "approve" | "reject") {
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

  return (
    <div>
      <Banner title="Review" icon={<InboxIcon />}
        caption="Pengajuan di database yang diakses group kamu sebagai Team Leader. Migration baru dijalankan setelah semua group menyetujui; satu penolakan membatalkan pengajuan.">
        {q.data && <BannerStat value={q.data.length} label="menunggu keputusanmu" />}
      </Banner>
      {q.isLoading && <Loading />}
      {q.error && <StateError error={q.error} onRetry={() => q.refetch()} />}
      {q.data?.length === 0 && <EmptyState title="Tidak ada pengajuan" hint="Pengajuan yang butuh persetujuanmu akan muncul di sini." />}
      {q.data && q.data.length > 0 && (
        <Table columns={["Database", "Migration", "Jenis", "Diajukan oleh", "Persetujuan", ""]}>
          {q.data.map((c) => (
            <tr key={c.id} className="align-top">
              <td className={td}>{c.connection_name}</td>
              <td className={td}>
                <Link to={`/databases/${c.connection_id}/migrations/${c.migration_id}`} className="underline-offset-2 hover:underline">{c.migration_name}</Link>
              </td>
              <td className={td}>{KIND_LABEL[c.kind]}</td>
              <td className={td}>{c.requested_by_name}<span className="block text-xs text-slate-400">{fmtDate(c.created_at)}</span></td>
              <td className={td}>{c.reviewers.filter((r) => r.decision === "approve").length}/{c.reviewers.length} group</td>
              <td className={`${td} whitespace-nowrap`}>
                <div className="flex gap-2">
                  <Button variant="primary" onClick={() => open(c, "approve")}>Setujui</Button>
                  <Button variant="danger" onClick={() => open(c, "reject")}>Tolak</Button>
                </div>
              </td>
            </tr>
          ))}
        </Table>
      )}

      {target && (
        <Modal title={`${target.decision === "approve" ? "Setujui" : "Tolak"} ${KIND_LABEL[target.cr.kind]}: ${target.cr.migration_name}`} onClose={() => setTarget(undefined)}>
          <div className="space-y-4">
            <Field label="Komentar (opsional)">
              <textarea className={inputCls} rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />
            </Field>
            <ErrorBox message={generalError(decide.error, [])} />
            <div className="flex justify-end gap-2">
              <Button onClick={() => setTarget(undefined)}>Batal</Button>
              <Button variant={target.decision === "approve" ? "primary" : "danger"} onClick={submit} disabled={decide.isPending}>
                {target.decision === "approve" ? "Setujui" : "Tolak"}
              </Button>
            </div>
          </div>
        </Modal>
      )}
    </div>
  );
}