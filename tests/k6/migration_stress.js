import http from 'k6/http';
import { check, sleep } from 'k6';
import exec from 'k6/execution';
import { SharedArray } from 'k6/data';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const PASSWORD = __ENV.K6_PASSWORD || 'k6-password-123';
const FIXTURES = __ENV.FIXTURES || './fixtures.json';
const PROFILE = __ENV.PROFILE || 'stress';
const POLL_K = 5;
// Keeps multi_db total arrival rate equal to same_db.
const MULTI_PACING_FACTOR = Number(__ENV.MULTI_PACING_FACTOR || 4);
const DRAIN_TIMEOUT_S = Number(__ENV.DRAIN_TIMEOUT_S || 300);
const TERMINAL = ['success', 'failed', 'partial', 'partially_reverted', 'uncertain'];

const meta = JSON.parse(open(FIXTURES));
const dbs = new SharedArray('databases', () => JSON.parse(open(FIXTURES)).databases);

// pacing null: poll only.
const PROFILES = {
  stress: {
    phases: [
      { stage: 'warmup', until: 60, pacing: 1.0 },
      { stage: 'ramp', until: 150, pacing: 0.35 },
      { stage: 'peak', until: 240, pacing: 0.15 },
      { stage: 'drain', until: 330, pacing: null },
      { stage: 'cooldown', until: 360, pacing: null },
    ],
    readers: [
      { duration: '60s', target: 10 },
      { duration: '90s', target: 30 },
      { duration: '90s', target: 60 },
      { duration: '90s', target: 10 },
      { duration: '30s', target: 0 },
    ],
  },
  baseline: {
    phases: [
      { stage: 'warmup', until: 60, pacing: 1.0 },
      { stage: 'drain', until: 90, pacing: null },
    ],
    readers: [
      { duration: '10s', target: 5 },
      { duration: '70s', target: 5 },
      { duration: '10s', target: 0 },
    ],
  },
};
const profile = PROFILES[PROFILE];
if (!profile) {
  throw new Error(`unknown PROFILE ${PROFILE}`);
}
const submitEnd = profile.phases[profile.phases.length - 1].until;

const apiErrorRate = new Rate('api_error_rate');
const queueWait = new Trend('migration_queue_wait_ms', true);
const execDuration = new Trend('migration_exec_duration_ms', true);
const completionRate = new Rate('migration_job_completion_rate');
const completedTotal = new Counter('migration_jobs_completed_total');
const finishedByStage = new Counter('migration_jobs_finished_by_stage');
const approveConflicts = new Counter('approve_conflicts_total');

const scenarios = {
  readers: {
    executor: 'ramping-vus',
    startVUs: 0,
    stages: profile.readers,
    gracefulRampDown: '10s',
    exec: 'reader',
  },
};
dbs.forEach((db, i) => {
  scenarios[`submit_${String.fromCharCode(97 + i)}`] = {
    executor: 'constant-vus',
    vus: 1,
    duration: `${submitEnd}s`,
    gracefulStop: '30s',
    exec: 'submitter',
    env: { DB_INDEX: String(i) },
    tags: { pool: db.pool },
  };
});

// Only exposes the submetrics in the summary.
const breakdown = {};
for (const p of profile.phases) {
  breakdown[`http_req_duration{kind:api,stage:${p.stage}}`] = ['max>=0'];
  breakdown[`http_reqs{kind:api,stage:${p.stage}}`] = ['count>=0'];
  breakdown[`api_error_rate{stage:${p.stage}}`] = ['rate>=0'];
  for (const pool of ['same_db', 'multi_db']) {
    breakdown[`migration_queue_wait_ms{pool:${pool},stage:${p.stage}}`] = ['max>=0'];
    breakdown[`migration_jobs_finished_by_stage{pool:${pool},stage:${p.stage}}`] = ['count>=0'];
  }
}

export const options = {
  scenarios,
  setupTimeout: '60s',
  teardownTimeout: `${DRAIN_TIMEOUT_S + 120}s`,
  summaryTrendStats: ['avg', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'count'],
  thresholds: Object.assign(
    {
      'http_req_duration{kind:api}': ['p(95)<500'],
      'http_req_duration{kind:poll}': ['max>=0'],
      api_error_rate: ['rate<0.01'],
      'migration_job_completion_rate{pool:multi_db}': ['rate>0.99'],
      'migration_job_completion_rate{pool:same_db}': ['rate>0.99'],
      'migration_queue_wait_ms{pool:multi_db}': ['p(95)<3000'],
      'migration_queue_wait_ms{pool:same_db}': ['p(95)<30000'],
      'migration_exec_duration_ms{pool:multi_db}': ['max>=0'],
      'migration_exec_duration_ms{pool:same_db}': ['max>=0'],
      'migration_jobs_completed_total{pool:multi_db}': ['count>=0'],
      'migration_jobs_completed_total{pool:same_db}': ['count>=0'],
    },
    breakdown,
  ),
};

function elapsedS() {
  return (Date.now() - exec.scenario.startTime) / 1000;
}

function stageSeconds(stage) {
  const i = profile.phases.findIndex((p) => p.stage === stage);
  return i < 0 ? null : profile.phases[i].until - (i > 0 ? profile.phases[i - 1].until : 0);
}

function phaseAt(t) {
  return profile.phases.find((p) => t < p.until) || profile.phases[profile.phases.length - 1];
}

// The jar drops Secure cookies over HTTP.
function login(username, kind) {
  const res = http.post(
    `${BASE_URL}/api/v1/auth/login`,
    JSON.stringify({ username, password: PASSWORD }),
    { headers: { 'Content-Type': 'application/json' }, tags: { kind: kind || 'auth', name: 'POST /auth/login' } },
  );
  if (!check(res, { 'login 200': (r) => r.status === 200 })) {
    throw new Error(`login ${username} failed: ${res.status} ${res.body}`);
  }
  return { username, sid: res.cookies.sid[0].value, csrf: res.json('csrf_token') };
}

function params(session, tags, extra) {
  return Object.assign(
    { cookies: { sid: { value: session.sid, replace: true } }, headers: { 'X-CSRF-Token': session.csrf }, tags },
    extra,
  );
}

function track(session, res) {
  const c = res.cookies.sid;
  if (c && c.length && c[0].value) {
    session.sid = c[0].value;
  }
  if (res.status === 401) {
    session.sid = null;
  }
}

function ms(a, b) {
  return a && b ? Date.parse(b) - Date.parse(a) : null;
}

const sub = { session: null, next: 0, inflight: [] };

function approve(db, phase) {
  const pid = db.proposals[sub.next];
  const res = http.post(
    `${BASE_URL}/api/v1/databases/${db.id}/proposals/${pid}/approve`,
    null,
    params(sub.session, { kind: 'api', name: 'POST /proposals/{pid}/approve' }),
  );
  track(sub.session, res);
  apiErrorRate.add(res.status !== 200, { kind: 'api' });
  if (res.status === 200 && res.json('enqueued') === true) {
    sub.inflight.push({ id: res.json('job_id'), stage: phase.stage });
    sub.next++;
    return;
  }
  // Already enqueued by an earlier attempt.
  if (res.status === 409 && res.json('error.code') === 'CONFLICT') {
    sub.next++;
  }
  if (res.status === 409) {
    approveConflicts.add(1);
  }
}

function pollOldest(db) {
  if (sub.inflight.length === 0) {
    return;
  }
  const batch = sub.inflight.slice(0, POLL_K).map((j) => ({
    method: 'GET',
    url: `${BASE_URL}/api/v1/databases/${db.id}/jobs/${j.id}`,
    params: params(sub.session, { kind: 'poll', name: 'GET /jobs/{jobID}' }),
  }));
  const responses = http.batch(batch);
  const done = new Set();
  responses.forEach((res, i) => {
    track(sub.session, res);
    if (res.status !== 200) {
      return;
    }
    const job = res.json();
    if (!TERMINAL.includes(job.status)) {
      return;
    }
    const j = sub.inflight[i];
    const tags = { pool: db.pool, stage: j.stage };
    const wait = ms(job.queued_at, job.started_at);
    const run = ms(job.started_at, job.finished_at);
    if (wait !== null) {
      queueWait.add(wait, tags);
    }
    if (run !== null) {
      execDuration.add(run, tags);
    }
    if (job.status === 'success') {
      completedTotal.add(1, { pool: db.pool });
    }
    done.add(j.id);
  });
  sub.inflight = sub.inflight.filter((j) => !done.has(j.id));
}

export function submitter() {
  const db = dbs[Number(__ENV.DB_INDEX)];
  if (!sub.session || !sub.session.sid) {
    sub.session = login(meta.approver);
  }
  const started = Date.now();
  const phase = phaseAt(elapsedS());
  exec.vu.metrics.tags.stage = phase.stage;

  let pacing = 0.5;
  if (phase.pacing !== null && sub.next < db.proposals.length) {
    pacing = db.pool === 'multi_db' ? phase.pacing * MULTI_PACING_FACTOR : phase.pacing;
    approve(db, phase);
  }
  pollOldest(db);
  sleep(Math.max(0, pacing - (Date.now() - started) / 1000));
}

let readerSession = null;

export function reader() {
  if (!readerSession || !readerSession.sid) {
    readerSession = login(meta.reader);
  }
  exec.vu.metrics.tags.stage = phaseAt(elapsedS()).stage;
  const db = dbs[Math.floor(Math.random() * dbs.length)];
  const roll = Math.random();
  let path;
  let name;
  if (roll < 0.3) {
    [path, name] = ['/auth/me', 'GET /auth/me'];
  } else if (roll < 0.55) {
    [path, name] = ['/databases', 'GET /databases'];
  } else if (roll < 0.8) {
    [path, name] = [`/databases/${db.id}/overview`, 'GET /databases/{id}/overview'];
  } else {
    [path, name] = [`/databases/${db.id}/history`, 'GET /databases/{id}/history'];
  }
  const res = http.get(`${BASE_URL}/api/v1${path}`, params(readerSession, { kind: 'api', name }));
  track(readerSession, res);
  apiErrorRate.add(res.status !== 200, { kind: 'api' });
  sleep(0.5 + Math.random() * 0.5);
}

function loadHistory(session, db) {
  const res = http.get(
    `${BASE_URL}/api/v1/databases/${db.id}/history`,
    params(session, { kind: 'teardown', name: 'GET /databases/{id}/history' }, { timeout: '60s' }),
  );
  track(session, res);
  if (res.status !== 200) {
    throw new Error(`history ${db.name}: ${res.status}`);
  }
  return res.json('executions').filter((e) => e.action_type === 'apply');
}

export function setup() {
  return { start: Date.now() };
}

export function teardown(data) {
  const session = login(meta.approver, 'teardown');
  const deadline = Date.now() + DRAIN_TIMEOUT_S * 1000;
  let histories;
  for (;;) {
    histories = dbs.map((db) => loadHistory(session, db));
    const pending = histories.flat().filter((e) => e.status === 'queued' || e.status === 'running').length;
    if (pending === 0 || Date.now() > deadline) {
      if (pending > 0) {
        console.warn(`teardown: ${pending} jobs still queued or running after ${DRAIN_TIMEOUT_S}s`);
      }
      break;
    }
    sleep(2);
  }

  const pools = {};
  dbs.forEach((db, i) => {
    const execs = histories[i];
    const ok = execs.filter((e) => e.status === 'success');
    execs.forEach((e) => completionRate.add(e.status === 'success', { pool: db.pool }));

    const p = (pools[db.pool] = pools[db.pool] || { submitted: 0, success: 0, first: Infinity, last: 0 });
    p.submitted += execs.length;
    p.success += ok.length;
    for (const e of ok) {
      const stage = phaseAt((Date.parse(e.finished_at) - data.start) / 1000).stage;
      finishedByStage.add(1, { pool: db.pool, stage });
      p.first = Math.min(p.first, Date.parse(e.created_at));
      p.last = Math.max(p.last, Date.parse(e.finished_at));
    }
    console.log(`teardown: ${db.name} (${db.pool}) submitted=${execs.length} success=${ok.length}`);
  });
  for (const [pool, p] of Object.entries(pools)) {
    const span = (p.last - p.first) / 1000;
    const rate = span > 0 ? (p.success / span).toFixed(2) : 'n/a';
    console.log(`teardown: pool ${pool} submitted=${p.submitted} success=${p.success} throughput=${rate} jobs/s over ${span.toFixed(1)}s`);
  }
}

function fmtTrend(v) {
  if (!v) {
    return 'n/a';
  }
  const f = (x) => (x === undefined ? '-' : `${x.toFixed(1)}`);
  return `avg=${f(v.avg)} med=${f(v.med)} p95=${f(v['p(95)'])} p99=${f(v['p(99)'])} max=${f(v.max)} n=${v.count}`;
}

export function handleSummary(data) {
  const m = data.metrics;
  const lines = [`profile ${PROFILE}`];
  const keys = Object.keys(m).sort();
  for (const k of keys) {
    const v = m[k].values;
    let text;
    if (m[k].type === 'trend') {
      text = fmtTrend(v);
    } else if (m[k].type === 'rate') {
      text = `rate=${(v.rate * 100).toFixed(2)}% (${v.passes}/${v.passes + v.fails})`;
    } else if (m[k].type === 'counter') {
      const stage = /stage:(\w+)/.exec(k);
      const secs = stage ? stageSeconds(stage[1]) : null;
      text = secs ? `count=${v.count} rate=${(v.count / secs).toFixed(2)}/s over ${secs}s` : `count=${v.count} rate=${v.rate.toFixed(2)}/s`;
    } else {
      continue;
    }
    const failed = m[k].thresholds && Object.values(m[k].thresholds).some((t) => !t.ok);
    lines.push(`${failed ? 'FAIL' : '    '} ${k}: ${text}`);
  }
  const out = { stdout: lines.join('\n') + '\n' };
  if (__ENV.SUMMARY_JSON) {
    out[__ENV.SUMMARY_JSON] = JSON.stringify(data, null, 2);
  }
  return out;
}
