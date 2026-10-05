package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A super admin creates an institution directly: it is verified at once, has
// referral codes, and its admin account exists so their first email + OTP
// sign-in attaches to it (GetUserForLogin repairs the UID by verified email).
func TestCreateInstitutionDirectly(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	tag := fmt.Sprintf("%d", time.Now().UnixNano())
	h := NewHandler(pool, nil, nil)
	post := func(body string) (int, map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		h.CreateInstitution(w, httptest.NewRequest("POST", "/admin/institutions", strings.NewReader(body)))
		var env struct{ Data map[string]any }
		json.Unmarshal(w.Body.Bytes(), &env)
		return w.Code, env.Data
	}
	adminEmail := "Head-" + tag + "@Example.test"
	code, out := post(`{"name":"Direct School ` + tag + `","type":"school","contact_email":"office-` + tag + `@example.test",
		"admin_name":"Ravi Head","admin_email":"` + adminEmail + `","city":"Pune"}`)
	if code != 201 {
		t.Fatalf("create: %d %v", code, out)
	}
	instID, _ := out["id"].(string)
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM audit_log WHERE target_id::text=$1`, instID)
		pool.Exec(ctx, `DELETE FROM users WHERE institution_id=$1`, instID)
		pool.Exec(ctx, `DELETE FROM institutions WHERE id=$1`, instID)
	})

	var status, sCode, tCode, city string
	if err := pool.QueryRow(ctx, `SELECT status, student_referral_code, teacher_referral_code, COALESCE(onboarding_city,'') FROM institutions WHERE id=$1`, instID).
		Scan(&status, &sCode, &tCode, &city); err != nil {
		t.Fatal(err)
	}
	if status != "verified" || sCode == "" || tCode == "" || city != "Pune" {
		t.Errorf("institution = %s %s %s %s", status, sCode, tCode, city)
	}
	var role, email, uStatus string
	if err := pool.QueryRow(ctx, `SELECT role, email, status FROM users WHERE institution_id=$1`, instID).Scan(&role, &email, &uStatus); err != nil {
		t.Fatalf("admin account missing: %v", err)
	}
	if role != "institution_admin" || email != strings.ToLower(adminEmail) || uStatus != "active" {
		t.Errorf("admin = %s %s %s", role, email, uStatus)
	}
	if out["invite_sent"] != false {
		t.Errorf("invite_sent = %v with no email service, want false", out["invite_sent"])
	}

	// One address is one Qwish account; bad input is refused before any write.
	if code, _ := post(`{"name":"Again ` + tag + `","type":"school","contact_email":"x-` + tag + `@example.test","admin_email":"` + adminEmail + `"}`); code != 409 {
		t.Errorf("duplicate admin email: %d, want 409", code)
	}
	for _, bad := range []string{
		`{"type":"school","contact_email":"a@example.test"}`,
		`{"name":"N","type":"university","contact_email":"a@example.test"}`,
		`{"name":"N","type":"school","contact_email":"not-an-email"}`,
		`{"name":"N","type":"school","contact_email":"a@example.test","timezone":"Mars/Base"}`,
	} {
		if code, _ := post(bad); code != 400 {
			t.Errorf("%s: %d, want 400", bad, code)
		}
	}
	var n int
	pool.QueryRow(ctx, `SELECT COUNT(*) FROM institutions WHERE name LIKE 'Again '||$1`, tag).Scan(&n)
	if n != 0 {
		t.Error("refused create left an institution behind")
	}
}
