# Student Management UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the institute dashboard and teacher panel usable for running a student roster — bulk lifecycle actions, claim-code distribution, editable enrollment fields, and teacher class-membership management — on top of the student-management backend that already ships.

**Architecture:** A five-file "roster kit" of UI primitives is copied byte-identically into `src/components/ui/` in both Next apps (the apps already duplicate `components/charts/` and `components/widgets/` the same way; there is no workspace or shared package, and this plan does not create one). The four student screens are then rebuilt on that kit. Bulk actions are sequential client-side loops over existing single-record endpoints. One Go handler gains three fields on its response.

**Tech Stack:** Go 1.26 + chi + pgx (backend); Next.js 16 App Router + React + TypeScript + Tailwind v4 (both panels); `bun` as package manager in both panels; `lucide-react` for icons.

## Global Constraints

- **Spec:** `qwish-backend/docs/superpowers/specs/2026-08-02-student-management-ui-design.md`. Read it before Task 1.
- **Next.js version has breaking changes from training data.** Both panels' `AGENTS.md` require reading the relevant guide in `node_modules/next/dist/docs/` before writing route or server-component code. All files in this plan are `"use client"` components, which limits exposure, but check the docs before adding any route file.
- **Design tokens only.** Use `bg-card`, `bg-canvas`, `bg-elevated`, `bg-input`, `border-border`, `text-foreground`, `text-muted`, `text-subtle`, `text-brand`, `success|warning|danger` and their `-muted` variants. Never hardcode a new palette value.
- **`--color-primary` does not exist** in either app's `globals.css`. Any `text-primary` / `border-primary` / `focus:border-primary` you encounter is a dead class — replace with the `brand` equivalent.
- **Both panels use `bun`.** Never run `npm` in `qwish-institute-dashboard` or `qwish-teacher-panel`.
- **Neither panel has a test runner.** Verification for frontend tasks is `bun run lint` and `bun run build`, plus the stated manual check. Do not add a test framework.
- **The backend does have tests.** `go test ./...` from `qwish-backend`.
- **Repos are separate.** `qwish-backend`, `qwish-institute-dashboard` and `qwish-teacher-panel` are each their own git repo. Commit inside the directory you changed. The institute dashboard is on branch `student-management`; the other two are on `main` — branch before committing there.
- **Ownership rule from the 2026-08-01 spec:** teachers never write `roll_number`, `grade`, `section` or `admission_date`. They propose. Only the institute dashboard writes those fields.
- **`ponytail:` comments** mark deliberate shortcuts with their ceiling and upgrade path. Two are specified in this plan; write them verbatim.

---

### Task 1: Backend — complete the institution roster payload

**Files:**
- Modify: `qwish-backend/internal/domain/institution/handler.go:104-195` (`ListStudents`)
- Test: `qwish-backend/internal/domain/institution/` — see Step 1 for how to check whether a test harness exists here

**Interfaces:**
- Consumes: nothing.
- Produces: `GET /api/v1/institution/students` rows gain `claim_code: string|null` and `groups: {id,name}[]`, and the endpoint accepts `grade` and `section` query parameters. Task 2 types these; Tasks 5–7 consume them.

- [ ] **Step 1: Check for an existing test harness in this package**

Run:
```bash
cd qwish-backend && ls internal/domain/institution/*_test.go internal/domain/enrollment/*_test.go
```

`internal/domain/enrollment/` has `teacher_scope_test.go` and a `testdb_test.go`/`fixtures_test.go` pattern. If `internal/domain/institution/` has **no** `testdb_test.go`, do not build one for this change — it is a SELECT-list change to one handler. Verify with Step 4's manual query instead, and note in the commit message that the package has no harness.

- [ ] **Step 2: Add the two filters and two columns**

In `ListStudents`, after the `groupID` filter block (currently ends at line 136), add:

```go
	if grade := q.Get("grade"); grade != "" {
		where += fmt.Sprintf(` AND e.grade=$%d`, n)
		args = append(args, grade)
		n++
	}
	if section := q.Get("section"); section != "" {
		where += fmt.Sprintf(` AND e.section=$%d`, n)
		args = append(args, section)
		n++
	}
```

Read `grade` and `section` alongside `search`/`groupID`/`status` at the top of the handler if you prefer, but keep the `$n` ordering: these two clauses must be appended after `group_id` and before the `LIMIT`/`OFFSET` arguments are appended.

- [ ] **Step 3: Add `claim_code` and `groups` to the SELECT and the row struct**

Change the main `rows, err := h.db.Query(...)` SELECT list to also fetch the claim code and the student's groups. Add these two expressions after the `avg_score` expression:

```sql
	        e.claim_code,
	        COALESCE((SELECT json_agg(json_build_object('id', g.id, 'name', g.name))
	                    FROM group_students gs JOIN groups g ON g.id = gs.group_id
	                   WHERE gs.user_id = e.user_id), '[]'::json) AS groups
```

Extend the row struct and the scan:

```go
	type groupRef struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	type studentRow struct {
		EnrollmentID  string     `json:"enrollment_id"`
		ID            *string    `json:"id"` // null until the roster row is claimed
		DisplayName   string     `json:"display_name"`
		Email         string     `json:"email"`
		RollNumber    *string    `json:"roll_number,omitempty"`
		Grade         *string    `json:"grade,omitempty"`
		Section       *string    `json:"section,omitempty"`
		Status        string     `json:"status"`
		TotalPoints   int64      `json:"total_points"`
		CurrentStreak int        `json:"current_streak"`
		LastActiveAt  *time.Time `json:"last_active_at,omitempty"`
		AverageScore  float64    `json:"average_score"`
		// Only a pending_claim row has a live code. Claimed rows carry NULL,
		// which is what stops the roster screen offering a code to copy.
		ClaimCode *string    `json:"claim_code"`
		Groups    []groupRef `json:"groups"`
	}
```

Scan them in the same order they appear in the SELECT:

```go
		rows.Scan(&s.EnrollmentID, &s.ID, &s.DisplayName, &s.Email, &s.RollNumber, &s.Grade,
			&s.Section, &s.Status, &s.TotalPoints, &s.CurrentStreak, &s.LastActiveAt, &s.AverageScore,
			&s.ClaimCode, &s.Groups)
```

`pgx` decodes a `json` column straight into `[]groupRef`. If the scan errors at runtime with a decode failure, change the aggregate to `::text` and `json.Unmarshal` it — but try the direct scan first.

- [ ] **Step 4: Build and verify**

Run:
```bash
cd qwish-backend && go build ./... && go vet ./internal/domain/institution/ && go test ./...
```
Expected: build and vet clean; tests pass (this package's tests, if any, are unaffected).

Then verify the SQL itself against a database. With `DATABASE_URL` set:
```bash
cd qwish-backend && go run ./cmd/api &
curl -s "localhost:8080/api/v1/institution/students?limit=2" -H "Authorization: Bearer $ADMIN_JWT" | head -40
```
Expected: each row now carries `claim_code` (a string for `pending_claim` rows, `null` for claimed ones) and a `groups` array. Add `&grade=X` and confirm the row count narrows.

If no database is reachable, say so explicitly when reporting the task rather than claiming it verified.

- [ ] **Step 5: Commit**

```bash
cd qwish-backend
git add internal/domain/institution/handler.go
git commit -m "feat(institution): return claim_code and groups, filter roster by grade/section

The roster screen cannot offer a claim code it never receives, and grade
and section have to filter server-side because the endpoint paginates.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 2: Institute — type and client updates for the new payload

**Files:**
- Modify: `qwish-institute-dashboard/src/lib/api/types.ts` (the `StudentRow` interface, currently lines 137-151)
- Modify: `qwish-institute-dashboard/src/lib/api/institution.ts` (the `StudentListParams` interface, currently lines 44-51)

**Interfaces:**
- Consumes: the payload from Task 1.
- Produces: `StudentRow.claim_code?: string | null`, `StudentRow.groups: { id: string; name: string }[]`, and `StudentListParams` accepting `grade?: string` and `section?: string`. Tasks 3–7 rely on these names.

- [ ] **Step 1: Widen `StudentRow`**

In `src/lib/api/types.ts`, replace the `groups` line of `StudentRow` and add `claim_code`:

```ts
export interface StudentRow {
  enrollment_id: string;
  id: string | null;
  display_name: string;
  email: string;
  roll_number?: string | null;
  grade?: string | null;
  section?: string | null;
  status: EnrollmentStatus;
  total_points: number;
  current_streak: number;
  last_active_at: string | null;
  average_score: number;
  groups: { id: string; name: string }[];
  /**
   * Present only while the row is unclaimed. A claimed enrollment carries
   * null, which is what makes "copy code" absent rather than broken.
   */
  claim_code?: string | null;
}
```

Note `groups` is no longer optional — the handler always sends at least `[]`. If `tsc` then complains anywhere that constructs a `StudentRow` literal (the group detail page may), give those literals a `groups: []`.

- [ ] **Step 2: Widen `StudentListParams`**

In `src/lib/api/institution.ts`:

```ts
export interface StudentListParams {
  search?: string;
  status?: EnrollmentStatus;
  group_id?: string;
  grade?: string;
  section?: string;
  sort?: "total_points" | "average_score" | "last_active";
  page?: number;
  limit?: number;
}
```

- [ ] **Step 3: Verify**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
```
Expected: both clean. If the build reports a `StudentRow` literal missing `groups`, fix that literal and re-run.

- [ ] **Step 4: Commit**

```bash
cd qwish-institute-dashboard
git add src/lib/api/types.ts src/lib/api/institution.ts
git commit -m "types: add claim_code and groups to StudentRow, grade/section filters

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 3: Roster kit — build it in the institute dashboard

Build all five primitives here, verify them against a real screen in Task 5, then copy the finished files to the teacher panel in Task 8. Copying before they are proven means fixing every bug twice.

**Files:**
- Create: `qwish-institute-dashboard/src/components/ui/toast.tsx`
- Create: `qwish-institute-dashboard/src/components/ui/confirm-dialog.tsx`
- Create: `qwish-institute-dashboard/src/components/ui/drawer.tsx`
- Create: `qwish-institute-dashboard/src/components/ui/field.tsx`
- Create: `qwish-institute-dashboard/src/components/ui/data-table.tsx`
- Modify: `qwish-institute-dashboard/src/app/(dashboard)/layout.tsx`

**Interfaces:**
- Consumes: nothing beyond React and the token set.
- Produces, all consumed by Tasks 5, 6, 8, 9 and 10:
  - `ToastProvider` (component), `useToast(): { push(message: string, tone?: "success" | "danger"): void }`
  - `ConfirmDialog` with props `{ open, title, body, confirmLabel, destructive?, pending?, onConfirm(): void, onCancel(): void }`
  - `Drawer` with props `{ open, title, onClose(): void, children }`
  - `Field` with props `{ label, htmlFor, error?, hint?, children }`
  - `DataTable<T>` with props `{ columns: Column<T>[], rows: T[], rowKey(row: T): string, loading?: boolean, empty?: ReactNode, selectable?: boolean, selected?: Set<string>, onSelectedChange?(next: Set<string>): void, minWidth?: string }`
  - `Column<T> = { key: string; header: ReactNode; align?: "left" | "right"; width?: string; cell(row: T): ReactNode }`

- [ ] **Step 1: Toast provider**

Create `src/components/ui/toast.tsx`:

```tsx
"use client";

import { createContext, useCallback, useContext, useMemo, useState } from "react";

type Tone = "success" | "danger";
interface Toast { id: number; message: string; tone: Tone }

const ToastContext = createContext<{ push: (message: string, tone?: Tone) => void } | null>(null);

export function useToast() {
  const ctx = useContext(ToastContext);
  if (!ctx) throw new Error("useToast must be used inside <ToastProvider>");
  return ctx;
}

export function ToastProvider({ children }: { children: React.ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const push = useCallback((message: string, tone: Tone = "success") => {
    const id = Date.now() + Math.random();
    setToasts((prev) => [...prev, { id, message, tone }]);
    // ponytail: fixed 5s dismissal, add hover-to-persist if anyone complains
    setTimeout(() => setToasts((prev) => prev.filter((t) => t.id !== id)), 5000);
  }, []);

  const value = useMemo(() => ({ push }), [push]);

  return (
    <ToastContext.Provider value={value}>
      {children}
      <div
        aria-live="polite"
        className="pointer-events-none fixed bottom-4 right-4 z-[100] flex w-80 flex-col gap-2"
      >
        {toasts.map((t) => (
          <div
            key={t.id}
            className={`pointer-events-auto rounded-[10px] border px-4 py-3 text-sm shadow-lg ${
              t.tone === "danger"
                ? "border-danger/30 bg-danger-muted text-danger"
                : "border-success/30 bg-success-muted text-success"
            }`}
          >
            <div className="flex items-start justify-between gap-3">
              <p>{t.message}</p>
              <button
                onClick={() => setToasts((prev) => prev.filter((x) => x.id !== t.id))}
                aria-label="Dismiss"
                className="text-xs opacity-70 hover:opacity-100"
              >
                ✕
              </button>
            </div>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}
```

- [ ] **Step 2: Confirm dialog**

Create `src/components/ui/confirm-dialog.tsx`:

```tsx
"use client";

import { useEffect } from "react";

export function ConfirmDialog({
  open,
  title,
  body,
  confirmLabel,
  destructive = false,
  pending = false,
  onConfirm,
  onCancel,
}: {
  open: boolean;
  title: string;
  body: React.ReactNode;
  confirmLabel: string;
  destructive?: boolean;
  pending?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}) {
  useEffect(() => {
    if (!open) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape" && !pending) onCancel();
    }
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, pending, onCancel]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        className="w-full max-w-md rounded-[14px] border border-border bg-elevated p-5"
      >
        <h2 id="confirm-title" className="text-base font-medium text-foreground">{title}</h2>
        <div className="mt-2 text-sm text-muted">{body}</div>
        <div className="mt-5 flex justify-end gap-2">
          <button
            onClick={onCancel}
            disabled={pending}
            className="rounded-[10px] border border-border px-3 py-2 text-sm text-foreground hover:bg-input disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            onClick={onConfirm}
            disabled={pending}
            className={`rounded-[10px] px-3 py-2 text-sm font-medium text-white disabled:opacity-50 ${
              destructive ? "bg-danger hover:bg-danger-hover" : "bg-emerald-600 hover:bg-emerald-500"
            }`}
          >
            {pending ? "Working…" : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 3: Drawer**

Create `src/components/ui/drawer.tsx`:

```tsx
"use client";

import { useEffect, useRef } from "react";
import { X } from "lucide-react";

export function Drawer({
  open,
  title,
  onClose,
  children,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const previouslyFocused = document.activeElement as HTMLElement | null;
    panelRef.current?.focus();
    function onKey(e: KeyboardEvent) {
      if (e.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("keydown", onKey);
      previouslyFocused?.focus();
    };
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex justify-end bg-black/60">
      <button aria-label="Close" onClick={onClose} className="flex-1 cursor-default" />
      <div
        ref={panelRef}
        tabIndex={-1}
        role="dialog"
        aria-modal="true"
        aria-labelledby="drawer-title"
        className="h-full w-full max-w-md overflow-y-auto border-l border-border bg-elevated p-5 outline-none"
      >
        <div className="mb-4 flex items-center justify-between">
          <h2 id="drawer-title" className="text-base font-medium text-foreground">{title}</h2>
          <button
            onClick={onClose}
            aria-label="Close"
            className="rounded p-1 text-subtle hover:bg-input hover:text-foreground"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        {children}
      </div>
    </div>
  );
}
```

- [ ] **Step 4: Field**

Create `src/components/ui/field.tsx`:

```tsx
"use client";

export function Field({
  label,
  htmlFor,
  error,
  hint,
  children,
}: {
  label: string;
  htmlFor: string;
  error?: string | null;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div>
      <label htmlFor={htmlFor} className="text-xs text-subtle">{label}</label>
      <div className="mt-1">{children}</div>
      {hint && !error && <p className="mt-1 text-xs text-subtle">{hint}</p>}
      {error && <p className="mt-1 text-xs text-danger">{error}</p>}
    </div>
  );
}

/** Shared input styling, so every roster form's inputs match without a wrapper component. */
export const inputClass =
  "w-full rounded-[10px] border border-border bg-input px-3 py-2 text-sm text-foreground placeholder:text-subtle focus:border-emerald-500/50 focus:outline-none transition-colors";
```

- [ ] **Step 5: DataTable**

Create `src/components/ui/data-table.tsx`:

```tsx
"use client";

export interface Column<T> {
  key: string;
  header: React.ReactNode;
  align?: "left" | "right";
  width?: string;
  cell: (row: T) => React.ReactNode;
}

export function DataTable<T>({
  columns,
  rows,
  rowKey,
  loading = false,
  empty,
  selectable = false,
  selected,
  onSelectedChange,
  minWidth = "64rem",
}: {
  columns: Column<T>[];
  rows: T[];
  rowKey: (row: T) => string;
  loading?: boolean;
  empty?: React.ReactNode;
  selectable?: boolean;
  selected?: Set<string>;
  onSelectedChange?: (next: Set<string>) => void;
  minWidth?: string;
}) {
  const selectedSet = selected ?? new Set<string>();
  const allKeys = rows.map(rowKey);
  const allSelected = allKeys.length > 0 && allKeys.every((k) => selectedSet.has(k));

  function toggleAll() {
    if (!onSelectedChange) return;
    onSelectedChange(allSelected ? new Set() : new Set(allKeys));
  }

  function toggleOne(key: string) {
    if (!onSelectedChange) return;
    const next = new Set(selectedSet);
    if (next.has(key)) next.delete(key);
    else next.add(key);
    onSelectedChange(next);
  }

  const colCount = columns.length + (selectable ? 1 : 0);

  return (
    <div className="overflow-x-auto rounded-[10px] border border-border bg-card">
      <table className="w-full text-sm" style={{ minWidth }}>
        <thead>
          <tr className="border-b border-border">
            {selectable && (
              <th className="w-10 px-4 py-3">
                <input
                  type="checkbox"
                  aria-label="Select all rows"
                  checked={allSelected}
                  onChange={toggleAll}
                  className="h-4 w-4 accent-emerald-600"
                />
              </th>
            )}
            {columns.map((c) => (
              <th
                key={c.key}
                style={c.width ? { width: c.width } : undefined}
                className={`px-4 py-3 text-xs font-medium uppercase tracking-wider text-subtle ${
                  c.align === "right" ? "text-right" : "text-left"
                }`}
              >
                {c.header}
              </th>
            ))}
          </tr>
        </thead>
        <tbody className="divide-y divide-border">
          {loading
            ? Array.from({ length: 5 }).map((_, i) => (
                <tr key={i}>
                  <td colSpan={colCount} className="px-4 py-3">
                    <div className="h-4 w-3/4 animate-pulse rounded bg-input" />
                  </td>
                </tr>
              ))
            : rows.map((row) => {
                const key = rowKey(row);
                return (
                  <tr key={key} className="transition-colors duration-100 hover:bg-input/50">
                    {selectable && (
                      <td className="px-4 py-3">
                        <input
                          type="checkbox"
                          aria-label={`Select row ${key}`}
                          checked={selectedSet.has(key)}
                          onChange={() => toggleOne(key)}
                          className="h-4 w-4 accent-emerald-600"
                        />
                      </td>
                    )}
                    {columns.map((c) => (
                      <td
                        key={c.key}
                        className={`px-4 py-3 ${c.align === "right" ? "text-right" : "text-left"}`}
                      >
                        {c.cell(row)}
                      </td>
                    ))}
                  </tr>
                );
              })}
        </tbody>
      </table>
      {!loading && rows.length === 0 && empty}
    </div>
  );
}
```

- [ ] **Step 6: Mount the toast provider**

Modify `src/app/(dashboard)/layout.tsx`:

```tsx
import Sidebar from "@/components/Sidebar";
import AuthGuard from "@/components/AuthGuard";
import { ToastProvider } from "@/components/ui/toast";

export default function DashboardLayout({ children }: { children: React.ReactNode }) {
  return (
    <AuthGuard>
      <ToastProvider>
        <div className="flex h-screen overflow-hidden bg-canvas">
          <Sidebar />
          <main className="flex-1 overflow-y-auto bg-canvas">
            {children}
          </main>
        </div>
      </ToastProvider>
    </AuthGuard>
  );
}
```

- [ ] **Step 7: Verify**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
```
Expected: clean. Unused-export warnings for the kit are expected at this point — nothing consumes it until Task 5.

- [ ] **Step 8: Commit**

```bash
cd qwish-institute-dashboard
git add src/components/ui "src/app/(dashboard)/layout.tsx"
git commit -m "feat(ui): add roster kit primitives and mount toast provider

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 4: Institute — bulk-action runner

A pure function, separated from the screen so the loop's partial-failure semantics can be read and changed without touching JSX.

**Files:**
- Create: `qwish-institute-dashboard/src/lib/bulk.ts`

**Interfaces:**
- Consumes: nothing.
- Produces: `runBulk<T>(items: T[], fn: (item: T) => Promise<unknown>, onProgress?: (done: number, total: number) => void): Promise<BulkResult<T>>` and `interface BulkResult<T> { succeeded: T[]; failed: { item: T; message: string }[] }`. Task 5 consumes both.

- [ ] **Step 1: Write it**

Create `src/lib/bulk.ts`:

```ts
import { ApiError } from "./apiClient";

export interface BulkResult<T> {
  succeeded: T[];
  failed: { item: T; message: string }[];
}

/**
 * Applies a single-record call across many records.
 *
 * One failure never aborts the run: an admin who suspends thirty students and
 * hits one roll-number conflict wants the other twenty-nine suspended and the
 * one named, not a silent stop halfway.
 *
 * ponytail: sequential client-side loop, add a batch endpoint if rosters exceed ~500
 */
export async function runBulk<T>(
  items: T[],
  fn: (item: T) => Promise<unknown>,
  onProgress?: (done: number, total: number) => void
): Promise<BulkResult<T>> {
  const result: BulkResult<T> = { succeeded: [], failed: [] };
  for (const item of items) {
    try {
      await fn(item);
      result.succeeded.push(item);
    } catch (err) {
      result.failed.push({
        item,
        message: err instanceof ApiError ? err.message : "Unexpected error",
      });
    }
    onProgress?.(result.succeeded.length + result.failed.length, items.length);
  }
  return result;
}

/** "12 updated." / "12 updated, 3 failed." — the string every bulk action reports. */
export function summariseBulk<T>(result: BulkResult<T>, verb: string): string {
  const n = result.succeeded.length;
  const base = `${n} ${n === 1 ? "student" : "students"} ${verb}.`;
  return result.failed.length === 0 ? base : `${base} ${result.failed.length} failed.`;
}
```

- [ ] **Step 2: Verify**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
```
Expected: clean.

- [ ] **Step 3: Commit**

```bash
cd qwish-institute-dashboard
git add src/lib/bulk.ts
git commit -m "feat: add bulk action runner with partial-failure reporting

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 5: Institute roster screen

**Files:**
- Rewrite: `qwish-institute-dashboard/src/app/(dashboard)/students/page.tsx` (currently 331 lines)
- Create: `qwish-institute-dashboard/src/components/students/bulk-bar.tsx`
- Create: `qwish-institute-dashboard/src/lib/claim-codes.ts`

**Interfaces:**
- Consumes: `DataTable`, `Column`, `ConfirmDialog`, `useToast` (Task 3); `runBulk`, `summariseBulk` (Task 4); `StudentRow.claim_code`/`groups` and `StudentListParams.grade`/`section` (Task 2); existing `getStudents`, `setEnrollmentStatus`, `getGroups`, `addStudentToGroup` from `@/lib/api/institution`.
- Produces: `BulkBar` component and `claimCodesCsv(rows: StudentRow[]): string`. Nothing later depends on them.

- [ ] **Step 1: Claim-code CSV helper**

Create `src/lib/claim-codes.ts`:

```ts
import type { StudentRow } from "./api/types";

/**
 * Only unclaimed rows carry a live code, so a mixed roster exports just the
 * rows that still need one. Handing out a code for a claimed enrollment would
 * be handing out a code that 409s.
 */
export function claimCodesCsv(rows: StudentRow[]): string {
  const pending = rows.filter((r) => r.status === "pending_claim" && r.claim_code);
  const header = "full_name,email,roll_number,grade,section,claim_code";
  const escape = (v: string | null | undefined) =>
    v == null ? "" : /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v;
  const lines = pending.map((r) =>
    [r.display_name, r.email, r.roll_number, r.grade, r.section, r.claim_code]
      .map(escape)
      .join(",")
  );
  return [header, ...lines].join("\n");
}

export function downloadCsv(filename: string, csv: string) {
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  a.click();
  URL.revokeObjectURL(url);
}
```

- [ ] **Step 2: Bulk bar**

Create `src/components/students/bulk-bar.tsx`:

```tsx
"use client";

import type { Group } from "@/lib/api/types";

export function BulkBar({
  count,
  groups,
  busy,
  progress,
  onClear,
  onAssignGroup,
  onStatus,
}: {
  count: number;
  groups: Group[];
  busy: boolean;
  progress: string | null;
  onClear: () => void;
  onAssignGroup: (groupId: string) => void;
  onStatus: (status: "active" | "suspended" | "graduated" | "transferred") => void;
}) {
  if (count === 0) return null;

  return (
    <div className="flex flex-wrap items-center gap-3 rounded-[10px] border border-border bg-elevated px-4 py-3">
      <span className="text-sm text-foreground">
        {count} selected{progress ? ` · ${progress}` : ""}
      </span>
      <div className="flex flex-wrap items-center gap-2">
        <select
          defaultValue=""
          disabled={busy}
          onChange={(e) => {
            if (e.target.value) {
              onAssignGroup(e.target.value);
              e.target.value = "";
            }
          }}
          className="rounded-[10px] border border-border bg-input px-3 py-1.5 text-sm text-foreground disabled:opacity-50"
        >
          <option value="">Assign to group…</option>
          {groups.map((g) => (
            <option key={g.id} value={g.id}>{g.name}</option>
          ))}
        </select>
        <BulkButton disabled={busy} onClick={() => onStatus("suspended")}>Suspend</BulkButton>
        <BulkButton disabled={busy} onClick={() => onStatus("active")}>Reactivate</BulkButton>
        <BulkButton disabled={busy} onClick={() => onStatus("graduated")}>Graduate</BulkButton>
        <BulkButton disabled={busy} onClick={() => onStatus("transferred")}>Transfer out</BulkButton>
      </div>
      <button
        onClick={onClear}
        disabled={busy}
        className="ml-auto text-xs text-subtle underline underline-offset-2 hover:text-foreground disabled:opacity-50"
      >
        Clear selection
      </button>
    </div>
  );
}

function BulkButton({
  onClick,
  disabled,
  children,
}: {
  onClick: () => void;
  disabled: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      disabled={disabled}
      className="rounded-[10px] border border-border bg-card px-3 py-1.5 text-sm text-muted transition-colors hover:bg-input hover:text-foreground disabled:opacity-50"
    >
      {children}
    </button>
  );
}
```

- [ ] **Step 3: Rewrite the roster page**

Rewrite `src/app/(dashboard)/students/page.tsx`. Keep from the current file: `StreakBadge`, `formatLastActive`, the header with Promote / Import CSV / Add student links, the pagination block, and the existing confirm copy (it correctly states what happens to the student's account). Replace: the hand-rolled `<table>` with `DataTable`, `window.confirm` with `ConfirmDialog`, and the `actionError` banner with toasts.

```tsx
"use client";

import { useCallback, useState } from "react";
import Link from "next/link";
import { Search, ChevronDown, MoreHorizontal, CheckCircle, Copy, Download } from "lucide-react";
import { useApi } from "@/lib/useApi";
import { getStudents, setEnrollmentStatus, getGroups, addStudentToGroup } from "@/lib/api/institution";
import type { StudentListParams } from "@/lib/api/institution";
import type { EnrollmentStatus, StudentRow } from "@/lib/api/types";
import { isUnclaimed, statusLabel } from "@/lib/enrollment";
import { StatusBadge } from "@/components/students/status-badge";
import { DataTable, type Column } from "@/components/ui/data-table";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { useToast } from "@/components/ui/toast";
import { runBulk, summariseBulk } from "@/lib/bulk";
import { claimCodesCsv, downloadCsv } from "@/lib/claim-codes";
import { BulkBar } from "@/components/students/bulk-bar";
import { ApiError } from "@/lib/apiClient";

function StreakBadge({ streak }: { streak: number }) {
  if (streak >= 7)
    return (
      <span className="inline-flex items-center rounded-[6px] border border-success/30 bg-success-muted px-2 py-0.5 text-xs font-medium text-success">
        {streak}d Active
      </span>
    );
  if (streak > 0)
    return (
      <span className="inline-flex items-center rounded-[6px] border border-warning/30 bg-warning-muted px-2 py-0.5 text-xs font-medium text-warning">
        {streak}d At risk
      </span>
    );
  return (
    <span className="inline-flex items-center rounded-[6px] border border-danger/30 bg-danger-muted px-2 py-0.5 text-xs font-medium text-danger">
      Broken
    </span>
  );
}

function formatLastActive(iso: string | null): string {
  if (!iso) return "Never";
  const diffDays = Math.floor((Date.now() - new Date(iso).getTime()) / 86400000);
  if (diffDays === 0) return "Today";
  if (diffDays === 1) return "Yesterday";
  return `${diffDays} days ago`;
}

/** The copy for each lifecycle change, phrased as what happens to the account. */
const CONFIRM_COPY: Record<Exclude<EnrollmentStatus, "pending_claim">, { title: string; body: string; destructive: boolean }> = {
  active: {
    title: "Reactivate students?",
    body: "They will be able to sign in again.",
    destructive: false,
  },
  suspended: {
    title: "Suspend students?",
    body: "They will not be able to sign in. Their data and history are kept.",
    destructive: true,
  },
  graduated: {
    title: "Graduate students?",
    body: "They leave your roster and keep their own account, points and history. This cannot be undone from here.",
    destructive: true,
  },
  transferred: {
    title: "Transfer students out?",
    body: "They leave your roster and can join another institution. This cannot be undone from here.",
    destructive: true,
  },
};

/** Past-tense verb per status, so the toast reads "12 students suspended." */
const STATUS_VERB: Record<Exclude<EnrollmentStatus, "pending_claim">, string> = {
  active: "reactivated",
  suspended: "suspended",
  graduated: "graduated",
  transferred: "transferred out",
};

export default function StudentsPage() {
  const toast = useToast();

  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState<StudentListParams["status"] | "all">("all");
  const [grade, setGrade] = useState("");
  const [section, setSection] = useState("");
  const [page, setPage] = useState(1);

  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [progress, setProgress] = useState<string | null>(null);
  const [pendingStatus, setPendingStatus] = useState<{
    status: Exclude<EnrollmentStatus, "pending_claim">;
    rows: StudentRow[];
  } | null>(null);

  const fetcher = useCallback(
    () =>
      getStudents({
        search: search || undefined,
        status: statusFilter !== "all" ? statusFilter : undefined,
        grade: grade || undefined,
        section: section || undefined,
        page,
        limit: 20,
      }),
    [search, statusFilter, grade, section, page]
  );
  const { data, loading, error, refetch } = useApi(fetcher, [search, statusFilter, grade, section, page]);
  const { data: groups } = useApi(() => getGroups(), []);

  const rows = data?.data ?? [];
  const selectedRows = rows.filter((r) => selected.has(r.enrollment_id));

  async function applyStatus(status: Exclude<EnrollmentStatus, "pending_claim">, targets: StudentRow[]) {
    setBusy(true);
    const result = await runBulk(
      targets,
      (row) => setEnrollmentStatus(row.enrollment_id, status),
      (done, total) => setProgress(`${done}/${total}`)
    );
    setBusy(false);
    setProgress(null);
    setPendingStatus(null);
    setSelected(new Set());
    toast.push(summariseBulk(result, STATUS_VERB[status]), result.failed.length ? "danger" : "success");
    for (const f of result.failed.slice(0, 3)) {
      toast.push(`${f.item.display_name}: ${f.message}`, "danger");
    }
    refetch();
  }

  async function assignGroup(groupId: string) {
    // An unclaimed row has no user id, and group membership is user-keyed.
    const targets = selectedRows.filter((r) => r.id !== null);
    const skipped = selectedRows.length - targets.length;
    setBusy(true);
    const result = await runBulk(
      targets,
      (row) => addStudentToGroup(groupId, row.id as string),
      (done, total) => setProgress(`${done}/${total}`)
    );
    setBusy(false);
    setProgress(null);
    setSelected(new Set());
    const suffix = skipped > 0 ? ` ${skipped} unclaimed row${skipped === 1 ? "" : "s"} skipped.` : "";
    toast.push(summariseBulk(result, "added to the group") + suffix, result.failed.length ? "danger" : "success");
    refetch();
  }

  async function copyCode(row: StudentRow) {
    if (!row.claim_code) return;
    try {
      await navigator.clipboard.writeText(row.claim_code);
      toast.push(`Claim code for ${row.display_name} copied.`);
    } catch {
      toast.push("Could not copy to the clipboard.", "danger");
    }
  }

  const columns: Column<StudentRow>[] = [
    {
      key: "student",
      header: "Student",
      cell: (s) =>
        isUnclaimed(s) ? (
          <div>
            <p className="font-medium text-muted">{s.display_name}</p>
            <p className="text-xs text-subtle">{s.email || "Awaiting sign-up"}</p>
          </div>
        ) : (
          <Link href={`/students/${s.id}`} className="transition-colors hover:text-emerald-400">
            <p className="font-medium text-foreground">{s.display_name}</p>
            <p className="text-xs text-subtle">{s.email}</p>
          </Link>
        ),
    },
    { key: "roll", header: "Roll no.", cell: (s) => <span className="font-mono text-xs text-muted">{s.roll_number || "—"}</span> },
    { key: "grade", header: "Grade", cell: (s) => <span className="text-xs text-muted">{s.grade || "—"}</span> },
    { key: "section", header: "Section", cell: (s) => <span className="text-xs text-muted">{s.section || "—"}</span> },
    {
      key: "groups",
      header: "Groups",
      cell: (s) =>
        s.groups.length > 0 ? (
          <div className="flex flex-wrap gap-1">
            {s.groups.map((g) => (
              <span key={g.id} className="rounded-[6px] bg-input px-2 py-0.5 text-xs text-muted">{g.name}</span>
            ))}
          </div>
        ) : (
          <span className="text-xs text-subtle">—</span>
        ),
    },
    { key: "points", header: "Points", align: "right", cell: (s) => <span className="font-mono text-xs font-semibold text-foreground">{s.total_points.toLocaleString()}</span> },
    { key: "score", header: "Avg Score", align: "right", cell: (s) => <span className="font-mono text-xs text-muted">{s.average_score.toFixed(1)}%</span> },
    { key: "streak", header: "Streak", cell: (s) => <StreakBadge streak={s.current_streak} /> },
    { key: "active", header: "Last Active", cell: (s) => <span className="text-xs text-subtle">{formatLastActive(s.last_active_at)}</span> },
    { key: "status", header: "Status", cell: (s) => <StatusBadge status={s.status} /> },
    {
      key: "actions",
      header: "",
      align: "right",
      width: "3rem",
      cell: (s) => (
        // Native <details> so the menu closes on Escape and needs no
        // outside-click handler.
        <details className="group relative inline-block">
          <summary
            aria-label={`Actions for ${s.display_name}`}
            className="cursor-pointer list-none rounded p-1 text-subtle transition-colors hover:bg-elevated hover:text-muted"
          >
            <MoreHorizontal className="h-4 w-4" />
          </summary>
          <div className="absolute right-0 z-10 mt-1 w-52 rounded-[10px] border border-border bg-elevated py-1 text-left shadow-lg">
            {s.status === "pending_claim" && s.claim_code && (
              <MenuItem onClick={() => copyCode(s)}>
                <Copy className="mr-2 inline h-3.5 w-3.5" />Copy claim code
              </MenuItem>
            )}
            {s.status === "suspended" ? (
              <MenuItem onClick={() => setPendingStatus({ status: "active", rows: [s] })}>Reactivate</MenuItem>
            ) : (
              <MenuItem onClick={() => setPendingStatus({ status: "suspended", rows: [s] })}>Suspend</MenuItem>
            )}
            <MenuItem onClick={() => setPendingStatus({ status: "graduated", rows: [s] })}>Graduate</MenuItem>
            <MenuItem onClick={() => setPendingStatus({ status: "transferred", rows: [s] })}>Transfer out</MenuItem>
          </div>
        </details>
      ),
    },
  ];

  return (
    <div className="max-w-screen-xl space-y-6 p-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 className="text-2xl font-semibold text-foreground">Students</h1>
          <p className="mt-1 text-sm text-muted">
            {data ? `${data.meta.total.toLocaleString()} enrolled students` : "Loading…"}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => {
              const csv = claimCodesCsv(rows);
              if (csv.split("\n").length <= 1) {
                toast.push("No unclaimed students in this view.", "danger");
                return;
              }
              downloadCsv("claim-codes.csv", csv);
            }}
            className="rounded-[10px] border border-border bg-card px-3 py-2 text-sm text-muted transition-colors hover:bg-input hover:text-foreground"
          >
            <Download className="mr-1.5 inline h-3.5 w-3.5" />Codes CSV
          </button>
          <Link href="/students/promote" className="rounded-[10px] border border-border bg-card px-3 py-2 text-sm text-muted transition-colors hover:bg-input hover:text-foreground">Promote</Link>
          <Link href="/students/import" className="rounded-[10px] border border-border bg-card px-3 py-2 text-sm text-muted transition-colors hover:bg-input hover:text-foreground">Import CSV</Link>
          <Link href="/students/new" className="rounded-[10px] bg-emerald-600 px-3 py-2 text-sm font-medium text-white transition-colors hover:bg-emerald-500">Add student</Link>
        </div>
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative max-w-sm flex-1">
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-subtle" />
          <input
            type="text"
            placeholder="Search by name or email…"
            value={search}
            onChange={(e) => { setSearch(e.target.value); setPage(1); }}
            className="w-full rounded-[10px] border border-border bg-input py-2 pl-9 pr-4 text-sm text-foreground transition-colors placeholder:text-subtle focus:border-emerald-500/50 focus:outline-none"
          />
        </div>
        <input
          type="text"
          placeholder="Grade"
          value={grade}
          onChange={(e) => { setGrade(e.target.value); setPage(1); }}
          className="w-24 rounded-[10px] border border-border bg-input px-3 py-2 text-sm text-foreground placeholder:text-subtle focus:border-emerald-500/50 focus:outline-none"
        />
        <input
          type="text"
          placeholder="Section"
          value={section}
          onChange={(e) => { setSection(e.target.value); setPage(1); }}
          className="w-24 rounded-[10px] border border-border bg-input px-3 py-2 text-sm text-foreground placeholder:text-subtle focus:border-emerald-500/50 focus:outline-none"
        />
        <div className="relative">
          <select
            value={statusFilter}
            onChange={(e) => { setStatusFilter(e.target.value as typeof statusFilter); setPage(1); }}
            className="cursor-pointer appearance-none rounded-[10px] border border-border bg-input px-4 py-2 pr-8 text-sm text-foreground transition-colors focus:border-emerald-500/50 focus:outline-none"
          >
            <option value="all">All Status</option>
            {(["pending_claim", "active", "suspended", "graduated", "transferred"] as const).map((s) => (
              <option key={s} value={s}>{statusLabel(s)}</option>
            ))}
          </select>
          <ChevronDown className="pointer-events-none absolute right-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-subtle" />
        </div>
      </div>

      <BulkBar
        count={selected.size}
        groups={groups ?? []}
        busy={busy}
        progress={progress}
        onClear={() => setSelected(new Set())}
        onAssignGroup={assignGroup}
        onStatus={(status) => setPendingStatus({ status, rows: selectedRows })}
      />

      {error && (
        <div className="rounded-[10px] border border-danger/30 bg-danger-muted p-4 text-sm text-danger">{error}</div>
      )}

      <DataTable
        columns={columns}
        rows={rows}
        rowKey={(s) => s.enrollment_id}
        loading={loading}
        selectable
        selected={selected}
        onSelectedChange={setSelected}
        empty={
          <div className="flex flex-col items-center justify-center py-16 text-center">
            <CheckCircle className="mb-4 h-12 w-12 text-zinc-700" />
            <p className="font-medium text-muted">No students match your filters</p>
            <p className="mt-1 text-sm text-subtle">Try adjusting your search or filter.</p>
          </div>
        }
      />

      {data && data.meta.total > data.meta.limit && (
        <div className="flex items-center justify-between text-sm text-subtle">
          <p>
            Showing {(page - 1) * data.meta.limit + 1}–
            {Math.min(page * data.meta.limit, data.meta.total)} of {data.meta.total.toLocaleString()}
          </p>
          <div className="flex gap-2">
            <button
              disabled={page === 1}
              onClick={() => setPage((p) => p - 1)}
              className="rounded-[10px] border border-border bg-card px-3 py-1.5 transition-colors hover:bg-input hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
            >
              ← Previous
            </button>
            <button
              disabled={page * data.meta.limit >= data.meta.total}
              onClick={() => setPage((p) => p + 1)}
              className="rounded-[10px] border border-border bg-card px-3 py-1.5 transition-colors hover:bg-input hover:text-foreground disabled:cursor-not-allowed disabled:opacity-40"
            >
              Next →
            </button>
          </div>
        </div>
      )}

      <ConfirmDialog
        open={pendingStatus !== null}
        title={pendingStatus ? CONFIRM_COPY[pendingStatus.status].title : ""}
        body={
          pendingStatus ? (
            <>
              <p>{CONFIRM_COPY[pendingStatus.status].body}</p>
              <p className="mt-2 text-foreground">
                {pendingStatus.rows.length === 1
                  ? pendingStatus.rows[0].display_name
                  : `${pendingStatus.rows.length} students`}
              </p>
            </>
          ) : null
        }
        confirmLabel="Confirm"
        destructive={pendingStatus ? CONFIRM_COPY[pendingStatus.status].destructive : false}
        pending={busy}
        onConfirm={() => pendingStatus && applyStatus(pendingStatus.status, pendingStatus.rows)}
        onCancel={() => setPendingStatus(null)}
      />
    </div>
  );
}

function MenuItem({ onClick, children }: { onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className="block w-full px-3 py-2 text-left text-sm text-muted transition-colors hover:bg-input hover:text-foreground"
    >
      {children}
    </button>
  );
}
```

Note the unused `ApiError` import if you do not end up referencing it — remove it rather than leaving a lint warning.

- [ ] **Step 4: Verify**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
```
Expected: clean.

Then `bun run dev` and check on `/students`:
1. Select two rows → the bulk bar appears with the count.
2. Bulk Suspend → confirm dialog names "2 students" → after confirming, a toast reports "2 students suspended." and the rows refresh.
3. Escape closes the confirm dialog without acting.
4. Set Grade to a value present in your data → the list narrows and the total updates (proves the filter is server-side).
5. On a `pending_claim` row, the row menu offers Copy claim code and the clipboard receives it.
6. "Codes CSV" downloads a file whose rows are only the unclaimed students of the current view.

- [ ] **Step 5: Commit**

```bash
cd qwish-institute-dashboard
git add "src/app/(dashboard)/students/page.tsx" src/components/students/bulk-bar.tsx src/lib/claim-codes.ts
git commit -m "feat(students): bulk actions, grade/section filters, claim-code distribution

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 6: Institute student detail screen

**Files:**
- Rewrite: `qwish-institute-dashboard/src/app/(dashboard)/students/[id]/page.tsx` (currently 265 lines)
- Create: `qwish-institute-dashboard/src/components/students/enrollment-drawer.tsx`

**Interfaces:**
- Consumes: `Drawer`, `Field`, `inputClass`, `ConfirmDialog`, `useToast` (Task 3); existing `getStudent`, `updateEnrollment`, `setEnrollmentStatus`, `getGroups`, `addStudentToGroup`, `removeStudentFromGroup`, `listEditRequests`, `reviewEditRequest` from `@/lib/api/institution`.
- Produces: `EnrollmentDrawer`. Nothing later depends on it.

- [ ] **Step 1: Confirm the review-queue signatures before writing against them**

Run:
```bash
cd qwish-institute-dashboard && sed -n '410,440p' src/lib/api/institution.ts
```

Read the exact parameter shapes of `listEditRequests` and `reviewEditRequest` and use them verbatim in Step 3. `listEditRequests` takes a status filter argument; if it cannot filter to one enrollment, fetch pending requests and filter client-side by `enrollment_id` — do not add a backend parameter for this.

- [ ] **Step 2: Enrollment drawer**

Create `src/components/students/enrollment-drawer.tsx`:

```tsx
"use client";

import { useState } from "react";
import { Drawer } from "@/components/ui/drawer";
import { Field, inputClass } from "@/components/ui/field";
import { updateEnrollment } from "@/lib/api/institution";
import { ApiError } from "@/lib/apiClient";

export interface EnrollmentFields {
  roll_number: string;
  grade: string;
  section: string;
  admission_date: string;
}

export function EnrollmentDrawer({
  open,
  enrollmentId,
  studentName,
  initial,
  onClose,
  onSaved,
}: {
  open: boolean;
  enrollmentId: string;
  studentName: string;
  initial: EnrollmentFields;
  onClose: () => void;
  onSaved: (message: string) => void;
}) {
  const [values, setValues] = useState<EnrollmentFields>(initial);
  const [rollError, setRollError] = useState<string | null>(null);
  const [formError, setFormError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  function set(key: keyof EnrollmentFields, value: string) {
    setValues((v) => ({ ...v, [key]: value }));
    setRollError(null);
    setFormError(null);
  }

  async function save() {
    if (values.admission_date && !/^\d{4}-\d{2}-\d{2}$/.test(values.admission_date)) {
      setFormError("Admission date must be YYYY-MM-DD.");
      return;
    }
    setBusy(true);
    try {
      await updateEnrollment(enrollmentId, {
        // The endpoint takes the roster shape; full_name lives on the users row
        // and is the student's to change, so it is echoed unchanged.
        full_name: studentName,
        roll_number: values.roll_number || undefined,
        grade: values.grade || undefined,
        section: values.section || undefined,
        admission_date: values.admission_date || undefined,
      });
      onSaved(`Updated ${studentName}.`);
      onClose();
    } catch (err) {
      // A roll-number clash belongs on the roll-number field, not in a toast
      // the admin has to read and then hunt for the cause of.
      if (err instanceof ApiError && err.code === "ROLL_NUMBER_TAKEN") {
        setRollError("Another live enrollment already uses this roll number.");
      } else {
        setFormError(err instanceof ApiError ? err.message : "Could not save.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Drawer open={open} title="Edit enrollment" onClose={onClose}>
      <div className="space-y-4">
        <Field label="Roll number" htmlFor="roll_number" error={rollError}>
          <input
            id="roll_number"
            value={values.roll_number}
            onChange={(e) => set("roll_number", e.target.value)}
            className={inputClass}
          />
        </Field>
        <Field label="Grade" htmlFor="grade">
          <input id="grade" value={values.grade} onChange={(e) => set("grade", e.target.value)} className={inputClass} />
        </Field>
        <Field label="Section" htmlFor="section">
          <input id="section" value={values.section} onChange={(e) => set("section", e.target.value)} className={inputClass} />
        </Field>
        <Field label="Admission date" htmlFor="admission_date" hint="YYYY-MM-DD">
          <input
            id="admission_date"
            value={values.admission_date}
            onChange={(e) => set("admission_date", e.target.value)}
            placeholder="2026-04-01"
            className={inputClass}
          />
        </Field>

        {formError && <p className="text-sm text-danger">{formError}</p>}

        <div className="flex justify-end gap-2 pt-2">
          <button
            onClick={onClose}
            disabled={busy}
            className="rounded-[10px] border border-border px-3 py-2 text-sm text-foreground hover:bg-input disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            onClick={save}
            disabled={busy}
            className="rounded-[10px] bg-emerald-600 px-3 py-2 text-sm font-medium text-white hover:bg-emerald-500 disabled:opacity-50"
          >
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </Drawer>
  );
}
```

- [ ] **Step 3: Rewrite the detail page**

Rewrite `src/app/(dashboard)/students/[id]/page.tsx`, keeping its existing structure — back link, avatar header, four summary cards, badges block, and the Quiz History / Points Ledger / Groups tabs — and changing these five things:

1. Replace the two-state Active/Suspended pill in the header with `<StatusBadge status={student.status} />`, and show roll number, grade and section beneath the email as `Roll 12 · Grade 9 · Section B`, with an em dash for each missing value.
2. Add an "Edit enrollment" button beside the suspend/reactivate button, opening `EnrollmentDrawer` with `initial` built from `student.roll_number ?? ""` and friends. `onSaved` pushes a toast and calls `refetch()`. `StudentProfile` extends `StudentRow`, so `enrollment_id` is available on `student`.
3. Replace the bespoke suspend modal with `ConfirmDialog`, and route the action through `setEnrollmentStatus(student.enrollment_id, "suspended")` rather than `updateStudentStatus`, so the enrollment and the user row move together. Add Graduate and Transfer out beside it, reusing the confirm copy from Task 5.
4. In the Groups tab, add a group `<select>` of `getGroups()` results that are not already in `student.groups`, calling `addStudentToGroup(groupId, student.id)`, and a Remove button per current group calling `removeStudentFromGroup(g.id, student.id)`. Both push a toast and `refetch()`.
5. Add a "Pending corrections" card above the tabs, rendered only when this student has pending requests. For each: the field label, `current_value ?? "not set"` → `proposed_value`, the requesting teacher's name, the note, and Approve / Reject buttons calling `reviewEditRequest` with the signature confirmed in Step 1. On success, push a toast and `refetch()` both the student and the request list.

Replace every `actionError` banner with `toast.push(message, "danger")`. Keep the existing `Skeleton()` function unchanged.

- [ ] **Step 4: Verify**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
```
Expected: clean.

Then `bun run dev` and check on a student's detail page:
1. Edit enrollment opens the drawer with current values pre-filled; Escape closes it; saving shows a toast and the header reflects the new values.
2. Entering a roll number already used by another live enrollment shows the error under the roll-number field and leaves the drawer open.
3. Suspend goes through the confirm dialog and the status badge changes.
4. Adding and removing a group updates the Groups tab.
5. With a pending edit-request for this student (create one from the teacher panel), the corrections card appears and Approve removes it.

- [ ] **Step 5: Commit**

```bash
cd qwish-institute-dashboard
git add "src/app/(dashboard)/students/[id]/page.tsx" src/components/students/enrollment-drawer.tsx
git commit -m "feat(students): editable enrollment, group management, inline edit-request review

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 7: Institute — retire the dead status endpoint call

**Files:**
- Modify: `qwish-institute-dashboard/src/lib/api/institution.ts:64-76` (`updateStudentStatus`)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. This is cleanup.

- [ ] **Step 1: Check for remaining callers**

Run:
```bash
cd qwish-institute-dashboard && grep -rn "updateStudentStatus" src/
```

After Task 6 the only hit should be the definition itself.

- [ ] **Step 2: Mark it, do not delete it**

The backend still serves `PATCH /institution/students/{userId}/status` for compatibility. Leave the function in place with a comment above it:

```ts
/**
 * Legacy: writes users.status only, leaving the enrollment untouched. The UI
 * uses setEnrollmentStatus instead, which moves both. Kept because the backend
 * route is still served and an external caller may exist.
 *
 * ponytail: dead in this app, delete once the backend route is retired
 */
```

If the grep in Step 1 found callers outside `students/`, leave them alone and report them — they are outside this plan's scope.

- [ ] **Step 3: Verify and commit**

```bash
cd qwish-institute-dashboard && bun run lint && bun run build
git add src/lib/api/institution.ts
git commit -m "docs: mark updateStudentStatus as legacy

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 8: Teacher panel — port the roster kit

**Files:**
- Create: `qwish-teacher-panel/src/components/ui/toast.tsx`
- Create: `qwish-teacher-panel/src/components/ui/confirm-dialog.tsx`
- Create: `qwish-teacher-panel/src/components/ui/drawer.tsx`
- Create: `qwish-teacher-panel/src/components/ui/field.tsx`
- Create: `qwish-teacher-panel/src/components/ui/data-table.tsx`
- Create: `qwish-teacher-panel/src/components/students/status-badge.tsx`
- Create: `qwish-teacher-panel/src/lib/enrollment.ts`
- Modify: `qwish-teacher-panel/src/app/(dashboard)/layout.tsx`

**Interfaces:**
- Consumes: the finished kit from Task 3.
- Produces: the same exports as Task 3, plus `StatusBadge` and `statusLabel`/`statusTone` in the teacher panel. Tasks 9 and 10 consume them.

- [ ] **Step 1: Branch first**

This repo is on `main`.

```bash
cd qwish-teacher-panel && git checkout -b student-management
```

- [ ] **Step 2: Copy the five kit files verbatim**

```bash
cd /Users/suyog/Documents/NumPieBackend
mkdir -p qwish-teacher-panel/src/components/ui
cp qwish-institute-dashboard/src/components/ui/*.tsx qwish-teacher-panel/src/components/ui/
```

Do not edit them. They compile against tokens both apps define. If `bun run build` later reports a token or import that does not resolve, fix it in **both** copies so they stay identical.

- [ ] **Step 3: Port the status badge and its label helpers**

The teacher panel's `TeacherStudentRow.status` is only `"active" | "suspended"` — a teacher never sees unclaimed, graduated or transferred rows. Give the panel its own narrow version rather than importing the institute's five-state enum.

Create `src/lib/enrollment.ts`:

```ts
export type TeacherEnrollmentStatus = "active" | "suspended";

export function statusLabel(status: TeacherEnrollmentStatus): string {
  return status === "active" ? "Active" : "Suspended";
}

export function statusTone(status: TeacherEnrollmentStatus): "success" | "danger" {
  return status === "active" ? "success" : "danger";
}
```

Create `src/components/students/status-badge.tsx`:

```tsx
import { statusLabel, statusTone, type TeacherEnrollmentStatus } from "@/lib/enrollment";

const toneClass: Record<string, string> = {
  success: "bg-success-muted text-success border-success/30",
  danger: "bg-danger-muted text-danger border-danger/30",
};

export function StatusBadge({ status }: { status: TeacherEnrollmentStatus }) {
  const label = statusLabel(status);
  return (
    <span
      aria-label={`Status: ${label}`}
      className={`inline-flex rounded-[6px] border px-2 py-0.5 text-xs font-medium ${toneClass[statusTone(status)]}`}
    >
      {label}
    </span>
  );
}
```

- [ ] **Step 4: Mount the toast provider**

Modify `src/app/(dashboard)/layout.tsx` exactly as in Task 3 Step 6 — the two layout files are currently identical, so the same edit applies.

- [ ] **Step 5: Verify**

Run:
```bash
cd qwish-teacher-panel && bun run lint && bun run build
```
Expected: clean.

Confirm the copies really are identical:
```bash
cd /Users/suyog/Documents/NumPieBackend
diff -r qwish-institute-dashboard/src/components/ui qwish-teacher-panel/src/components/ui
```
Expected: no output.

- [ ] **Step 6: Commit**

```bash
cd qwish-teacher-panel
git add src/components/ui src/components/students/status-badge.tsx src/lib/enrollment.ts "src/app/(dashboard)/layout.tsx"
git commit -m "feat(ui): port roster kit from the institute dashboard

Copied verbatim, matching how charts/ and widgets/ are already shared
between the two panels.

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 9: Teacher roster screen

**Files:**
- Rewrite: `qwish-teacher-panel/src/app/(dashboard)/students/page.tsx` (currently 164 lines)
- Create: `qwish-teacher-panel/src/components/students/add-to-class-dialog.tsx`

**Interfaces:**
- Consumes: `DataTable`, `Column`, `ConfirmDialog`, `useToast`, `StatusBadge` (Task 8); existing `listStudents`, `listClasses`, `getClass`, `addStudentToClass`, `removeStudentFromClass` from `@/lib/api/teacher`; the existing `ProposeEditDialog`.
- Produces: `AddToClassDialog`. Nothing later depends on it.

- [ ] **Step 1: Add-to-class dialog**

The candidate list is the teacher's own students who are not already in the selected class. `GET /teacher/students` is class-scoped, so this lets a teacher move a student between their own classes; it cannot reach a student they do not teach, and that is the documented limit.

Create `src/components/students/add-to-class-dialog.tsx`:

```tsx
"use client";

import { useMemo, useState } from "react";
import { useApi } from "@/lib/useApi";
import { listStudents, addStudentToClass } from "@/lib/api/teacher";
import { ApiError } from "@/lib/apiClient";

export function AddToClassDialog({
  classId,
  className,
  memberIds,
  onClose,
  onAdded,
}: {
  classId: string;
  className: string;
  memberIds: Set<string>;
  onClose: () => void;
  onAdded: (message: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const { data, loading } = useApi(() => listStudents({ limit: 50 }), []);

  const candidates = useMemo(
    () =>
      (data?.data ?? [])
        .filter((s) => !memberIds.has(s.id))
        .filter((s) =>
          query.trim() === ""
            ? true
            : `${s.display_name} ${s.email}`.toLowerCase().includes(query.trim().toLowerCase())
        ),
    [data, memberIds, query]
  );

  async function add(userId: string, name: string) {
    setBusyId(userId);
    setError(null);
    try {
      await addStudentToClass(classId, userId);
      onAdded(`${name} added to ${className}.`);
      onClose();
    } catch (err) {
      setError(
        err instanceof ApiError && err.code === "NOT_IN_YOUR_CLASS"
          ? "You do not teach this class."
          : err instanceof ApiError
            ? err.message
            : "Could not add this student."
      );
      setBusyId(null);
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4">
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="add-to-class-title"
        className="flex max-h-[80vh] w-full max-w-md flex-col rounded-[14px] border border-border bg-elevated p-5"
      >
        <h2 id="add-to-class-title" className="text-base font-medium text-foreground">
          Add a student to {className}
        </h2>
        <p className="mt-1 text-sm text-muted">
          Shows students from your other classes who are not already in this one.
        </p>

        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search your students…"
          className="mt-4 w-full rounded-[10px] border border-border bg-canvas px-3 py-2 text-sm text-foreground placeholder:text-subtle focus:border-emerald-500/40 focus:outline-none"
        />

        <div className="mt-3 flex-1 overflow-y-auto">
          {loading && <p className="py-6 text-center text-sm text-subtle">Loading…</p>}
          {!loading && candidates.length === 0 && (
            <p className="py-6 text-center text-sm text-subtle">No students available to add.</p>
          )}
          {candidates.map((s) => (
            <div key={s.id} className="flex items-center justify-between border-b border-border py-2 last:border-0">
              <div>
                <p className="text-sm text-foreground">{s.display_name}</p>
                <p className="text-xs text-subtle">{s.email}</p>
              </div>
              <button
                onClick={() => add(s.id, s.display_name)}
                disabled={busyId !== null}
                className="rounded-[10px] bg-emerald-500 px-3 py-1.5 text-xs font-medium text-black hover:bg-emerald-400 disabled:opacity-50"
              >
                {busyId === s.id ? "Adding…" : "Add"}
              </button>
            </div>
          ))}
        </div>

        {error && <p className="mt-3 text-sm text-danger">{error}</p>}

        <div className="mt-4 flex justify-end">
          <button
            onClick={onClose}
            className="rounded-[10px] border border-border px-3 py-2 text-sm text-foreground hover:bg-input"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
```

- [ ] **Step 2: Rewrite the roster page**

Rewrite `src/app/(dashboard)/students/page.tsx` keeping its search form, sort select and pagination, and changing these things:

1. Replace the hand-rolled `<table>` with `DataTable` and these columns: Name (linking to `/students/detail?id=${s.id}`), Roll no. (`font-mono text-xs`), Grade, Section, Email, Points (right), Streak (right), Avg score (right), Last active (right), Status (`StatusBadge`), and a right-aligned actions cell. Missing roll/grade/section render as an em dash. Do not make these editable — they are institution-owned; the only path is a proposal.
2. Add a class `<select>` beside the sort select, populated from `listClasses()`, defaulting to "All my classes" (`class_id: undefined`). Selecting one passes `class_id` to `listStudents`.
3. When a class is selected, show an "Add student" button opening `AddToClassDialog` with `memberIds` built from the current rows, and give each row a Remove action calling `removeStudentFromClass(classId, s.id)` behind a `ConfirmDialog` reading "Remove {name} from {class}? They keep their account and their record; only the class membership changes." With no class selected, neither control renders.
4. Move "Suggest a correction" into a `<details>` row menu matching the institute roster's pattern, still opening the existing `ProposeEditDialog`.
5. Replace the `notice` banner state with `useToast()`. `ProposeEditDialog`'s `onSubmitted` prop takes a message string — pass `(msg) => toast.push(msg)`.
6. Replace `text-primary` with `text-brand` and `focus:border-primary` with `focus:border-emerald-500/40` wherever they appear in this file.

- [ ] **Step 3: Verify**

Run:
```bash
cd qwish-teacher-panel && bun run lint && bun run build
```
Expected: clean.

Then `bun run dev` and check on `/students`:
1. Roll no., Grade and Section columns are populated for students who have them.
2. With "All my classes" selected, no Add or Remove control is visible.
3. Selecting a class narrows the list, and Add student opens a picker excluding students already in that class.
4. Adding a student shows a toast and the row appears after the refetch.
5. Remove asks for confirmation and the row disappears on success.
6. "Suggest a correction" still submits and toasts.

- [ ] **Step 4: Commit**

```bash
cd qwish-teacher-panel
git add "src/app/(dashboard)/students/page.tsx" src/components/students/add-to-class-dialog.tsx
git commit -m "feat(students): enrollment columns, class filter, class membership management

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 10: Teacher student detail screen

**Files:**
- Modify: `qwish-teacher-panel/src/app/(dashboard)/students/detail/page.tsx` (currently 210 lines)

**Interfaces:**
- Consumes: `StatusBadge`, `useToast` (Task 8); the existing `ProposeEditDialog`; `fieldLabel` and `PROPOSABLE_FIELDS` from `@/lib/proposals`.
- Produces: nothing.

- [ ] **Step 1: Read the current page in full**

Run:
```bash
cd qwish-teacher-panel && cat "src/app/(dashboard)/students/detail/page.tsx"
```

It already has a header, stat cards, quiz history and a `Suspense` wrapper around a `useSearchParams` read. Keep all of that structure — this task adds a card and a per-field action, it does not rewrite the page.

- [ ] **Step 2: Add the enrollment card**

Below the stat cards, add a read-only "Enrollment" card listing Roll number, Grade and Section from `data.roll_number` / `data.grade` / `data.section`, plus the classes from `data.classes` as chips. Each of the three fields gets a "Suggest a correction" button on its row that opens `ProposeEditDialog` for this student's `data.enrollment_id`.

`ProposeEditDialog` currently always opens on `roll_number` and lets the teacher pick the field. Add an optional `initialField?: ProposableField` prop to it, defaulting to `"roll_number"`, so a per-row button lands on the right field:

```tsx
export default function ProposeEditDialog({
  enrollmentId,
  studentName,
  initialField = "roll_number",
  onClose,
  onSubmitted,
}: {
  enrollmentId: string;
  studentName: string;
  initialField?: ProposableField;
  onClose: () => void;
  onSubmitted: (message: string) => void;
}) {
  const [field, setField] = useState<ProposableField>(initialField);
```

Everything else in that component is unchanged, and the existing call site in Task 9 keeps working because the prop is optional.

Add the status badge to the header beside the student's name, and replace this file's `text-primary` occurrences with `text-brand`.

- [ ] **Step 3: Verify**

Run:
```bash
cd qwish-teacher-panel && bun run lint && bun run build
```
Expected: clean.

Then `bun run dev` and open a student from `/students`:
1. The Enrollment card shows roll number, grade, section and class chips.
2. "Suggest a correction" on the Grade row opens the dialog with Grade preselected.
3. Submitting toasts, and the request appears on `/edit-requests`.
4. Nothing on this page writes an institution-owned field directly.

- [ ] **Step 4: Commit**

```bash
cd qwish-teacher-panel
git add "src/app/(dashboard)/students/detail/page.tsx" src/components/students/propose-edit-dialog.tsx
git commit -m "feat(students): enrollment card with per-field correction proposals

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

---

### Task 11: Cross-app consistency pass

**Files:**
- Modify: any file in either panel still referencing the non-existent `primary` token
- Verify: `qwish-institute-dashboard/src/components/ui/` vs `qwish-teacher-panel/src/components/ui/`

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

- [ ] **Step 1: Find remaining dead token classes**

Run:
```bash
cd /Users/suyog/Documents/NumPieBackend
grep -rn "text-primary\|border-primary\|bg-primary" qwish-teacher-panel/src qwish-institute-dashboard/src
```

Replace each with the `brand` equivalent (`text-brand`, `border-brand`, `bg-brand`). If a hit is outside the student screens, still fix it — it is a one-word change and the class currently renders nothing.

- [ ] **Step 2: Confirm the kit copies have not drifted**

Run:
```bash
cd /Users/suyog/Documents/NumPieBackend
diff -r qwish-institute-dashboard/src/components/ui qwish-teacher-panel/src/components/ui
```
Expected: no output. If they differ, reconcile so both hold the newer version.

- [ ] **Step 3: Full verification of both apps**

Run:
```bash
cd qwish-institute-dashboard && bun run lint && bun run build
cd ../qwish-teacher-panel && bun run lint && bun run build
cd ../qwish-backend && go build ./... && go test ./...
```
Expected: all clean. Report the actual output — if anything fails, fix it before claiming the plan complete.

- [ ] **Step 4: Commit**

```bash
cd qwish-institute-dashboard && git add -A src && git commit -m "style: replace dead primary token classes with brand

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
cd ../qwish-teacher-panel && git add -A src && git commit -m "style: replace dead primary token classes with brand

Co-Authored-By: Claude Opus 5 <noreply@anthropic.com>"
```

Skip either commit if that repo has nothing to stage.

---

## Spec Coverage

| Spec section | Task |
|---|---|
| Backend Delta (claim_code, groups, grade/section filters) | 1 |
| Design System — `primary` token cleanup | 8 (student screens), 11 (sweep) |
| Roster kit — five primitives + status badge | 3, 8 |
| Institute roster — selection, bulk bar, partial-failure reporting | 4, 5 |
| Institute roster — grade/section filters | 2, 5 |
| Institute roster — claim-code copy and CSV | 5 |
| Institute roster — ConfirmDialog replaces window.confirm, toasts replace banner | 5 |
| Institute detail — header, enrollment drawer, ROLL_NUMBER_TAKEN inline | 6 |
| Institute detail — group add/remove, inline edit-request review | 6 |
| Teacher roster — roll/grade/section columns | 9 |
| Teacher roster — class filter, add/remove membership, picker limitation | 9 |
| Teacher roster — correction proposals in a row menu | 9 |
| Teacher detail — read-only enrollment card, per-field proposals | 10 |
| Error handling — toasts by default, field-level codes inline | 5, 6, 9 |
| Testing — lint + build both apps, manual checks per screen | every task's verify step, 11 |
