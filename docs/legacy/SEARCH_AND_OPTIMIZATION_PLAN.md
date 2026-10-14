# Search Backend & Upload/Download Optimization Plan

**Last Updated**: 2026-01-22
**Status**: Planning Phase

---

## Table of Contents
1. [Search Backend Implementation Plan](#1-search-backend-implementation-plan)
2. [File Upload Optimizations](#2-file-upload-optimizations)
3. [File Download Optimizations](#3-file-download-optimizations)
4. [Performance Benchmarks](#4-performance-benchmarks)

---

## 1. Search Backend Implementation Plan

### Current Situation
- Seafile uses Elasticsearch for full-text search of file content and metadata
- Indexes: file names, file content (PDF, Office docs, text files), commit messages
- SesameFS currently has NO search implementation

### Options Analysis

#### Option A: Elasticsearch (Seafile's Approach) ⭐ RECOMMENDED
**Pros:**
- Industry-standard for full-text search
- Excellent performance for large datasets
- Built-in analyzers for multiple languages
- Can index file content (PDF, DOCX, XLSX, TXT, etc.)
- Rich query DSL for complex searches
- Horizontal scaling support
- Highlight matching text in results

**Cons:**
- Additional infrastructure requirement (heavy JVM process)
- Memory intensive (recommend 2GB+ heap)
- Requires separate indexing pipeline
- Adds operational complexity

**Implementation Plan:**
```
1. Add Elasticsearch to docker-compose.yml (single node for dev, cluster for prod)
2. Create indexing service:
   - Index on file upload
   - Index on file rename/move
   - Delete index on file delete
   - Batch re-index existing files
3. Index structure:
   {
     "file_id": "uuid",
     "repo_id": "uuid",
     "path": "/folder/file.pdf",
     "name": "file.pdf",
     "content": "extracted text from file...",
     "extension": "pdf",
     "size": 1024000,
     "mtime": timestamp,
     "owner": "user@example.com",
     "tags": ["important", "project-x"]
   }
4. Search endpoint: GET /api/v2.1/search/?q=query&repo_id=xxx&type=file
5. Search features:
   - Filename search (fuzzy matching)
   - Content search (full-text)
   - Filter by: repo, extension, date range, owner, tags
   - Sort by: relevance, date, name, size
```

**Estimated Effort**: 3-5 days
**Resource Requirements**:
- Dev: Elasticsearch 8.x (Docker)
- Prod: Elasticsearch cluster (3 nodes minimum)
- Memory: 2GB heap per node (dev), 4-8GB per node (prod)

#### Option B: PostgreSQL Full-Text Search
**Pros:**
- No additional infrastructure (reuse existing DB)
- Built-in FTS with tsvector/tsquery
- Good performance for small-medium datasets
- Simpler operational model

**Cons:**
- Limited file content extraction (would need external tools)
- Slower than Elasticsearch for large datasets
- Less sophisticated ranking
- Not designed for horizontal scaling
- Limited language support

**Not Recommended**: We use Cassandra, not PostgreSQL, so this would require adding PostgreSQL just for search.

#### Option C: Cassandra + Apache Solr
**Pros:**
- Solr integrates well with Cassandra (DataStax Search)
- Similar capabilities to Elasticsearch
- Can leverage existing Cassandra infrastructure

**Cons:**
- DataStax Search is enterprise feature (paid)
- Open-source Solr requires separate cluster
- Less mature ecosystem than Elasticsearch
- Harder to operate than Elasticsearch

**Not Recommended**: Adds complexity without clear benefits over Elasticsearch.

#### Option D: Lightweight - Bleve (Go-native)
**Pros:**
- Pure Go library (no external service)
- Simple integration
- Good for small datasets
- Lower resource usage

**Cons:**
- Not suitable for distributed architecture
- Performance degrades with large datasets
- No horizontal scaling
- Limited file content extraction

**Not Recommended**: Doesn't scale for production use.

### Recommended Architecture: Elasticsearch

```
┌─────────────┐
│  SesameFS   │
│   Server    │
└──────┬──────┘
       │
       ├─────────────────┐
       │                 │
       v                 v
┌──────────────┐  ┌─────────────┐
│  Cassandra   │  │Elasticsearch│
│   (Metadata) │  │  (Search)   │
└──────────────┘  └─────────────┘
```

**Indexing Pipeline:**
1. File upload → Extract text → Index in Elasticsearch
2. File modify → Re-extract → Re-index
3. File delete → Remove from index
4. File move/rename → Update index

**Text Extraction Tools:**
- PDF: pdftotext (poppler-utils)
- Office: Apache Tika (Java) or pandoc
- Text: direct read
- Images with OCR: tesseract (optional, expensive)

### Implementation Steps

#### Phase 1: Basic Infrastructure (Day 1)
```yaml
# docker-compose.yml
elasticsearch:
  image: docker.elastic.co/elasticsearch/elasticsearch:8.11.0
  environment:
    - discovery.type=single-node
    - xpack.security.enabled=false
    - "ES_JAVA_OPTS=-Xms2g -Xmx2g"
  ports:
    - "9200:9200"
  volumes:
    - es_data:/usr/share/elasticsearch/data
```

```go
// internal/search/elasticsearch.go
type SearchClient struct {
    client *elasticsearch.Client
}

func NewSearchClient(urls []string) (*SearchClient, error) {
    cfg := elasticsearch.Config{Addresses: urls}
    client, err := elasticsearch.NewClient(cfg)
    return &SearchClient{client: client}, err
}
```

#### Phase 2: Indexing Service (Day 2-3)
```go
// internal/search/indexer.go
type FileDocument struct {
    FileID    string    `json:"file_id"`
    RepoID    string    `json:"repo_id"`
    Path      string    `json:"path"`
    Name      string    `json:"name"`
    Content   string    `json:"content"`
    Extension string    `json:"extension"`
    Size      int64     `json:"size"`
    MTime     time.Time `json:"mtime"`
    Owner     string    `json:"owner"`
    Tags      []string  `json:"tags"`
}

func (s *SearchClient) IndexFile(doc FileDocument) error {
    data, _ := json.Marshal(doc)
    req := esapi.IndexRequest{
        Index:      "sesamefs-files",
        DocumentID: doc.FileID,
        Body:       bytes.NewReader(data),
    }
    return s.client.PerformRequest(req)
}
```

#### Phase 3: Text Extraction (Day 3-4)
```go
// internal/search/extractor.go
func ExtractText(filePath string, mimeType string) (string, error) {
    switch {
    case strings.HasPrefix(mimeType, "text/"):
        return extractTextFile(filePath)
    case mimeType == "application/pdf":
        return extractPDF(filePath)
    case strings.Contains(mimeType, "officedocument"):
        return extractOffice(filePath)
    default:
        return "", nil // No text extraction
    }
}

func extractPDF(path string) (string, error) {
    cmd := exec.Command("pdftotext", path, "-")
    output, err := cmd.Output()
    return string(output), err
}
```

#### Phase 4: Search API (Day 4-5)
```go
// internal/api/v2/search.go
func (h *SearchHandler) Search(c *gin.Context) {
    query := c.Query("q")
    repoID := c.Query("repo_id")

    results, err := h.searchClient.Search(SearchRequest{
        Query:  query,
        RepoID: repoID,
        Limit:  100,
    })

    c.JSON(http.StatusOK, results)
}
```

### Search Query Examples

#### Basic Search
```bash
GET /api/v2.1/search/?q=quarterly+report

Response:
{
  "total": 15,
  "results": [
    {
      "repo_id": "...",
      "path": "/2023/Q4_Report.pdf",
      "name": "Q4_Report.pdf",
      "score": 8.5,
      "highlight": "...quarterly <em>report</em> shows..."
    }
  ]
}
```

#### Advanced Search
```bash
GET /api/v2.1/search/?q=budget&repo_id=xxx&ext=pdf&after=2023-01-01&tags=important
```

### Performance Considerations

**Indexing Speed:**
- Async indexing (don't block uploads)
- Batch indexing for initial import
- Rate limiting to prevent overwhelming Elasticsearch

**Search Speed:**
- Cache frequent queries (Redis)
- Limit result size (max 1000)
- Pagination for large result sets

---

## 2. File Upload Optimizations

### Current Implementation Analysis

**What Works:**
- ✅ Chunked uploads via Seafile protocol (desktop client)
- ✅ Multipart uploads via REST API (web frontend)
- ✅ SpillBuffer for memory management (16MB threshold)
- ✅ SHA-256 block storage
- ✅ Deduplication at block level

**What Needs Optimization:**

### 2.1 Resumable Uploads (HIGH PRIORITY)

**Problem**: Network failures or browser crashes lose all upload progress

**Current State**:
- Seafile JS client has resumable upload code (`reliable-upload.cpp`)
- Backend has stub endpoint: `GET /repos/:id/file-uploaded-bytes/`
- Not fully implemented

**Implementation Plan:**

```go
// internal/storage/upload_session.go
type UploadSession struct {
    SessionID    string
    RepoID       string
    FilePath     string
    TotalSize    int64
    UploadedSize int64
    Chunks       []ChunkInfo
    ExpiresAt    time.Time
}

type ChunkInfo struct {
    Offset int64
    Size   int64
    Hash   string
    Stored bool
}

// Store in Redis with TTL
func (s *SessionManager) GetUploadedBytes(sessionID string) (int64, error)
func (s *SessionManager) RecordChunk(sessionID string, chunk ChunkInfo) error
```

**API Endpoints:**
```
POST /api/v2.1/repos/:id/upload-session/
  → Creates session, returns session_id

GET /api/v2.1/repos/:id/upload-session/:session_id/
  → Returns uploaded bytes and missing chunks

POST /api/v2.1/repos/:id/upload-chunk/:session_id/
  → Upload chunk with offset and size

POST /api/v2.1/repos/:id/complete-upload/:session_id/
  → Finalize upload, create file commit
```

**Estimated Effort**: 2-3 days

### 2.2 Parallel Chunk Upload (MEDIUM PRIORITY)

**Problem**: Large files upload slowly (sequential chunks)

**Solution**: Allow parallel chunk uploads

```javascript
// Frontend concurrent upload
const CONCURRENT_CHUNKS = 3;
const chunkQueue = chunks.map((chunk, i) => ({
    offset: i * chunkSize,
    data: chunk,
}));

await Promise.all(
    chunkQueue.slice(0, CONCURRENT_CHUNKS).map(uploadChunk)
);
```

**Backend Changes:**
- Session manager must handle concurrent writes
- Use atomic operations for chunk tracking
- Lock-free design with CAS operations

**Estimated Effort**: 1-2 days

### 2.3 Upload Progress Streaming (LOW PRIORITY)

**Problem**: Client polls for progress (inefficient)

**Solution**: Server-Sent Events (SSE) for real-time progress

```go
func (h *UploadHandler) StreamProgress(c *gin.Context) {
    sessionID := c.Param("session_id")
    c.Header("Content-Type", "text/event-stream")

    ticker := time.NewTicker(500 * time.Millisecond)
    for range ticker.C {
        progress := h.sessionMgr.GetProgress(sessionID)
        fmt.Fprintf(c.Writer, "data: %d\n\n", progress)
        c.Writer.Flush()
    }
}
```

**Estimated Effort**: 1 day

### 2.4 Client-Side Content-Defined Chunking (FUTURE)

**Problem**: Fixed-size chunks don't deduplicate well for modified files

**Solution**: Use FastCDC on client side (requires desktop client changes)

**Status**: Not recommended - requires rebuilding desktop client

---

## 3. File Download Optimizations

### Current Implementation Analysis

**What Works:**
- ✅ Token-based download URLs
- ✅ Range request support (partial downloads)
- ✅ Block-level retrieval from S3

**What Needs Optimization:**

### 3.1 Download Streaming ✅ DONE (2026-02-16)

**Problem**: Large files load entirely into memory before sending

**Status**: ✅ Fully resolved. All logic consolidated in `internal/streaming/` package.

**Benchmark**: ~300 MB/s for 11.42 GB across all 4 download paths (localhost).

**Optimizations** (shared via `streaming.StreamBlocks()`):
1. **Prefetch pipeline** — Block N+1 fetched in goroutine while N streams to HTTP (`streaming.PrefetchBlock`)
2. **4 MB copy buffers** — `io.CopyBuffer` with `sync.Pool` (`streaming.GetCopyBuf`)
3. **Batch block ID resolution** — Cassandra `IN` queries, 100/batch (`streaming.BatchResolveBlockIDs`)
4. **Custom S3 transport** — 64 conn/host, 128 KB read buffers (was Go default: 2 conn/host)
5. **Reduced flush frequency** — Every 4 blocks instead of every block
6. **ZIP Store** — No compression for directory downloads

**Files**: `internal/streaming/streaming.go` (shared), `internal/api/seafhttp.go`, `internal/api/v2/fileview.go`, `internal/api/v2/sharelink_view.go`, `internal/storage/s3.go`

### 3.2 CDN Integration (MEDIUM PRIORITY)

**Problem**: All downloads go through SesameFS server (bandwidth bottleneck)

**Solution**: Generate presigned S3 URLs, serve via CloudFront/CDN

```go
func (h *FileHandler) GetDownloadLink(c *gin.Context) {
    // Generate presigned URL (valid for 1 hour)
    url, err := h.s3Store.GeneratePresignedURL(objectKey, 1*time.Hour)

    c.JSON(http.StatusOK, gin.H{
        "download_url": url,
        "expires_at": time.Now().Add(1 * time.Hour).Unix(),
    })
}
```

**Benefits:**
- Reduced server bandwidth (80-90%)
- Faster downloads (direct from S3/CDN)
- Global distribution via CloudFront edge locations

**Considerations:**
- Encrypted libraries: Can't use presigned URLs (need server decryption)
- Access control: Short TTL on presigned URLs

**Estimated Effort**: 2-3 days

### 3.3 Compression — Partially Done (2026-02-16)

**Problem**: Text files transfer without compression

**Decision**: ZIP directory downloads now use `zip.Store` (no compression) intentionally. Deflate was the **primary bottleneck** — CPU-bound at ~50-100 MB/s on a single core. Since most stored data is already compressed (images, videos, office docs) or incompressible (encrypted blocks), Deflate provided negligible size reduction at massive CPU cost.

**Still TODO**: HTTP-level gzip for individual text file downloads (low priority — text files are small).

```go
// Current approach for ZIP archives:
header := &zip.FileHeader{Name: path, Method: zip.Store} // No compression
w, err := zw.CreateHeader(header)
```

**Estimated Effort for remaining gzip**: 0.5 day

### 3.4 Byte-Range Support Enhancement (LOW PRIORITY)

**Current**: Basic range support exists
**Enhancement**: Multi-range support for video players

```
Range: bytes=0-1023, 8192-9216
```

**Estimated Effort**: 1 day

### 3.5 Download Acceleration - Multi-Connection (FUTURE)

**Problem**: Single TCP connection limits download speed

**Solution**: Allow clients to open multiple connections for same file

**Status**: Requires client-side changes (browser/desktop app)

---

## 4. Performance Benchmarks

### Target Metrics

#### Upload Performance
| File Size | Current | Target | Optimization |
|-----------|---------|--------|--------------|
| 10 MB | 2-3 sec | 1-2 sec | Streaming |
| 100 MB | 20-30 sec | 10-15 sec | Resumable + parallel chunks |
| 1 GB | 5-8 min | 2-4 min | Parallel chunks |
| 10 GB | 50-80 min | 20-30 min | Parallel + resumable |

#### Download Performance
| File Size | Current | Target | Optimization |
|-----------|---------|--------|--------------|
| 10 MB | 1-2 sec | 0.5-1 sec | Streaming + CDN |
| 100 MB | 10-15 sec | 3-5 sec | CDN + compression |
| 1 GB | 2-3 min | 30-60 sec | CDN |
| 10 GB | 20-30 min | 5-10 min | CDN |

#### Search Performance
| Query Type | Target | Notes |
|------------|--------|-------|
| Filename search | < 100ms | 10,000+ files |
| Content search | < 500ms | 1,000+ documents |
| Complex filter | < 1s | Multiple conditions |

### Testing Plan

```bash
# Upload performance test
ab -n 100 -c 10 -p file.dat http://localhost:8080/upload

# Download performance test
ab -n 100 -c 10 http://localhost:8080/download/file.dat

# Search performance test
ab -n 1000 -c 50 "http://localhost:8080/api/v2.1/search/?q=test"
```

---

## 5. Implementation Priority

### Phase 1: Search (Week 1-2)
1. ✅ Setup Elasticsearch infrastructure
2. ✅ Implement basic indexing
3. ✅ Text extraction for PDF/Office
4. ✅ Search API endpoint
5. ✅ Frontend integration

### Phase 2: Download Optimization (Week 3)
1. ✅ Implement streaming downloads
2. ✅ Add compression support
3. ✅ CDN integration (if using AWS/GCP)

### Phase 3: Upload Optimization (Week 4)
1. ✅ Resumable upload sessions
2. ✅ Parallel chunk uploads
3. ✅ Progress tracking

### Phase 4: Performance Tuning (Week 5)
1. ✅ Benchmark all endpoints
2. ✅ Identify bottlenecks
3. ✅ Optimize slow paths
4. ✅ Load testing

---

## 6. Resource Requirements

### Development Environment
```yaml
# docker-compose.yml additions
elasticsearch:
  image: docker.elastic.co/elasticsearch/elasticsearch:8.11.0
  environment:
    - discovery.type=single-node
    - xpack.security.enabled=false
    - "ES_JAVA_OPTS=-Xms2g -Xmx2g"
  volumes:
    - es_data:/usr/share/elasticsearch/data

redis:
  image: redis:7-alpine
  ports:
    - "6379:6379"
  volumes:
    - redis_data:/data
```

### Production Requirements
- Elasticsearch cluster: 3 nodes, 4GB RAM each
- Redis cluster: 2 nodes for upload sessions
- CDN: CloudFront or similar
- S3: Standard tier for active files, Glacier for archives

---

## 7. Monitoring & Metrics

### Key Metrics to Track
```go
// Prometheus metrics
var (
    searchLatency = prometheus.NewHistogram(...)
    uploadThroughput = prometheus.NewHistogram(...)
    downloadThroughput = prometheus.NewHistogram(...)
    cacheHitRate = prometheus.NewGauge(...)
)
```

### Dashboards
- Upload/download throughput (MB/s)
- Search query latency (p50, p95, p99)
- Active upload sessions
- CDN hit rate
- S3 bandwidth usage

---

## Questions?

For implementation details or clarification, see:
- [Current testing procedures](../TESTING.md); the original walkthrough is preserved in [MANUAL_TESTING_GUIDE-2026-01-23.md](MANUAL_TESTING_GUIDE-2026-01-23.md).
- `/docs/ARCHITECTURE.md` - System design
- `/docs/DATABASE-GUIDE.md` - Cassandra schema
