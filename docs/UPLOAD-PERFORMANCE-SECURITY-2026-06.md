# Upload Performance & Security Audit — 2026-06

Auditoría completa del path de upload chunked. Cada hallazgo incluye veredicto
verificado contra código fuente, severidad, y rama de fix recomendada.

## P-1 — Permit serializa el S3 PUT (RESUELTO en esta rama)

**Severidad: CRÍTICA**  
**Rama: fix/upload-permit-unwrap-s3-put**

`finalizeUploadBlockMetadataConcurrency = 1` crea un semáforo de 1 slot
(`seafhttp.go`). El código anterior adquiría ese permit _antes_ de
`retrySeafHTTPBlockMaterialization`, lo que incluía dentro del semáforo:

1. `putUploadedBlockAutoFn` → S3 Exists + S3 PUT
2. `registerUploadedBlockAndMappingForUploadFn` → Cassandra LWT
3. `clearSeafHTTPS3OrphanFenceFn` → Cassandra fence check

El comment del código describía la intención correcta ("Keep block PUTs parallel,
but serialize IncrementOrCreateBlock"), pero la implementación la violaba. Los 8
workers de `finalizeUploadConcurrency` quedaban sin efecto: todos bloqueaban en
el único slot.

**Fix aplicado:** permit movido al interior del callback `materialize`
(el LWT de Cassandra). S3 Exists + S3 PUT ahora corren en paralelo en los 8
workers. Fence check no necesita el permit.

**Impacto esperado:** 5–8× throughput local (MinIO); mayor en S3 real por RTT.

---

## P-2 — 2 S3 round-trips por bloque (Exists/HEAD + PUT)

**Severidad: ALTA mientras P-1 esté activo; MEDIA después**  
**Rama pendiente**

`PutBlockAuto` en `internal/storage/blocks.go:100–119` hace:
```go
exists, err := bs.s3.Exists(ctx, key)   // HEAD — round-trip 1
// si no existe:
_, err = bs.s3.PutAuto(ctx, key, reader, size)  // PUT — round-trip 2
```

Mismo patrón en `PutBlock` y `PutBlockData`. Con P-1 activo, ambos RTTs iban
serializados. Con P-1 resuelto, corren en paralelo en los 8 workers, pero siguen
siendo 2 RTTs por bloque nuevo.

**Fix propuesto:** eliminar el Exists/HEAD de `PutBlockAuto` y usar PUT directo.
La dedup intra-upload vía `upload.BlockAlreadyAccounted()` ya evita PUTs
redundantes dentro del mismo upload. S3 acepta re-PUT idempotente.
Alternativa: verificar existencia en Cassandra (≪1 ms) en lugar de S3 HEAD
(10–80 ms en prod).

---

## S-1 — Multi-nodo: chunk state node-local

**Severidad: ALTA para topología multi-nodo**  
**Rama pendiente**

**Tokens:** En producción (database != nil), `server.go:190-192` usa
`NewCassandraTokenAdapter` — los tokens son Cassandra-backed y multi-nodo
seguros. El `TokenManager` in-memory es solo el fallback sin DB.

**Chunk state (problema real):** `chunkManager` global del proceso
(`seafhttp.go:375`) usa `map[string]*ChunkUpload` + temp files en `os.TempDir()`
— completamente node-local. Si el LB distribuye chunks del mismo upload entre
nodos, el nodo B crea un tracker vacío y la finalización falla.

**Fix inmediato:** sticky sessions en el LB usando el token como hash key
(token ya en Cassandra, sin cambios de servidor).  
**Fix definitivo:** chunk state distribuido (Redis) o materialización directa
a S3 sin staging local.

---

## S-2 — `server.max_upload_mb` no aplicado a chunked uploads

**Severidad: MEDIA**  
**Rama pendiente**

Definido en `internal/config/config.go`. No referenciado en seafhttp.go para el
path chunked. Solo aplica un límite hardcoded de 1 GiB para single-shot uploads.
Sin quota configurada en el org, un usuario puede subir archivos de tamaño
arbitrario.

**Fix:** leer `cfg.SeafHTTP.MaxUploadMB` en `GetOrCreateUpload` antes de
`Truncate(totalSize)` y rechazar con 413 si `totalSize > max`.

---

## S-3 — Staging completo en /tmp (disk exhaustion vector)

**Severidad: MEDIA**  
**Rama pendiente**

`os.TempDir()` (`seafhttp.go:346`) + `Truncate(totalSize)` (`seafhttp.go:396`).
En Linux con ext4/xfs, `Truncate` crea un archivo sparse (sin alocación física
inmediata), pero la presión de disco crece a medida que llegan chunks. No hay
límite por nodo, por org, ni por token. Janitor limpia huérfanos a las 2h
(`chunkDiskTTL`).

Con uploads grandes concurrentes y sin quotas conservadoras, /tmp puede agotarse.

**Fix:** límite configurable de bytes en staging por nodo; rechazar
`GetOrCreateUpload` si se superaría.

---

## S-4 — TOCTOU en quota check bajo finalizaciones concurrentes al mismo org

**Severidad: MEDIA**  
**Rama pendiente**

El permit de finalización (`acquireSeafHTTPUploadFinalizePermit`) es por repo,
no por org. Dos uploads a repos distintos del mismo org corren en paralelo. Ambos
pasan el quota check antes de que ninguno actualice los counters de storage. La
ventana de carrera no es de milisegundos: con P-1 activo cada finalización tarda
segundos; incluso después de resolverlo, la ventana cubre toda la materialización.

**Fix:** reserva atómica de bytes al inicio del upload (pending reservation en
Cassandra) confirmada/liberada al finalizar.

---

## S-5 — Filename controlado por cliente; content-type ignorado

**Severidad: BAJA**

`filename := header.Filename` (`seafhttp.go:1359`) — sin validación adicional
más allá de `filepath.Join` (que neutraliza path traversal). Content-type del
cliente se ignora; el servidor siempre retorna `application/octet-stream` en
descargas.

No es un vector de explotación activo. El filename controlado por cliente es
comportamiento esperado en protocolos file-sharing.

---

## Tabla resumen

| ID  | Hallazgo                              | Veredicto       | Severidad | Estado         |
|-----|---------------------------------------|-----------------|-----------|----------------|
| P-1 | Permit serializa S3 PUT               | ✅ Confirmado   | CRÍTICA   | **RESUELTO**   |
| P-2 | 2 S3 RTTs por bloque (Exists + PUT)   | ✅ Confirmado   | ALTA→MEDIA| Pendiente      |
| S-1 | Chunk state node-local (multi-nodo)   | ✅ Confirmado   | ALTA      | Pendiente      |
| S-2 | max_upload_mb no enforced (chunked)   | ✅ Confirmado   | MEDIA     | Pendiente      |
| S-3 | Staging /tmp sin límite de disco      | ✅ Confirmado   | MEDIA     | Pendiente      |
| S-4 | TOCTOU quota check cross-repo         | ✅ Confirmado   | MEDIA     | Pendiente      |
| S-5 | Filename cliente, content-type ignore | ✅ Confirmado   | BAJA      | Pendiente      |

Nota: tokens de upload en producción son Cassandra-backed (multi-nodo seguros).
El in-memory `TokenManager` es solo el fallback sin DB.
