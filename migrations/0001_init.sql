-- +goose Up
CREATE EXTENSION IF NOT EXISTS vector;

CREATE TYPE problem_state   AS ENUM ('open', 'solved', 'invalid');
CREATE TYPE solution_kind   AS ENUM ('process_change', 'off_the_shelf', 'custom_software', 'dont_automate');
CREATE TYPE tried_outcome   AS ENUM ('worked', 'partly', 'failed');
CREATE TYPE display_mode    AS ENUM ('named', 'anonymous');
CREATE TYPE provider        AS ENUM ('linkedin', 'x');
CREATE TYPE standing_tier   AS ENUM ('member', 'declared_background', 'domain_contributor', 'domain_expert');

CREATE TABLE users (
  id               uuid PRIMARY KEY,
  handle           text NOT NULL UNIQUE CHECK (handle ~ '^[a-z0-9_]{3,24}$'),
  display_name     text NOT NULL CHECK (length(display_name) BETWEEN 1 AND 80),
  in_directory     boolean NOT NULL DEFAULT false,
  declared_history text CHECK (length(declared_history) <= 2000),  -- shown, labelled unverified, zero weight
  suspended_at     timestamptz,     -- set by admin; blocks all writes
  deleted_at       timestamptz,     -- account deletion: profile scrubbed, content kept as "deleted user"
  created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE identities (
  user_id      uuid NOT NULL REFERENCES users(id),
  provider     provider NOT NULL,
  provider_uid text NOT NULL,
  profile_url  text,
  created_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (provider, provider_uid)
);
CREATE INDEX ON identities (user_id);

CREATE TABLE sessions (
  token_hash bytea PRIMARY KEY,            -- sha256 of cookie value
  user_id    uuid NOT NULL REFERENCES users(id),
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE domains (             -- industry axis, seeded, fixed list at launch
  id   smallint PRIMARY KEY,
  slug text NOT NULL UNIQUE,
  name text NOT NULL
);

CREATE TABLE problems (
  id                  uuid PRIMARY KEY,
  domain_id           smallint NOT NULL REFERENCES domains(id),
  author_id           uuid NOT NULL REFERENCES users(id),
  author_display      display_mode NOT NULL,
  state               problem_state NOT NULL DEFAULT 'open',
  soft_solved         boolean NOT NULL DEFAULT false,
  current_revision_id uuid,          -- FK added below; set in same tx as first revision
  forked_from_revision_id uuid,      -- non-null when this problem is a fork
  score               double precision NOT NULL DEFAULT 0,  -- = current revision score
  on_meta_board       boolean NOT NULL DEFAULT false,
  is_seed             boolean NOT NULL DEFAULT false,
  created_at          timestamptz NOT NULL DEFAULT now(),
  updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE problem_revisions (
  id                 uuid PRIMARY KEY,
  problem_id         uuid NOT NULL REFERENCES problems(id),
  parent_revision_id uuid REFERENCES problem_revisions(id),  -- null only for the first
  author_id          uuid NOT NULL REFERENCES users(id),
  author_display     display_mode NOT NULL,
  title              text NOT NULL CHECK (length(title) BETWEEN 10 AND 140),
  current_process    text NOT NULL CHECK (length(current_process) BETWEEN 50 AND 4000),
  pain               text NOT NULL CHECK (length(pain) BETWEEN 20 AND 2000),
  tried              text NOT NULL CHECK (length(tried) <= 2000),
  why_note           text CHECK (length(why_note) <= 500),  -- required when parent is non-null (enforced in service)
  score              double precision NOT NULL DEFAULT 0,
  vote_count         integer NOT NULL DEFAULT 0,
  embedding          vector(384),    -- filled in phase 5
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON problem_revisions (problem_id, score DESC);
CREATE INDEX ON problem_revisions (parent_revision_id);

ALTER TABLE problems
  ADD FOREIGN KEY (current_revision_id) REFERENCES problem_revisions(id) DEFERRABLE INITIALLY DEFERRED,
  ADD FOREIGN KEY (forked_from_revision_id) REFERENCES problem_revisions(id);
CREATE INDEX ON problems (state, soft_solved, score DESC) WHERE on_meta_board = false;
CREATE INDEX ON problems (domain_id, score DESC);
CREATE INDEX ON problems (created_at DESC);

CREATE TABLE solutions (
  id                  uuid PRIMARY KEY,
  problem_id          uuid NOT NULL REFERENCES problems(id),   -- the problem, not a revision
  author_id           uuid NOT NULL REFERENCES users(id),
  author_display      display_mode NOT NULL,
  kind                solution_kind NOT NULL,
  current_revision_id uuid,
  score               double precision NOT NULL DEFAULT 0,
  created_at          timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON solutions (problem_id, score DESC);

CREATE TABLE solution_revisions (
  id                 uuid PRIMARY KEY,
  solution_id        uuid NOT NULL REFERENCES solutions(id),
  parent_revision_id uuid REFERENCES solution_revisions(id),
  author_id          uuid NOT NULL REFERENCES users(id),
  author_display     display_mode NOT NULL,
  body               text NOT NULL CHECK (length(body) BETWEEN 50 AND 8000),
  why_note           text CHECK (length(why_note) <= 500),
  score              double precision NOT NULL DEFAULT 0,
  vote_count         integer NOT NULL DEFAULT 0,
  created_at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON solution_revisions (solution_id, score DESC);
ALTER TABLE solutions ADD FOREIGN KEY (current_revision_id)
  REFERENCES solution_revisions(id) DEFERRABLE INITIALLY DEFERRED;

-- One vote table per target type keeps FKs real.
CREATE TABLE problem_revision_votes (
  revision_id uuid NOT NULL REFERENCES problem_revisions(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  weight      real NOT NULL,          -- snapshot of voter weight at vote time
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (revision_id, user_id)
);
CREATE TABLE solution_revision_votes (
  revision_id uuid NOT NULL REFERENCES solution_revisions(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  weight      real NOT NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (revision_id, user_id)
);

CREATE TABLE solution_trials (     -- "tried it" + outcome
  solution_id uuid NOT NULL REFERENCES solutions(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  outcome     tried_outcome NOT NULL,
  note        text CHECK (length(note) <= 1000),
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (solution_id, user_id)
);

CREATE TABLE problem_state_events (   -- append-only audit of every state change
  id         uuid PRIMARY KEY,
  problem_id uuid NOT NULL REFERENCES problems(id),
  from_state problem_state NOT NULL,
  to_state   problem_state NOT NULL,
  actor_id   uuid REFERENCES users(id),  -- null = system
  reason     text NOT NULL CHECK (length(reason) <= 500),
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON problem_state_events (problem_id, created_at);

CREATE TABLE founder_interest (
  problem_id uuid NOT NULL REFERENCES problems(id),
  user_id    uuid NOT NULL REFERENCES users(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (problem_id, user_id)
);

CREATE TABLE user_domain_standing (   -- computed by jobs, read on every vote
  user_id   uuid NOT NULL REFERENCES users(id),
  domain_id smallint NOT NULL REFERENCES domains(id),
  tier      standing_tier NOT NULL DEFAULT 'member',
  points    double precision NOT NULL DEFAULT 0,  -- internal, never displayed
  updated_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, domain_id)
);

CREATE TABLE reports (      -- "flag this" from any signed-in user; reviewed in /admin
  id          uuid PRIMARY KEY,
  reporter_id uuid NOT NULL REFERENCES users(id),
  target_kind text NOT NULL CHECK (target_kind IN ('problem_revision','solution_revision','user')),
  target_id   uuid NOT NULL,
  reason      text NOT NULL CHECK (length(reason) BETWEEN 5 AND 500),
  resolved_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON reports (created_at) WHERE resolved_at IS NULL;

CREATE TABLE vouches (
  voucher_id uuid NOT NULL REFERENCES users(id),
  vouchee_id uuid NOT NULL REFERENCES users(id),
  domain_id  smallint NOT NULL REFERENCES domains(id),
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (voucher_id, vouchee_id, domain_id),
  CHECK (voucher_id <> vouchee_id)
);

-- +goose Down
DROP TABLE vouches;
DROP TABLE reports;
DROP TABLE user_domain_standing;
DROP TABLE founder_interest;
DROP TABLE problem_state_events;
DROP TABLE solution_trials;
DROP TABLE solution_revision_votes;
DROP TABLE problem_revision_votes;
ALTER TABLE solutions DROP CONSTRAINT solutions_current_revision_id_fkey;
DROP TABLE solution_revisions;
DROP TABLE solutions;
ALTER TABLE problems DROP CONSTRAINT problems_current_revision_id_fkey;
ALTER TABLE problems DROP CONSTRAINT problems_forked_from_revision_id_fkey;
DROP TABLE problem_revisions;
DROP TABLE problems;
DROP TABLE domains;
DROP TABLE sessions;
DROP TABLE identities;
DROP TABLE users;
DROP TYPE standing_tier;
DROP TYPE provider;
DROP TYPE display_mode;
DROP TYPE tried_outcome;
DROP TYPE solution_kind;
DROP TYPE problem_state;
DROP EXTENSION IF EXISTS vector;
