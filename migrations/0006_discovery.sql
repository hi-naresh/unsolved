-- Phase 5: outputs of ClusterNightly. Approved addition to the build doc (NJ, 2026-09-30).
-- +goose Up
CREATE TABLE merge_suggestions (
  id           uuid PRIMARY KEY,
  problem_a_id uuid NOT NULL REFERENCES problems(id),
  problem_b_id uuid NOT NULL REFERENCES problems(id),
  similarity   real NOT NULL,
  dismissed_at timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (problem_a_id, problem_b_id),
  CHECK (problem_a_id < problem_b_id)
);
CREATE INDEX ON merge_suggestions (created_at DESC) WHERE dismissed_at IS NULL;

CREATE TABLE trending_problems (
  problem_id  uuid PRIMARY KEY REFERENCES problems(id),
  cluster_id  integer NOT NULL,
  rank        integer NOT NULL,
  computed_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON trending_problems (rank);

-- +goose Down
DROP TABLE trending_problems;
DROP TABLE merge_suggestions;
