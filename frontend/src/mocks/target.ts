import type { ColumnInfo, ConstraintInfo, IndexInfo, SchemaInfo, TableInfo } from "../lib/types";

//[mock] isi "database target" palsu: struktur + baris. key rows = "schema.table"
export type Target = { schemas: SchemaInfo[]; rows: Record<string, unknown[][]> };
export const emptyTarget = (): Target => ({ schemas: [{ name: "public", tables: [] }], rows: {} });

const col = (name: string, type: string, o: Partial<ColumnInfo> = {}): ColumnInfo =>
  ({ name, type, nullable: false, default: null, is_primary_key: false, ...o });
const idCol = (t: string) => col("id", "integer", { default: `nextval('${t}_id_seq'::regclass)`, is_primary_key: true });
const pkC = (t: string, c = "id"): ConstraintInfo => ({ name: `${t}_pkey`, type: "primary_key", definition: `PRIMARY KEY (${c})` });
const pkI = (t: string, c = "id"): IndexInfo => ({
  name: `${t}_pkey`, is_unique: true, definition: `CREATE UNIQUE INDEX ${t}_pkey ON public.${t} USING btree (${c})`,
});
const tbl = (name: string, columns: ColumnInfo[], constraints: ConstraintInfo[] = [], indexes: IndexInfo[] = []): TableInfo =>
  ({ name, type: "table", columns, constraints: [pkC(name), ...constraints], indexes: [pkI(name), ...indexes] });

const ts = (d: number) => new Date(Date.UTC(2026, 8, 1 + (d % 50), 8 + (d % 10), (d * 7) % 60)).toISOString();

export function seedTargets(): Record<string, Target> {
  const names = ["Andi Pratama", "Budi Santoso", "Citra Lestari", "Dewi Anggraini", "Eko Wijaya", "Fajar Nugroho", "Gita Maharani", "Hendra Gunawan"];
  const customers = names.map((n, i) => [i + 1, n, i % 3 === 2 ? null : `${n.split(" ")[0].toLowerCase()}@contoh.id`, ts(i)]);
  const orders = Array.from({ length: 120 }, (_, i) => {
    const id = i + 1;
    return [id, 1 + (id % 8), Math.round((20 + ((id * 37) % 480)) * 100) / 100, id % 7 === 0 ? null : `Pesanan #${id}`, ts(id)];
  });
  const topCustomers = names.map((n, i) => [n, orders.filter((o) => o[1] === i + 1).length]);

  const toko: Target = {
    schemas: [{
      name: "public",
      tables: [
        tbl("customers", [
          idCol("customers"), col("name", "text"), col("email", "text", { nullable: true }),
          col("created_at", "timestamp with time zone", { default: "now()" }),
        ], [{ name: "customers_email_key", type: "unique", definition: "UNIQUE (email)" }],
        [{ name: "customers_email_key", is_unique: true, definition: "CREATE UNIQUE INDEX customers_email_key ON public.customers USING btree (email)" }]),
        tbl("orders", [
          idCol("orders"), col("customer_id", "integer"), col("total", "numeric(12,2)"),
          col("note", "text", { nullable: true }), col("created_at", "timestamp with time zone", { default: "now()" }),
        ], [
          { name: "orders_customer_fk", type: "foreign_key", definition: "FOREIGN KEY (customer_id) REFERENCES customers(id)" },
          { name: "orders_total_check", type: "check", definition: "CHECK ((total >= (0)::numeric))" },
        ], [{ name: "orders_customer_id_idx", is_unique: false, definition: "CREATE INDEX orders_customer_id_idx ON public.orders USING btree (customer_id)" }]),
        { name: "top_customers", type: "view", columns: [col("name", "text"), col("total_orders", "bigint")], constraints: [], indexes: [] },
      ],
    }],
    rows: { "public.customers": customers, "public.orders": orders, "public.top_customers": topCustomers },
  };

  const hr: Target = {
    schemas: [{
      name: "public",
      tables: [
        tbl("departments", [idCol("departments"), col("name", "text")]),
        tbl("employees", [
          idCol("employees"), col("full_name", "text"), col("department_id", "integer"), col("joined_on", "date"),
        ], [{ name: "employees_department_fk", type: "foreign_key", definition: "FOREIGN KEY (department_id) REFERENCES departments(id)" }]),
      ],
    }],
    rows: {
      "public.departments": [[1, "Engineering"], [2, "Finance"], [3, "People"], [4, "Operations"]],
      "public.employees": Array.from({ length: 15 }, (_, i) => [i + 1, `Karyawan ${i + 1}`, 1 + (i % 4), `202${i % 6}-0${1 + (i % 9)}-15`]),
    },
  };
  return { c1: toko, c2: hr };
}

//[mock] pemisah koma level atas (abaikan koma di dalam tanda kurung, mis. numeric(12,2))
function splitTop(s: string): string[] {
  const out: string[] = [];
  let depth = 0, cur = "";
  for (const ch of s) {
    if (ch === "(") depth++;
    if (ch === ")") depth--;
    if (ch === "," && depth === 0) { out.push(cur.trim()); cur = ""; } else cur += ch;
  }
  if (cur.trim()) out.push(cur.trim());
  return out;
}

const KNOWN = new Set(["create", "alter", "drop", "insert", "update", "delete", "select", "truncate", "comment", "grant", "revoke", "with", "set", "analyze"]);
const NAME = String.raw`(?:"?(\w+)"?\.)?"?(\w+)"?`;

//[mock] meniru transaction: bekerja di salinan, hanya dikembalikan jika SEMUA statement berhasil (throw = rollback).
//[mock] hanya CREATE TABLE, ALTER TABLE ... ADD COLUMN, dan DROP TABLE yang mengubah struktur; statement lain dianggap sukses.
export function applySql(t: Target, sql: string): Target {
  const next = structuredClone(t);
  const find = (sch: string, name: string) => {
    const s = next.schemas.find((x) => x.name === sch);
    if (!s) throw new Error(`schema "${sch}" does not exist`);
    return { s, t: s.tables.find((x) => x.name === name) };
  };
  for (const stmt of sql.replace(/--.*$/gm, "").split(";").map((x) => x.trim()).filter(Boolean)) {
    const first = stmt.split(/\s+/)[0];
    if (!KNOWN.has(first.toLowerCase())) throw new Error(`syntax error at or near "${first}"`);
    let m: RegExpMatchArray | null;

    if ((m = stmt.match(new RegExp(String.raw`^create\s+table\s+(if\s+not\s+exists\s+)?${NAME}\s*\(([\s\S]*)\)$`, "i")))) {
      const [, ine, sch = "public", name, body] = m;
      const { s, t: existing } = find(sch, name);
      if (existing) { if (ine) continue; throw new Error(`relation "${name}" already exists`); }
      const columns: ColumnInfo[] = [];
      for (const part of splitTop(body)) {
        if (/^(primary|foreign|unique|check|constraint)\b/i.test(part)) continue;
        const c = part.match(/^"?(\w+)"?\s+(\w+(?:\s*\([^)]*\))?)/);
        if (!c) throw new Error(`syntax error at or near "${part.split(/\s+/)[0]}"`);
        const isPk = /primary\s+key/i.test(part);
        const raw = c[2].toLowerCase();
        columns.push(col(c[1], raw === "serial" ? "integer" : raw === "bigserial" ? "bigint" : raw, {
          nullable: !isPk && !/not\s+null/i.test(part), is_primary_key: isPk, default: part.match(/default\s+(\S+)/i)?.[1] ?? null,
        }));
      }
      const pk = columns.find((c) => c.is_primary_key);
      s.tables.push({
        name, type: "table", columns,
        constraints: pk ? [pkC(name, pk.name)] : [], indexes: pk ? [pkI(name, pk.name)] : [],
      });
      next.rows[`${sch}.${name}`] = [];
    } else if ((m = stmt.match(new RegExp(String.raw`^alter\s+table\s+(?:if\s+exists\s+)?${NAME}\s+add\s+column\s+(if\s+not\s+exists\s+)?"?(\w+)"?\s+(\w+(?:\s*\([^)]*\))?)(.*)$`, "is")))) {
      const [, sch = "public", name, ine, cname, ctype, rest] = m;
      const { t: tb } = find(sch, name);
      if (!tb) throw new Error(`relation "${name}" does not exist`);
      if (tb.columns.some((c) => c.name === cname)) { if (ine) continue; throw new Error(`column "${cname}" of relation "${name}" already exists`); }
      tb.columns.push(col(cname, ctype.toLowerCase(), { nullable: !/not\s+null/i.test(rest), default: rest.match(/default\s+(\S+)/i)?.[1] ?? null }));
      next.rows[`${sch}.${name}`]?.forEach((r) => r.push(null));
    } else if ((m = stmt.match(new RegExp(String.raw`^drop\s+table\s+(if\s+exists\s+)?${NAME}`, "i")))) {
      const [, ie, sch = "public", name] = m;
      const { s, t: tb } = find(sch, name);
      if (!tb) { if (ie) continue; throw new Error(`table "${name}" does not exist`); }
      s.tables = s.tables.filter((x) => x !== tb);
      delete next.rows[`${sch}.${name}`];
    }
  }
  return next;
}
