import type { ReactNode } from "react";
import { td, th } from "./ui";

//[list] satu definisi kolom, dua tampilan: tabel di layar lebar (lg ke atas), list card di layar kecil
export type Column<T> = {
  header: string;
  cell: (row: T) => ReactNode;
  //[list] di card: title = judul, status = badge di kanan judul, actions = tombol di dasar card; tanpa role = pasangan label dan nilai
  role?: "title" | "status" | "actions";
  //[list] isi panjang (mis. pesan error) memakai lebar penuh di card
  wide?: boolean;
  nowrap?: boolean;
};

//[list] nilai kosong tidak ditampilkan di card, supaya label tanpa isi tidak muncul
const empty = (v: ReactNode) => v == null || v === false || v === "";

export function DataList<T>({ rows, columns, rowKey, label }: {
  rows: T[]; columns: Column<T>[]; rowKey: (row: T) => string; label?: string;
}) {
  const title = columns.find((c) => c.role === "title");
  const status = columns.find((c) => c.role === "status");
  const actions = columns.find((c) => c.role === "actions");
  const fields = columns.filter((c) => !c.role);
  return (
    <>
      <div className="hidden overflow-x-auto rounded-2xl border bg-white shadow-sm lg:block">
        <table aria-label={label} className="w-full">
          <thead className="bg-slate-50">
            <tr>{columns.map((c, i) => <th key={i} className={th}>{c.header}</th>)}</tr>
          </thead>
          <tbody className="divide-y">
            {rows.map((r) => (
              <tr key={rowKey(r)} className="align-top">
                {columns.map((c, i) => (
                  <td key={i} className={`${td} ${c.nowrap ? "whitespace-nowrap" : ""}`}>
                    {c.role === "actions" ? <div className="flex justify-end gap-2">{c.cell(r)}</div> : c.cell(r)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <ul aria-label={label} className="grid gap-3 md:grid-cols-2 lg:hidden">
        {rows.map((r) => (
          <li key={rowKey(r)} className="flex flex-col gap-3 rounded-2xl border bg-white p-4 shadow-sm">
            {(title || status) && (
              <div className="flex items-start justify-between gap-3">
                <div className="min-w-0 break-words font-medium">{title?.cell(r)}</div>
                {status && <div className="shrink-0">{status.cell(r)}</div>}
              </div>
            )}
            {fields.length > 0 && (
              <dl className="grid grid-cols-2 gap-x-4 gap-y-2.5 text-sm">
                {fields.map((c, i) => {
                  const v = c.cell(r);
                  if (empty(v)) return null;
                  return (
                    <div key={i} className={`min-w-0 ${c.wide ? "col-span-2" : ""}`}>
                      <dt className="text-xs uppercase tracking-wide text-slate-500">{c.header}</dt>
                      <dd className="break-words">{v}</dd>
                    </div>
                  );
                })}
              </dl>
            )}
            {actions && <div className="mt-auto flex flex-wrap gap-2 border-t pt-3">{actions.cell(r)}</div>}
          </li>
        ))}
      </ul>
    </>
  );
}
