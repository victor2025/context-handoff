// client/handoff.js
// Thin HTTP client for the handoff server. Uses global fetch (Node >= 20).
export const DEFAULT_SERVER = 'http://127.0.0.1:8765';

export function resolveServer(explicit) {
  const s = explicit || process.env.CONTEXT_HANDOFF_SERVER || DEFAULT_SERVER;
  return s.replace(/\/+$/, '');
}

async function parse(res) {
  const text = await res.text();
  let json = null;
  try { json = text ? JSON.parse(text) : null; } catch { /* non-json */ }
  if (!res.ok) {
    const code = json?.error?.code || `HTTP_${res.status}`;
    const message = json?.error?.message || res.statusText;
    const err = new Error(`${code}: ${message}`);
    err.code = code;
    err.status = res.status;
    throw err;
  }
  return json;
}

export async function health(server) {
  const res = await fetch(`${server}/healthz`);
  return parse(res);
}

export async function saveContext(server, { payload, tokenHash, ttl, title, format, metadata }) {
  const body = { payload, tokenHash };
  if (ttl != null) body.ttl = ttl;
  if (title != null) body.title = title;
  if (format != null) body.format = format;
  if (metadata != null) body.metadata = metadata;
  const res = await fetch(`${server}/api/contexts`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body)
  });
  return parse(res);
}

export async function readCiphertext(server, id) {
  const res = await fetch(`${server}/api/contexts/${id}`);
  return parse(res);
}

export async function readPlaintext(server, id, token) {
  const res = await fetch(`${server}/api/contexts/${id}/load`, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({ token })
  });
  return parse(res);
}
