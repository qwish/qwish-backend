

**QuizApp**  
**Institution PRD**  
Product Requirements — School / College Layer

Version 1.0  •  February 2026

| Status | Draft |
| :---- | :---- |
| **Layer** | Institution (School / College Admin) |
| **Platform** | Next.js Web (desktop-first) |
| **Depends On** | QuizApp PRD v1.1 |
| **Out of Scope** | Student quiz-taking, super admin controls, brand sponsorship |

# **1\. Purpose & Scope**

This document defines the product requirements for the Institution layer of QuizApp — the web-based dashboard available to school and college administrators. Institution admins can monitor their students and teachers, manage class groups, configure institution-level rules, and maintain a healthy, active platform environment. No quiz creation or solving happens in this layer.

## **1.1 Who Is This User?**

* A staff member at a school, college, or tuition centre with an admin role assigned by the QuizApp team during institution onboarding.  
* They are responsible for managing their institution’s presence on QuizApp: teachers, students, groups, and rules.  
* They interact exclusively through a web browser (desktop-first, responsive for tablet).  
* They do not take quizzes, earn points, or appear on leaderboards.

## **1.2 Goals for V1**

* Give institution admins full visibility into teacher and student activity within their institution.  
* Allow admins to manage teacher permissions: assign to groups, suspend, or remove.  
* Allow admins to organise students into class groups for better filtering and reporting.  
* Allow admins to configure institution-level point rules.  
* Provide a clear, in-app reporting view (no export in V1).

## **1.3 Out of Scope for V1**

* Report export (PDF, CSV) — in-app only for V1.  
* Approving teacher-created quizzes before publish (teachers publish directly to institution).  
* Messaging or announcements to students (admin → student comms is Phase 2).  
* Mobile app access — web only.

# **2\. Institution Onboarding & Access**

## **2.1 Institution Setup**

* New institutions are onboarded manually by the QuizApp team following a verification process.  
* Upon activation, the institution receives: a unique student referral code, a unique teacher referral code, and an admin account provisioned by QuizApp.  
* The admin account is created with the institution’s official email. Admin cannot self-register.  
* The admin distributes the student and teacher referral codes to their staff and students through their own channels.

## **2.2 Admin Login**

* Email and password login at the web dashboard URL.  
* Forgot password via OTP email flow (same as student auth).  
* Session is maintained for 30 days via a refresh token. Re-authentication required after inactivity.

## **2.3 Multiple Admins**

* An institution can have more than one admin account.  
* Additional admins are created by the QuizApp team on request. Self-provisioning is not available in V1.  
* All admins within an institution have identical permissions (no tiered admin roles at the institution level in V1).

# **3\. Dashboard Overview**

The institution dashboard is a web application with a persistent left sidebar for navigation and a main content area. All data is scoped strictly to the admin’s institution.

## **3.1 Sidebar Navigation**

| Section | Description |
| :---- | :---- |
| Overview | Top-level institution summary with key metrics |
| Students | Full student list with search, filter, and individual profiles |
| Teachers | Full teacher list with activity stats and management actions |
| Groups | Class/group management — create groups, assign teachers and students |
| Quizzes | All quizzes published within the institution, with aggregate results |
| Topic Requests | Student-submitted quiz topic requests for teachers to action |
| Settings | Institution name, timezone, point rules, referral codes |

## **3.2 Overview Screen**

* Institution name and verification badge.  
* Key metric cards:  
  * Total enrolled students  
  * Active students this week (completed ≥ 1 quiz)  
  * Total teachers  
  * Total quizzes published (all time)  
  * Average score across all quizzes this month  
  * Top student this week (display name \+ points)  
* Activity chart: quizzes completed per day over the last 30 days (bar chart).  
* Top 5 quizzes by completion rate this month.  
* Top 5 students by points this week (mini leaderboard card).

# **4\. Student Management**

## **4.1 Student List**

* Paginated table of all enrolled students.  
* Columns: display name, email, group(s), total points, average score, current streak, last active date, status (Active / Suspended).  
* Search by name or email.  
* Filter by: group, status (active / suspended / never active), streak status (active / at risk / broken).  
* Sort by: name, total points, average score, last active.

## **4.2 Student Profile View (Admin)**

* Full read-only view of a student’s data within the institution.  
* Sections:  
  * Summary: points balance, average score, total quizzes, streak, badges earned.  
  * Quiz history: all attempts with quiz name, score, points delta, date.  
  * Points ledger: full transaction history with expiry dates.  
  * Group membership: which groups the student belongs to.  
* Admin cannot edit student data, but can suspend or reactivate the student account.

## **4.3 Suspending a Student**

* Admin can suspend a student from the student profile or from the student list via a context menu.  
* Suspended students cannot log in or access any quiz content.  
* Admin must provide a reason for suspension (required, free text, max 300 characters).  
* Suspension is logged with timestamp, reason, and admin name for audit purposes.  
* Admin can reactivate a suspended student at any time.  
* Students are not notified of suspension via the platform in V1 (institution handles communication).

# **5\. Teacher Management**

## **5.1 Teacher List**

* Table of all teachers enrolled via the institution’s teacher referral code.  
* Columns: name, email, group(s) assigned, quizzes created, total student attempts on their quizzes, last active date, status.  
* Search by name or email.  
* Filter by: group, status (active / suspended).

## **5.2 Teacher Profile View (Admin)**

* Summary: quizzes created (draft \+ published), total attempts across their quizzes, average score on their quizzes.  
* Quiz list: all quizzes created by the teacher with title, type, status, completion count, and average score.  
* Group assignments: which groups the teacher is assigned to.

## **5.3 Removing or Suspending a Teacher**

* Admin can suspend a teacher, preventing login and quiz creation.  
* Published quizzes by a suspended teacher remain accessible to students (quizzes are not automatically unpublished).  
* Admin can permanently remove a teacher from the institution. On removal, the teacher’s account is disassociated. Published quizzes remain, attributed to “\[Removed Teacher\]”.  
* Removal and suspension actions are logged for audit.  
* A confirmation modal is shown before any destructive action.

## **5.4 Assigning Teachers to Groups**

* Admin can assign a teacher to one or more groups from the teacher profile view or the Groups section.  
* An assigned teacher can see all students in their group(s) and their quiz performance.  
* Teachers not assigned to any group have visibility of all institution students by default.

# **6\. Groups (Classes)**

Groups represent classes, cohorts, or batch divisions within an institution. They allow admins and teachers to organise students and scope reporting.

## **6.1 Creating a Group**

* Admin provides: group name (e.g. “Grade 10A”), optional description.  
* A group invite code is auto-generated. Students can join a group using this code (in addition to the institution referral code).  
* Alternatively, admin can manually assign students to groups from the student list.

## **6.2 Group Dashboard**

* Per-group view showing: student list, average score, total quizzes completed, top scorer, group streak leaders.  
* Admin can remove students from a group without removing them from the institution.  
* Admin can assign or reassign teachers to a group.  
* Admin can archive a group (hides it from active views but retains historical data).

## **6.3 Group Filtering**

* All student and quiz views throughout the dashboard can be filtered by group.  
* Leaderboard within the dashboard can be scoped to a group (institution-admin view only, not visible to students in V1).

# **7\. Quiz Oversight**

Institution admins have read-only visibility into all quizzes published within their institution. They cannot create, edit, or delete quizzes — that remains with teachers.

## **7.1 Quiz List**

* All published and closed quizzes within the institution.  
* Columns: title, type, created by (teacher), status, questions, completion count, average score, published date.  
* Filter by: type (Knowledge Check / Play & Win), teacher, status, group.

## **7.2 Quiz Detail View (Admin)**

* Aggregate stats: total attempts, completion rate, average score, per-question accuracy.  
* Student results table: display name, score, points delta, time taken, date.  
* Admin can flag a quiz for review (sends a notification to the QuizApp team) if content is deemed inappropriate.

## **7.3 Topic Requests**

* Admin can see all topic requests submitted by students within their institution.  
* Table view: student name, topic requested, subject, date submitted, status (Pending / In Progress / Done).  
* Admin can mark requests as “In Progress” or “Done”.  
* Admin can assign a topic request to a specific teacher.

# **8\. Institution-Level Point Rules**

Admins can configure institution-specific point rules that layer on top of the platform defaults. These rules are applied only to quizzes within their institution.

## **8.1 Configurable Rules**

| Rule | Description | Default |
| :---- | :---- | :---- |
| Point multiplier | Apply a multiplier (0.5x – 2.0x) to all points earned within the institution. Useful for boosting engagement during exam season. | 1.0x (no change) |
| Streak grace window | Enable or disable the 12-hour grace window for streak preservation. | Enabled |
| Play & Win retake | Allow or block students from viewing their score until the quiz end date. | Score visible immediately after attempt |
| Point expiry override | Set a shorter expiry window (e.g. 3 months) for points earned within the institution. | Platform default: 6 months |

* Changes to point rules apply to new quiz attempts only. Historical points are not retroactively modified.  
* A change log is maintained showing: rule changed, old value, new value, changed by (admin), timestamp.

# **9\. Reporting**

All reports are in-app only in V1. Export functionality (PDF, CSV) is planned for Phase 2\.

## **9.1 Available Reports**

| Report | Description | Filters Available |
| :---- | :---- | :---- |
| Student Performance | Per-student: quizzes taken, average score, points earned, streak status. | Group, date range, active/inactive |
| Teacher Activity | Per-teacher: quizzes created, attempts generated, average score on their quizzes. | Date range |
| Quiz Analytics | Per-quiz: completion rate, score distribution, per-question accuracy. | Teacher, type, date range |
| Streak Health | Count of students with active / at-risk / broken streaks. | Group |
| Points Summary | Total points distributed within the institution, per-student breakdown, expiring points. | Group, date range |

## **9.2 Report Display**

* Reports are rendered as tables and simple charts (bar, line) directly in the dashboard.  
* Date range picker available on all reports. Default: last 30 days.  
* Charts are not interactive in V1 (no drill-down). Drill-down to individual student/teacher profiles is via hyperlinked table rows.

# **10\. Institution Settings**

## **10.1 General Settings**

* Institution name (display name used in student app).  
* Institution timezone (used for streak day boundaries). Dropdown of IANA timezones.  
* Institution type (School / College / Tuition) — set at onboarding, editable by admin.

## **10.2 Referral Codes**

* View current student and teacher referral codes.  
* Admin can request a referral code reset from this screen (sends a request to the QuizApp team — codes are reset manually in V1 to prevent abuse).

## **10.3 Audit Log**

* Chronological log of all admin actions: suspensions, removals, group changes, rule changes.  
* Each entry shows: action type, affected user, reason (if provided), admin who performed it, timestamp.  
* Read-only. Cannot be edited or deleted by institution admins.

# **11\. Phase 2 Preview (Institution Layer)**

| Feature | Description |
| :---- | :---- |
| Report export | PDF and CSV export for all reports. |
| Scheduled email summaries | Weekly or monthly email digests of institution metrics sent to admin. |
| Admin-to-student announcements | Broadcast a message to all students or a specific group via in-app notification. |
| Quiz approval workflow | Option for institution admin to approve teacher-created quizzes before they are published to students. |
| Custom badge creation | Institution admin can create institution-specific badges awarded to students. |
| Multiple admin roles | Read-only admin vs full admin permission tiers within the institution. |

# **12\. Open Questions**

| \# | Question | Owner | Status |
| :---- | :---- | :---- | :---- |
| 1 | Can institution admins see the global leaderboard, or only the institution leaderboard? | Product | Open |
| 2 | Should group invite codes expire or remain permanent? | Product | Open |
| 3 | When a teacher is removed, who owns their quizzes for future result visibility? | Product | Open |
| 4 | Should admins receive email notifications when a teacher is newly enrolled via referral code? | Product | Open |
| 5 | Is there a cap on the number of groups an institution can create in V1? | Product | Open |
| 6 | Can admins modify student group membership in bulk (CSV upload)? | Product | Open (likely Phase 2\) |

