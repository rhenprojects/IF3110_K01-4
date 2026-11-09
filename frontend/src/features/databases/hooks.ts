import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type { BatchItem, DatabaseItem, Execution, Migration, MigrationBatch, MigrationInput, RowsResponse, SchemaResponse } from "../../lib/types";

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
  });

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

//[execute] berhasil atau gagal (rollback) sama-sama 200; yang membedakan field status.
//[execute] seluruh cache database ini di-invalidate karena schema dan isi tabel bisa berubah.
export const useExecuteMigration = (id: string) => {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (mid: string) => api<Migration>(`${base(id)}/migrations/${enc(mid)}/execute`, { method: "POST" }),
    onSettled: () => qc.invalidateQueries({ queryKey: ["db", id] }),
  });
};

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
