import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../../lib/api";
import type { DatabaseItem, Migration, MigrationInput, RowsResponse, SchemaResponse } from "../../lib/types";

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
