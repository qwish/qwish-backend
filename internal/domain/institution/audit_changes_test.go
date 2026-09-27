package institution

import "testing"

func TestAuditChangesListsOnlyDifferences(t *testing.T) {
	got := auditChanges(
		[]byte(`{"mode":"verify_first","match":"any","emails":[]}`),
		[]byte(`{"mode":"custom","match":"any","emails":["a@x.in"]}`),
	)
	if len(got) != 2 || got[0].Field != "emails" || got[1].Field != "mode" {
		t.Fatalf("got %+v", got)
	}
	if got[1].Before != "verify_first" || got[1].After != "custom" {
		t.Errorf("mode change = %+v", got[1])
	}
	if auditChanges(nil, nil) != nil {
		t.Error("entries without values carry no changes")
	}
}

func TestAuditGroupsAreDisjoint(t *testing.T) {
	seen := map[string]string{}
	for g, actions := range auditActionGroups {
		for _, a := range actions {
			if other, dup := seen[a]; dup {
				t.Errorf("%s is in both %s and %s", a, other, g)
			}
			seen[a] = g
		}
	}
}
