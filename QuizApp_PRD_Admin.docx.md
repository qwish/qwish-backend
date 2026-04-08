

**QuizApp**  
**Admin PRD**  
Product Requirements — Super Admin Layer

Version 1.0  •  February 2026

| Status | Draft |
| :---- | :---- |
| **Layer** | Super Admin |
| **Platform** | Next.js Web (internal tool) |
| **Depends On** | QuizApp PRD v1.1 |
| **Access** | Internal QuizApp team only — not accessible to institutions, students, or brands |

# **1\. Purpose & Scope**

This document defines the product requirements for the Super Admin layer of QuizApp — the internal control panel used exclusively by the QuizApp team. Super admins manage everything that cannot be self-served: institution verification, public quiz moderation, brand onboarding, point economy configuration, platform-wide announcements, and ad/promotional content. This is a power-user tool; usability is important but secondary to completeness and auditability.

## **1.1 Who Is This User?**

* A QuizApp internal team member with a provisioned super admin account.  
* They manage the entire platform across all institutions, users, and brands.  
* All actions are logged and auditable.  
* Access is web-only. No mobile admin interface.

## **1.2 Goals for V1**

* Give the QuizApp team full operational control of the platform from a single interface.  
* Support institution onboarding and verification workflows.  
* Enable public quiz moderation and approval.  
* Manage the point economy (base values, expiry, streak bonuses).  
* Broadcast platform-wide announcements to users.  
* Manage ad/promotional content shown in the app.  
* Support brand onboarding for Phase 2 sponsorship features.  
* Provide read-only user impersonation for support triage.

## **1.3 Admin Role Tiers**

| Role | Description | Key Permissions |
| :---- | :---- | :---- |
| Super Admin | Full platform control. All sections accessible. | All actions including destructive operations, role management, and config changes. |
| Moderator | Content and community focused. No financial or config access. | Quiz moderation, user reports, announcement publishing, read-only impersonation. |
| Support Agent | User support focused. Read-only across most sections. | Read-only impersonation, view user data, flag content for review. Cannot modify config or approve institutions. |

* Admin accounts are created only by Super Admins.  
* Super Admins can promote, demote, or deactivate any admin account.  
* A Super Admin cannot demote their own account (prevents lockout).

# **2\. Dashboard Navigation**

| Section | Description | Accessible By |
| :---- | :---- | :---- |
| Overview | Platform-wide health metrics and activity feed | All |
| Institutions | Verification queue, institution management, referral codes | Super Admin, Moderator (read) |
| Users | Cross-platform user search, view, suspend, impersonate | All |
| Quizzes | Public quiz moderation queue and published quiz oversight | All |
| Reports & Flags | User-submitted reports and flagged content | All |
| Brands | Brand onboarding, sponsorship approval, reward pools (Phase 2\) | Super Admin |
| Ads & Promotions | Create, schedule, and manage in-app promotional content | Super Admin, Moderator |
| Announcements | Platform-wide and institution-targeted announcements | Super Admin, Moderator |
| Point Economy | Base point values, bonuses, expiry config | Super Admin only |
| Admin Accounts | Create, manage, and assign roles to admin users | Super Admin only |
| Audit Log | Immutable log of all admin actions across the platform | Super Admin only |

# **3\. Platform Overview**

## **3.1 Health Metrics (Live)**

* Total registered users (students, teachers, parents) — all time and this week.  
* Total active users this week (completed ≥ 1 quiz).  
* Total institutions (pending verification / verified / suspended).  
* Total quizzes (published / pending approval / reported).  
* Total quiz attempts today and this week.  
* Platform-wide average score this week.  
* Total points distributed this week and all time.

## **3.2 Activity Feed**

* Real-time feed of notable platform events: new institution registrations, quiz approvals, large point distributions, user reports, new brand sign-ups.  
* Each event is a link to the relevant item in the admin panel.  
* Feed is filterable by event type.

# **4\. Institution Management**

## **4.1 Institution Verification Queue**

* New institution registrations appear in a pending queue.  
* Each queue item shows: institution name, type (school/college/tuition), contact email, date submitted.  
* Super Admin reviews the submission and either Approves or Rejects.  
* On approval: institution is activated, student \+ teacher referral codes are auto-generated and displayed, admin account is provisioned and credentials sent to the institution contact email.  
* On rejection: Super Admin provides a rejection reason. A rejection email is sent to the institution contact.  
* The verification workflow in V1 is manual — no automated checks.

## **4.2 Institution List**

* Table of all institutions: name, type, status (pending / verified / suspended), student count, teacher count, total quizzes, date verified.  
* Search by name. Filter by status and type.

## **4.3 Institution Detail View**

* All institution metadata: name, type, timezone, referral codes, status, verification date, verifying admin.  
* Student and teacher counts with links to filtered user search.  
* Quiz count with link to filtered quiz list.  
* Admin accounts at this institution.  
* Action buttons: Suspend Institution • Reactivate • Reset Referral Codes • Edit Details.

## **4.4 Suspending an Institution**

* Suspending an institution immediately prevents all users in the institution from logging in.  
* Published quizzes are hidden from all users while the institution is suspended.  
* Super Admin must provide a reason. Action is logged.  
* A confirmation modal with the impact summary is shown before confirming.  
* Reactivation restores all access instantly.

# **5\. User Management**

## **5.1 User Search**

* Global search across all users by: name, email, display name, institution.  
* Filter by: role (student, teacher, admin, parent), status (active / suspended / deleted), institution.  
* Results table: name, email, role, institution, status, last active, total points, current streak.

## **5.2 User Detail View**

* Full profile: name, email, role, institution, status, member since, last active.  
* Stats: total points, quizzes taken, average score, current streak, longest streak, badges.  
* Quiz attempt history (paginated).  
* Points ledger (full transaction history).  
* Reports filed by this user.  
* Reports filed against this user or their content.  
* Parent/student links (if applicable).

## **5.3 User Actions**

| Action | Who Can Perform | Notes |
| :---- | :---- | :---- |
| Suspend user | All admin roles | Requires reason. User cannot log in. Logged. |
| Reactivate user | All admin roles | Restores login access. |
| Soft delete user | Super Admin only | GDPR/compliance delete. Data retained per policy, PII anonymised. |
| Adjust points balance | Super Admin only | Manual ledger entry with mandatory reason. Used for support corrections only. |
| Reset password | Super Admin, Support Agent | Triggers an OTP reset email to the user. |
| View as user (impersonation) | All admin roles | Read-only. Admin can see exactly what the user sees without acting as them. Session is logged with impersonating admin ID. |

## **5.4 Read-Only Impersonation**

* Super Admin selects “View as User” on any user profile.  
* A new browser tab opens showing the user’s app view (Flutter web or Next.js web) in a read-only mode.  
* A persistent banner at the top of the impersonated view reads: “\[Admin Name\] viewing as \[User Display Name\] — Read Only.”  
* All interactive elements are disabled: no quiz attempts, no point transactions, no profile changes.  
* The impersonation session is logged: start time, end time, admin, user viewed.  
* The impersonated user is not notified.

# **6\. Quiz Moderation**

## **6.1 Public Quiz Approval Queue**

* Teachers who publish quizzes with visibility set to “public” submit them for approval.  
* The approval queue lists pending quizzes: title, teacher, institution, question count, submitted date.  
* Super Admin or Moderator reviews each quiz in full — all questions, options, correct answers, and media.  
* Actions: Approve (quiz goes live) • Reject with reason (returned to teacher as draft with feedback) • Request edits (quiz stays pending, feedback sent to teacher).  
* Approval and rejection are logged with the reviewing admin’s name, timestamp, and reason.

## **6.2 Reported Content Queue**

* All user-submitted reports (quiz or question level) appear in this queue.  
* Each report shows: reporting user, quiz/question flagged, reason selected, additional description, date submitted.  
* Moderator reviews the report and selects a resolution:  
  * No action — report dismissed.  
  * Edit required — quiz unpublished and returned to teacher with notes.  
  * Remove quiz — quiz unpublished immediately. Teacher notified.  
  * Escalate — flagged for Super Admin review.  
* Reporter is shown a generic “we’ve reviewed your report” status update in V1 (no detailed outcome shared).

## **6.3 Published Quiz Oversight**

* Full list of all published quizzes across all institutions and public.  
* Filter by: institution, type, visibility, status, date range.  
* Super Admin can unpublish any quiz at any time with a mandatory reason.  
* Unpublishing a quiz while a student has an in-progress attempt allows the attempt to complete before removing visibility.

# **7\. Reports & Flags**

## **7.1 Report Queue**

* Unified view of all user-submitted reports: quiz reports, question reports, and (Phase 2\) duel reports.  
* Priority tagging: reports with multiple submissions against the same item are auto-elevated to high priority.  
* Table: report type, item name, reporter, reason, date, priority, status (Open / Reviewing / Resolved).  
* Assignable to specific moderators.

## **7.2 Flagged Institutions**

* Institution admins can flag quizzes within their institution for QuizApp review.  
* Flagged items appear in this section separate from user reports.  
* Same resolution workflow as user reports.

# **8\. Ads & Promotional Content**

QuizApp does not run third-party advertising. All promotional content is first-party: QuizApp-produced content promoting platform features, new quiz types, milestones, and partner campaigns. Admins create and schedule this content in this section.

## **8.1 Promotional Content Types**

| Type | Placement | Description |
| :---- | :---- | :---- |
| Home Banner | Student Home screen | Full-width card promoting a feature, event, or campaign. Image \+ title \+ CTA button. |
| Quiz Browser Banner | Top of quiz browser tab | Slim banner with text and optional CTA. Used for “Try this new question type” style prompts. |
| Splash Interstitial | Shown on app open (max once per 7 days per user) | Full-screen modal with image, headline, body, and CTA. Dismissible. |
| Achievement Prompt | Shown after quiz completion | Contextual prompt triggered by a condition (e.g. “You’re 2 quizzes away from the Explorer badge”). |

## **8.2 Creating a Promotional Item**

* Admin selects placement type, provides title, body copy, CTA label and destination URL (internal deep link or external URL).  
* Image upload (optional for non-interstitial types). Images stored in Cloudflare R2.  
* Targeting: All users • Students only • Specific institution(s) • Users with no streak (lapsed users).  
* Scheduling: publish immediately or set a start date and optional end date.  
* Preview: admin can preview how the item will appear before publishing.

## **8.3 Promotional Content Rules**

* Only one Home Banner can be active at a time. A new banner replaces the current one.  
* A Splash Interstitial is shown to each user at most once every 7 days regardless of how many are scheduled.  
* All promotional content is reviewed and approved by a Super Admin before going live (Moderators can create but not publish).  
* Content must not mislead users about features not yet available. Super Admin is responsible for compliance.

# **9\. Platform-Wide Announcements**

## **9.1 Announcement Types**

| Type | Delivery | Use Case |
| :---- | :---- | :---- |
| In-app banner | Shown on student and teacher Home screens | Maintenance windows, new feature launches, policy updates |
| In-app notification | Notification bell in the app | Targeted updates to specific institutions or user groups |
| Email broadcast | Sent via Resend to matched users | Critical updates, major feature launches. Opt-out respected. |

## **9.2 Creating an Announcement**

* Admin selects delivery type(s) (can combine in-app \+ email for the same message).  
* Audience targeting: All users • All students • All teachers • Specific institution(s) • Users in a specific country (V1: India only).  
* Content: title (required), body (required), CTA button with label \+ link (optional).  
* Schedule: immediate or future date/time.  
* All email broadcasts require Super Admin approval before sending.  
* In-app announcements can be published by Moderators.

## **9.3 Announcement History**

* Log of all announcements sent: content, audience, delivery type, sent by, sent at, estimated reach count.  
* In-app banners can be retracted after sending. Emails cannot be recalled.

# **10\. Point Economy Configuration**

The point economy config defines the default values for all point-related events on the platform. These are platform defaults; institution admins can apply multipliers on top (see Institution PRD Section 8). All changes are logged and reversible.

## **10.1 Configurable Values**

| Config Key | Description | Default |
| :---- | :---- | :---- |
| base\_points\_per\_question | Points awarded per correct answer on a standard question. | TBD |
| performance\_bonus\_pct\_75 | Bonus percentage applied when score ≥ 75%. | TBD |
| deduction\_pct\_below\_50 | Percentage of base points deducted when score \< 50%. | TBD |
| streak\_bonus\_7\_day | Flat bonus points awarded at 7-day streak milestone. | TBD |
| streak\_bonus\_15\_day | Flat bonus points at 15-day streak milestone. | TBD |
| streak\_bonus\_30\_day | Flat bonus points at 30-day streak milestone. | TBD |
| combo\_multiplier\_step | Points multiplier increment per consecutive correct answer in Speed Chain. | TBD |
| clue\_reveal\_deduction\_per\_clue | Point reduction per clue revealed in Clue Reveal questions. | TBD |
| points\_expiry\_months | Default months until earned points expire. | 6 |
| confidence\_multiplier\_table | JSON config defining point multipliers per confidence level (correct \+ wrong). | TBD |
| points\_floor | Minimum points balance a user can hold. | 0 |

## **10.2 Editing Config Values**

* Super Admin only. Moderators and Support Agents cannot access this section.  
* Each value has a description, current value, and a history of past values with timestamps and the admin who changed it.  
* Changes take effect on new quiz attempts only. In-progress attempts use the config active at attempt start.  
* A “Preview Impact” tool shows an example calculation before saving (e.g. “A student scoring 80% on a 20-question quiz would earn X pts”).  
* All changes are logged in the platform audit log.

# **11\. Brand Management (Phase 2 Preparation)**

Brand management is a Phase 2 feature. This section documents the admin controls needed and is included here to ensure the admin panel is designed to accommodate them without rework.

## **11.1 Brand Onboarding**

* Brands are onboarded manually by the QuizApp team. No self-serve in V1 or Phase 2 launch.  
* Super Admin creates a brand profile: brand name, contact email, logo, category.  
* Brand is assigned a portal login (separate from the main app) for Phase 2\.

## **11.2 Sponsorship Approval**

* Brands can request to sponsor a public Play & Win quiz by selecting it from the approved quiz list.  
* Super Admin reviews and approves the sponsorship: brand, quiz, reward pool type, reward value.  
* On approval, the quiz is tagged as sponsored and the brand name appears on the quiz card in the student app.

## **11.3 Reward Pool Tracking**

* Per-sponsorship view: quiz, brand, reward type, total pool, claimed count, remaining.  
* Super Admin can pause or close a sponsorship early if needed.

# **12\. Admin Account Management**

## **12.1 Creating Admin Accounts**

* Super Admin provides: name, email, role (Super Admin / Moderator / Support Agent).  
* An invite email is sent with a one-time setup link (valid 48 hours).  
* New admin sets their password on first login.

## **12.2 Managing Admin Accounts**

* Super Admin can: change role, suspend, reactivate, or permanently delete an admin account.  
* Deleting an admin account anonymises their name in the audit log entries (actions are retained, author anonymised).  
* A Super Admin cannot suspend or delete their own account.  
* All role changes are logged.

# **13\. Audit Log**

The audit log is an immutable, append-only record of every admin action taken on the platform. It cannot be edited or deleted by any admin role.

## **13.1 What Is Logged**

* Every institution creation, verification, suspension, and reactivation.  
* Every user suspension, reactivation, deletion, and manual point adjustment.  
* Every quiz approval, rejection, and unpublishing action.  
* Every point economy config change.  
* Every announcement created and sent.  
* Every promotional content item created, published, or retracted.  
* Every read-only impersonation session (admin, user, start time, end time).  
* Every admin account creation, role change, and deactivation.

## **13.2 Log Format**

| Field | Description |
| :---- | :---- |
| timestamp | UTC datetime of the action |
| admin\_id | ID of the admin who performed the action |
| admin\_name | Display name at time of action (preserved even if account later deleted) |
| admin\_role | Role at time of action |
| action\_type | Categorised action key (e.g. USER\_SUSPENDED, QUIZ\_APPROVED) |
| target\_type | Type of entity affected (user, institution, quiz, config, etc.) |
| target\_id | ID of the affected entity |
| reason | Admin-provided reason (where required) |
| old\_value | Previous value (for config changes) |
| new\_value | New value (for config changes) |

## **13.3 Log Access**

* Accessible only to Super Admins.  
* Searchable by: admin name, action type, target type, date range.  
* Logs are retained indefinitely and cannot be purged via the admin panel.

# **14\. Open Questions**

| \# | Question | Owner | Status |
| :---- | :---- | :---- | :---- |
| 1 | What point config values should be set at launch? Needs product decision before go-live. | Product | Open |
| 2 | Should Moderators be able to approve institutions or is that Super Admin only? | Product | Open |
| 3 | Should read-only impersonation be available for web view only, or also replicate the Flutter mobile view? | Engineering | Open |
| 4 | Is there a rate limit or cool-down on platform-wide email broadcasts to prevent accidental spam? | Engineering | Open |
| 5 | Should the audit log be exportable (CSV) for legal/compliance purposes? | Product / Legal | Open |
| 6 | Are there geographic restrictions on which institutions can join (India only for V1)? | Product | Open |
| 7 | What happens to a student’s points when their institution is suspended — frozen or still valid? | Product | Open |
| 8 | Confidence multiplier table: who owns defining this — product or a learning science consultant? | Product | Open |

