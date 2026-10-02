package leadership

import (
	"slices"
	"testing"
)

func TestGrants(t *testing.T) {
	cse, ece, mech := "cse", "ece", "mech"
	cases := []struct {
		name   string
		grants Grants
		perm   string
		dept   string
		want   bool
	}{
		{"no grants", nil, PermStudentsRead, cse, false},
		{"principal reaches every department", Grants{{Role: "principal"}}, PermStudentsRead, ece, true},
		{"principal reaches unassigned classes", Grants{{Role: "principal"}}, PermStudentsRead, "", true},
		{"director publishes", Grants{{Role: "director"}}, PermActivitiesPublish, cse, true},
		{"hod own department", Grants{{Role: "hod", DepartmentID: cse}}, PermStudentsRead, cse, true},
		{"hod other department", Grants{{Role: "hod", DepartmentID: cse}}, PermStudentsRead, ece, false},
		{"hod unassigned class", Grants{{Role: "hod", DepartmentID: cse}}, PermStudentsRead, "", false},
		{"hod publishes in own department", Grants{{Role: "hod", DepartmentID: cse}}, PermActivitiesPublish, cse, true},
		{"dean cannot publish", Grants{{Role: "dean", DepartmentID: cse}}, PermActivitiesPublish, cse, false},
		{"vice principal is no copy of principal", Grants{{Role: "vice_principal"}}, PermActivitiesPublish, cse, false},
		{"vice principal institution scope reads", Grants{{Role: "vice_principal"}}, PermReportsRead, mech, true},
		// Never combine one grant's permission with another grant's scope:
		// dean (no publish) institution-wide is impossible, but dean of ECE plus
		// HOD of CSE must not publish in ECE.
		{"no scope borrowing", Grants{{Role: "dean", DepartmentID: ece}, {Role: "hod", DepartmentID: cse}}, PermActivitiesPublish, ece, false},
		{"unknown permission", Grants{{Role: "principal"}}, "students.export", cse, false},
	}
	for _, c := range cases {
		if got := c.grants.Can(c.perm, c.dept); got != c.want {
			t.Errorf("%s: Can(%s,%q)=%v want %v", c.name, c.perm, c.dept, got, c.want)
		}
	}

	all, depts := Grants{{Role: "hod", DepartmentID: cse}, {Role: "dean", DepartmentID: ece}, {Role: "dean", DepartmentID: cse}}.Scope(PermStudentsRead)
	slices.Sort(depts)
	if all || !slices.Equal(depts, []string{cse, ece}) {
		t.Fatalf("Scope = %v %v", all, depts)
	}
	if all, _ := (Grants{{Role: "hod", DepartmentID: cse}, {Role: "principal"}}).Scope(PermStaffRead); !all {
		t.Fatal("principal grant should give institution scope")
	}
}

func TestValidScope(t *testing.T) {
	for _, c := range []struct {
		role, dept string
		ok         bool
	}{
		{"director", "", true}, {"director", "d", false},
		{"principal", "", true}, {"principal", "d", false},
		{"vice_principal", "", true}, {"vice_principal", "d", true},
		{"dean", "", false}, {"dean", "d", true},
		{"hod", "", false}, {"hod", "d", true},
		{"registrar", "", false},
	} {
		if got := ValidScope(c.role, c.dept) == nil; got != c.ok {
			t.Errorf("ValidScope(%s,%q) ok=%v want %v", c.role, c.dept, got, c.ok)
		}
	}
}
