package enrollment

import "testing"

func TestReasonFor(t *testing.T) {
	src := "other-inst"
	cases := []struct {
		name string
		r    AdmissionRequest
		want string
	}{
		{"staff account", AdmissionRequest{Status: "pending", flags: requestFlags{role: "teacher"}}, "staff_account"},
		{"suspended here", AdmissionRequest{Status: "pending", flags: requestFlags{role: "student", suspendedHere: true}}, "suspended_member_new_code"},
		{"claim taken", AdmissionRequest{Status: "pending", flags: requestFlags{role: "student", claimTaken: true}}, "claim_code_used"},
		{"archived class", AdmissionRequest{Status: "pending", flags: requestFlags{role: "student"},
			Targets: []AdmissionTarget{{Kind: "class", Meta: &TargetMeta{Archived: true}}}}, "destination_closed"},
		{"transfer waiting", AdmissionRequest{Status: "approved", SourceInstitutionID: &src, flags: requestFlags{role: "student"}}, "transfer_waiting"},
		{"joined claim is not 'used'", AdmissionRequest{Status: "joined", flags: requestFlags{role: "student", claimTaken: true}}, ""},
		{"ordinary request", AdmissionRequest{Status: "pending", flags: requestFlags{role: "student"}}, ""},
	}
	for _, c := range cases {
		if got, _ := reasonFor(c.r); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
