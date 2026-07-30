-- Migration 013: stable, non-secret identity for link-origin access tokens

ALTER TABLE access_tokens
	ADD IF NOT EXISTS source_id TEXT;
