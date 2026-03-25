# Storage & Traffic Quotas — Plan de Implementacion

## Contexto

SesameFS tiene columnas de quota en DB (`storage_quota`, `storage_used`, `quota_bytes`, `used_bytes`) pero **nunca se actualizan**. No existe tracking de trafico. Los endpoints de estadisticas devuelven stubs vacios o 501. El frontend ya tiene paginas de charts/tablas listas esperando data real.

**Objetivo**: Implementar tracking real de storage y trafico, enforcement de quotas por plan, y activar los endpoints de estadisticas.

---

## Reglas de Negocio

### Planes actuales

Un servicio externo de billing gestiona los planes y llama a la API admin de SesameFS para setear los limites en cada org. SesameFS **no gestiona billing** — solo almacena los limites actuales y los enforcea.

#### Planes mensuales

| Plan | Precio | Users | Storage | Upload/mes | Download/mes | Trafico |
|------|--------|-------|---------|------------|--------------|---------|
| **Personal Free** | Gratis | 1 | 2 GB | 10 GB | 10 GB | Combinado* |
| **Starter** | $4/mo | 1 incl. | 250 GB | 50 TB | 250 GB | Separado |
| **StarterPlus** | $10/mo | 1 incl. | 2 TB | 50 TB | 2 TB | Separado |
| **Business** | $40/mo | 1 incl. | 8 TB | 50 TB | 8 TB | Separado |
| **Enterprise** | Custom | Custom | Custom | Custom | Custom | Custom |

*Free tiene 10GB **combinado** (upload+download juntos). Paid plans tienen limites separados.

#### Planes anuales

Mismos tiers con descuento anual. La diferencia:
- **Billing cycle** (monthly/annual) = frecuencia de pago y renovacion
- **Traffic reset** = siempre mensual, sin importar el billing cycle
- Un plan anual con 250GB/mes de download no acumula: cada mes se resetea a 0
- El billing service puede llamar a la API para cambiar quotas en cualquier momento (upgrade, downgrade, renovacion)

#### On-demand (planes pagos)

Los planes pagos incluyen 1 usuario. Mas alla de lo incluido:
- **Storage extra**: billing cobra por tiers de uso
- **Traffic extra**: billing cobra por tiers de uso
- **Users extra**: billing cobra por usuario adicional
- SesameFS **no bloquea** en planes pagos — solo avisa (soft warning). Billing se encarga del cobro.

### Modelo de datos por org

Billing setea estos campos en cada org via `PUT /admin/organizations/:org_id/`:

| Campo | Tipo | Free | Starter | Enterprise | Significado |
|-------|------|------|---------|------------|-------------|
| `plan` | string | `"free"` | `"starter"` | `"enterprise-acme"` | Nombre del plan |
| `billing_cycle` | string | `"monthly"` | `"monthly"` | `"annual"` | Ciclo de facturacion |
| `storage_quota` | int64 | 2 GB | 250 GB | custom | Limite de almacenamiento. -1 = ilimitado |
| `traffic_quota` | int64 | 10 GB | -1 | custom | Limite mensual total (upload+download). Campo universal — si viene, es EL limite. -1 = sin limite combinado |
| `traffic_upload_quota` | int64 | -1 | 50 TB | custom | Limite mensual upload adicional. -1 = sin limite individual de upload |
| `traffic_download_quota` | int64 | -1 | 250 GB | custom | Limite mensual download adicional. -1 = sin limite individual de download |
| `max_users` | int | 1 | -1 | 50 | Hard cap de usuarios. -1 = ilimitado (billing cobra extra) |

**Logica de evaluacion de trafico (el mas restrictivo gana):**
1. Si `traffic_quota != -1` → check `upload_used + download_used <= traffic_quota`
2. Si `traffic_upload_quota != -1` → check `upload_used <= traffic_upload_quota`
3. Si `traffic_download_quota != -1` → check `download_used <= traffic_download_quota`
4. Cualquier check que falle → bloqueado (free) o warning (pago)

`traffic_quota` es el campo universal. Billing puede usarlo solo (simple) o combinarlo con los individuales (granular). Ejemplos:

| Plan | traffic_quota | traffic_upload_quota | traffic_download_quota | Efecto |
|------|--------------|---------------------|----------------------|--------|
| Free | 10 GB | -1 | -1 | 10GB total, distribuye como quiera |
| Custom simple | 500 GB | -1 | -1 | 500GB total, sin limites por direccion |
| Starter | -1 | 50 TB | 250 GB | Sin limite total, pero download tope 250GB |
| Enterprise custom | 100 TB | -1 | 10 TB | 100TB total Y download max 10TB |

### Enforcement segun tipo de plan

| Escenario | Plan Free | Plan Pago |
|-----------|-----------|-----------|
| Storage excedido | **Hard block** — rechazar upload con 403 | **Soft warning** — permitir, billing cobra overage |
| Traffic excedido (cualquier check) | **Hard block** — rechazar con 403 | **Soft warning** — permitir, billing cobra overage |
| Max users excedido | **Hard block** — rechazar crear usuario con 403 | Si max_users=-1: permitir, billing cobra. Si max_users>0: hard block |
| Warning threshold | N/A (se bloquea directo) | Avisar al llegar a 80% del limite incluido |

### Quotas a nivel usuario

Dentro de una org, el admin puede asignar limites individuales por usuario:

- `quota_bytes` (storage) — ya existe en schema
- `traffic_upload_quota` — nuevo
- `traffic_download_quota` — nuevo
- Valor -1 = heredar del pool de la org (sin limite individual)
- El check mas restrictivo gana: si la org esta bloqueada, el user no puede subir aunque le quede quota individual
- No hay `traffic_quota` (combinado) a nivel usuario — solo a nivel org

---

## Tracking de Trafico — 6 Categorias

Compatible con Seafile. Se trackea upload y download por separado, subdividido por canal:

| Traffic Type | Descripcion | Handler(s) |
|-------------|-------------|------------|
| `sync-file-upload` | Desktop sync client sube bloques | `PutBlock` (sync.go:762) |
| `sync-file-download` | Desktop sync client baja bloques | `GetBlock` (sync.go:665) |
| `web-file-upload` | Web UI sube archivos (resumable.js) | `HandleUpload` (seafhttp.go:481), `UploadFile` (files.go:2638), `UploadBlock` (blocks.go:149) |
| `web-file-download` | Web UI descarga archivos | `HandleDownload` (seafhttp.go:1251), `HandleZipDownload` (seafhttp.go:1584), `DownloadBlock` (blocks.go:244), `DownloadHistoricFile` (fileview.go:759) |
| `link-file-upload` | Subida via share/upload link publico | `HandleUpload` con token source=link |
| `link-file-download` | Descarga via share link publico | `handleShareLinkDownload` (sharelink_view.go:391) |

Para quota enforcement se evaluan 3 checks (el mas restrictivo gana):
1. **Combinado** (`traffic_quota`): upload_total + download_total vs limite combinado
2. **Upload** (`traffic_upload_quota`): sync+web+link upload vs limite upload
3. **Download** (`traffic_download_quota`): sync+web+link download vs limite download

---

## Phase 1: Schema — Nuevas tablas y columnas

### Archivos a modificar
- `internal/db/db.go` — agregar migrations
- `internal/models/models.go` — agregar campos a structs

### 1.1 Tabla `traffic_counters` (counter table)

Tracking diario por org/user/tipo. Partition por `(org_id, month)` para scoping mensual natural.

```sql
CREATE TABLE IF NOT EXISTS traffic_counters (
    org_id UUID,
    month TEXT,           -- '202603'
    day DATE,
    user_id UUID,
    traffic_type TEXT,    -- 'sync-file-upload', etc.
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), day, user_id, traffic_type)
)
```

Patron existente: `gc_queue_stats` ya usa `COUNTER` (db.go:834).

Permite:
- Queries por dia (estadisticas con group_by=day)
- Sumas por mes (quota check)
- Desglose por user (estadisticas per-user)

### 1.2 Tabla `traffic_monthly` (counter table — fast quota check)

Contadores mensuales agregados para enforcement rapido (1 partition read per check).

```sql
CREATE TABLE IF NOT EXISTS traffic_monthly (
    org_id UUID,
    month TEXT,           -- '202603'
    scope TEXT,           -- 'org:upload', 'org:download', 'org:combined', '<user_id>:upload', '<user_id>:download'
    bytes_transferred COUNTER,
    PRIMARY KEY ((org_id, month), scope)
)
```

- `scope='org:upload'` = total upload de la org (para check de `traffic_upload_quota`)
- `scope='org:download'` = total download de la org (para check de `traffic_download_quota`)
- `scope='org:combined'` = total upload+download de la org (para check de `traffic_quota` combinado)
- `scope='<user_id>:upload'` = upload del usuario
- `scope='<user_id>:download'` = download del usuario
- Se incrementa en paralelo con `traffic_counters` (fire-and-forget)
- Cada operacion incrementa 3 scopes org (upload o download + combined) y 1 scope user

### 1.3 Tabla `storage_counters` (counter table)

Contadores atomicos de storage para increment/decrement concurrente.

```sql
CREATE TABLE IF NOT EXISTS storage_counters (
    scope TEXT,           -- 'org:<uuid>', 'user:<org_uuid>:<user_uuid>', 'lib:<org_uuid>:<lib_uuid>'
    bytes_used COUNTER,
    file_count COUNTER,
    PRIMARY KEY ((scope))
)
```

### 1.4 ALTER TABLE organizations

```sql
ALTER TABLE organizations ADD traffic_quota BIGINT          -- combined monthly limit (-1=no aplica)
ALTER TABLE organizations ADD traffic_upload_quota BIGINT   -- upload monthly limit (-1=unlimited)
ALTER TABLE organizations ADD traffic_download_quota BIGINT -- download monthly limit (-1=unlimited)
ALTER TABLE organizations ADD max_users INT                 -- hard cap (-1=unlimited)
ALTER TABLE organizations ADD plan TEXT                     -- plan name from billing
ALTER TABLE organizations ADD billing_cycle TEXT            -- "monthly" | "annual"
```

`storage_quota` ya existe. Los nuevos campos permiten al billing service setear todo via API.

### 1.5 ALTER TABLE users

```sql
ALTER TABLE users ADD traffic_upload_quota BIGINT
ALTER TABLE users ADD traffic_download_quota BIGINT
```

`quota_bytes` (storage) ya existe.

### 1.6 Actualizar structs en models.go

```go
// Organization — agregar:
TrafficQuota         int64  `json:"traffic_quota"`          // combined monthly limit, -1=N/A
TrafficUploadQuota   int64  `json:"traffic_upload_quota"`   // upload monthly limit, -1=unlimited
TrafficDownloadQuota int64  `json:"traffic_download_quota"` // download monthly limit, -1=unlimited
MaxUsers             int    `json:"max_users"`              // hard cap, -1=unlimited
Plan                 string `json:"plan,omitempty"`
BillingCycle         string `json:"billing_cycle,omitempty"` // "monthly" | "annual"

// User — agregar:
TrafficUploadQuota   int64 `json:"traffic_upload_quota"`   // -1=inherit from org
TrafficDownloadQuota int64 `json:"traffic_download_quota"` // -1=inherit from org
```

---

## Phase 2: TrafficRecorder — Core de tracking

### Archivos nuevos
- `internal/traffic/recorder.go`

### Archivos a modificar
- `internal/api/server.go` — inicializacion

### 2.1 TrafficRecorder struct

```go
package traffic

type Recorder struct {
    session *gocql.Session
}

// Record registra bytes transferidos. Corre en goroutine, nunca bloquea el request.
func (r *Recorder) Record(orgID, userID, trafficType string, bytes int64)
```

Logica interna:
1. Calcula `month := time.Now().Format("200601")` y `day := time.Now().Truncate(24h)`
2. Increment `traffic_counters` con (org_id, month, day, user_id, traffic_type)
3. Determina direction: si trafficType contiene "upload" → direction="upload"; si "download" → direction="download"
4. Increment `traffic_monthly` con 4 scopes:
   - `"org:<direction>"` (ej: `"org:upload"`) — para check de quota individual
   - `"org:combined"` — para check de quota combinada
   - `"<userID>:<direction>"` — para check de quota per-user
   - (3 writes a traffic_monthly por operacion)
5. Todo dentro de `go func() { ... }()` — fire-and-forget
6. Errores se logean, nunca se propagan

### 2.2 Constantes

```go
const (
    SyncUpload   = "sync-file-upload"
    SyncDownload = "sync-file-download"
    WebUpload    = "web-file-upload"
    WebDownload  = "web-file-download"
    LinkUpload   = "link-file-upload"
    LinkDownload = "link-file-download"
)
```

### 2.3 Inyeccion global

Patron existente: `SetGCHooks` en gc_hooks.go.

```go
var globalRecorder struct {
    mu sync.RWMutex
    r  *Recorder
}

func SetRecorder(r *Recorder) { ... }
func Get() *Recorder { ... }
```

Se inicializa en `server.go` junto al resto de servicios.

---

## Phase 3: Instrumentacion — Uploads

### Archivos a modificar
- `internal/api/seafhttp.go` — HandleUpload, AccessToken struct
- `internal/api/sync.go` — PutBlock
- `internal/api/v2/blocks.go` — UploadBlock
- `internal/api/v2/files.go` — UploadFile
- `internal/db/tokens.go` — AccessToken struct (agregar Source)
- `internal/api/v2/sharelink_view.go` — marcar tokens como "link"

### 3.1 Agregar `Source` a AccessToken

```go
// En ambos AccessToken (seafhttp.go:43 y db/tokens.go:21)
Source string // "web" (default) o "link" (share/upload link)
```

En `GetUploadLinkUploadURL` (sharelink_view.go:1593) y `GetShareLinkUploadURL` (sharelink_view.go:1685): al crear el token, setear `Source: "link"`. Requiere extender `CreateUploadToken` para aceptar source, o crear `CreateUploadTokenWithSource`.

### 3.2 Puntos de instrumentacion

| Handler | Archivo:Linea | Traffic Type | Size Source | Cuando |
|---------|--------------|-------------|-------------|--------|
| `HandleUpload` | seafhttp.go:~710 | `WebUpload` o `LinkUpload` (segun token.Source) | `finalSize` del commit | Despues de commit exitoso |
| `PutBlock` | sync.go:~868 | `SyncUpload` | `len(data)` | Despues de store exitoso |
| `UploadBlock` | blocks.go:~240 | `WebUpload` | `len(data)` | Despues de store exitoso |
| `UploadFile` | files.go:~2700 | `WebUpload` | `len(content)` | Despues de commit exitoso |

Ejemplo de instrumentacion:
```go
// Al final de HandleUpload, despues del commit exitoso:
if rec := traffic.Get(); rec != nil {
    tt := traffic.WebUpload
    if token.Source == "link" {
        tt = traffic.LinkUpload
    }
    rec.Record(token.OrgID, token.UserID, tt, finalSize)
}
```

---

## Phase 4: Instrumentacion — Downloads

### Archivos a modificar
- `internal/api/seafhttp.go` — HandleDownload, HandleZipDownload
- `internal/api/sync.go` — GetBlock
- `internal/api/v2/blocks.go` — DownloadBlock
- `internal/api/v2/sharelink_view.go` — handleShareLinkDownload
- `internal/api/v2/fileview.go` — DownloadHistoricFile

### 4.1 Puntos de instrumentacion

| Handler | Archivo | Traffic Type | Size Source | Cuando |
|---------|---------|-------------|-------------|--------|
| `HandleDownload` | seafhttp.go | `WebDownload` | `fileSize` de fs_objects | Despues de streaming exitoso |
| `HandleZipDownload` | seafhttp.go | `WebDownload` | byte count acumulado | Despues de zip stream |
| `GetBlock` | sync.go | `SyncDownload` | `len(data)` | Despues de enviar |
| `DownloadBlock` | blocks.go | `WebDownload` | `len(data)` | Despues de enviar |
| `handleShareLinkDownload` | sharelink_view.go | `LinkDownload` | `fileSize` de metadata | Despues de streaming |
| `DownloadHistoricFile` | fileview.go | `WebDownload` | file size | Despues de enviar |

Nota: registrar DESPUES de enviar exitosamente. Si streaming falla a mitad, idealmente contar bytes enviados. Si no es practico, no contar (el error es raro y el drift es aceptable).

---

## Phase 5: Storage Tracking

### Archivos a modificar
- `internal/api/v2/write_helpers.go` — helpers de increment/decrement
- `internal/api/seafhttp.go` — increment en upload
- `internal/api/sync.go` — increment en PutBlock
- `internal/api/v2/files.go` — increment en UploadFile, decrement en DeleteFile
- `internal/api/v2/libraries.go` — decrement en delete library
- `internal/gc/worker.go` — recalculacion periodica

### 5.1 Helpers en write_helpers.go

```go
func incrementStorageCounters(db, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64)
func decrementStorageCounters(db, orgID, userID, libraryID string, deltaBytes int64, deltaFiles int64)
```

Cada uno hace 3 counter updates atomicos: `org:<orgID>`, `user:<orgID>:<userID>`, `lib:<orgID>:<libID>`. Fire-and-forget en goroutine.

### 5.2 Instrumentacion

- **Upload**: despues de commit exitoso, `incrementStorageCounters(db, orgID, userID, libID, fileSize, 1)`
- **Delete file**: en `DeleteFile`, `decrementStorageCounters(db, orgID, userID, libID, fileSize, 1)`
- **Delete library**: en soft-delete (trash), no decrementar aun. En hard-delete (GC cascade), decrementar por el size total.

### 5.3 Deduplicacion

Solo incrementar storage cuando el bloque es **nuevo** (`ref_count` pasa de 0 a 1). Si ya existe (dedup), no incrementar. Los handlers ya detectan esto.

### 5.4 Recalculacion periodica (GC Phase 13)

Nueva fase en `gc/worker.go`: `RecalculateStorageCounters`
1. Para cada org: sumar `size_bytes` de todas las libraries activas → UPDATE `organizations.storage_used`
2. Para cada user: sumar `size_bytes` de sus libraries → UPDATE `users.used_bytes`
3. Para cada library: sumar `size_bytes` de sus fs_objects del head commit → UPDATE `libraries.size_bytes`
4. Corre 1x/dia. Corrige drift acumulado.

---

## Phase 6: Quota Enforcement

### Archivos nuevos
- `internal/traffic/checker.go`

### Archivos a modificar
- `internal/api/sync.go` — QuotaCheck (linea 1636)
- `internal/api/seafhttp.go` — pre-check en HandleUpload
- `internal/api/v2/files.go` — pre-check en UploadFile
- `internal/api/v2/blocks.go` — pre-check en UploadBlock
- `internal/api/v2/admin.go` — pre-check en crear usuario (max_users)

### 6.1 QuotaChecker

```go
type QuotaStatus struct {
    Allowed    bool   // puede proceder
    Warning    bool   // >80% del limite (solo planes pagos)
    UsedBytes  int64  // uso actual
    LimitBytes int64  // limite del check que fallo (-1=ilimitado)
    Reason     string // "storage", "traffic-combined", "traffic-upload", "traffic-download", "max-users"
    Plan       string // nombre del plan
}

type Checker struct {
    session *gocql.Session
}

func (c *Checker) CheckStorageQuota(orgID string, additionalBytes int64) (QuotaStatus, error)
func (c *Checker) CheckTrafficQuota(orgID, userID, direction string, additionalBytes int64) (QuotaStatus, error)
func (c *Checker) CheckMaxUsers(orgID string) (QuotaStatus, error)
```

### 6.2 Logica de CheckTrafficQuota

`direction` es `"upload"` o `"download"`. Un solo metodo evalua los 3 checks de trafico y retorna el mas restrictivo.

```
CheckTrafficQuota(orgID, userID, direction, additionalBytes):

  1. SELECT traffic_quota, traffic_upload_quota, traffic_download_quota, plan
     FROM organizations WHERE org_id = ?

  2. month = time.Now().Format("200601")

  3. CHECK 1: Quota combinada (traffic_quota)
     Si traffic_quota != -1:
       SELECT bytes FROM traffic_monthly WHERE org_id=? AND month=? AND scope='org:combined'
       Evaluar: combined_used + additional > traffic_quota

  4. CHECK 2: Quota de direction (traffic_upload_quota o traffic_download_quota)
     quota = traffic_upload_quota si direction=="upload", else traffic_download_quota
     Si quota != -1:
       SELECT bytes FROM traffic_monthly WHERE scope='org:<direction>'
       Evaluar: direction_used + additional > quota

  5. CHECK 3: Quota per-user
     Si userID != "":
       SELECT traffic_upload_quota, traffic_download_quota FROM users WHERE org_id=? AND user_id=?
       user_quota = el que corresponda segun direction
       Si user_quota != -1:
         SELECT bytes FROM traffic_monthly WHERE scope='<userID>:<direction>'
         Evaluar: user_used + additional > user_quota

  6. Para cada check que falle:
     Si plan == "" o plan == "free" → Allowed=false (hard block)
     Si plan pago → Allowed=true, Warning=true

  7. Retornar el status MAS restrictivo de los 3 checks
     Reason = el check que fallo ("traffic-combined", "traffic-upload", "traffic-download")
```

### 6.3 Integracion en handlers

**QuotaCheck endpoint** (sync.go:1636 — desktop client):
```go
func (h *SyncHandler) QuotaCheck(c *gin.Context) {
    orgID := c.GetString("org_id")
    userID := c.GetString("user_id")
    status, _ := checker.CheckStorageQuota(orgID, 0)
    c.JSON(200, gin.H{
        "has_quota":  status.Allowed,
        "remaining":  status.LimitBytes - status.UsedBytes,
    })
}
```

**Upload pre-check** (antes de leer datos del request):
```go
storageStatus, _ := checker.CheckStorageQuota(orgID, contentLength)
if !storageStatus.Allowed {
    c.JSON(403, gin.H{"error": "storage quota exceeded"})
    return
}
trafficStatus, _ := checker.CheckTrafficQuota(orgID, userID, "upload", contentLength)
if !trafficStatus.Allowed {
    c.JSON(403, gin.H{"error": "traffic quota exceeded", "reason": trafficStatus.Reason})
    return
}
if trafficStatus.Warning {
    c.Header("X-Quota-Warning", trafficStatus.Reason)
}
// proceder con upload...
```

**Download pre-check** (antes de streaming):
```go
trafficStatus, _ := checker.CheckTrafficQuota(orgID, userID, "download", fileSize)
if !trafficStatus.Allowed {
    c.JSON(403, gin.H{"error": "traffic quota exceeded", "reason": trafficStatus.Reason})
    return
}
if trafficStatus.Warning {
    c.Header("X-Quota-Warning", trafficStatus.Reason)
}
```

**Crear usuario** (admin.go, org_admin.go):
```go
usersStatus, _ := checker.CheckMaxUsers(orgID)
if !usersStatus.Allowed {
    c.JSON(403, gin.H{"error": "user limit reached"})
    return
}
```

### 6.4 Header de warning

Para planes pagos con soft warning, setear header `X-Quota-Warning: storage|traffic-upload|traffic-download` para que el frontend muestre un aviso sin bloquear.

### 6.5 Share link enforcement

Share link downloads/uploads: el trafico cuenta contra la org del creador del link. Si la org del creador esta en free y excedio quota, el share link devuelve 403. El campo `trafficOverLimit` en respuestas de share link (actualmente hardcodeado a `false` en sharelink_view.go) se evalua contra la quota real.

---

## Phase 7: Statistics API

### Archivos a modificar
- `internal/api/v2/admin_extra.go` — reemplazar stubs
- `internal/api/v2/admin.go` — agregar rutas nuevas
- `internal/api/v2/org_admin.go` — reemplazar stubs 501
- `frontend/src/utils/seafile-api.js` — fix URLs rotas

### 7.1 Reemplazar stubs existentes

**`AdminStatisticTraffic`** (admin_extra.go:152):
1. Reusar `generateDateRange(c)` existente para parsear start/end/group_by
2. Para cada fecha: query `traffic_counters` partition `(platform_org_id, month)` filtrando `day`
3. Para superadmin: iterar todas las orgs, o usar una partition especial "platform" que acumule cross-org
4. Sumar por traffic_type, retornar formato existente:
```json
[{"datetime": "2026-03-24T00:00:00+00:00", "sync-file-upload": 12345, "sync-file-download": 67890, "web-file-upload": 11111, "web-file-download": 22222, "link-file-upload": 3333, "link-file-download": 4444}]
```

**`AdminStatisticStorage`** (admin_extra.go:116):
- Query `storage_counters` para cada org, sumar totales por dia
- Retornar `[{datetime, total_storage}]`

**`OrgStatisticTraffic`** (org_admin.go — actualmente 501):
- Igual que AdminStatisticTraffic pero scoped a la org del caller

**`OrgStatisticUserTraffic`** (org_admin.go — actualmente 501):
- Query `traffic_counters` para un mes, agrupar por user_id
- Retornar lista paginada:
```json
{
  "user_monthly_traffic_list": [
    {"email": "user@example.com", "name": "User", "sync_file_upload": 123, "sync_file_download": 456, "web_file_upload": 789, "web_file_download": 012, "link_file_upload": 345, "link_file_download": 678}
  ],
  "has_next_page": false
}
```

### 7.2 Endpoints nuevos

| Ruta | Handler | Descripcion |
|------|---------|-------------|
| `GET /admin/statistics/user-traffic/` | `AdminListUserTraffic` | Per-user traffic cross-org (superadmin) |
| `GET /admin/statistics/org-traffic/` | `AdminListOrgTraffic` | Per-org traffic summary (superadmin) |

### 7.3 Fix frontend (seafile-api.js)

Bugs actuales:
- `orgAdminStatisticSystemTraffic()` apunta a `/total-storage/` → corregir a `/system-traffic/`
- `orgAdminListUserTraffic()` apunta a `/total-storage/` → corregir a `/user-traffic/`

Funciones faltantes a agregar:
- `sysAdminListUserTraffic(month, page, perPage, orderBy)` → `/admin/statistics/user-traffic/`
- `sysAdminListOrgTraffic(month, page, perPage, orderBy)` → `/admin/statistics/org-traffic/`

---

## Phase 8: Plan/Quota API (para billing service externo)

### Archivos a modificar
- `internal/api/v2/admin.go` — extender PUT org, PUT user endpoints
- `internal/api/v2/admin_extra.go` — subscription info endpoint
- `internal/api/v2/org_admin.go` — exponer quota status a org admin

### 8.1 Extender PUT /admin/organizations/:org_id/

Ya acepta `storage_quota`. Agregar soporte para todos los campos que billing envia:

```
storage_quota            (int64, bytes) — ya existe
traffic_quota            (int64, bytes/mes combinado, -1=no aplica) — NUEVO
traffic_upload_quota     (int64, bytes/mes, -1=ilimitado) — NUEVO
traffic_download_quota   (int64, bytes/mes, -1=ilimitado) — NUEVO
max_users                (int, -1=ilimitado) — NUEVO
plan                     (string, nombre del plan) — NUEVO
billing_cycle            (string, "monthly"|"annual") — NUEVO
```

**Ejemplo: billing setea plan free**
```json
{
    "plan": "free",
    "billing_cycle": "monthly",
    "storage_quota": 2147483648,
    "traffic_quota": 10737418240,
    "traffic_upload_quota": -1,
    "traffic_download_quota": -1,
    "max_users": 1
}
```

**Ejemplo: billing setea plan Starter**
```json
{
    "plan": "starter",
    "billing_cycle": "monthly",
    "storage_quota": 268435456000,
    "traffic_quota": -1,
    "traffic_upload_quota": 54975581388800,
    "traffic_download_quota": 268435456000,
    "max_users": -1
}
```

### 8.2 Extender PUT /admin/organizations/:org_id/users/:email/

Ya acepta `quota_total` (storage). Agregar:
```
traffic_upload_quota   (int64, bytes/mes)
traffic_download_quota (int64, bytes/mes)
```

El org admin puede asignar limites individuales de trafico.

### 8.3 GET /api/v2.1/subscription/ (nuevo)

Retorna info del plan actual de la org del usuario autenticado. Consumido por el componente de subscription del frontend.

```json
{
    "plan": "free",
    "billing_cycle": "monthly",
    "storage_quota": 2147483648,
    "storage_used": 1073741824,
    "storage_percent": 50.0,
    "traffic_quota": 10737418240,
    "traffic_combined_used": 5368709120,
    "traffic_upload_quota": -1,
    "traffic_upload_used": 2684354560,
    "traffic_download_quota": -1,
    "traffic_download_used": 2684354560,
    "traffic_reset_date": "2026-04-01",
    "max_users": 1,
    "current_users": 1
}
```

### 8.4 Extender GET /org/admin/info/

Ya retorna `storage_quota` y `storage_usage`. Agregar:
```json
{
    "traffic_quota": 10737418240,
    "traffic_combined_used": 5368709120,
    "traffic_upload_quota": -1,
    "traffic_upload_used": 2684354560,
    "traffic_download_quota": -1,
    "traffic_download_used": 2684354560,
    "max_users": 1,
    "plan": "free",
    "billing_cycle": "monthly"
}
```

### 8.5 Extender GET /api/v2.1/account/info/

Ya retorna `total` (storage quota) y `usage` (storage used). Agregar traffic info del usuario:
```json
{
    "traffic_upload_quota": -1,
    "traffic_upload_used": 536870912,
    "traffic_download_quota": -1,
    "traffic_download_used": 1073741824
}
```

---

## Orden de Implementacion

```
Phase 1 (Schema)
    |
    v
Phase 2 (TrafficRecorder core)
    |
    +---> Phase 3 (Upload instrumentation)  +---> Phase 5 (Storage tracking)
    |                |                              |
    |                v                              v
    |         Phase 4 (Download instr.)      Phase 6 (Quota enforcement)
    |                |                              |
    |                v                              |
    +---------> Phase 7 (Statistics API) <----------+
                     |
                     v
              Phase 8 (Plan/Quota API)
```

- Phases 3+5 son paralelas (no dependen entre si)
- Phase 4 depende de Phase 3 (misma mecanica, complementa)
- Phase 6 depende de Phase 5 (necesita storage counters)
- Phase 7 necesita Phases 3+4 (necesita datos de trafico)
- Phase 8 es mayormente independiente pero va al final (necesita los campos de Phase 1)

---

## Archivos Criticos (resumen)

| Archivo | Cambios |
|---------|---------|
| `internal/db/db.go` | 3 tablas nuevas (traffic_counters, traffic_monthly, storage_counters) + 6 ALTER TABLE |
| `internal/models/models.go` | Campos nuevos en Organization, User |
| `internal/traffic/recorder.go` | **Nuevo** — TrafficRecorder |
| `internal/traffic/checker.go` | **Nuevo** — QuotaChecker |
| `internal/api/seafhttp.go` | AccessToken.Source, instrumentar HandleUpload/HandleDownload, pre-checks |
| `internal/api/sync.go` | Instrumentar PutBlock/GetBlock, implementar QuotaCheck real |
| `internal/api/v2/blocks.go` | Instrumentar UploadBlock/DownloadBlock, pre-checks |
| `internal/api/v2/files.go` | Instrumentar UploadFile, decrement en DeleteFile |
| `internal/api/v2/write_helpers.go` | incrementStorageCounters/decrementStorageCounters |
| `internal/api/v2/admin_extra.go` | Reemplazar stubs de statistics, subscription endpoint |
| `internal/api/v2/admin.go` | Nuevas rutas statistics, extender PUT org |
| `internal/api/v2/org_admin.go` | Reemplazar stubs 501, extender endpoints |
| `internal/api/v2/sharelink_view.go` | Token source=link, trafficOverLimit real |
| `internal/api/v2/fileview.go` | Instrumentar DownloadHistoricFile |
| `internal/api/v2/libraries.go` | Decrement storage en delete |
| `internal/api/server.go` | Inicializar TrafficRecorder y QuotaChecker |
| `internal/gc/worker.go` | Phase 13: RecalculateStorageCounters |
| `internal/db/tokens.go` | AccessToken.Source field |
| `frontend/src/utils/seafile-api.js` | Fix URLs rotas, agregar funciones faltantes |

---

## Consideraciones de ScyllaDB

### Counter tables
- Usamos counter tables para `traffic_counters`, `traffic_monthly`, y `storage_counters`
- Counter tables NO soportan TTL — cleanup manual de meses viejos si es necesario
- Counter tables NO soportan conditional updates (LWT) — no es problema para nuestro caso
- Counters son eventually consistent — aceptable para quotas (drift temporal es ok)

### Partitioning
- `traffic_counters`: partition `(org_id, month)` — una partition por org por mes (~6 tipos * ~30 dias * N users filas)
- `traffic_monthly`: partition `(org_id, month)` — pocas filas por partition (2 org scopes + 2 per user)
- `storage_counters`: partition `(scope)` — una fila por entity (org, user, library)

### Hot partitions
- Orgs con trafico masivo podrian crear hot partitions en `traffic_counters`
- Mitigacion: partition por `(org_id, month)` limita el tamaño. Si necesario, shardear por semana
- Premature optimization — evaluar despues de datos reales

---

## Verificacion

### Tests unitarios
- TrafficRecorder: mock de session, verificar queries generados
- QuotaChecker: test con free (hard block), paid (soft warning), unlimited (-1)
- Storage counters: increment/decrement correcto

### Tests de integracion
- Upload archivo → verificar traffic_counters incrementado con tipo correcto
- Upload archivo → verificar traffic_monthly incrementado (org:upload, org:combined, user:upload)
- Upload archivo → verificar storage_counters incrementado
- Download archivo → verificar traffic_counters incrementado
- Share link download → verificar tipo `link-file-download`
- Sync upload → verificar tipo `sync-file-upload`
- Org free con traffic_quota=10GB → upload 6GB + download 5GB → segunda op bloqueada (combinado)
- Org starter con traffic_download_quota=250GB → download >250GB → warning header (soft)
- Org free con storage_quota=2GB → upload >2GB → rechazo 403
- Org free con max_users=1 → crear segundo usuario → rechazo 403
- Org paid con max_users=-1 → crear usuarios sin limite
- GET /admin/statistics/system-traffic/ → verificar datos reales en formato correcto
- GET /org/:id/admin/statistics/user-traffic/ → verificar desglose por usuario
- PUT /admin/organizations/:id/ con todos los campos de plan → verificar persistencia
- GET /api/v2.1/subscription/ → verificar datos reales con plan info

### Tests manuales
- Web upload → dashboard sys-admin muestra trafico web
- Desktop sync → estadisticas muestran sync-file-upload
- Share link download → estadisticas muestran link-file-download
- Org free: 2GB storage limit → subir >2GB → bloqueo
- Org free: 10GB traffic combinado → superar → bloqueo
- Org paid: exceder download quota → warning header pero no bloqueo
- Enterprise con max_users=50 → usuario 51 bloqueado
- Frontend subscription component → muestra datos reales
- Billing API: PUT org con plan change → quotas actualizadas inmediatamente
