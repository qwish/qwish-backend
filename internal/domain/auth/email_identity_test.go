package auth

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNormalizeEmail(t *testing.T) {
	cases := map[string]string{
		"Priya@Example.com":   "priya@example.com",
		"  priya@example.com": "priya@example.com",
		"PRIYA@EXAMPLE.COM\t": "priya@example.com",
		"priya@example.com":   "priya@example.com",
		"":                    "",
		"   ":                 "",
	}
	for in, want := range cases {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

// The message is what a person reads when they are locked out of signing up.
// It has to name where the address already lives and not leak a schema token.
func TestErrEmailTakenHuman(t *testing.T) {
	msg := ErrEmailTaken{Surface: "the institute dashboard", Role: "institution_admin"}.Human()

	if strings.Contains(msg, "institution_admin") {
		t.Errorf("message shows the raw role token: %q", msg)
	}
	for _, want := range []string{"institute dashboard", "institution admin", "one Qwish account"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
}

func TestSurfaceForRoleCoversEveryRole(t *testing.T) {
	// The users.role CHECK constraint (001) plus admin_accounts.role (001).
	// A role with no surface renders as the bare word "Qwish", which tells the
	// person nothing about where to sign in.
	for _, role := range []string{
		"student", "teacher", "institution_admin", "parent",
		"moderator", "support_agent", "super_admin",
	} {
		if _, ok := surfaceForRole[role]; !ok {
			t.Errorf("role %q has no surface phrase", role)
		}
	}
}

// The rule only exists if the database enforces it. Everything above is a
// message; this is the guarantee.
func TestOneIdentityPerEmailIsEnforced(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()

	email := "dup-" + uuid.NewString()[:8] + "@example.test"

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES ($1,'Dup Check','Dup Check',$2,'student') RETURNING id`,
		uuid.NewString(), email,
	).Scan(&userID); err != nil {
		t.Fatalf("seed student: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM streaks WHERE user_id=$1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
		pool.Exec(ctx, `DELETE FROM admin_accounts WHERE lower(btrim(email))=$1`, email)
	})

	t.Run("same address cannot also be an admin", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO admin_accounts (supabase_uid, name, email, role)
			 VALUES ($1,'Dup Check',$2,'super_admin')`,
			uuid.NewString(), email)
		if err == nil {
			t.Fatal("a student's address was accepted as a super admin")
		}
		if !IsEmailTakenErr(err) {
			t.Errorf("expected the one_identity_per_email trigger, got %v", err)
		}
	})

	t.Run("case is not a loophole", func(t *testing.T) {
		_, err := pool.Exec(ctx,
			`INSERT INTO admin_accounts (supabase_uid, name, email, role)
			 VALUES ($1,'Dup Check',$2,'moderator')`,
			uuid.NewString(), strings.ToUpper(email))
		if err == nil {
			t.Fatal("uppercasing the address bypassed the rule")
		}
		if !IsEmailTakenErr(err) {
			t.Errorf("expected the one_identity_per_email trigger, got %v", err)
		}
	})

	t.Run("stored form is normalised", func(t *testing.T) {
		var stored string
		if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE id=$1`, userID).Scan(&stored); err != nil {
			t.Fatalf("read back: %v", err)
		}
		if stored != NormalizeEmail(stored) {
			t.Errorf("stored %q is not normalised", stored)
		}
	})

	t.Run("lookup reports who holds it", func(t *testing.T) {
		taken := EmailIdentityIn(ctx, pool, strings.ToUpper(email))
		if taken == nil {
			t.Fatal("EmailIdentityIn said a registered address was free")
		}
		if taken.Role != "student" {
			t.Errorf("role = %q, want student", taken.Role)
		}
	})

	t.Run("updating a row to a taken address is refused", func(t *testing.T) {
		otherEmail := "other-" + uuid.NewString()[:8] + "@example.test"
		var otherID string
		if err := pool.QueryRow(ctx,
			`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
			 VALUES ($1,'Other','Other',$2,'teacher') RETURNING id`,
			uuid.NewString(), otherEmail,
		).Scan(&otherID); err != nil {
			t.Fatalf("seed teacher: %v", err)
		}
		defer func() {
			pool.Exec(ctx, `DELETE FROM streaks WHERE user_id=$1`, otherID)
			pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, otherID)
		}()

		if _, err := pool.Exec(ctx, `UPDATE users SET email=$1 WHERE id=$2`, email, otherID); err == nil {
			t.Error("a teacher was allowed to take over a student's address by update")
		}
	})

	t.Run("a row keeping its own address is untouched", func(t *testing.T) {
		// The self-exclusion in the trigger: re-saving your own email is not a
		// collision with yourself.
		if _, err := pool.Exec(ctx, `UPDATE users SET email=$1 WHERE id=$2`, email, userID); err != nil {
			t.Errorf("re-saving an unchanged address was refused: %v", err)
		}
	})
}

func TestGetUserForLoginRepairsChangedSupabaseUID(t *testing.T) {
	pool := openTestDB(t)
	ctx := context.Background()
	oldUID := uuid.NewString()
	newUID := uuid.NewString()
	email := "uid-repair-" + uuid.NewString()[:8] + "@example.test"

	var userID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO users (supabase_uid, full_name, display_name, email, role)
		 VALUES ($1,'UID Repair','UID Repair',$2,'student') RETURNING id`,
		oldUID, email,
	).Scan(&userID); err != nil {
		t.Fatalf("seed student: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(ctx, `DELETE FROM streaks WHERE user_id=$1`, userID)
		pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, userID)
	})

	svc := &Service{db: pool}
	got, err := svc.GetUserForLogin(ctx, newUID, strings.ToUpper(email))
	if err != nil {
		t.Fatalf("GetUserForLogin: %v", err)
	}
	if got.ID != userID {
		t.Fatalf("user id = %q, want %q", got.ID, userID)
	}

	var storedUID string
	if err := pool.QueryRow(ctx, `SELECT supabase_uid FROM users WHERE id=$1`, userID).Scan(&storedUID); err != nil {
		t.Fatalf("read repaired UID: %v", err)
	}
	if storedUID != newUID {
		t.Errorf("supabase_uid = %q, want %q", storedUID, newUID)
	}
}
