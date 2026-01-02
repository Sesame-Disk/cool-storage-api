-- Migration: Create starred_files table
-- This table stores user's starred/favorited files

CREATE TABLE IF NOT EXISTS starred_files (
    user_id UUID,
    repo_id UUID,
    path TEXT,
    starred_at TIMESTAMP,
    PRIMARY KEY ((user_id), repo_id, path)
);

-- Index for looking up all starred items in a repo (for permission checks)
CREATE INDEX IF NOT EXISTS starred_files_by_repo ON starred_files (repo_id);
