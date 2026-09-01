-- Multi-institution targeting for admin-authored content. The legacy singular
-- institution_id columns remain populated for backward compatibility.
CREATE TABLE IF NOT EXISTS announcement_institutions (
  announcement_id UUID NOT NULL REFERENCES announcements(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  PRIMARY KEY (announcement_id, institution_id)
);

CREATE TABLE IF NOT EXISTS promo_institutions (
  promo_id UUID NOT NULL REFERENCES promotional_content(id) ON DELETE CASCADE,
  institution_id UUID NOT NULL REFERENCES institutions(id) ON DELETE CASCADE,
  PRIMARY KEY (promo_id, institution_id)
);

CREATE INDEX IF NOT EXISTS idx_announcement_institutions_institution
  ON announcement_institutions(institution_id);
CREATE INDEX IF NOT EXISTS idx_promo_institutions_institution
  ON promo_institutions(institution_id);

ALTER TABLE announcement_institutions ENABLE ROW LEVEL SECURITY;
ALTER TABLE promo_institutions ENABLE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS announcement_institutions_admin ON announcement_institutions;
CREATE POLICY announcement_institutions_admin ON announcement_institutions
  FOR ALL TO authenticated USING (is_admin()) WITH CHECK (is_admin());
DROP POLICY IF EXISTS promo_institutions_admin ON promo_institutions;
CREATE POLICY promo_institutions_admin ON promo_institutions
  FOR ALL TO authenticated USING (is_admin()) WITH CHECK (is_admin());
