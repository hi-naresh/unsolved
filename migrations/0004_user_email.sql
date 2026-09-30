-- Phase 3: optional email for Solved prompts, asked for in settings.
-- +goose Up
ALTER TABLE users ADD COLUMN email text CHECK (length(email) <= 254);

-- +goose Down
ALTER TABLE users DROP COLUMN email;
