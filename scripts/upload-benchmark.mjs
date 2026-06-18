import http from 'node:http';
import crypto from 'node:crypto';

const TOKEN = process.env.TOKEN || 'dev-token-admin';
const CHUNK = 8 * 1024 * 1024; // 8 MiB — matches frontend resumableUploadFileBlockSize
const MB = 1024 * 1024;

// Targets: [label, baseURL]. frontend = full web path (nginx SPA -> LB -> region -> minio).
// region  = server-direct (region API -> minio), isolates server+S3 from proxy hops.
const TARGETS = [
  ['web (frontend→LB→region)', process.env.WEB_BASE || 'http://frontend'],
  ['server-direct (region)', process.env.REGION_BASE || 'http://sesamefs-usa:8080'],
];

const SIZES_MB = (process.env.SIZES_MB || '1,8,16,32,64,128,256,512,1024').split(',').map(Number);
const SINGLE_MAX_MB = Number(process.env.SINGLE_MAX_MB || 256); // single-shot only up to here

function req(base, method, path, { headers = {}, body = null } = {}) {
  return new Promise((resolve, reject) => {
    const u = new URL(base + path);
    const r = http.request(
      { method, hostname: u.hostname, port: u.port || 80, path: u.pathname + u.search, headers },
      (res) => {
        const chunks = [];
        res.on('data', (d) => chunks.push(d));
        res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, body: Buffer.concat(chunks) }));
      },
    );
    r.on('error', reject);
    if (body) r.write(body);
    r.end();
  });
}

function multipart(fields, file) {
  const boundary = '----bench' + crypto.randomBytes(8).toString('hex');
  const parts = [];
  for (const [k, v] of Object.entries(fields)) {
    parts.push(Buffer.from(`--${boundary}\r\nContent-Disposition: form-data; name="${k}"\r\n\r\n${v}\r\n`));
  }
  parts.push(Buffer.from(
    `--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${file.filename}"\r\n` +
    `Content-Type: application/octet-stream\r\n\r\n`));
  parts.push(file.data);
  parts.push(Buffer.from(`\r\n--${boundary}--\r\n`));
  return { boundary, body: Buffer.concat(parts) };
}

const authJSON = { Authorization: 'Token ' + TOKEN, 'Content-Type': 'application/json' };

async function getRepo(base) {
  // Reuse an existing library (org has a 3-library cap); only create if none exist.
  const list = await req(base, 'GET', '/api2/repos/', { headers: { Authorization: 'Token ' + TOKEN } });
  if (list.status === 200) {
    const arr = JSON.parse(list.body.toString());
    if (Array.isArray(arr) && arr.length) return arr[0].id || arr[0].repo_id;
  }
  const r = await req(base, 'POST', '/api/v2.1/repos/', {
    headers: authJSON, body: Buffer.from(JSON.stringify({ name: 'bench-' + Date.now() })),
  });
  if (r.status < 200 || r.status >= 300) throw new Error('getRepo ' + r.status + ': ' + r.body.toString().slice(0, 200));
  const j = JSON.parse(r.body.toString());
  return j.repo_id || j.id || j.repo?.id;
}

async function getUploadPath(base, repoId) {
  const r = await req(base, 'GET', `/api2/repos/${repoId}/upload-link/?p=/`, { headers: { Authorization: 'Token ' + TOKEN } });
  if (r.status !== 200) throw new Error('upload-link ' + r.status + ': ' + r.body.toString().slice(0, 200));
  const url = JSON.parse(r.body.toString());           // JSON-encoded string
  const token = url.split('/upload-api/')[1];
  return '/seafhttp/upload-api/' + token;               // re-anchor to current base
}

async function chunkedUpload(base, repoId, sizeMB) {
  const total = sizeMB * MB;
  const uploadPath = await getUploadPath(base, repoId);
  const filename = `chunked-${sizeMB}mb-${crypto.randomBytes(4).toString('hex')}.bin`;
  const nChunks = Math.ceil(total / CHUNK);
  let lastMs = 0, firstMs = 0;
  const t0 = Date.now();
  for (let i = 0; i < nChunks; i++) {
    const start = i * CHUNK;
    const end = Math.min(start + CHUNK, total) - 1;
    const data = crypto.randomBytes(end - start + 1); // unique bytes => no dedup
    const { boundary, body } = multipart({ parent_dir: '/', 'ret-json': '1' }, { filename, data });
    const ct0 = Date.now();
    const res = await req(base, 'POST', uploadPath, {
      headers: {
        Authorization: 'Token ' + TOKEN,
        'Content-Type': 'multipart/form-data; boundary=' + boundary,
        'Content-Range': `bytes ${start}-${end}/${total}`,
        'Content-Length': body.length,
      }, body,
    });
    const cms = Date.now() - ct0;
    if (i === 0) firstMs = cms;
    if (i === nChunks - 1) lastMs = cms;
    if (res.status !== 200) throw new Error(`chunk ${i}/${nChunks} status ${res.status}: ${res.body.toString().slice(0, 200)}`);
  }
  const totalMs = Date.now() - t0;
  return { totalMs, lastMs, firstMs, nChunks, mbps: (sizeMB / (totalMs / 1000)) };
}

async function singleShot(base, repoId, sizeMB) {
  const total = sizeMB * MB;
  const uploadPath = await getUploadPath(base, repoId);
  const filename = `single-${sizeMB}mb-${crypto.randomBytes(4).toString('hex')}.bin`;
  const data = crypto.randomBytes(total);
  const { boundary, body } = multipart({ parent_dir: '/', 'ret-json': '1' }, { filename, data });
  const t0 = Date.now();
  const res = await req(base, 'POST', uploadPath, {
    headers: {
      Authorization: 'Token ' + TOKEN,
      'Content-Type': 'multipart/form-data; boundary=' + boundary,
      'Content-Length': body.length,
    }, body,
  });
  const totalMs = Date.now() - t0;
  if (res.status !== 200) throw new Error(`single ${sizeMB}MB status ${res.status}: ${res.body.toString().slice(0, 200)}`);
  return { totalMs, mbps: (sizeMB / (totalMs / 1000)) };
}

function pad(s, n) { s = String(s); return s + ' '.repeat(Math.max(0, n - s.length)); }

// Concurrency probe: run N simultaneous chunked uploads of SIZE MB and report
// aggregate throughput. If the server serializes block materialization globally,
// aggregate MB/s stays flat as N rises (no scaling).
if (process.env.CONC_TEST === '1') {
  const base = process.env.REGION_BASE || 'http://sesamefs-usa:8080';
  const size = Number(process.env.CONC_SIZE_MB || 64);
  const repoId = await getRepo(base);
  console.log(`\n==== CONCURRENCY PROBE (chunked ${size}MB each, target=${base}, repo=${repoId}) ====`);
  console.log('  ' + pad('parallel', 10) + pad('wall(s)', 10) + pad('agg MB/s', 10) + 'per-upload MB/s');
  for (const N of (process.env.CONC_LEVELS || '1,2,4,8').split(',').map(Number)) {
    const t0 = Date.now();
    const results = await Promise.all(Array.from({ length: N }, () => chunkedUpload(base, repoId, size).catch((e) => ({ err: e.message }))));
    const wall = (Date.now() - t0) / 1000;
    const ok = results.filter((r) => !r.err);
    const agg = (size * ok.length) / wall;
    const per = ok.length ? (ok.reduce((s, r) => s + r.mbps, 0) / ok.length) : 0;
    console.log('  ' + pad(N, 10) + pad(wall.toFixed(2), 10) + pad(agg.toFixed(1), 10) + per.toFixed(1) + (ok.length < N ? `  (${N - ok.length} failed)` : ''));
  }
  console.log('DONE');
  process.exit(0);
}

for (const [label, base] of TARGETS) {
  console.log(`\n================ TARGET: ${label}  (${base}) ================`);
  let repoId;
  try { repoId = await getRepo(base); } catch (e) { console.log('  SKIP — createRepo failed:', e.message); continue; }
  console.log('  repo:', repoId);

  console.log('\n  CHUNKED (8MiB blocks, sequential — the web large-file path):');
  console.log('  ' + pad('size', 8) + pad('chunks', 8) + pad('total(s)', 10) + pad('MB/s', 9) + pad('1st chunk(ms)', 15) + 'last/finalize(ms)');
  for (const mb of SIZES_MB) {
    try {
      const r = await chunkedUpload(base, repoId, mb);
      console.log('  ' + pad(mb + 'MB', 8) + pad(r.nChunks, 8) + pad((r.totalMs / 1000).toFixed(2), 10) +
        pad(r.mbps.toFixed(1), 9) + pad(r.firstMs, 15) + r.lastMs);
    } catch (e) { console.log('  ' + pad(mb + 'MB', 8) + 'FAIL: ' + e.message); }
  }

  console.log('\n  SINGLE-SHOT (whole file one request, one block — small-file path / API):');
  console.log('  ' + pad('size', 8) + pad('total(s)', 10) + pad('MB/s', 9));
  for (const mb of SIZES_MB.filter((m) => m <= SINGLE_MAX_MB)) {
    try {
      const r = await singleShot(base, repoId, mb);
      console.log('  ' + pad(mb + 'MB', 8) + pad((r.totalMs / 1000).toFixed(2), 10) + pad(r.mbps.toFixed(1), 9));
    } catch (e) { console.log('  ' + pad(mb + 'MB', 8) + 'FAIL: ' + e.message); }
  }
}
console.log('\nDONE');
