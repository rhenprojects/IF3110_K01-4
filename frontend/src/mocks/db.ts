import type { AccessEntry, Connection, Group, User } from "../lib/types";

export type StoredUser = User & { password: string };
type Store = {
  users: StoredUser[];
  groups: Group[];
  connections: Connection[];
  access: Record<string, AccessEntry[]>; //[access] key = group id
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
  ],
  access: {
    g1: [{ connection_id: "c1", level: "edit" }, { connection_id: "c2", level: "view" }],
    g2: [{ connection_id: "c1", level: "view" }],
  },
});

//[store] simpan di sessionStorage agar tahan reload
export function load(): Store {
  try {
    const raw = sessionStorage.getItem(KEY);
    if (raw) return JSON.parse(raw) as Store;
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
