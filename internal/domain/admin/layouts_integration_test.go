package admin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// seedAdmin creates a throwaway admin account and removes it (and its layouts,
// via ON DELETE CASCADE) when the test ends.
func seedAdmin(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		INSERT INTO admin_accounts (supabase_uid, name, email, role)
		VALUES (gen_random_uuid(), 'Layout Test', $1, 'super_admin')
		RETURNING id`, email).Scan(&id)
	if err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM admin_accounts WHERE id = $1`, id)
	})
	return id
}

func TestLayoutCreateAndList(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-create@test.local")
	ctx := context.Background()

	created, err := svc.Create(ctx, admin, "Executive", json.RawMessage(`{"widgets":[]}`), true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "Executive" || !created.IsDefault {
		t.Errorf("created = %+v, want name=Executive is_default=true", created)
	}

	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Errorf("List = %+v, want the one created layout", list)
	}
}

// A fresh admin must get an empty array, not null — the client renders a preset
// in that case and a null would crash the map over it.
func TestListForFreshAdminIsEmptyNotNull(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-fresh@test.local")

	list, err := svc.List(context.Background(), admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if list == nil {
		t.Fatal("List returned nil; want an empty slice")
	}
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0", len(list))
	}
}

// The core isolation guarantee: one admin's layouts are invisible to another on
// every verb.
func TestLayoutsAreIsolatedPerAdmin(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	ctx := context.Background()
	a := seedAdmin(t, pool, "layout-a@test.local")
	b := seedAdmin(t, pool, "layout-b@test.local")

	mine, err := svc.Create(ctx, a, "Mine", json.RawMessage(`{}`), false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := svc.List(ctx, b)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("admin B sees %d of admin A's layouts, want 0", len(list))
	}

	name := "Hijacked"
	if _, err := svc.Update(ctx, b, mine.ID, &name, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update across admins: err = %v, want ErrNotFound", err)
	}
	if err := svc.Delete(ctx, b, mine.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete across admins: err = %v, want ErrNotFound", err)
	}

	// A cross-admin reorder must not touch the other admin's rows either.
	if err := svc.Reorder(ctx, b, []string{mine.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	after, err := svc.List(ctx, a)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(after) != 1 || after[0].Sort != mine.Sort {
		t.Errorf("admin B's reorder changed admin A's layout: %+v", after)
	}
}

func TestLayoutDuplicateNameRejected(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-dup@test.local")
	ctx := context.Background()

	if _, err := svc.Create(ctx, admin, "Same", json.RawMessage(`{}`), false); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := svc.Create(ctx, admin, "Same", json.RawMessage(`{}`), false); !errors.Is(err, ErrDuplicateName) {
		t.Errorf("second Create: err = %v, want ErrDuplicateName", err)
	}
}

// Two admins may each have a layout of the same name — the uniqueness is
// per-admin, not global.
func TestSameNameAcrossAdminsIsFine(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	ctx := context.Background()
	a := seedAdmin(t, pool, "layout-name-a@test.local")
	b := seedAdmin(t, pool, "layout-name-b@test.local")

	if _, err := svc.Create(ctx, a, "Executive", json.RawMessage(`{}`), false); err != nil {
		t.Fatalf("Create for A: %v", err)
	}
	if _, err := svc.Create(ctx, b, "Executive", json.RawMessage(`{}`), false); err != nil {
		t.Errorf("Create for B with the same name: %v", err)
	}
}

// Two sequential is_default sets must leave exactly one default. The partial
// unique index means a non-transactional implementation fails here.
func TestOnlyOneDefaultSurvives(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-default@test.local")
	ctx := context.Background()

	if _, err := svc.Create(ctx, admin, "First", json.RawMessage(`{}`), true); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	second, err := svc.Create(ctx, admin, "Second", json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatalf("Create second: %v", err)
	}

	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var defaults []string
	for _, l := range list {
		if l.IsDefault {
			defaults = append(defaults, l.ID)
		}
	}
	if len(defaults) != 1 {
		t.Fatalf("found %d defaults (%v), want exactly 1", len(defaults), defaults)
	}
	if defaults[0] != second.ID {
		t.Errorf("default is %s, want the most recently set (%s)", defaults[0], second.ID)
	}
}

// Promoting an existing layout via Update must also clear the old default.
func TestUpdateSetsDefaultExclusively(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-promote-update@test.local")
	ctx := context.Background()

	if _, err := svc.Create(ctx, admin, "First", json.RawMessage(`{}`), true); err != nil {
		t.Fatalf("Create first: %v", err)
	}
	other, err := svc.Create(ctx, admin, "Other", json.RawMessage(`{}`), false)
	if err != nil {
		t.Fatalf("Create other: %v", err)
	}

	yes := true
	if _, err := svc.Update(ctx, admin, other.ID, nil, nil, &yes, nil); err != nil {
		t.Fatalf("Update: %v", err)
	}

	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var defaults int
	for _, l := range list {
		if l.IsDefault {
			defaults++
			if l.ID != other.ID {
				t.Errorf("default is %s, want %s", l.ID, other.ID)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("found %d defaults, want 1", defaults)
	}
}

// Deleting the default must promote a survivor, or the admin lands on a canvas
// with no selected layout.
func TestDeletingDefaultPromotesSurvivor(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-promote@test.local")
	ctx := context.Background()

	def, err := svc.Create(ctx, admin, "Default", json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatalf("Create default: %v", err)
	}
	if _, err := svc.Create(ctx, admin, "Other", json.RawMessage(`{}`), false); err != nil {
		t.Fatalf("Create other: %v", err)
	}

	if err := svc.Delete(ctx, admin, def.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("len(list) = %d, want 1", len(list))
	}
	if !list[0].IsDefault {
		t.Error("the surviving layout was not promoted to default")
	}
}

func TestDeletingLastLayoutLeavesNone(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-last@test.local")
	ctx := context.Background()

	only, err := svc.Create(ctx, admin, "Only", json.RawMessage(`{}`), true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, admin, only.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("len(list) = %d, want 0 — the client falls back to a preset", len(list))
	}
}

func TestReorderSetsSort(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-order@test.local")
	ctx := context.Background()

	a, err := svc.Create(ctx, admin, "A", json.RawMessage(`{}`), false)
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	b, err := svc.Create(ctx, admin, "B", json.RawMessage(`{}`), false)
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}
	c, err := svc.Create(ctx, admin, "C", json.RawMessage(`{}`), false)
	if err != nil {
		t.Fatalf("Create C: %v", err)
	}

	if err := svc.Reorder(ctx, admin, []string{c.ID, a.ID, b.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	list, err := svc.List(ctx, admin)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{c.ID, a.ID, b.ID}
	if len(list) != len(want) {
		t.Fatalf("len(list) = %d, want %d", len(list), len(want))
	}
	for i, id := range want {
		if list[i].ID != id {
			t.Errorf("position %d = %s, want %s", i, list[i].ID, id)
		}
	}
}

func TestUpdateUnknownIDIsNotFound(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-unknown@test.local")
	name := "x"
	_, err := svc.Update(context.Background(), admin,
		"11111111-1111-1111-1111-111111111111", &name, nil, nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateIsPartial(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-partial@test.local")
	ctx := context.Background()

	created, err := svc.Create(ctx, admin, "Original", json.RawMessage(`{"a":1}`), true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Changing only the name must leave layout and is_default untouched.
	name := "Renamed"
	updated, err := svc.Update(ctx, admin, created.ID, &name, nil, nil, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Renamed" {
		t.Errorf("Name = %q, want Renamed", updated.Name)
	}
	if !updated.IsDefault {
		t.Error("is_default was cleared by a name-only update")
	}
	if string(updated.Layout) != `{"a": 1}` && string(updated.Layout) != `{"a":1}` {
		t.Errorf("Layout = %s, want the original payload", updated.Layout)
	}
}

func TestOversizedLayoutRejected(t *testing.T) {
	pool := openTestDB(t)
	svc := NewLayoutsService(pool)
	admin := seedAdmin(t, pool, "layout-big@test.local")

	big := make([]byte, maxLayoutBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	payload := json.RawMessage(`{"pad":"` + string(big) + `"}`)

	if _, err := svc.Create(context.Background(), admin, "Big", payload, false); !errors.Is(err, ErrLayoutTooBig) {
		t.Errorf("err = %v, want ErrLayoutTooBig", err)
	}
}
