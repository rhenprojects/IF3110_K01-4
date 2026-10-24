import { http, HttpResponse } from "msw";
import type { AccessEntry, Connection, ConnectionInput, CreateUserInput, Group, GroupInput, UpdateUserInput } from "../lib/types";
import { currentUser, err } from "./auth";
import { load, publicUser, save, type StoredUser } from "./db";

const P = "/api/v1/admin";
const SSL = ["disable", "allow", "prefer", "require", "verify-ca", "verify-full"];
const now = () => new Date().toISOString();
const uid = () => crypto.randomUUID();
const notFound = (what: string) => err(404, `${what} not found`);

//[guard] 401 jika belum login, 403 jika bukan admin
function deny() {
  const u = currentUser();
  if (!u) return err(401, "Unauthorized");
  if (!u.is_admin) return err(403, "Platform admin only");
  return null;
}

type Ctx<T> = { s: ReturnType<typeof load>; id: string; body: T };

//[wrap] cek admin, load store, jalankan handler, simpan store
function admin<T extends object = object>(fn: (c: Ctx<T>) => Response) {
  return async ({ params, request }: { params: Record<string, unknown>; request: Request }) => {
    const d = deny();
    if (d) return d;
    const s = load();
    const hasBody = request.method !== "GET" && request.method !== "DELETE";
    const body = (hasBody ? await request.json().catch(() => ({})) : {}) as T;
    const res = fn({ s, id: String(params.id ?? ""), body });
    save(s);
    return res;
  };
}

//[validasi] aturan sama seperti backend (user_handler/user.go)
function checkUser(b: Partial<CreateUserInput>, requireAll: boolean): Response | null {
  if (requireAll && !/^[a-zA-Z0-9_.-]{3,50}$/.test(b.username ?? ""))
    return err(400, "3-50 characters: letters, digits, '_', '.', '-'", "username");
  if ((b.name ?? "").length > 100) return err(400, "max 100 characters", "name");
  if ((requireAll || b.password !== undefined) && (b.password ?? "").length < 8)
    return err(400, "at least 8 characters", "password");
  return null;
}

//[validasi] aturan sama seperti backend (targetdb/service.go)
function checkConn(b: Partial<ConnectionInput>, requirePassword: boolean): Response | null {
  const name = (b.name ?? "").trim(), host = (b.host ?? "").trim();
  const port = b.port || 5432;
  if (!name || name.length > 100) return err(400, "required, max 100 characters", "name");
  if (!host || host.length > 255) return err(400, "required, max 255 characters", "host");
  if (/[,/\s]/.test(host)) return err(400, "must be a single hostname or IP address", "host");
  if (port < 1 || port > 65535) return err(400, "must be between 1 and 65535", "port");
  if (!(b.db_name ?? "").trim()) return err(400, "required, max 100 characters", "db_name");
  if (!(b.username ?? "").trim()) return err(400, "required, max 100 characters", "username");
  if (requirePassword && !b.password) return err(400, "required", "password");
  if (b.ssl_mode && !SSL.includes(b.ssl_mode)) return err(400, `must be one of ${SSL.join(", ")}`, "ssl_mode");
  return null;
}

//[test] host "down" / "slow" mensimulasikan koneksi gagal / timeout
function testResult(host: string) {
  if (host === "down") return err(502, "cannot connect to target database");
  if (host === "slow") return err(504, "timed out connecting to target database");
  return HttpResponse.json({ status: "ok", server_version: "PostgreSQL 16.4 (mock)" });
}

export const adminHandlers = [
  //[user] get/post sudah ada di backend (post saja), sisanya usulan
  http.get(`${P}/users`, admin(({ s }) => HttpResponse.json({ users: s.users.map(publicUser) }))),
  http.post(`${P}/users`, admin<CreateUserInput>(({ s, body }) => {
    const bad = checkUser(body, true);
    if (bad) return bad;
    if (s.users.some((u) => u.username === body.username)) return err(409, "username already exists");
    const u: StoredUser = {
      id: uid(), username: body.username, name: body.name?.trim() || body.username, password: body.password,
      is_admin: !!body.is_admin, created_at: now(), updated_at: now(),
    };
    s.users.push(u);
    return HttpResponse.json(publicUser(u), { status: 201 });
  })),
  http.put(`${P}/users/:id`, admin<UpdateUserInput>(({ s, id, body }) => {
    const u = s.users.find((x) => x.id === id);
    if (!u) return notFound("user");
    const bad = checkUser(body, false);
    if (bad) return bad;
    if (body.name) u.name = body.name.trim();
    if (body.password) u.password = body.password;
    if (typeof body.is_admin === "boolean") u.is_admin = body.is_admin;
    u.updated_at = now();
    return HttpResponse.json(publicUser(u));
  })),
  http.delete(`${P}/users/:id`, admin(({ s, id }) => {
    if (id === currentUser()?.id) return err(409, "cannot delete yourself");
    if (!s.users.some((u) => u.id === id)) return notFound("user");
    s.users = s.users.filter((u) => u.id !== id);
    s.groups.forEach((g) => (g.member_ids = g.member_ids.filter((m) => m !== id)));
    return new HttpResponse(null, { status: 204 });
  })),

  //[group] semua usulan (belum ada di backend)
  http.get(`${P}/groups`, admin(({ s }) => HttpResponse.json({ groups: s.groups }))),
  http.post(`${P}/groups`, admin<GroupInput>(({ s, body }) => {
    const name = (body.name ?? "").trim();
    if (!name || name.length > 100) return err(400, "required, max 100 characters", "name");
    if (s.groups.some((g) => g.name === name)) return err(409, "group name already exists");
    const g: Group = { id: uid(), name, description: body.description ?? "", member_ids: [], created_at: now(), updated_at: now() };
    s.groups.push(g);
    return HttpResponse.json(g, { status: 201 });
  })),
  http.put(`${P}/groups/:id`, admin<GroupInput>(({ s, id, body }) => {
    const g = s.groups.find((x) => x.id === id);
    if (!g) return notFound("group");
    const name = (body.name ?? "").trim();
    if (!name || name.length > 100) return err(400, "required, max 100 characters", "name");
    if (s.groups.some((x) => x.id !== id && x.name === name)) return err(409, "group name already exists");
    g.name = name;
    g.description = body.description ?? g.description;
    g.updated_at = now();
    return HttpResponse.json(g);
  })),
  http.delete(`${P}/groups/:id`, admin(({ s, id }) => {
    if (!s.groups.some((g) => g.id === id)) return notFound("group");
    s.groups = s.groups.filter((g) => g.id !== id);
    delete s.access[id];
    return new HttpResponse(null, { status: 204 });
  })),
  http.put(`${P}/groups/:id/members`, admin<{ user_ids?: string[] }>(({ s, id, body }) => {
    const g = s.groups.find((x) => x.id === id);
    if (!g) return notFound("group");
    const ids = body.user_ids ?? [];
    if (ids.some((u) => !s.users.some((x) => x.id === u))) return err(400, "unknown user id", "user_ids");
    g.member_ids = [...new Set(ids)];
    g.updated_at = now();
    return HttpResponse.json(g);
  })),

  //[akses] level akses group per koneksi database
  http.get(`${P}/groups/:id/access`, admin(({ s, id }) =>
    s.groups.some((g) => g.id === id) ? HttpResponse.json({ access: s.access[id] ?? [] }) : notFound("group"))),
  http.put(`${P}/groups/:id/access`, admin<{ access?: AccessEntry[] }>(({ s, id, body }) => {
    if (!s.groups.some((g) => g.id === id)) return notFound("group");
    const list = body.access ?? [];
    for (const e of list) {
      if (e.level !== "view" && e.level !== "edit") return err(400, "must be view or edit", "level");
      if (!s.connections.some((c) => c.id === e.connection_id)) return err(400, "unknown connection id", "connection_id");
    }
    s.access[id] = list;
    return HttpResponse.json({ access: list });
  })),

  //[koneksi] sesuai backend (targetdb/handler.go): put untuk update, list dibungkus
  http.get(`${P}/connections`, admin(({ s }) => HttpResponse.json({ connections: s.connections }))),
  http.post(`${P}/connections/test`, admin<ConnectionInput>(({ body }) => checkConn(body, true) ?? testResult(body.host.trim()))),
  http.get(`${P}/connections/:id`, admin(({ s, id }) => {
    const c = s.connections.find((x) => x.id === id);
    return c ? HttpResponse.json(c) : notFound("database connection");
  })),
  http.post(`${P}/connections`, admin<ConnectionInput>(({ s, body }) => {
    const bad = checkConn(body, true);
    if (bad) return bad;
    if (s.connections.some((c) => c.name === body.name.trim())) return err(409, "database connection name already exists");
    const c: Connection = {
      id: uid(), name: body.name.trim(), host: body.host.trim(), port: body.port || 5432, db_name: body.db_name.trim(),
      username: body.username.trim(), ssl_mode: body.ssl_mode || "prefer", created_by: currentUser()!.id,
      created_at: now(), updated_at: now(),
    };
    s.connections.push(c);
    return HttpResponse.json(c, { status: 201 });
  })),
  http.put(`${P}/connections/:id`, admin<ConnectionInput>(({ s, id, body }) => {
    const c = s.connections.find((x) => x.id === id);
    if (!c) return notFound("database connection");
    const bad = checkConn(body, false);
    if (bad) return bad;
    if (s.connections.some((x) => x.id !== id && x.name === body.name.trim())) return err(409, "database connection name already exists");
    Object.assign(c, {
      name: body.name.trim(), host: body.host.trim(), port: body.port || 5432, db_name: body.db_name.trim(),
      username: body.username.trim(), ssl_mode: body.ssl_mode || "prefer", updated_at: now(),
    });
    return HttpResponse.json(c);
  })),
  http.delete(`${P}/connections/:id`, admin(({ s, id }) => {
    if (!s.connections.some((c) => c.id === id)) return notFound("database connection");
    s.connections = s.connections.filter((c) => c.id !== id);
    for (const k of Object.keys(s.access)) s.access[k] = s.access[k].filter((e) => e.connection_id !== id);
    return new HttpResponse(null, { status: 204 });
  })),
  http.post(`${P}/connections/:id/test`, admin(({ s, id }) => {
    const c = s.connections.find((x) => x.id === id);
    return c ? testResult(c.host) : notFound("database connection");
  })),
];
