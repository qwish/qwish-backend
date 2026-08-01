package user

import "testing"

// Only the fields the client actually sent are written. A nil pointer means
// "not supplied" and must not blank an existing value.
func TestBuildUserPatchOnlyIncludesSuppliedFields(t *testing.T) {
	phone := "555-0100"
	req := personalFields{Phone: &phone}

	set, args := buildUserPatch(req)
	if set != "phone=$1" {
		t.Fatalf("set = %q, want phone=$1", set)
	}
	if len(args) != 1 || args[0] != phone {
		t.Fatalf("args = %v, want [%s]", args, phone)
	}
}

func TestBuildUserPatchEmptyRequest(t *testing.T) {
	set, args := buildUserPatch(personalFields{})
	if set != "" || len(args) != 0 {
		t.Fatalf("set=%q args=%v, want empty for a no-op patch", set, args)
	}
}

func TestBuildUserPatchNumbersFieldsInOrder(t *testing.T) {
	gender, guardian := "female", "A Guardian"
	set, args := buildUserPatch(personalFields{Gender: &gender, GuardianName: &guardian})
	if set != "gender=$1, guardian_name=$2" {
		t.Fatalf("set = %q", set)
	}
	if len(args) != 2 {
		t.Fatalf("args = %v, want two", args)
	}
}
