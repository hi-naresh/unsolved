-- Phase 4: weighted community vote for Solved (when the poster is silent) and Invalid.
-- Approved addition to the build doc (NJ, 2026-09-30).
-- +goose Up
CREATE TABLE problem_state_votes (
  problem_id uuid NOT NULL REFERENCES problems(id),
  user_id    uuid NOT NULL REFERENCES users(id),
  to_state   problem_state NOT NULL CHECK (to_state IN ('solved', 'invalid')),
  weight     real NOT NULL,          -- snapshot of voter weight at vote time
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (problem_id, user_id, to_state)
);

-- +goose Down
DROP TABLE problem_state_votes;
