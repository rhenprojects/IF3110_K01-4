import type { AccessEntry, Connection, Group, Migration, User } from "../lib/types";
import { seedTargets, type Target } from "./target";

export type StoredUser = User & { password: string };
export type Store = {
  users: StoredUser[];
  groups: Group[];
  connections: Connection[];
  access: Record<string, AccessEntry[]>; //[access] key = group id
  targets: Record<string, Target>; //[target] key = connection id: isi database target palsu
  migrations: Record<string, Migration[]>; //[migration] key = connection id, urut dari yang terbaru dibuat
};

const KEY = "mock_db";
const SESSION = "mock_session";
const T = "2026-10-24T00:00:00Z";

const seed = (): Store => ({
  users: [
    { id: "u1", username: "admin", name: "Admin Demo", is_admin: true, password: "password", created_at: T, updated_at: T },
    { id: "u2", username: "budi", name: "Budi", is_admin: false, password: "password", created_at: T, updated_at: T },
    { id: "u3", username: "sinta", name: "Sinta", is_admin: false, password: "password", created_at: T, updated_at: T },
  ],
  groups: [
    { id: "g1", name: "Tim Backend", description: "Developer backend", member_ids: ["u2"], created_at: T, updated_at: T },
    { id: "g2", name: "Tim QA", description: "", member_ids: ["u3"], created_at: T, updated_at: T },
  ],
  connections: [
    { id: "c1", name: "toko", host: "localhost", port: 5432, db_name: "toko", username: "postgres", ssl_mode: "prefer", created_by: "u1", created_at: T, updated_at: T },
    { id: "c2", name: "hr", host: "localhost", port: 5432, db_name: "hr", username: "postgres", ssl_mode: "prefer", created_by: "u1", created_at: T, updated_at: T },
    //[mock] host "down" = database target tidak terjangkau (502) untuk demo penanganan error
    { id: "c3", name: "analytics", host: "down", port: 5432, db_name: "analytics", username: "postgres", ssl_mode: "prefer", created_by: "u1", created_at: T, updated_at: T },
  ],
  access: {
    g1: [{ connection_id: "c1", level: "edit" }, { connection_id: "c2", level: "view" }, { connection_id: "c3", level: "edit" }],
    g2: [{ connection_id: "c1", level: "view" }],
  },
  targets: seedTargets(),
  migrations: seedMigrations(),
});

//[mock] riwayat awal agar halaman migration tidak kosong saat demo (urut terbaru dibuat dulu)
function seedMigrations(): Record<string, Migration[]> {
  const m = (id: string, conn: string, o: Partial<Migration> & Pick<Migration, "name" | "sql" | "status">): Migration => ({
    id, connection_id: conn, version: null, error: "", created_by: "u2", created_by_name: "Budi", executed_by: null,
    executed_by_name: null, created_at: T, updated_at: T, executed_at: null, duration_ms: null, ...o,
  });
  const ran = { executed_by: "u2", executed_by_name: "Budi" };
  return {
    c1: [
      m("m4", "c1", { name: "tambah tabel produk", status: "draft", created_at: "2026-10-24T08:00:00Z",
        sql: "CREATE TABLE products (\n  id serial PRIMARY KEY,\n  name text NOT NULL,\n  price numeric(12,2) NOT NULL\n);" }),
      m("m3", "c1", { name: "hapus tabel lama", status: "failed", created_at: "2026-10-23T09:00:00Z", ...ran,
        executed_at: "2026-10-23T09:05:00Z", duration_ms: 7, sql: "DROP TABLE tabel_lama;",
        error: 'table "tabel_lama" does not exist' }),
      m("m2", "c1", { name: "tambah kolom note di orders", status: "success", version: 2, created_at: "2026-10-22T09:00:00Z", ...ran,
        executed_at: "2026-10-22T09:02:00Z", duration_ms: 42, sql: "ALTER TABLE orders ADD COLUMN note text;" }),
      m("m1", "c1", { name: "tabel awal customers dan orders", status: "success", version: 1, created_at: "2026-10-20T09:00:00Z",
        created_by: "u1", created_by_name: "Admin Demo", executed_by: "u1", executed_by_name: "Admin Demo",
        executed_at: "2026-10-20T09:03:00Z", duration_ms: 118, sql: "CREATE TABLE customers (id serial PRIMARY KEY, name text NOT NULL);\nCREATE TABLE orders (id serial PRIMARY KEY, customer_id integer NOT NULL);" }),
    ],
    c2: [
      m("m5", "c2", { name: "tabel departments", status: "success", version: 1, created_by: "u1", created_by_name: "Admin Demo",
        executed_by: "u1", executed_by_name: "Admin Demo", created_at: "2026-10-21T09:00:00Z", executed_at: "2026-10-21T09:01:00Z",
        duration_ms: 35, sql: "CREATE TABLE departments (id serial PRIMARY KEY, name text NOT NULL);" }),
    ],
  };
}

//[store] simpan di sessionStorage agar tahan reload
export function load(): Store {
  try {
    const raw = sessionStorage.getItem(KEY);
    //[store] data lama di sessionStorage mungkin belum punya field baru, isi dari seed
    if (raw) return { ...seed(), ...(JSON.parse(raw) as Partial<Store>) };
  } catch {
    //[store] storage tidak tersedia, pakai seed
  }
  return seed();
}

export function save(s: Store) {
  try {
    sessionStorage.setItem(KEY, JSON.stringify(s));
  } catch {
    //[store] abaikan jika gagal
  }
}

export const sessionUserId = () => sessionStorage.getItem(SESSION);
export const setSession = (id: string | null) =>
  id ? sessionStorage.setItem(SESSION, id) : sessionStorage.removeItem(SESSION);

export const publicUser = ({ password: _p, ...u }: StoredUser): User => u;
