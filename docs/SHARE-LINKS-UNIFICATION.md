# Share Links Unification — Implementación Completada

> **Status**: Implementado
> **Date**: 2026-03-13
> **Reemplaza**: `share_links` (old), `share_links_by_creator` (old), `upload_links`, `upload_links_by_creator`

---

## 1. Resumen

Se unificaron 4 tablas separadas (share_links, upload_links y sus respectivas _by_creator) en un sistema unificado de 4 tablas `share_links` que soporta 3 tipos de link: **share**, **upload** e **internal** (smart links).

### Problemas resueltos

| Problema | Antes | Después |
|----------|-------|---------|
| Código duplicado | ~800 líneas en 2 archivos | ~500 líneas en 1 archivo con helpers compartidos |
| Admin full table scan | Sí (iteraba todos los usuarios) | No (1 query por org) |
| Orden cronológico | No (clustering por token random) | Sí (created_at DESC nativo) |
| download_count | Nunca se incrementaba | Se incrementa en handler de descarga |
| max_downloads | Código muerto | Funcional (depende de download_count) |
| Upload link tracking | Sin tracking | view_count + upload_count |
| Links huérfanos | Imposible de limpiar eficientemente | 1 query via _by_library |
| Links expirados persisten | Para siempre | TTL automático en Cassandra |
| Permission format | Mixed (string + JSON) | Solo JSON estandarizado |
| Smart links exponen ruta | URL con path completo del archivo | Token opaco → `/smart-link/{token}` |
| Extensibilidad | +2 tablas por tipo nuevo | +1 valor en link_type |
| Columna `token` es reservada CQL | Requería quoting | `link_token` evita conflicto |

---

## 2. Schema Final (4 tablas)

### 2.1 `share_links` — Tabla principal (lookup por token)

```sql
CREATE TABLE IF NOT EXISTS share_links (
    link_token       TEXT PRIMARY KEY,
    link_type        TEXT,          -- 'share' | 'upload' | 'internal'
    org_id           UUID,
    library_id       UUID,
    file_path        TEXT,          -- "/" = root, "/folder/" = dir, "/folder/file.pdf" = file
    created_by       UUID,
    permission       TEXT,          -- JSON: {"can_edit":false,"can_download":true,"can_upload":false}
                                    -- NULL/empty para upload e internal links
    password_hash    TEXT,          -- bcrypt hash. NULL = sin password
    expires_at       TIMESTAMP,     -- NULL = nunca expira
    single_use       BOOLEAN,       -- auto-desactiva después del primer uso
    active           BOOLEAN,       -- soft-disable sin borrar (default true)
    view_count       INT,           -- veces que se abrió la página del link
    download_count   INT,           -- descargas (share links)
    upload_count     INT,           -- archivos subidos (upload links)
    max_downloads    INT,           -- límite de descargas, NULL = ilimitado
    last_accessed_at TIMESTAMP,     -- última interacción
    created_at       TIMESTAMP
);
```

**Uso:** Resolve directo `WHERE link_token = ?` — O(1). Usado por:
- `resolveShareLink()` — validar y servir el link público
- Delete individual — leer clustering keys para dual-delete
- Counter increment — `UPDATE SET view_count = view_count + 1`

### 2.2 `share_links_by_creator` — "Mis links" (ordenado por fecha)

```sql
CREATE TABLE IF NOT EXISTS share_links_by_creator (
    org_id           UUID,
    created_by       UUID,
    created_at       TIMESTAMP,
    link_token       TEXT,
    link_type        TEXT,
    library_id       UUID,
    file_path        TEXT,
    permission       TEXT,
    expires_at       TIMESTAMP,
    single_use       BOOLEAN,
    active           BOOLEAN,
    view_count       INT,
    download_count   INT,
    upload_count     INT,
    max_downloads    INT,
    has_password     BOOLEAN,
    last_accessed_at TIMESTAMP,
    PRIMARY KEY ((org_id, created_by), created_at, link_token)
) WITH CLUSTERING ORDER BY (created_at DESC, link_token ASC);
```

**Uso:** `GET /api/v2.1/share-links/` y `GET /api/v2.1/upload-links/`
- Query: `WHERE org_id = ? AND created_by = ?`
- Filtra `link_type` en Go para servir endpoints separados
- Orden cronológico DESC nativo — sin sort en memoria

### 2.3 `admin_links_by_org_created` — Org-admin read model

```sql
CREATE TABLE IF NOT EXISTS admin_links_by_org_created (
    org_id           UUID,
    link_type        TEXT,
    bucket_day       TEXT,
    created_at       TIMESTAMP,
    link_token       TEXT,
    library_id       UUID,
    repo_name        TEXT,
    file_path        TEXT,
    obj_name         TEXT,
    created_by       UUID,
    creator_email    TEXT,
    creator_name     TEXT,
    permission       TEXT,
    expires_at       TIMESTAMP,
    has_password     BOOLEAN,
    active           BOOLEAN,
    view_count       INT,
    upload_count     INT,
    PRIMARY KEY ((org_id, link_type, bucket_day), created_at, link_token)
) WITH CLUSTERING ORDER BY (created_at DESC, link_token ASC);
```

**Uso:**
- Org admin: `GET /org/admin/links/` — una sola query
- Superadmin: `GET /admin/share-links/` — query por org_id
- No incluye counters (tabla ligera para admin)

### 2.4 `share_links_by_library` — Cleanup de links huérfanos

```sql
CREATE TABLE IF NOT EXISTS share_links_by_library (
    org_id       UUID,
    library_id   UUID,
    link_token   TEXT,
    link_type    TEXT,
    created_by   UUID,
    created_at   TIMESTAMP,
    PRIMARY KEY ((org_id, library_id), link_token)
);
```

**Uso:** Al borrar permanentemente una librería, encontrar y eliminar todos sus links.
`created_by` y `created_at` se incluyen para poder hacer quad-delete sin lookup adicional a la tabla principal.

---

## 3. Tipos de Link

| link_type | Descripción | URL pública | Permission |
|-----------|-------------|-------------|------------|
| `share` | Link público de descarga/preview | `/d/{token}` | JSON requerido |
| `upload` | Link público para subir archivos | `/u/d/{token}` | Vacío (implícito upload-only) |
| `internal` | Smart link interno (requiere auth, redirige a vista de archivo) | `/smart-link/{token}` | Vacío |

Agregar un tipo nuevo = agregar un valor en `link_type` + handler. **Cero cambios de schema.**

---

## 4. Permission Format — Estandarizado a JSON

### Antes (mixed):
```
"download"         → can_download=true
"preview_only"     → can_download=false
"preview_download" → can_download=true
"edit"             → can_edit=true, can_download=true
{"can_edit":false,"can_download":true,"can_upload":false}
```

### Después (siempre JSON):
```json
{"can_edit":false,"can_download":true,"can_upload":false}
{"can_edit":false,"can_download":false,"can_upload":false}
{"can_edit":true,"can_download":true,"can_upload":false}
```

El helper `normalizePermissionInput()` convierte cualquier formato legacy a JSON canónico al crear/actualizar.

---

## 5. Quad-Write Pattern (4 tablas)

Cada operación CRUD escribe a las tablas canónicas y a los read models admin en un logged batch:

```go
batch := session.NewBatch(gocql.LoggedBatch)
batch.Query("INSERT INTO share_links (...) VALUES (?)", ...)
batch.Query("INSERT INTO share_links_by_creator (...) VALUES (?)", ...)
batch.Query("INSERT INTO share_links_by_library (...) VALUES (?)", ...)
batch.Query("INSERT INTO admin_links_by_created (...) VALUES (?)", ...)
batch.Query("INSERT INTO admin_links_by_org_created (...) VALUES (?)", ...)
session.ExecuteBatch(batch)
```

**TTL**: Cuando hay `expires_at`, se aplica `USING TTL` a las tablas canónicas y a las proyecciones admin. Las filas expiradas se auto-eliminan.

**Delete**: Logged batch DELETE de las tablas canónicas y las proyecciones admin. Requiere clustering keys (`created_at`, `link_token`) para las tablas con clustering order.

**Update**: Patrón delete + re-insert para manejar cambios de TTL correctamente.

---

## 6. Counter Strategy

Los counters se actualizan solo en la tabla principal (fire-and-forget goroutine):

```go
// Al abrir la página de un link:
UPDATE share_links SET view_count = view_count + 1, last_accessed_at = ? WHERE link_token = ?

// Al descargar (+ single_use deactivation si aplica):
UPDATE share_links SET download_count = download_count + 1, last_accessed_at = ? WHERE link_token = ?

// Al subir archivo via upload link:
UPDATE share_links SET upload_count = upload_count + 1, last_accessed_at = ? WHERE link_token = ?
```

**NO se actualizan** en `_by_creator`, `_by_org` ni `_by_library` (son contadores aproximados, la fuente de verdad es la tabla principal).

---

## 7. Migraciones

Las migraciones en `db.go` incluyen:

1. **DROP** de las tablas viejas (old schema):
   - `DROP TABLE IF EXISTS share_links` (old, tenía `share_token` como PK)
   - `DROP TABLE IF EXISTS share_links_by_creator` (old)
   - `DROP TABLE IF EXISTS upload_links`
   - `DROP TABLE IF EXISTS upload_links_by_creator`
   - `DROP TABLE IF EXISTS public_links` (si existió de migraciones intermedias)
   - `DROP TABLE IF EXISTS public_links_by_creator`
   - `DROP TABLE IF EXISTS public_links_by_org`
   - `DROP TABLE IF EXISTS public_links_by_library`

2. **CREATE** de las 4 tablas nuevas con el schema final.

---

## 8. Archivos modificados

| Archivo | Cambio |
|---------|--------|
| `internal/db/db.go` | DROP tablas viejas + 4 tablas nuevas con `link_token` |
| `internal/models/models.go` | `ShareLink` + `LinkPerms` (struct unificado) |
| `internal/api/v2/share_links.go` | Reescrito (~600 líneas). Helpers: `insertShareLink`, `deleteShareLink`, `normalizePermissionInput`, `parsePermsJSON`, `permsToJSON` |
| `internal/api/v2/upload_links.go` | Reescrito (~350 líneas). Usa helpers de share_links.go via `shareHandler` |
| `internal/api/v2/shares.go` | Usa `models.ShareLink`, queries a `share_links` |
| `internal/api/v2/sharelink_view.go` | `resolveShareLink` usa `share_links`, incrementa `view_count`. Incrementa `download_count`/`upload_count`. Enforce `single_use` → `active=false` |
| `internal/api/v2/share_links_export.go` | Query actualizada a `share_links WHERE link_token` |
| `internal/api/v2/files.go` | `GetSmartLink` crea links `internal` con token en DB |
| `internal/api/v2/admin_share_links.go` | Los listados leen `admin_links_by_created` por buckets diarios |
| `internal/api/v2/org_admin_share_links.go` | Los listados leen `admin_links_by_org_created` por org + tipo + bucket |
| `internal/gc/store_cassandra.go` | `ListShareLinks` y `DeleteShareLink` usan `link_token`. Delete hace quad-delete con logged batch |
| `internal/api/v2/deleted_libraries.go` | `PermanentDeleteRepo` limpia links vía `share_links_by_library` + `cleanupLibraryLinks()` |

---

## 9. API Compatibility

Los endpoints REST no cambian. La unificación es puramente a nivel de base de datos.

```
# Share links
GET/POST    /api/v2.1/share-links/
PUT/DELETE  /api/v2.1/share-links/:token

# Upload links
GET/POST    /api/v2.1/upload-links/
PUT/DELETE  /api/v2.1/upload-links/:token

# Smart links (requiere auth, redirige al frontend)
GET         /api/v2.1/smart-link/?repo_id=xxx&path=/path  → genera token
GET         /api/v2.1/smart-link/:token                    → redirect

# Admin
GET/DELETE  /api/v2.1/admin/share-links/
GET/DELETE  /api/v2.1/admin/upload-links/

# Org Admin
GET/DELETE  /org/admin/links/
GET/DELETE  /org/admin/upload-links/
```

---

## 10. Comparación Final

| Aspecto | Antes | Después |
|---------|:-----:|:-------:|
| Tablas | 4 | 4 (unificadas) |
| Full table scan en admin | Sí | No |
| Admin itera usuarios | Sí (1 query/usuario) | No (1 query/org) |
| Orden cronológico nativo | No | Sí |
| Handler code duplicado | ~800 líneas en 2 archivos | ~500 líneas compartidas |
| Permission format | Mixed | Solo JSON |
| Counters funcionales | No | Sí |
| Soft-disable (active) | No | Sí |
| Single-use links | No | Sí |
| Activity tracking | No | Sí |
| Cleanup de huérfanos | Imposible | 1 query |
| TTL auto para expirados | No | Sí |
| Smart links seguros | Expone path | Token opaco |
| Tipos extensibles | +2 tablas/tipo | +1 valor |
| Columna PK es reservada CQL | `token` requiere quoting | `link_token` sin conflicto |

---

## 11. Post-Review: Bugs Corregidos

Después de la implementación inicial, se identificaron y corrigieron los siguientes gaps:

### 11.1 GC: `share_token` → `link_token` (Bug)

**Archivo:** `internal/gc/store_cassandra.go` — `ListShareLinks()`

El query usaba `share_token` (nombre de columna del schema viejo) en lugar de `link_token`. Causaría error runtime al ejecutar el GC.

**Fix:** Actualizado el SELECT y el Scan a `link_token`.

### 11.2 GC: Quad-delete en `DeleteShareLink` (Bug)

**Archivo:** `internal/gc/store_cassandra.go` — `DeleteShareLink()`

Solo borraba de la tabla principal `share_links`. Dejaba filas huérfanas en `_by_creator`, `_by_org` y `_by_library`.

**Fix:** Ahora lee los clustering keys (`org_id`, `created_by`, `library_id`, `created_at`) y ejecuta un logged batch DELETE contra las 4 tablas.

### 11.3 `download_count` nunca se incrementaba (Gap)

**Archivo:** `internal/api/v2/sharelink_view.go` — `handleShareLinkDownload()`

El handler generaba el download token y redirigía, pero nunca incrementaba `download_count`.

**Fix:** Se agrega fire-and-forget goroutine:
```go
UPDATE share_links SET download_count = download_count + 1, last_accessed_at = ? WHERE link_token = ?
```

### 11.4 `upload_count` nunca se incrementaba (Gap)

**Archivos:** `internal/api/v2/sharelink_view.go` — `PostUploadLinkDone()`, `PostShareLinkUploadDone()`

Ambos handlers eran stubs que solo retornaban `{"success": true}` sin trackear la actividad.

**Fix:** Se agrega fire-and-forget goroutine en ambos handlers:
```go
UPDATE share_links SET upload_count = upload_count + 1, last_accessed_at = ? WHERE link_token = ?
```

### 11.5 `single_use` almacenado pero nunca enforced (Gap)

**Archivo:** `internal/api/v2/sharelink_view.go`

El campo `single_use` se escribía en la DB vía `insertShareLink()` pero nunca se leía ni se actuaba sobre él.

**Fix:**
1. Se agrega `singleUse` al struct `shareLinkData`
2. Se lee `single_use` en `resolveShareLink()` (query + Scan)
3. En `handleShareLinkDownload()`: si `singleUse`, se ejecuta `UPDATE share_links SET active = false WHERE link_token = ?` después de la descarga
4. En `PostUploadLinkDone()` y `PostShareLinkUploadDone()`: mismo patrón de deactivación

### 11.6 `active` solo se leía, nunca se escribía `false` (Gap)

**Archivo:** `internal/api/v2/sharelink_view.go`

`resolveShareLink()` ya chequeaba `if !active { isExpired = true }`, pero ningún code path escribía `active = false`.

**Fix:** Resuelto como parte de 11.5 — `single_use` links se desactivan automáticamente (`active = false`) después del primer uso exitoso.

### 11.7 Cleanup de links al borrar librería (Gap)

**Archivo:** `internal/api/v2/deleted_libraries.go`

1. Comentarios referenciaban tablas viejas (`upload_links / upload_links_by_creator`)
2. `PermanentDeleteRepo()` no limpiaba los links asociados a la librería

**Fix:**
1. Comentarios actualizados para reflejar el schema unificado
2. Se agrega `cleanupLibraryLinks()` — escanea `share_links_by_library WHERE org_id = ? AND library_id = ?` y ejecuta quad-delete logged batch por cada link encontrado
3. Se invoca como goroutine async en `PermanentDeleteRepo()` para no bloquear la respuesta HTTP

### 11.8 `share_links_by_library` faltaban columnas para quad-delete (Bug)

**Archivo:** `internal/db/db.go`, `internal/api/v2/share_links.go`, `internal/api/v2/deleted_libraries.go`

`cleanupLibraryLinks()` necesitaba `created_by` y `created_at` para hacer el quad-delete contra `share_links_by_creator` (que tiene PK `((org_id, created_by), created_at, link_token)`), pero esas columnas no existían en `share_links_by_library`.

Además, el DELETE de `share_links_by_creator` dentro de `cleanupLibraryLinks()` no incluía `org_id` (partition key requerido).

**Fix:**
1. Agregadas columnas `created_by UUID` y `created_at TIMESTAMP` al schema de `share_links_by_library`
2. Actualizado `insertShareLink()` para incluir `created_by` y `created_at` en el INSERT de `_by_library`
3. Corregido el DELETE para incluir `org_id` en la condición de `share_links_by_creator`

### 11.9 Update de links reseteaba counters a 0 (Bug)

**Archivos:** `internal/api/v2/share_links.go`, `internal/api/v2/upload_links.go`

`UpdateShareLink` y `UpdateUploadLink` usan patrón delete + re-insert para manejar cambios de TTL. Pero `insertShareLink()` siempre insertaba `0, 0, 0` para `view_count`, `download_count`, `upload_count`, perdiendo los valores acumulados.

**Fix:**
1. `insertShareLink()` acepta parámetros `viewCount, downloadCount, uploadCount int`
2. `UpdateShareLink` pasa los valores leídos del link actual (`viewCount, downloadCount, 0`)
3. `UpdateUploadLink` lee `view_count, upload_count` y los preserva (`viewCount, 0, uploadCount`)
4. Todos los call sites de creación pasan `0, 0, 0`
