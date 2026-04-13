C4Context
    title  - Domain: storage

    Container(chunker, "FastCDC Chunker", "", "Content-defined chunking (2-256MB adaptive). SHA-256 block IDs. Deduplication via content addressing")
    Container(block_store, "Block Store", "", "SHA-256 addressed blocks. Two-level sharding (blocks/xx/yy/hash). Path traversal safe. No integrity check on download")
    ContainerDb(s3, "S3 / MinIO", "", "Object storage for file blocks. NO ServerSideEncryption in Put calls. Multi-region bucket support. TLS enabled")
    Container(gc, "Garbage Collector", "", "Background block cleanup. Grace period prevents premature deletion. Queue-based with persistence")

    Rel(api_v2, block_store, "File ops", "")
    Rel(seafhttp, block_store, "Upload/download blocks", "")
    Rel(block_store, s3, "Put/Get (no SSE)", "")
    Rel(block_store, chunker, "Content-defined chunking", "")
    Rel(gc, s3, "Delete orphan blocks", "")
    Rel(gc, cassandra, "Ref count queries", "")
