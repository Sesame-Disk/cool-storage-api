-- Migration 004: persist default overwrite behavior for seafhttp upload tokens.

ALTER TABLE access_tokens ADD replace_existing BOOLEAN;