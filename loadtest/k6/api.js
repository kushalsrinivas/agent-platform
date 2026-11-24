// k6 public API load test. Run against a dev-auth Compose stack with a raised API rate limit:
//
//	API_RATE_LIMIT_PER_MINUTE=100000 docker compose up -d api
//	k6 run loadtest/k6/api.js
//
// Environment: API_URL (default http://localhost:8080), WORKER_POOL (default default).
import http from 'k6/http';
import { check, sleep } from 'k6';
import { randomIntBetween } from 'https://jslib.k6.io/k6-utils/1.4.0/index.js';

const API = (__ENV.API_URL || 'http://localhost:8080').replace(/\/$/, '');
const POOL = __ENV.WORKER_POOL || 'default';

export const options = {
  scenarios: {
    api: {
      executor: 'ramping-vus',
      startVUs: 0,
      stages: [
        { duration: __ENV.RAMP || '30s', target: Number(__ENV.VUS || 20) },
        { duration: __ENV.STEADY || '2m', target: Number(__ENV.VUS || 20) },
        { duration: '15s', target: 0 },
      ],
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.02'],
    http_req_duration: ['p(95)<500', 'p(99)<1500'],
  },
};

function jsonHeaders(token) {
  const headers = { 'Content-Type': 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  return headers;
}

function must(response, label) {
  const ok = check(response, { [`${label} status ${response.status}`]: (r) => r.status >= 200 && r.status < 300 });
  if (!ok) throw new Error(`${label} failed: ${response.status} ${response.body}`);
  return response.json();
}

export function setup() {
  const suffix = `${Date.now().toString(36)}${randomIntBetween(100, 999)}`;
  const token = must(http.post(`${API}/v1/dev/token`, JSON.stringify({ principal_id: `k6_${suffix}` }), {
    headers: jsonHeaders(),
  }), 'dev token').access_token;

  const org = must(http.post(`${API}/v1/orgs`, JSON.stringify({ name: 'k6 Org', slug: `k6-${suffix}` }), {
    headers: jsonHeaders(token),
  }), 'org');

  const project = must(http.post(`${API}/v1/orgs/${org.id}/projects`,
    JSON.stringify({ name: 'k6 Project', slug: 'k6-project' }), { headers: jsonHeaders(token) }), 'project');

  const agent = must(http.post(`${API}/v1/orgs/${org.id}/projects/${project.id}/agents`, JSON.stringify({
    name: 'k6-agent',
    version: {
      spec: {
        model: { provider: 'echo', name: 'echo-1', credential_ref: 'default' },
        instructions: 'Return the input.',
        runtime: { abi_version: 1, image_digest: `sha256:${'a'.repeat(64)}` },
        limits: { max_turns: 4, timeout_seconds: 120 },
        checkpoint: { mode: 'on_node_end', require_checkpoint: false },
      },
    },
  }), { headers: jsonHeaders(token) }), 'agent');

  must(http.post(`${API}/v1/orgs/${org.id}/projects/${project.id}/agents/${agent.id}/versions/1:publish`, null,
    { headers: jsonHeaders(token) }), 'publish agent');

  const workflow = must(http.post(`${API}/v1/orgs/${org.id}/projects/${project.id}/workflows`, JSON.stringify({
    name: 'k6-flow',
    version: {
      graph: {
        nodes: [{
          id: 'agent',
          type: 'agent',
          agent: { agent_id: agent.id, version: 1 },
          worker_pool: POOL,
          timeout_seconds: 120,
        }],
        edges: [],
        policy: { on_failure: 'fail_fast' },
        input_schema: { type: 'object', required: ['topic'], properties: { topic: { type: 'string' } } },
      },
    },
  }), { headers: jsonHeaders(token) }), 'workflow');

  must(http.post(`${API}/v1/orgs/${org.id}/projects/${project.id}/workflows/${workflow.id}/versions/1:publish`, null,
    { headers: jsonHeaders(token) }), 'publish workflow');

  const execution = must(http.post(`${API}/v1/orgs/${org.id}/projects/${project.id}/executions`, JSON.stringify({
    workflow_id: workflow.id,
    workflow_version: 1,
    input: { topic: 'k6 seed' },
  }), { headers: { ...jsonHeaders(token), 'Idempotency-Key': `k6-seed-${suffix}` } }), 'seed execution');

  return { token, orgID: org.id, projectID: project.id, workflowID: workflow.id, executionID: execution.id, suffix };
}

export default function (data) {
  const base = `${API}/v1/orgs/${data.orgID}/projects/${data.projectID}`;
  const headers = jsonHeaders(data.token);
  const roll = randomIntBetween(1, 100);
  let response;

  if (roll <= 60) {
    response = http.get(`${base}/executions?limit=20`, { headers, tags: { endpoint: 'list_executions' } });
  } else if (roll <= 95) {
    response = http.get(`${base}/executions/${data.executionID}`, { headers, tags: { endpoint: 'get_execution' } });
  } else {
    response = http.post(`${base}/executions`, JSON.stringify({
      workflow_id: data.workflowID,
      workflow_version: 1,
      input: { topic: `k6 ${__VU}-${__ITER}` },
    }), {
      headers: { ...headers, 'Idempotency-Key': `k6-${data.suffix}-${__VU}-${__ITER}` },
      tags: { endpoint: 'create_execution' },
    });
  }

  check(response, {
    'status is successful': (r) => r.status >= 200 && r.status < 300 && r.status !== 429,
  });
  sleep(Math.random() * 0.3 + 0.1);
}
