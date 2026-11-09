import { http, HttpResponse } from "msw";
import type { AccessLevel, BatchItem, BatchStatus, Connection, Migration, MigrationBatch, MigrationInput } from "../lib/types";
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

//[batch] mesin status batch tiruan: satu item dimajukan tiap polling GET /batches/:bid
const TERMINAL: BatchStatus[] = ["success", "partial", "failed", "cancelled"];
const itemId = (s: Store) => Object.values(s.batchItems).flat().reduce((n, i) => Math.max(n, i.id), 0);

function attachBatchNames(s: Store, b: MigrationBatch) {
  b.created_by_name = s.users.find((u) => u.id === b.created_by)?.name ?? b.created_by;
  if (b.cancelled_by) b.cancelled_by_name = s.users.find((u) => u.id === b.cancelled_by)?.name ?? b.cancelled_by;
  return b;
}

function createBatch(s: Store, conn: Connection, user: StoredUser, ids: string[]): MigrationBatch {
  const b: MigrationBatch = {
    id: crypto.randomUUID(), connection_id: conn.id, status: "queued", total: ids.length, processed: 0, failed: 0,
    cancel_requested: false, created_by: user.id, created_by_name: user.name, cancelled_by: null, cancelled_by_name: null,
    error: "", heartbeat_at: null, started_at: null, finished_at: null, created_at: now(), updated_at: now(), execution_count: 0,
  };
  const base = itemId(s);
  s.batchItems[b.id] = ids.map((mid, i) => ({
    id: base + i + 1, batch_id: b.id, migration_id: mid,
    migration_name: s.migrations[conn.id]?.find((m) => m.id === mid)?.name,
    attempt: 1 + Object.values(s.batchItems).flat().filter((it) => it.migration_id === mid).reduce((n, it) => Math.max(n, it.attempt), 0),
    execution_id: crypto.randomUUID(), status: "pending", error: "", compensation_status: "none", compensated_at: null,
    started_at: null, finished_at: null, duration_ms: null, created_at: now(), updated_at: now(),
  }));
  s.batches.unshift(b);
  return b;
}

function finishBatch(b: MigrationBatch, status: BatchStatus, error = "") {
  Object.assign(b, { status, error, finished_at: now(), updated_at: now() });
}

function advanceBatch(s: Store, conn: Connection, b: MigrationBatch) {
  if (b.status === "queued") Object.assign(b, { status: "running", started_at: now() });
  if (b.status !== "running") return;
  b.heartbeat_at = b.updated_at = now();
  if (b.cancel_requested) return finishBatch(b, "cancelled");
  const items = s.batchItems[b.id] ?? [];
  const it = items.find((i) => i.status === "pending");
  if (!it) {
    if (b.failed > 0 && b.processed > 0) return finishBatch(b, "partial");
    return finishBatch(b, b.failed > 0 ? "failed" : "success");
  }
  const m = s.migrations[conn.id]?.find((x) => x.id === it.migration_id);
  it.started_at = now();
  if (!m) return Object.assign(it, { status: "skipped", error: "migration no longer exists", finished_at: now(), duration_ms: 0, updated_at: now() });
  if (conn.host === "down" || conn.host === "slow") {
    Object.assign(it, { status: "failed", error: "cannot connect to target database", finished_at: now(), duration_ms: 1, updated_at: now() });
    b.processed++; b.failed++;
    return finishBatch(b, "failed", "cannot connect to target database");
  }
  const t0 = performance.now();
  try {
    s.targets[conn.id] = applySql(s.targets[conn.id] ?? emptyTarget(), m.sql);
    m.status = "success";
    m.version = Math.max(0, ...(s.migrations[conn.id] ?? []).map((x) => x.version ?? 0)) + 1;
    m.error = "";
    it.status = "success";
  } catch (e) {
    m.status = "failed";
    m.version = null;
    m.error = (e as Error).message;
    it.status = "failed";
    it.error = m.error;
    it.compensation_status = "retained";
    it.compensated_at = now();
    b.failed++;
  }
  b.processed++;
  Object.assign(m, { executed_by: b.created_by, executed_by_name: b.created_by_name, executed_at: now(), updated_at: now(),
    duration_ms: Math.max(1, Math.round(performance.now() - t0)) });
  Object.assign(it, { finished_at: now(), duration_ms: m.duration_ms, updated_at: now() });
  if (!items.some((i) => i.status === "pending")) {
    if (b.cancel_requested) finishBatch(b, "cancelled");
    else if (b.failed > 0 && b.processed > 0) finishBatch(b, "partial");
    else if (b.failed > 0) finishBatch(b, "failed");
    else finishBatch(b, "success");
  }
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
  //[batch] daftar batch + detail; progres dimajukan satu item setiap GET detail (cukup untuk demo)
  http.get(`${P}/:id/batches`, db("view", ({ s, conn, url }) => {
    const limit = Math.min(100, Math.max(1, Number(url.searchParams.get("limit")) || 20));
    const list = s.batches.filter((b) => b.connection_id === conn.id).slice(0, limit).map((b) => attachBatchNames(s, b));
    return HttpResponse.json({ batches: list });
  })),

  http.post(`${P}/:id/batches`, db("edit", ({ s, conn, user, body }) => {
    const raw = Array.isArray(body.migration_ids) ? body.migration_ids.map(String) : [];
    const ids = [...new Set(raw)];
    if (ids.length === 0) return err(400, "required", "migration_ids");
    if (ids.length > 200) return err(400, "max 200 migrations per batch", "migration_ids");
    const list = s.migrations[conn.id] ?? [];
    const found = ids.flatMap((id) => list.filter((m) => m.id === id));
    if (found.length !== ids.length || found.some((m) => m.status !== "draft")) return err(409, "only draft migrations can be executed");
    if (found.some((m) => /pg_sleep/i.test(m.sql))) return err(409, "another migration is already running on this database");
    const b = createBatch(s, conn, user, ids);
    Object.assign(b, { status: "running", started_at: now() });
    return HttpResponse.json(attachBatchNames(s, b), { status: 202 });
  })),

  http.get(`${P}/:id/batches/:bid`, db("view", ({ s, conn, params }) => {
    const b = s.batches.find((x) => x.id === params.bid && x.connection_id === conn.id);
    if (!b) return err(404, "batch not found");
    advanceBatch(s, conn, b);
    return HttpResponse.json(attachBatchNames(s, b));
  })),

  http.get(`${P}/:id/batches/:bid/items`, db("view", ({ s, conn, params }) => {
    const b = s.batches.find((x) => x.id === params.bid && x.connection_id === conn.id);
    if (!b) return err(404, "batch not found");
    const items = (s.batchItems[b.id] ?? []).map((it) => ({ ...it, migration_name: s.migrations[conn.id]?.find((m) => m.id === it.migration_id)?.name }));
    return HttpResponse.json({ items, next_after_id: items.at(-1)?.id ?? 0 });
  })),

  http.post(`${P}/:id/batches/:bid/cancel`, db("edit", ({ s, conn, user, params }) => {
    const b = s.batches.find((x) => x.id === params.bid && x.connection_id === conn.id);
    if (!b) return err(404, "batch not found");
    if (TERMINAL.includes(b.status)) return err(409, "batch already finished");
    Object.assign(b, { cancel_requested: true, cancelled_by: user.id, updated_at: now() });
    if (b.status === "queued") finishBatch(b, "cancelled");
    return HttpResponse.json({ status: "cancel_requested" }, { status: 202 });
  })),

  http.post(`${P}/:id/batches/:bid/retry`, db("edit", ({ s, conn, user, params }) => {
    const b = s.batches.find((x) => x.id === params.bid && x.connection_id === conn.id);
    if (!b) return err(404, "batch not found");
    const ids = (s.batchItems[b.id] ?? []).filter((i) => i.status === "pending" || i.status === "skipped").map((i) => i.migration_id);
    if (ids.length === 0) return err(409, "nothing to retry");
    const next = createBatch(s, conn, user, ids);
    Object.assign(next, { status: "running", started_at: now() });
    return HttpResponse.json(attachBatchNames(s, next), { status: 201 });
  })),

  //[audit] eksekusi per item, terbaru dulu
  http.get(`${P}/:id/executions`, db("view", ({ s, conn, url }) => {
    const bid = url.searchParams.get("batch_id");
    const mid = url.searchParams.get("migration_id");
    const limit = Math.min(200, Math.max(1, Number(url.searchParams.get("limit")) || 50));
    const items = s.batches
      .filter((b) => b.connection_id === conn.id && (!bid || b.id === bid))
      .flatMap((b) => s.batchItems[b.id] ?? [])
      .filter((i) => !mid || i.migration_id === mid)
      .sort((a, z) => z.id - a.id)
      .slice(0, limit)
      .map((it) => ({ ...it, migration_name: s.migrations[conn.id]?.find((m) => m.id === it.migration_id)?.name }));
    return HttpResponse.json({ items, next_after_id: items.at(-1)?.id ?? 0 });
  })),
];
