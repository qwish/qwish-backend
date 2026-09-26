# Class joining and institute admissions

Apply `migrations/074_admissions.sql` before deploying the API, then release the institute dashboard and NumPie app. Existing institutes default to `allow_all`. No existing memberships are changed by the migration.

## Student flow

A student enters a class code or opens `https://app.qwish.in/join?code=CODE`. NumPie preserves the code through authentication, previews the class and institute, and asks for one confirmation. Institute and personal roster codes remain supported by the same flow.

- A new student gets one institute enrollment plus the class membership in one transaction, or one pending request with no access.
- Existing active institute members can join more classes without repeating institute admission.
- Repeat submissions are idempotent. Multiple class requests at one institute share a request. One student can have only one open request across institutes; cancel it before requesting elsewhere.
- Suspended accounts/enrollments cannot join or transfer around restrictions.
- Transfers always require destination approval, regardless of admission mode. Approval leaves the existing membership active. The student explicitly confirms the switch; the transaction ends the old enrollment, removes old class access, and creates the new membership. Historical records remain with their institute.
- Adding a class or refreshing a rotated code after transfer approval returns the request to pending for another review.
- Approval/transfer completion rechecks institute verification, class archival, code rotation, roster ownership, and current memberships. If some targets are unavailable, valid targets are joined and unavailable ones are recorded. If every target is unavailable, nothing is joined; obtain a new invite or cancel/decline the request.
- Declined or cancelled requests remain in history; students may submit a fresh request.

## Institute settings

`Settings → Admissions` offers `allow_all`, `verify_first`, and `custom`.

Custom criteria use the authenticated account's verified email only:

- Exact email domains (subdomains must be listed separately).
- Explicit preapproved email addresses.
- An email match against a pending roster record.

Admins choose whether any or all enabled criteria must match. Empty lists disable their criterion. Custom mode requires at least one criterion; unmatched students go to manual verification. A unique matching roster record is claimed rather than creating a duplicate enrollment; ambiguous matches are left for admin reconciliation.

Changes apply to new requests. Previously submitted requests remain pending. The private `admission_policies` table has RLS enabled with no client policies; email allowlists are not placed in the directly readable institutions table. Reviews and policy changes are written to the institute audit log in the same transaction.

## API

All paths below are under `/api/v1`. Student endpoints require a student role; institute endpoints require an institution admin and derive scope from the authenticated user.

| Method | Path | Body / query |
| --- | --- | --- |
| POST | `/students/join/preview` | `{code}` |
| POST | `/students/join/confirm` | `{code, kind, target_id}` from preview |
| GET | `/students/join/requests` | `offset` (50 per page) |
| PATCH | `/students/join/requests/{requestId}` | `{action: "cancel" \| "complete"}` |
| GET | `/institution/admissions/policy` | — |
| PUT | `/institution/admissions/policy` | `{mode, match, email_domains, emails, roster_match}` |
| GET | `/institution/admissions/requests` | `filter=open\|history`, `offset` (50 per page) |
| PATCH | `/institution/admissions/requests/{requestId}` | `{action: "approve" \| "decline", reason?}` |

Preview adds `requires_approval`, `transfer_required`, `already_requested`, and optional `request_id`/`request_status`. Confirmation returns `{destination, status, enrollment?}`; enrollment is present only for `status=joined`. Transfer approvals use `approved` until the student completes them.

Requests return institute/name/email, source institute ID for transfers, status, review reason, and targets with `pending`, `joined`, or `unavailable` outcomes. Responses use the API's existing `{data: ...}` envelope. Codes and policy allowlists are never included in request lists.

Legacy `/students/claim` and `/students/join-class` use the same admission service. They return `ADMISSION_PENDING` for queued requests so older apps cannot treat them as active memberships. Student signups using an institute referral code and student `/auth/referral-code` updates return `JOIN_FLOW_REQUIRED`; create the account first, then use the preview/confirm flow. Staff referral behavior remains separate and cannot change an account's role.

## Verification

Database integration tests in `internal/domain/enrollment/admissions_test.go` use `TEST_DATABASE_URL` pointing to a scratch database with all migrations. Run the affected enrollment/auth packages with `-race`. NumPie joining widget/cubit tests cover immediate joining, pending approval, response-loss recovery, accepted invite URLs, and a narrow display with large text.
