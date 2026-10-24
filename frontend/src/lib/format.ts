export const fmtDate = (iso?: string | null) =>
  iso ? new Date(iso).toLocaleString("id-ID", { dateStyle: "medium", timeStyle: "short" }) : "—";

export const fmtDuration = (ms?: number | null) =>
  ms == null ? "—" : ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(2)} s`;

//[sql] peringatan UX saja, backend tidak membatasi isi SQL
export const isDestructive = (sql: string) => /\b(drop|truncate|delete\s+from)\b/i.test(sql);
