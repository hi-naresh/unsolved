-- +goose Up
ALTER TABLE users
  ADD COLUMN contribution_preference text NOT NULL DEFAULT ''
    CHECK (contribution_preference IN ('', 'identifier', 'solver', 'both')),
  ADD COLUMN onboarding_completed boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE users
  DROP COLUMN onboarding_completed,
  DROP COLUMN contribution_preference;
