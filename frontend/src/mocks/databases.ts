import { http, HttpResponse } from "msw";
import type { AccessLevel, Connection, Migration, MigrationInput } from "../lib/types";
import { currentUser, err } from "./auth";
import { load, save, type Store, type StoredUser } from "./db";
import { applySql, emptyTarget } from "./target";

const P = "/api/v1/databases";
const now = () => new Date().toISOString();

//[akses] level efektif = tertinggi dari semua group user (contract 3.4); admin TIDAK punya jalan pintas (contract 3.5)
function levelFor(s: Store, userId: string, connId: string): AccessLevel | null {
  let best: AccessLevel | null = null;
  for (const g of s.groups) {
    if (!g.member_ids.includes(userId)) continue;
    const l = s.access[g.id]?.find((e) => e.connection_id === connId)?.level;
    if (l === "edit") return "edit";
    if (l === "view") best = "view";
  }
  return best;
}

//[mock] host "down" / "slow" = database target tidak terjangkau / timeout
const unreachable = (c: Connection) =>
  c.host === "down" ? err(502, "cannot connect to target database")
    : c.host === "slow" ? err(504, "timed out connecting to target database") : null;

type Ctx = { s: Store; conn: Connection; level: AccessLevel; user: StoredUser; params: Record<string, string>; url: URL; body: Record<string, unknown> };

//[akses] 401 belum login, 404 database tidak ada, 403 tidak punya akses / level kurang (urutan sama dengan contract bagian 8)
function db(need: AccessLevel, fn: (c: Ctx) => Response | Promise<Response>) {
  return async ({ params, request }: { params: Record<string, unknown>; request: Request }) => {
    const user = currentUser();
    if (!user) return err(401, "Unauthorized");
    const s = load();
    const conn = s.connections.find((c) => c.id === params.id);
    if (!conn) return err(404, "database not found");
    const level = levelFor(s, user.id, conn.id);
    if (!level) return err(403, "no access to this database");
    if (need === "edit" && level !== "edit") return err(403, "edit access required");
    const hasBody = request.method !== "GET";
    const body = hasBody ? ((await request.json().catch(() => ({}))) as Record<string, unknown>) : {};
    const p = Object.fromEntries(Object.entries(params).map(([k, v]) => [k, String(v)]));
    const res = await fn({ s, conn, level, user, params: p, url: new URL(request.url), body });
    save(s);
    return res;
  };
}

function checkInput(b: Partial<MigrationInput>): Response | null {
  const name = (b.name ?? "").trim();
  if (!name || name.length > 200) return err(400, "required, max 200 characters", "name");
  if (!(b.sql ?? "").trim()) return err(400, "required", "sql");
  return null;
}

export const databaseHandlers = [
  http.get(P, () => {
    const user = currentUser();
    if (!user) return err(401, "Unauthorized");
    const s = load();
    const databases = s.connections.flatMap((c) => {
      const level = levelFor(s, user.id, c.id);
      return level ? [{ id: c.id, name: c.name, db_name: c.db_name, level }] : [];
    });
    return HttpResponse.json({ databases });
  }),

  http.get(`${P}/:id`, db("view", ({ conn, level }) =>
    HttpResponse.json({ id: conn.id, name: conn.name, db_name: conn.db_name, level }))),

  http.get(`${P}/:id/schema`, db("view", ({ s, conn }) => {
    const down = unreachable(conn);
    if (down) return down;
    return HttpResponse.json({ schemas: (s.targets[conn.id] ?? emptyTarget()).schemas });
  })),

  http.get(`${P}/:id/tables/:schema/:table/rows`, db("view", ({ s, conn, params, url }) => {
    const down = unreachable(conn);
    if (down) return down;
    const t = s.targets[conn.id] ?? emptyTarget();
    const tb = t.schemas.find((x) => x.name === params.schema)?.tables.find((x) => x.name === params.table);
    if (!tb) return err(404, "table not found");
    const limit = Math.min(200, Math.max(1, Number(url.searchParams.get("limit")) || 50));
    const offset = Math.max(0, Number(url.searchParams.get("offset")) || 0);
    const all = t.rows[`${params.schema}.${params.table}`] ?? [];
    return HttpResponse.json({
      columns: tb.columns.map((c) => ({ name: c.name, type: c.type })),
      rows: all.slice(offset, offset + limit), total: all.length, limit, offset,
    });
  })),

  http.get(`${P}/:id/migrations`, db("view", ({ s, conn }) => HttpResponse.json({ migrations: s.migrations[conn.id] ?? [] }))),
  http.get(`${P}/:id/migrations/:mid`, db("view", ({ s, conn, params }) => {
    const m = s.migrations[conn.id]?.find((x) => x.id === params.mid);
    return m ? HttpResponse.json(m) : err(404, "migration not found");
  })),

  http.post(`${P}/:id/migrations`, db("edit", ({ s, conn, user, body }) => {
    const b = body as Partial<MigrationInput>;
    const bad = checkInput(b);
    if (bad) return bad;
    const m: Migration = {
      id: crypto.randomUUID(), connection_id: conn.id, version: null, name: b.name!.trim(), sql: b.sql!, status: "draft", error: "",
      created_by: user.id, created_by_name: user.name, executed_by: null, executed_by_name: null,
      created_at: now(), updated_at: now(), executed_at: null, duration_ms: null,
    };
    (s.migrations[conn.id] ??= []).unshift(m);
    return HttpResponse.json(m, { status: 201 });
  })),

  http.put(`${P}/:id/migrations/:mid`, db("edit", ({ s, conn, params, body }) => {
    const m = s.migrations[conn.id]?.find((x) => x.id === params.mid);
    if (!m) return err(404, "migration not found");
    const b = body as Partial<MigrationInput>;
    const bad = checkInput(b);
    if (bad) return bad;
    if (m.status !== "draft") return err(409, "only draft migrations can be edited");
    Object.assign(m, { name: b.name!.trim(), sql: b.sql!, updated_at: now() });
    return HttpResponse.json(m);
  })),

  //[execute] SQL gagal = tetap 200 dengan status "failed" (contract 9); database tidak terjangkau = 502/504 dan tetap draft
  http.post(`${P}/:id/migrations/:mid/execute`, db("edit", ({ s, conn, user, params }) => {
    const list = (s.migrations[conn.id] ??= []);
    const m = list.find((x) => x.id === params.mid);
    if (!m) return err(404, "migration not found");
    if (m.status !== "draft") return err(409, "only draft migrations can be executed");
    const down = unreachable(conn);
    if (down) return down;
    //[mock] SQL berisi pg_sleep mensimulasikan migration lain yang sedang berjalan di database yang sama
    if (/pg_sleep/i.test(m.sql)) return err(409, "another migration is already running on this database");
    const t0 = performance.now();
    try {
      s.targets[conn.id] = applySql(s.targets[conn.id] ?? emptyTarget(), m.sql);
      m.status = "success";
      m.version = Math.max(0, ...list.map((x) => x.version ?? 0)) + 1;
      m.error = "";
    } catch (e) {
      m.status = "failed";
      m.version = null;
      m.error = (e as Error).message;
    }
    Object.assign(m, {
      executed_by: user.id, executed_by_name: user.name, executed_at: now(), updated_at: now(),
      duration_ms: Math.max(1, Math.round(performance.now() - t0)),
    });
    return HttpResponse.json(m);
  })),
];
