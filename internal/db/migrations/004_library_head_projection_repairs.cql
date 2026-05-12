CREATE TABLE IF NOT EXISTS library_head_projection_repairs (
    org_id          UUID,
    library_id      UUID,
    commit_id       TEXT,
    queued_at       TIMESTAMP,
    last_attempt_at TIMESTAMP,
    retry_count     INT,
    last_error      TEXT,
    PRIMARY KEY ((org_id), library_id)
) WITH default_time_to_live = 0
  AND CLUSTERING ORDER BY (library_id ASC);

CREATE TABLE IF NOT EXISTS library_head_projection_repair_orgs_by_bucket (
  bucket      TEXT,
  org_id      UUID,
  updated_at  TIMESTAMP,
  PRIMARY KEY ((bucket), org_id)
) WITH default_time_to_live = 0;