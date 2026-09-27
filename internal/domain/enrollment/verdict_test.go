package enrollment

import (
	"strings"
	"testing"
)

func TestVerdictForUsesSourceRowAndDescribesChanges(t *testing.T) {
	m := rosterMatch{
		byRoll:    map[string]string{"11A-014": "e1"},
		byEmail:   map[string]string{},
		current:   map[string]rosterCurrent{"e1": {FullName: "Aarya Kulkarni", Grade: "11", Section: "A"}},
		elsewhere: map[string]bool{"manasi@x.in": true},
	}
	v := verdictFor(RosterInput{SourceRow: 7, FullName: "Aarya Kulkarni", RollNumber: "11A-014", Grade: "11", Section: "B"}, 0, m)
	if v.Row != 7 || v.Action != "update" {
		t.Fatalf("got row %d action %s", v.Row, v.Action)
	}
	if strings.Join(v.Changes, ",") != "Section A → B" {
		t.Errorf("changes = %v", v.Changes)
	}

	v = verdictFor(RosterInput{SourceRow: 38, FullName: "Manasi", Email: "Manasi@x.in"}, 5, m)
	if v.Action != "error" || v.Row != 38 {
		t.Errorf("email active elsewhere should be an error on row 38, got %+v", v)
	}

	v = verdictFor(RosterInput{FullName: "New"}, 3, m)
	if v.Action != "create" || v.Row != 5 {
		t.Errorf("no source row falls back to index+2: %+v", v)
	}
}

func TestParseCSVKeepsLineNumbers(t *testing.T) {
	rows, bad, err := ParseCSV(strings.NewReader("full_name,roll_number\n,1\nAditi,2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 1 || bad[0].Row != 2 {
		t.Fatalf("bad = %+v", bad)
	}
	if len(rows) != 1 || rows[0].SourceRow != 3 {
		t.Fatalf("the valid row is on line 3, got %+v", rows)
	}
}
