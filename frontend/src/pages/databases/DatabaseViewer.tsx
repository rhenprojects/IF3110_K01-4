import { useMemo, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useRows, useSchema } from "../../features/databases/hooks";
import { Badge, Button, EmptyState, Loading, Table, td } from "../../components/ui";
import type { TableInfo } from "../../lib/types";
import { StateError, useDb } from "./shared";

type Picked = { schema: string; table: TableInfo };

function Structure({ t }: { t: TableInfo }) {
  const h = "mb-2 mt-5 text-sm font-medium first:mt-0";
  const code = "font-mono text-xs text-slate-600";
  return (
    <div>
      <h3 className={h}>Kolom</h3>
      <Table columns={["Kolom", "Tipe", "Nullable", "Default", "Key"]}>
        {t.columns.map((c) => (
          <tr key={c.name}>
            <td className={`${td} font-medium`}>{c.name}</td>
            <td className={`${td} font-mono text-xs`}>{c.type}</td>
            <td className={td}>{c.nullable ? "ya" : "tidak"}</td>
            <td className={`${td} ${code}`}>{c.default ?? "—"}</td>
            <td className={td}>{c.is_primary_key && <Badge tone="dark">PK</Badge>}</td>
          </tr>
        ))}
      </Table>
      <h3 className={h}>Constraint</h3>
      {t.constraints.length === 0 ? <p className="text-sm text-slate-500">Tidak ada.</p> : (
        <Table columns={["Nama", "Tipe", "Definisi"]}>
          {t.constraints.map((c) => (
            <tr key={c.name}><td className={td}>{c.name}</td><td className={td}>{c.type}</td><td className={`${td} ${code}`}>{c.definition}</td></tr>
          ))}
        </Table>
      )}
      <h3 className={h}>Index</h3>
      {t.indexes.length === 0 ? <p className="text-sm text-slate-500">Tidak ada.</p> : (
        <Table columns={["Nama", "Unik", "Definisi"]}>
          {t.indexes.map((i) => (
            <tr key={i.name}><td className={td}>{i.name}</td><td className={td}>{i.is_unique ? "ya" : "tidak"}</td><td className={`${td} ${code}`}>{i.definition}</td></tr>
          ))}
        </Table>
      )}
    </div>
  );
}

function Cell({ v }: { v: unknown }) {
  if (v === null || v === undefined) return <span className="italic text-slate-400">NULL</span>;
  const s = typeof v === "object" ? JSON.stringify(v) : String(v);
  return <span className="block max-w-xs truncate" title={s}>{s}</span>;
}

//[data] di-key per table (lihat pemakaian) sehingga halaman kembali ke awal saat pindah table
function DataGrid({ dbId, schema, table }: { dbId: string; schema: string; table: string }) {
  const [limit, setLimit] = useState(50);
  const [offset, setOffset] = useState(0);
  const q = useRows(dbId, schema, table, limit, offset);
  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  const d = q.data;
  const from = d.total === 0 ? 0 : d.offset + 1;
  const to = d.offset + d.rows.length;
  return (
    <div>
      <div className="max-h-[28rem] overflow-auto rounded border bg-white">
        <table className="w-full">
          <thead className="sticky top-0 bg-slate-50">
            <tr>
              {d.columns.map((c) => (
                <th key={c.name} className="whitespace-nowrap px-3 py-2 text-left text-xs font-medium text-slate-700">
                  {c.name} <span className="font-normal text-slate-400">{c.type}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody className="divide-y">
            {d.rows.length === 0 && (
              <tr><td colSpan={d.columns.length} className="px-3 py-6 text-center text-sm text-slate-500">Table kosong.</td></tr>
            )}
            {d.rows.map((r, i) => (
              <tr key={d.offset + i}>{r.map((v, j) => <td key={j} className={td}><Cell v={v} /></td>)}</tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="mt-3 flex flex-wrap items-center justify-between gap-3 text-sm">
        <span className="text-slate-500">{from}–{to} dari {d.total} baris</span>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-1 text-slate-500">
            Per halaman
            <select className="rounded border border-slate-300 px-2 py-1 text-slate-900" value={limit}
              onChange={(e) => { setLimit(Number(e.target.value)); setOffset(0); }}>
              {[25, 50, 100, 200].map((n) => <option key={n}>{n}</option>)}
            </select>
          </label>
          <Button disabled={offset === 0} onClick={() => setOffset(Math.max(0, offset - limit))}>Sebelumnya</Button>
          <Button disabled={offset + limit >= d.total} onClick={() => setOffset(offset + limit)}>Berikutnya</Button>
        </div>
      </div>
    </div>
  );
}

export default function DatabaseViewer() {
  const db = useDb();
  const q = useSchema(db.id);
  const [sp, setSp] = useSearchParams();
  const all = useMemo<Picked[]>(() => q.data?.schemas.flatMap((s) => s.tables.map((table) => ({ schema: s.name, table }))) ?? [], [q.data]);

  if (q.isLoading) return <Loading />;
  if (q.error || !q.data) return <StateError error={q.error} onRetry={() => q.refetch()} />;
  if (all.length === 0) return <EmptyState title="Database ini belum punya table" hint="Table muncul di sini setelah dibuat lewat migration." />;

  //[url] pilihan table + tab disimpan di query string agar tahan reload dan bisa dibagikan
  const sel = all.find((x) => x.schema === sp.get("schema") && x.table.name === sp.get("table")) ?? all[0];
  const view = sp.get("view") === "data" ? "data" : "struktur";
  const go = (schema: string, table: string, v: string) => setSp({ schema, table, view: v }, { replace: true });

  return (
    <div className="grid gap-6 md:grid-cols-[16rem_1fr]">
      <aside aria-label="Schema" className="space-y-3 text-sm">
        {q.data.schemas.map((s) => (
          <details key={s.name} open>
            <summary className="cursor-pointer font-medium">{s.name} <span className="text-slate-400">({s.tables.length})</span></summary>
            <ul className="mt-1 space-y-0.5 border-l pl-2">
              {s.tables.map((t) => {
                const active = sel.schema === s.name && sel.table.name === t.name;
                return (
                  <li key={t.name}>
                    <button type="button" aria-current={active ? "true" : undefined} onClick={() => go(s.name, t.name, view)}
                      className={`flex w-full items-center justify-between rounded px-2 py-1 text-left ${active ? "bg-slate-900 text-white" : "hover:bg-slate-100"}`}>
                      <span className="truncate">{t.name}</span>
                      {t.type === "view" && <span className="text-xs opacity-70">view</span>}
                    </button>
                  </li>
                );
              })}
            </ul>
          </details>
        ))}
      </aside>
      <section className="min-w-0">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h2 className="text-lg font-semibold">
            <span className="text-slate-400">{sel.schema}.</span>{sel.table.name}{" "}
            <Badge>{sel.table.type}</Badge>
          </h2>
          <div role="tablist" className="flex gap-1">
            {(["struktur", "data"] as const).map((v) => (
              <Button key={v} variant={view === v ? "primary" : "ghost"} role="tab" aria-selected={view === v}
                onClick={() => go(sel.schema, sel.table.name, v)}>{v === "struktur" ? "Struktur" : "Data"}</Button>
            ))}
          </div>
        </div>
        {view === "struktur"
          ? <Structure t={sel.table} />
          : <DataGrid key={`${sel.schema}.${sel.table.name}`} dbId={db.id} schema={sel.schema} table={sel.table.name} />}
      </section>
    </div>
  );
}
