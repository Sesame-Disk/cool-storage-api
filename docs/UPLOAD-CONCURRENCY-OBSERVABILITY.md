# Upload Concurrency Observability

This runbook covers the Prometheus metrics added around upload finalization contention and retry behavior.

## Scrape scope

- SesameFS exposes `/metrics` only on the internal listener path.
- Scrape from the node itself, from a private network, or through `docker compose exec sesamefs ...` as described in [DEPLOY](./DEPLOY.md).

## Metrics to watch

### `chunk_upload_finalization_attempts_total{result=...}`

Counts how chunked uploads interact with the in-memory finalization gate.

- `started`: the current request won the gate and will run finalize.
- `not_complete`: a normal intermediate chunk arrived before the upload was complete.
- `already_finalizing`: a second request arrived after another goroutine had already started finalize.

Operationally, `already_finalizing` is the contention signal. `not_complete` is expected background traffic and should not be alerted on by itself.

### `upload_finalize_head_conflicts_total{surface=...}`

Counts metadata publish conflicts that forced a retry.

Current surfaces:

- `seafhttp_single`
- `seafhttp_multiblock`
- `v2_direct`

### `upload_finalize_retry_exhausted_total{surface=...}`

Counts finalize operations that spent the full retry budget and returned a retryable `409` conflict to the client.

### `upload_finalize_attempts_{bucket,sum,count}{surface,result}`

Histogram of how many publish attempts were needed per finalize call.

Useful `result` values:

- `success`
- `quota_exceeded`
- `retry_exhausted`
- `error`

### `upload_finalize_duration_seconds_{bucket,sum,count}{surface,result}`

Histogram of finalize latency, including CAS retries and backoff.

## Suggested dashboard panels

### Finalize conflicts by surface

```promql
sum by (surface) (
  rate(upload_finalize_head_conflicts_total[5m])
)
```

### Retry exhaustion by surface

```promql
sum by (surface) (
  increase(upload_finalize_retry_exhausted_total[15m])
)
```

### Finalize p95 latency by surface

```promql
histogram_quantile(
  0.95,
  sum by (le, surface) (
    rate(upload_finalize_duration_seconds_bucket{result="success"}[5m])
  )
)
```

### Average finalize attempts by surface

```promql
sum by (surface) (
  rate(upload_finalize_attempts_sum{result="success"}[5m])
)
/
clamp_min(
  sum by (surface) (
    rate(upload_finalize_attempts_count{result="success"}[5m])
  ),
  1
)
```

### Chunk finalize gate contention

```promql
rate(chunk_upload_finalization_attempts_total{result="already_finalizing"}[5m])
```

### Chunk finalize gate background volume

```promql
rate(chunk_upload_finalization_attempts_total{result="not_complete"}[5m])
```

This is useful for capacity context, but it is not a contention alert by itself.

## Suggested alerts

### Upload finalize retry exhaustion

Trigger when any surface starts exhausting the finalize retry budget.

```yaml
groups:
  - name: sesamefs-upload-concurrency
    rules:
      - alert: SesameFSUploadFinalizeRetryExhausted
        expr: increase(upload_finalize_retry_exhausted_total[10m]) > 0
        for: 5m
        labels:
          severity: warning
        annotations:
          summary: "Upload finalize exhausted retry budget"
          description: "A SesameFS upload finalize path returned a retryable 409 after exhausting metadata publish retries."
```

### Sustained finalize conflict rate

Trigger when conflict pressure is persistent relative to successful finalizations.

```yaml
groups:
  - name: sesamefs-upload-concurrency
    rules:
      - alert: SesameFSUploadFinalizeConflictRateHigh
        expr: |
          (
            sum(rate(upload_finalize_head_conflicts_total[10m]))
            /
            clamp_min(sum(rate(upload_finalize_attempts_count{result="success"}[10m])), 1)
          ) > 0.10
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Upload finalize conflict rate is high"
          description: "More than 10% as many finalize head conflicts as successful finalizations over the last 10 minutes."
```

### Finalize latency regression

Trigger when finalize latency rises even if retries are still succeeding.

```yaml
groups:
  - name: sesamefs-upload-concurrency
    rules:
      - alert: SesameFSUploadFinalizeLatencyP95High
        expr: |
          histogram_quantile(
            0.95,
            sum by (le) (
              rate(upload_finalize_duration_seconds_bucket{result="success"}[10m])
            )
          ) > 5
        for: 15m
        labels:
          severity: warning
        annotations:
          summary: "Upload finalize p95 latency is elevated"
          description: "Successful upload finalize operations have p95 latency above 5 seconds for 15 minutes."
```

## How to read the signals together

- `already_finalizing` rising without `upload_finalize_head_conflicts_total` usually means more overlap at the chunk gate, not necessarily metadata publish failure.
- `upload_finalize_head_conflicts_total` rising with stable latency usually means retries are absorbing contention.
- `upload_finalize_retry_exhausted_total` above zero means users are now seeing retryable `409` responses and client retry behavior matters.
- High `not_complete` with low `already_finalizing` is usually just heavy chunked upload traffic.

## Client retry behavior on `409`

The retryable `409` response is handled differently across clients. Knowing
which surface is generating retries matters when reading the metrics.

- **Web frontend** (`frontend/src/components/file-uploader`, `shared-link-file-uploader`,
  `pages/upload-link`): performs **one** auto-retry per file when it sees `409`
  or a matching error string, gated by a per-file `finalizeConflictAutoRetried`
  flag. Subsequent `409` responses surface to the user as a manual retry prompt.
- **Mobile frontend** (`mobile-frontend/src/lib/upload.ts`): the generic upload
  retry loop reattempts any failure up to `maxRetries`, regardless of status
  code. A sustained `409` window therefore produces up to `maxRetries`
  reattempts per file from mobile clients, not just one.

If `upload_finalize_retry_exhausted_total` stays elevated and the bulk of
traffic is mobile, expect more amplification than the web baseline alone
would suggest.