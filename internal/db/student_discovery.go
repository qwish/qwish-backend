package db

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// StudentDiscoverySQL filters institution-linked enrollments (e) and users (u).
// Portfolio evidence comes only from the latest snapshot shared with this institute.
func StudentDiscoverySQL(q url.Values, args *[]interface{}) string {
	out := ""
	add := func(sql string, value interface{}) {
		*args = append(*args, value)
		out += " AND " + fmt.Sprintf(sql, len(*args))
	}
	for _, field := range []string{"grade", "section", "status"} {
		if value := strings.TrimSpace(q.Get(field)); value != "" {
			add("e."+field+" = $%d", value)
		}
	}
	if classID := strings.TrimSpace(q.Get("class_id")); classID != "" {
		add("EXISTS (SELECT 1 FROM group_students gs JOIN groups g ON g.id=gs.group_id WHERE gs.user_id=u.id AND g.institution_id=e.institution_id AND g.archived_at IS NULL AND g.id::text=$%d)", classID)
	}
	if days, err := strconv.Atoi(q.Get("active_days")); err == nil && days > 0 && days <= 365 {
		add("u.last_active_at >= now() - ($%d::int * interval '1 day')", days)
	}
	if interest := strings.TrimSpace(q.Get("interest")); interest != "" {
		add("EXISTS (SELECT 1 FROM unnest(u.interests) interest WHERE interest ILIKE $%d)", "%"+interest+"%")
	}
	skill, experience := strings.TrimSpace(q.Get("skill")), strings.TrimSpace(q.Get("experience"))
	if skill != "" || experience != "" {
		out += ` AND EXISTS (SELECT 1 FROM user_profile_entries pe
   JOIN LATERAL (SELECT content FROM user_profile_entry_revisions pv
    WHERE pv.entry_id=pe.id AND pv.institution_id=e.institution_id ORDER BY pv.revision DESC LIMIT 1) pv ON true
   WHERE pe.user_id=u.id`
		if skill != "" {
			add("EXISTS (SELECT 1 FROM jsonb_array_elements_text(COALESCE(pv.content->'skills','[]'::jsonb)) skill WHERE skill ILIKE $%d)", "%"+skill+"%")
		}
		if experience != "" {
			add("pv.content->>'subtype' = $%d", experience)
		}
		out += ")"
	}
	return out
}
