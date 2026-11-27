// Stress test k6 untuk bonus "Stress Testing dengan k6" (Milestone 2).
//
// Yang diukur:
// 1. Akses API baca (databases, migrations, detail, jobs) dengan beban bertahap
//    naik lalu turun, supaya terlihat pemulihan setelah beban puncak.
// 2. Pengiriman migration (create draft + submit change request) bersamaan
//    dengan akses baca.
// 3. Di teardown: approval leader terakhir lewat bulk approve, lalu menunggu
//    queue sampai selesai dan menghitung waktu tunggu queue + durasi job.
//
// Response pengiriman job TIDAK dianggap bukti eksekusi; bukti diambil dari
// status job di queue (lihat ringkasan teardown).
//
// Jalankan lewat scripts/k6-stress.sh (mendukung binary k6 atau image Docker).

import http from "k6/http";
import { check, sleep, fail } from "k6";
import { Counter, Trend } from "k6/metrics";

const BASE = (__ENV.BASE_URL || "http://localhost:9000/api/v1").replace(/\/$/, "");
const EDITOR = { u: __ENV.EDITOR_USERNAME || "admin", p: __ENV.EDITOR_PASSWORD || "admin-password" };
const LEADER = { u: __ENV.LEADER_USERNAME || EDITOR.u, p: __ENV.LEADER_PASSWORD || EDITOR.p };
const DB_ID = __ENV.DB_ID || "";
const PREPARE = Number(__ENV.PREPARE || 10);
const JOB_SQL = __ENV.JOB_SQL || "SELECT 1";
const PEAK_VUS = Number(__ENV.PEAK_VUS || 20);
const SUBMIT_VUS = Number(__ENV.SUBMIT_VUS || 2);
const RAMP_UP = __ENV.RAMP_UP || "20s";
const HOLD = __ENV.HOLD || "40s";
const RAMP_DOWN = __ENV.RAMP_DOWN || "20s";
const READ_P95_MS = Number(__ENV.READ_P95_MS || 800);
const SUBMIT_P95_MS = Number(__ENV.SUBMIT_P95_MS || 1500);
const ERROR_RATE = __ENV.ERROR_RATE || "0.01";

const submitCreated = new Counter("migration_submit_created");
const submitFailed = new Counter("migration_submit_failed");
const jobWait = new Trend("job_queue_wait_ms", true);
const jobDuration = new Trend("job_duration_ms", true);

export const options = {
  scenarios: {
    read: {
      executor: "ramping-vus",
      exec: "read",
      startVUs: 1,
      stages: [
        { duration: RAMP_UP, target: PEAK_VUS },
        { duration: HOLD, target: PEAK_VUS },
        { duration: RAMP_DOWN, target: 0 },
      ],
      gracefulStop: "10s",
    },
    submit: {
      executor: "constant-vus",
      exec: "submit",
      vus: SUBMIT_VUS,
      duration: __ENV.SUBMIT_DURATION || "60s",
      gracefulStop: "10s",
    },
  },
  thresholds: {
    http_req_failed: [`rate<${ERROR_RATE}`],
    "http_req_duration{scenario:read}": [`p(95)<${READ_P95_MS}`],
    "http_req_duration{scenario:submit}": [`p(95)<${SUBMIT_P95_MS}`],
  },
};

// Login sekali di setup; cookie + token CSRF dibagikan ke semua VU lewat data.
function login(username, password) {
  const res = http.post(`${BASE}/users/login`, JSON.stringify({ username, password }), {
    headers: { "Content-Type": "application/json" },
    tags: { name: "login" },
  });
  if (res.status !== 200) fail(`login ${username} gagal: ${res.status} ${res.body}`);
  const cookies = http.cookieJar().cookiesForURL(BASE);
  const parts = [];
  let csrf = "";
  for (const name of Object.keys(cookies)) {
    parts.push(`${name}=${cookies[name][0]}`);
    if (name === "csrf_token") csrf = cookies[name][0];
  }
  return { cookie: parts.join("; "), csrf };
}

function authedGet(path, user, tag) {
  return http.get(`${BASE}${path}`, { headers: { Cookie: user.cookie }, tags: { name: tag } });
}

function authedPost(path, body, user, tag) {
  return http.post(`${BASE}${path}`, JSON.stringify(body), {
    headers: { Cookie: user.cookie, "Content-Type": "application/json", "X-CSRF-Token": user.csrf },
    tags: { name: tag },
  });
}

export function setup() {
  const editor = login(EDITOR.u, EDITOR.p);
  const leader = login(LEADER.u, LEADER.p);

  let dbId = DB_ID;
  const databases = authedGet("/databases", editor, "setup.databases");
  if (databases.status !== 200) fail(`GET /databases: ${databases.status} ${databases.body}`);
  const list = databases.json("databases") || [];
  if (!dbId) {
    const editable = list.filter((d) => d.level === "edit");
    if (editable.length === 0) fail("tidak ada database dengan akses edit untuk akun EDITOR");
    dbId = editable[0].id;
  }

  const stamp = Date.now();
  const migrations = [];
  const requests = [];
  for (let i = 0; i < PREPARE; i++) {
    const created = authedPost(
      `/databases/${dbId}/migrations`,
      { name: `k6-stress-${stamp}-${i}`, sql: JOB_SQL, sql_down: "SELECT 1", batch_size: 0 },
      editor,
      "setup.create",
    );
    if (created.status !== 201) fail(`create migration: ${created.status} ${created.body}`);
    const mid = created.json("id");
    const submitted = authedPost(`/databases/${dbId}/migrations/${mid}/change-requests`, { kind: "up" }, editor, "setup.submit");
    if (submitted.status !== 201) fail(`submit change request: ${submitted.status} ${submitted.body}`);
    migrations.push(mid);
    requests.push(submitted.json("id"));
  }

  // Approval leader terakhir memicu dispatch ke queue. Pakai endpoint bulk
  // (maks 100 id per panggilan).
  let approved = 0;
  const failures = [];
  for (let i = 0; i < requests.length; i += 100) {
    const batch = requests.slice(i, i + 100);
    const res = authedPost(`/databases/${dbId}/change-requests/approve`, { ids: batch, comment: "k6 stress" }, leader, "setup.approve");
    if (res.status !== 200) fail(`bulk approve: ${res.status} ${res.body}`);
    for (const item of res.json("results") || []) {
      if (item.error) failures.push(`${item.id}: ${item.error}`);
      else approved++;
    }
  }
  if (approved === 0) {
    fail(`tidak ada change request yang tersetujui (leader=${LEADER.u}); pastikan LEADER_USERNAME adalah Team Leader semua group. Error: ${failures.slice(0, 3).join("; ")}`);
  }
  if (failures.length > 0) {
    console.warn(`${failures.length} pengajuan gagal disetujui: ${failures.slice(0, 3).join("; ")}`);
  }

  return { dbId, editor, leader, migrations, approved, startedAt: Date.now() };
}

export function read(data) {
  const picks = [
    () => authedGet("/databases", data.editor, "read.databases"),
    () => authedGet(`/databases/${data.dbId}/migrations`, data.editor, "read.migrations"),
    () => authedGet(`/databases/${data.dbId}/jobs`, data.editor, "read.jobs"),
    () => {
      const mid = data.migrations[Math.floor(Math.random() * data.migrations.length)];
      return authedGet(`/databases/${data.dbId}/migrations/${mid}`, data.editor, "read.detail");
    },
  ];
  const res = picks[Math.floor(Math.random() * picks.length)]();
  check(res, { "status 200": (r) => r.status === 200 });
  sleep(Math.random() * 0.5 + 0.2);
}

export function submit(data) {
  const name = `k6-submit-${__VU}-${__ITER}-${Date.now()}`;
  const created = authedPost(
    `/databases/${data.dbId}/migrations`,
    { name, sql: "SELECT 1", sql_down: "SELECT 1", batch_size: 0 },
    data.editor,
    "submit.create",
  );
  const ok = check(created, { "create 201": (r) => r.status === 201 });
  if (!ok) {
    submitFailed.add(1);
    sleep(1);
    return;
  }
  const mid = created.json("id");
  const submitted = authedPost(`/databases/${data.dbId}/migrations/${mid}/change-requests`, { kind: "up" }, data.editor, "submit.change-request");
  if (check(submitted, { "submit 201": (r) => r.status === 201 })) submitCreated.add(1);
  else submitFailed.add(1);
  sleep(1);
}

// Kumpulkan bukti eksekusi dari queue: waktu tunggu, durasi job, sisa job.
export function teardown(data) {
  const deadline = Date.now() + Number(__ENV.DRAIN_TIMEOUT_MS || 180000);
  let pending = 0;
  let drained = false;
  while (Date.now() < deadline) {
    const res = authedGet(`/databases/${data.dbId}/jobs`, data.editor, "teardown.jobs");
    if (res.status !== 200) break;
    const jobs = res.json("jobs") || [];
    pending = jobs.filter((j) => ["queued", "running", "uncertain"].includes(j.status)).length;
    if (pending === 0) {
      drained = true;
      for (const job of jobs) {
        if (!job.started_at) continue;
        jobWait.add(new Date(job.started_at) - new Date(job.created_at));
        if (job.finished_at) jobDuration.add(new Date(job.finished_at) - new Date(job.started_at));
      }
      break;
    }
    sleep(1);
  }
  const summary = {
    database: data.dbId,
    prepared_migrations: data.migrations.length,
    approved: data.approved,
    queue_drained: drained,
    pending_jobs_at_end: pending,
    total_elapsed_ms: Date.now() - data.startedAt,
  };
  console.log(`K6_SUMMARY ${JSON.stringify(summary)}`);
  if (!drained) console.warn("queue belum kosong sampai batas waktu; naikkan DRAIN_TIMEOUT_MS");
}
