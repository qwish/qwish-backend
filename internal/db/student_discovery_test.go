package db

import (
	"net/url"
	"strings"
	"testing"
)

func TestDiscoveryParametersAndPortfolioPrivacy(t *testing.T) {
	args := []interface{}{"institute", "search", "teacher"}
	q := url.Values{"grade": {"3"}, "section": {"A"}, "status": {"active"}, "class_id": {"class"}, "active_days": {"30"}, "interest": {"robotics"}, "skill": {"Python' OR true --"}, "experience": {"project"}}
	sql := StudentDiscoverySQL(q, &args)
	if len(args) != 11 {
		t.Fatalf("arguments = %d, want 11", len(args))
	}
	if strings.Contains(sql, "Python") {
		t.Fatal("skill was interpolated into SQL")
	}
	for _, fragment := range []string{"e.grade = $4", "g.institution_id=e.institution_id", "pv.institution_id=e.institution_id", "ORDER BY pv.revision DESC LIMIT 1", "pv.content->>'subtype' = $11"} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("missing boundary or parameter: %s", fragment)
		}
	}
	if args[9] != "%Python' OR true --%" {
		t.Fatalf("skill argument = %v", args[9])
	}
}

func TestDiscoveryEmptyAndInvalidActivity(t *testing.T) {
	for _, days := range []string{"", "-1", "366", "abc"} {
		args := []interface{}{"institute"}
		if sql := StudentDiscoverySQL(url.Values{"active_days": {days}}, &args); sql != "" || len(args) != 1 {
			t.Errorf("invalid activity %q added a filter", days)
		}
	}
}
