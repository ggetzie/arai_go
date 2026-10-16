-- Idempotent schema additions owned by the Go port. The Django tables
-- main_generatedq and main_about are shared and their columns are never altered.

CREATE INDEX IF NOT EXISTS main_generatedq_displayed_idx ON main_generatedq (displayed);

-- Per-visitor vote state, replacing Django's Redis-backed session["vote_states"].
CREATE TABLE IF NOT EXISTS arai_vote_state (
    session_id  text        NOT NULL,
    question_id integer     NOT NULL REFERENCES main_generatedq (id) ON DELETE CASCADE,
    state       smallint    NOT NULL CHECK (state BETWEEN -1 AND 1),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, question_id)
);

CREATE INDEX IF NOT EXISTS arai_vote_state_updated_at_idx ON arai_vote_state (updated_at);
