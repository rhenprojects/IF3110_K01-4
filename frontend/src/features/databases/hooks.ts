import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type {
  BatchItem, ChangeRequest, DatabaseItem, JobKind, Execution, Migration, MigrationBatch, MigrationExecution, MigrationInput, MigrationJob, RowsResponse, SchemaResponse,
} from "../../lib/types";

//[query] semua key diawali "db" supaya ikut dibuang saat logout / 401 (lihat dropCache di auth.tsx).
//[query] retry dimatikan: 403/404/502 bukan error sementara, user cukup menekan "Coba lagi".
const base = (id: string) => `/databases/${encodeURIComponent(id)}`;
const enc = encodeURIComponent;

export const useDatabases = () =>
  useQuery({
    queryKey: ["db", "list"], retry: false,
    queryFn: () => api<{ databases: DatabaseItem[] }>("/databases").then((r) => r.databases),
  });

export const useDatabase = (id: string) =>
  useQuery({ queryKey: ["db", id, "info"], retry: false, queryFn: () => api<DatabaseItem>(base(id)) });

export const useSchema = (id: string) =>
  useQuery({ queryKey: ["db", id, "schema"], retry: false, queryFn: () => api<SchemaResponse>(`${base(id)}/schema`) });

export const useRows = (id: string, schema: string, table: string, limit: number, offset: number) =>
  useQuery({
    queryKey: ["db", id, "rows", schema, table, limit, offset],
    retry: false,
    enabled: !!schema && !!table,
    placeholderData: keepPreviousData, //[ux] halaman sebelumnya tetap tampil saat memuat halaman berikutnya
    queryFn: () => api<RowsResponse>(`${base(id)}/tables/${enc(schema)}/${enc(table)}/rows?limit=${limit}&offset=${offset}`),
  });

export const useMigrations = (id: string) =>
  useQuery({
    queryKey: ["db", id, "migrations"], retry: false,
    queryFn: () => api<{ migrations: Migration[] }>(`${base(id)}/migrations`).then((r) => r.migrations),
  });

export const useMigration = (id: string, mid?: string) =>
  useQuery({
    queryKey: ["db", id, "migrations", mid], retry: false, enabled: !!mid,
    queryFn: () => api<Migration>(`${base(id)}/migrations/${enc(mid!)}`),
    refetchInterval: (q) => (q.state.data?.batch_status === "running" ? 1500 : false),
  });

export const useCancelBatchMigration = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mid: string) => api(`${base(id)}/migrations/${enc(mid)}/batch/cancel`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] === "migrations" || q.queryKey[2] === "change-requests" }),
  });
};

export const useRevertBatchMigration = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mid: string) => api(`${base(id)}/migrations/${enc(mid)}/batch/revert`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] === "migrations" || q.queryKey[2] === "change-requests" }),
  });
};

export const useResumeBatchMigration = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mid: string) => api(`${base(id)}/migrations/${enc(mid)}/batch/resume`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] === "migrations" || q.queryKey[2] === "change-requests" }),
  });
};

export const useSaveMigration = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ mid, input }: { mid?: string; input: MigrationInput }) =>
      mid
        ? api<Migration>(`${base(id)}/migrations/${enc(mid)}`, { method: "PUT", body: JSON.stringify(input) })
        : api<Migration>(`${base(id)}/migrations`, { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["db", id, "migrations"] }),
  });
};

//[review] pengajuan perubahan; setelah semua Team Leader setuju, server memasukkannya ke queue otomatis.
export const useChangeRequests = (id: string) =>
  useQuery({
    queryKey: ["db", id, "change-requests"], retry: false,
    queryFn: () => api<{ change_requests: ChangeRequest[] }>(`${base(id)}/change-requests`).then((r) => r.change_requests),
    refetchInterval: (q) => (q.state.data?.some((c) => c.status === "pending_review") ? 3000 : false),
  });

export const useSubmitChangeRequest = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ mid, kind }: { mid: string; kind: JobKind }) =>
      api<ChangeRequest>(`${base(id)}/migrations/${enc(mid)}/change-requests`, { method: "POST", body: JSON.stringify({ kind }) }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] === "change-requests" || q.queryKey[2] === "migrations" }),
  });
};

//[review] mengulang masuk queue untuk pengajuan yang sudah disetujui tetapi belum punya job
export const useDispatchChangeRequest = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (rid: string) => api<ChangeRequest>(`${base(id)}/change-requests/${enc(rid)}/dispatch`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] !== "rows" && q.queryKey[2] !== "schema" }),
  });
};

//[review] inbox Team Leader lintas database: permintaan yang menunggu keputusan user ini
export const usePendingReviews = () =>
  useQuery({
    queryKey: ["db", "pending-reviews"], retry: false,
    queryFn: () => api<{ change_requests: ChangeRequest[] }>("/change-requests/pending").then((r) => r.change_requests),
  });

export const useDecideChangeRequest = () => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ dbId, rid, decision, comment }: { dbId: string; rid: string; decision: "approve" | "reject"; comment: string }) =>
      api<ChangeRequest>(`${base(dbId)}/change-requests/${enc(rid)}/${decision}`, { method: "POST", body: JSON.stringify({ comment }) }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db"] }),
  });
};

//[history] seluruh attempt eksekusi database ini; hideRollback menyembunyikan pasangan eksekusi yang dibatalkan dan rollback-nya
export const useHistory = (id: string, hideRollback: boolean) =>
  useQuery({
    queryKey: ["db", id, "history", hideRollback], retry: false,
    queryFn: () => api<{ executions: MigrationExecution[] }>(`${base(id)}/history${hideRollback ? "?hide_rollback=true" : ""}`).then((r) => r.executions),
  });

//[queue] polling hanya selama masih ada job yang belum selesai
const pending = (status?: string) => status === "queued" || status === "running" || status === "uncertain";

export const useJobs = (id: string) =>
  useQuery({
    queryKey: ["db", id, "jobs"], retry: false,
    queryFn: () => api<{ jobs: MigrationJob[] }>(`${base(id)}/jobs`).then((r) => r.jobs),
    refetchInterval: (q) => (q.state.data?.some((j) => pending(j.status)) ? 1500 : false),
  });

//[queue] saat job selesai, cache migration/schema/rows di-invalidate karena isinya bisa berubah
export const useJob = (id: string, jid?: string) => {
  const qc = useQueryClient();
  return useQuery({
    queryKey: ["db", id, "jobs", jid], retry: false, enabled: !!jid,
    queryFn: async () => {
      const j = await api<MigrationJob>(`${base(id)}/jobs/${enc(jid!)}`);
      if (!pending(j.status)) {
        qc.invalidateQueries({ queryKey: ["db", id], predicate: (q) => q.queryKey[2] !== "jobs" });
      }
      return j;
    },
    refetchInterval: (q) => (pending(q.state.data?.status) ? 1000 : false),
  });
};

export const useMigrationExecutions = (id: string, mid?: string) =>
  useQuery({
    queryKey: ["db", id, "migrations", mid, "executions"], retry: false, enabled: !!mid,
    queryFn: () => api<{ executions: MigrationExecution[] }>(`${base(id)}/migrations/${enc(mid!)}/executions`).then((r) => r.executions),
  });

//[batch] progress hidup: polling hanya selama batch masih queued/running
const live = (status?: string) => status === "queued" || status === "running";

export const useBatches = (id: string) =>
  useQuery({
    queryKey: ["db", id, "batches"], retry: false,
    queryFn: () => api<{ batches: MigrationBatch[] }>(`${base(id)}/batches`).then((r) => r.batches),
    refetchInterval: (q) => (q.state.data?.some((b) => live(b.status)) ? 1500 : false),
  });

export const useBatch = (id: string, bid?: string) =>
  useQuery({
    queryKey: ["db", id, "batches", bid], retry: false, enabled: !!bid,
    queryFn: () => api<MigrationBatch>(`${base(id)}/batches/${enc(bid!)}`),
    refetchInterval: (q) => (live(q.state.data?.status) ? 1500 : false),
  });

export const useBatchItems = (id: string, bid?: string) =>
  useQuery({
    queryKey: ["db", id, "batches", bid, "items"], retry: false, enabled: !!bid,
    queryFn: () => api<{ items: BatchItem[]; next_after_id: number }>(`${base(id)}/batches/${enc(bid!)}/items`).then((r) => r.items),
    refetchInterval: (q) => (q.state.data?.some((i) => live(i.status)) ? 1500 : false),
  });

export const useExecutions = (id: string, filter: { batch_id?: string; migration_id?: string } = {}) => {
  const qs = new URLSearchParams();
  if (filter.batch_id) qs.set("batch_id", filter.batch_id);
  if (filter.migration_id) qs.set("migration_id", filter.migration_id);
  return useQuery({
    queryKey: ["db", id, "executions", filter.batch_id ?? "", filter.migration_id ?? ""], retry: false,
    queryFn: () => api<{ items: Execution[]; next_after_id: number }>(`${base(id)}/executions?${qs}`).then((r) => r.items),
  });
};

export const useCreateBatch = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (migrationIDs: string[]) =>
      api<MigrationBatch>(`${base(id)}/batches`, { method: "POST", body: JSON.stringify({ migration_ids: migrationIDs }) }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["db", id, "batches"] });
      qc.invalidateQueries({ queryKey: ["db", id, "migrations"] });
    },
  });
};

export const useCancelBatch = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (bid: string) => api<{ status: string }>(`${base(id)}/batches/${enc(bid)}/cancel`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id, "batches"] }),
  });
};

export const useRetryBatch = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (bid: string) => api<MigrationBatch>(`${base(id)}/batches/${enc(bid)}/retry`, { method: "POST" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["db", id, "batches"] }),
  });
};
