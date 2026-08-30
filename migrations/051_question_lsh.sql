-- Locality-sensitive hash buckets for near-duplicate question detection.
CREATE TABLE IF NOT EXISTS question_lsh_buckets (
  question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  band        SMALLINT NOT NULL,
  bucket      BIGINT NOT NULL,
  PRIMARY KEY (question_id, band)
);
CREATE INDEX IF NOT EXISTS question_lsh_lookup
  ON question_lsh_buckets (band, bucket);
ALTER TABLE question_lsh_buckets ENABLE ROW LEVEL SECURITY;
