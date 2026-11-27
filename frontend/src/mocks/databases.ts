import { http, HttpResponse } from "msw";
import type { AccessLevel, BatchItem, BatchStatus, ChangeRequest, Connection, JobKind, Migration, MigrationBatch, MigrationInput, MigrationJob } from "../lib/types";
import { currentUser, err } from "./auth";
import { load, save, type Store, type StoredUser } from "./db";
import { applySql, emptyTarget } from "./target";

const P = "/api/v1/databases";
const now = () => new Date().toISOString();

//[akses] level efektif = tertinggi dari semua group user (contract 3.4); admin TIDAK punya jalan pintas (contract 3.5)
function canBatchFor(s: Store, userId: string, connId: string): boolean {
  const groups = s.groups.filter((g) => s.access[g.id]?.some((e) => e.connection_id === connId));
  return groups.length > 0 && groups.every((g) => g.leader_id === userId);
}

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

const definition = (b: Partial<MigrationInput>) => ({
  name: b.name!.trim(), sql: b.sql!, sql_down: b.sql_down ?? "",
  irreversible: !!b.irreversible, irreversible_reason: (b.irreversible_reason ?? "").trim(),
});

//[queue] sama dengan engine.ValidateDefinition: up wajib, lalu down atau alasan irreversible
function checkDefinition(m: Migration): Response | null {
  if (!m.sql.trim()) return err(400, "required", "sql");
  if (m.irreversible) return m.irreversible_reason.trim() ? null : err(400, "required when irreversible", "irreversible_reason");
  return m.sql_down.trim() ? null : err(400, "required unless irreversible", "sql_down");
}

//[queue] job tiruan dimajukan satu langkah setiap polling: queued -> running -> success/failed
const ACTIVE = ["queued", "running", "uncertain"];

function advanceJob(s: Store, conn: Connection, j: MigrationJob) {
  if (j.status === "queued") {
    Object.assign(j, { status: "running", attempts: j.attempts + 1, worker_id: "mock-worker-1", started_at: now(), heartbeat_at: now(), updated_at: now() });
    return;
  }
  if (j.status !== "running") return;
  const list = s.migrations[conn.id] ?? [];
  const m = list.find((x) => x.id === j.migration_id);
  if (!m) return Object.assign(j, { status: "failed", error: "migration not found", finished_at: now(), updated_at: now() });
  if (conn.host === "down" || conn.host === "slow") {
    //[mock] database tidak terjangkau = error sementara: di-retry sampai max_attempts
    const final = j.attempts >= j.max_attempts;
    Object.assign(j, { status: final ? "failed" : "queued", error: "cannot connect to target database", updated_at: now(), finished_at: final ? now() : null });
    recordExecution(s, j, m, "failed", 1, "cannot connect to target database", "08006");
    if (final && j.kind === "up") Object.assign(m, { status: "failed", error: j.error, updated_at: now() });
    return;
  }
  const t0 = performance.now();
  let error = "";
  const rollback = j.kind === "rollback";
  try {
    s.targets[conn.id] = applySql(s.targets[conn.id] ?? emptyTarget(), rollback ? m.sql_down : m.sql);
    if (!rollback) {
      m.status = "success";
      m.version = m.version ?? Math.max(0, ...list.map((x) => x.version ?? 0)) + 1;
      m.error = "";
    }
  } catch (e) {
    error = (e as Error).message;
    //[mock] sama dengan backend: hanya up yang gagal mengubah baris migration; forward gagal dan rollback tidak
    if (j.kind === "up") Object.assign(m, { status: "failed", version: null, error });
  }
  const user = s.users.find((u) => u.id === j.requested_by);
  const duration = Math.max(1, Math.round(performance.now() - t0));
  if (!rollback && (j.kind === "up" || !error)) {
    Object.assign(m, { executed_by: j.requested_by, executed_by_name: user?.name ?? j.requested_by, executed_at: now(), updated_at: now(), duration_ms: duration });
  }
  Object.assign(j, { status: error ? "failed" : "success", error, finished_at: now(), updated_at: now() });
  recordExecution(s, j, m, error ? "failed" : "success", duration, error, error ? "42601" : "");
}

function recordExecution(s: Store, j: MigrationJob, m: Migration, status: "success" | "failed", duration: number, error: string, sqlstate: string) {
  s.executions.push({
    id: s.executions.length + 1, execution_id: j.execution_id, attempt: j.attempts, job_id: j.id, connection_id: j.connection_id,
    migration_id: m.id, kind: j.kind, status, revision: m.revision, migration_version: status === "success" ? m.version : null,
    sql: j.kind === "rollback" ? m.sql_down : m.sql, rollback_of: j.rollback_of, executed_by: j.requested_by, started_at: j.started_at ?? now(), finished_at: now(),
    duration_ms: duration, error, sqlstate, recovered: false, created_at: now(),
  });
}

//[history] eksekusi sukses terakhir migration: kind rollback berarti migration sedang dalam keadaan dibatalkan
const lastApplied = (s: Store, mid: string) => s.executions.filter((x) => x.migration_id === mid && x.status === "success").at(-1);

function enqueueJob(s: Store, cr: ChangeRequest) {
  const j: MigrationJob = {
    id: crypto.randomUUID(), connection_id: cr.connection_id, migration_id: cr.migration_id, execution_id: crypto.randomUUID(), kind: cr.kind,
    status: "queued", rollback_of: cr.rollback_of, attempts: 0, max_attempts: 3, run_after: now(), worker_id: "", heartbeat_at: null,
    error: "", requested_by: cr.requested_by, created_at: now(), updated_at: now(), started_at: null, finished_at: null,
  };
  s.jobs.unshift(j);
  cr.job_id = j.id;
  return j;
}

//[review] can_decide dihitung per user seperti field turunan di backend
const asSeen = (cr: ChangeRequest, userId: string): ChangeRequest =>
  ({ ...cr, can_decide: cr.status === "pending_review" && cr.reviewers.some((r) => r.decision === "pending" && r.leader_id === userId) });

//[review] setuju / tolak: leader tidak harus anggota group, jadi tidak lewat db()
const decide = (decision: "approve" | "reject") =>
  http.post(`${P}/:id/change-requests/:rid/${decision}`, async ({ params, request }) => {
    const user = currentUser();
    if (!user) return err(401, "Unauthorized");
    const s = load();
    const cr = s.changeRequests.find((c) => c.id === params.rid && c.connection_id === params.id);
    if (!cr) return err(404, "change request not found");
    const mine = cr.reviewers.filter((r) => r.leader_id === user.id);
    if (mine.length === 0) return err(403, "only a group leader of this database can decide");
    if (cr.status !== "pending_review") return err(409, "change request is no longer pending review");
    const open = mine.filter((r) => r.decision === "pending");
    if (open.length === 0) return err(409, "you have already decided");
    const body = (await request.json().catch(() => ({}))) as { comment?: string };
    open.forEach((r) => Object.assign(r, { decision, comment: (body.comment ?? "").trim(), decided_at: now() }));
    const m = s.migrations[cr.connection_id]?.find((x) => x.id === cr.migration_id);
    if (decision === "reject") {
      cr.status = "rejected";
      if (m && cr.kind === "up") Object.assign(m, { status: "draft", updated_at: now() });
    } else if (cr.reviewers.every((r) => r.decision === "approve")) {
      cr.status = "approved";
      if (m && cr.kind === "up") Object.assign(m, { status: "approved", updated_at: now() });
      enqueueJob(s, cr);
    }
    Object.assign(cr, { updated_at: now(), decided_at: cr.status === "pending_review" ? null : now() });
    save(s);
    return HttpResponse.json(asSeen(cr, user.id));
  });

const withName = (s: Store, conn: Connection, j: MigrationJob) =>
  ({ ...j, migration_name: s.migrations[conn.id]?.find((m) => m.id === j.migration_id)?.name });

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
      return level ? [{ id: c.id, name: c.name, db_name: c.db_name, level, can_batch: canBatchFor(s, user.id, c.id) }] : [];
    });
    return HttpResponse.json({ databases });
  }),

  http.get(`${P}/:id`, db("view", ({ s, conn, user, level }) =>
    HttpResponse.json({ id: conn.id, name: conn.name, db_name: conn.db_name, level, can_batch: canBatchFor(s, user.id, conn.id) }))),

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
      id: crypto.randomUUID(), connection_id: conn.id, version: null, ...definition(b), revision: 1, status: "draft", error: "",
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
    if (s.jobs.some((j) => j.migration_id === m.id)) return err(409, "migration is immutable once queued");
    Object.assign(m, { ...definition(b), revision: m.revision + 1, updated_at: now() });
    return HttpResponse.json(m);
  })),

  //[review] ajukan up / rollback / forward; migration baru masuk queue setelah semua Team Leader setuju
  http.post(`${P}/:id/migrations/:mid/change-requests`, db("edit", ({ s, conn, user, params, body }) => {
    const m = s.migrations[conn.id]?.find((x) => x.id === params.mid);
    if (!m) return err(404, "migration not found");
    const kind = body.kind as JobKind;
    if (!["up", "rollback", "forward"].includes(kind)) return err(400, "must be up, rollback or forward", "kind");
    const bad = checkDefinition(m);
    if (bad) return bad;
    const applied = lastApplied(s, m.id);
    const rolledBack = applied?.kind === "rollback";
    if (kind === "up" && m.status !== "draft") return err(409, "only draft migrations can be submitted");
    if (kind === "rollback" && (m.status !== "success" || m.irreversible || rolledBack)) return err(409, "migration cannot be rolled back");
    if (kind === "forward" && !(m.status === "failed" || (m.status === "success" && rolledBack))) return err(409, "only failed or rolled back migrations can be forwarded");
    if (s.changeRequests.some((c) => c.migration_id === m.id && (c.status === "pending_review" || (c.status === "approved" && !c.job_id))) ||
        s.jobs.some((j) => j.migration_id === m.id && ACTIVE.includes(j.status))) return err(409, "migration already has a pending change request");
    const groups = s.groups.filter((g) => s.access[g.id]?.some((e) => e.connection_id === conn.id));
    const noLeader = groups.find((g) => !g.leader_id);
    if (noLeader) return err(409, `group ${noLeader.name} has no leader`);
    const leaderName = (id: string | null) => s.users.find((u) => u.id === id)?.name ?? null;
    const cr: ChangeRequest = {
      id: crypto.randomUUID(), connection_id: conn.id, migration_id: m.id, kind, rollback_of: kind === "rollback" ? applied?.execution_id ?? null : null,
      status: "pending_review", requested_by: user.id, job_id: null, created_at: now(), updated_at: now(), decided_at: null,
      migration_name: m.name, connection_name: conn.name, requested_by_name: user.name, can_decide: false,
      reviewers: groups.map((g) => ({ group_id: g.id, group_name: g.name, leader_id: g.leader_id, leader_name: leaderName(g.leader_id), decision: "pending", comment: "", decided_at: null })),
    };
    if (kind === "up") Object.assign(m, { status: "pending_review", updated_at: now() });
    s.changeRequests.unshift(cr);
    return HttpResponse.json(asSeen(cr, user.id), { status: 201 });
  })),

  http.get(`${P}/:id/change-requests`, db("view", ({ s, conn, user, url }) => {
    const status = url.searchParams.get("status");
    const list = s.changeRequests.filter((c) => c.connection_id === conn.id && (!status || c.status === status));
    return HttpResponse.json({ change_requests: list.map((c) => asSeen(c, user.id)) });
  })),

  //[review] inbox leader lintas database, paling lama menunggu dulu
  http.get("/api/v1/change-requests/pending", () => {
    const user = currentUser();
    if (!user) return err(401, "Unauthorized");
    const list = load().changeRequests.map((c) => asSeen(c, user.id)).filter((c) => c.can_decide).reverse();
    return HttpResponse.json({ change_requests: list });
  }),

  decide("approve"),
  decide("reject"),

  //[history] semua attempt; hide_rollback membuang eksekusi yang dibatalkan dan rollback suksesnya (dipasangkan lewat execution_id)
  http.get(`${P}/:id/history`, db("view", ({ s, conn, url }) => {
    const all = s.executions.filter((x) => x.connection_id === conn.id);
    const hidden = new Set<string>();
    if (url.searchParams.get("hide_rollback") === "true") {
      all.filter((x) => x.kind === "rollback" && x.status === "success" && x.rollback_of).forEach((x) => {
        hidden.add(x.execution_id);
        hidden.add(x.rollback_of!);
      });
    }
    return HttpResponse.json({ executions: all.filter((x) => !hidden.has(x.execution_id)) });
  })),

  //[review] untuk pengajuan yang sudah disetujui tetapi belum masuk queue
  http.post(`${P}/:id/change-requests/:rid/dispatch`, db("edit", ({ s, conn, params }) => {
    const cr = s.changeRequests.find((c) => c.id === params.rid && c.connection_id === conn.id);
    if (!cr) return err(404, "change request not found");
    if (cr.status !== "approved" || cr.job_id) return err(409, "change request is not waiting to be queued");
    enqueueJob(s, cr);
    return HttpResponse.json(cr);
  })),

  http.get(`${P}/:id/jobs`, db("view", ({ s, conn }) => {
    const jobs = s.jobs.filter((j) => j.connection_id === conn.id);
    jobs.filter((j) => ACTIVE.includes(j.status)).forEach((j) => advanceJob(s, conn, j));
    return HttpResponse.json({ jobs: jobs.slice(0, 50).map((j) => withName(s, conn, j)) });
  })),

  http.get(`${P}/:id/jobs/:jid`, db("view", ({ s, conn, params }) => {
    const j = s.jobs.find((x) => x.id === params.jid && x.connection_id === conn.id);
    if (!j) return err(404, "job not found");
    advanceJob(s, conn, j);
    return HttpResponse.json(withName(s, conn, j));
  })),

  http.get(`${P}/:id/migrations/:mid/executions`, db("view", ({ s, conn, params }) => {
    if (!s.migrations[conn.id]?.some((m) => m.id === params.mid)) return err(404, "migration not found");
    return HttpResponse.json({ executions: s.executions.filter((x) => x.migration_id === params.mid) });
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
    if (!canBatchFor(s, user.id, conn.id)) return err(403, "only a group leader can run a batch; every group with access must have you as its leader");
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
