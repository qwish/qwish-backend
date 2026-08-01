package auth

import "testing"

// Every response that hands the app a user object must describe the same user.
//
// The NumPie client reads institution_id and institution independently:
// institution_id gates the "My Campus" toggle on the Assessments screen, and
// institution carries the name shown on Profile. verify-otp sent neither and
// create-profile sent only the nested object, so a learner who joined an
// institution lost it on the next sign-in — the account was right in the
// database the whole time, and every payload but /users/me forgot to say so.
func TestUserPayloadCarriesInstitution(t *testing.T) {
	instID := "inst-1"
	u := &UserProfile{
		ID:            "u1",
		FullName:      "Aditi Sharma",
		DisplayName:   "Aditi",
		Email:         "aditi@school.edu",
		Role:          "student",
		Status:        "active",
		InstitutionID: &instID,
	}

	got := userPayload(u, "Delhi Public School")

	if got["institution_id"] != instID {
		t.Errorf("institution_id = %v, want %q — without it the client hides the My Campus toggle",
			got["institution_id"], instID)
	}

	inst, ok := got["institution"].(map[string]string)
	if !ok {
		t.Fatalf("institution = %#v, want a {id,name} object — Profile reads its name", got["institution"])
	}
	if inst["id"] != instID || inst["name"] != "Delhi Public School" {
		t.Errorf("institution = %#v, want id=%q name=%q", inst, instID, "Delhi Public School")
	}

	// The fields the client already relied on must not move.
	for k, want := range map[string]any{
		"id": "u1", "full_name": "Aditi Sharma", "display_name": "Aditi",
		"email": "aditi@school.edu", "role": "student", "status": "active",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}
}

// A learner who has joined nothing must not get an empty object that reads as
// "joined" — the client treats a blank id as no institution, and the two sides
// should agree rather than rely on that.
func TestUserPayloadOmitsAbsentInstitution(t *testing.T) {
	u := &UserProfile{
		ID: "u1", FullName: "Aditi Sharma", DisplayName: "Aditi",
		Email: "aditi@school.edu", Role: "student", Status: "active",
	}

	got := userPayload(u, "")

	if v, present := got["institution_id"]; present && v != nil {
		t.Errorf("institution_id = %v, want absent or nil", v)
	}
	if v, present := got["institution"]; present && v != nil {
		t.Errorf("institution = %#v, want absent or nil", v)
	}
}
