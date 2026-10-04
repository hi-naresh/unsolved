-- +goose Up
ALTER TYPE provider ADD VALUE IF NOT EXISTS 'google';
ALTER TYPE provider ADD VALUE IF NOT EXISTS 'github';
ALTER TYPE provider ADD VALUE IF NOT EXISTS 'reddit';

-- +goose Down
-- PostgreSQL cannot remove enum values without recreating the type and all dependents.
