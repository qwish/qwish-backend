# Qwish entity and platform map

Reviewed against this workspace on 2026-10-06. Based on backend migrations 001–094, API route registration, frontend clients, and the coming-soon Mongoose models. This documents checked-in implementation, not a live production schema inspection.

The current migration-defined model contains **123 PostgreSQL tables and 235 foreign-key relationships**, plus the separate MongoDB `Waitlist` and `Contact` entities. Supabase-managed auth tables are external to the application-owned inventory.

The complete persisted graph is [QWISH_ENTITY_MAP.mmd](QWISH_ENTITY_MAP.mmd); the proposed dependency-grouped platform graph is [QWISH_PLATFORM_MAP.mmd](QWISH_PLATFORM_MAP.mmd).

## Dependency-based feature grouping — proposed unified platform

Group the connected features into **one Qwish platform with seven internal modules and role-specific workspaces**. This is a proposed product grouping based on the documented dependencies, not a claim that the current frontend projects have been merged. The source implementation review above remains dated 2026-10-06; this grouping was added on 2026-10-21 without a new schema audit.

The learner app, teacher panel, institute/leadership dashboard, recruiter dashboard, super-admin console and connected public website are entry points into the same platform. A module owns a feature once; different workspaces expose the parts their users are authorised to use. These are logical module boundaries within the shared backend, not seven independent deployments or databases.

[Grouped platform diagram](QWISH_PLATFORM_MAP.mmd) shows the modules and their dependencies. In that diagram, a solid module arrow means **the source depends on the target**, and a dashed arrow describes a proposed workspace association or workflow integration. These are semantic dependencies, unlike the exact foreign-key arrows in the detailed entity diagrams.

| Module | PostgreSQL tables | Primary workspaces |
| --- | ---: | --- |
| Identity and institute operations | 20 | Learner, teacher, institute/leadership; shared user identity for recruiter; admin oversight |
| Teaching, learning and intervention | 42 | Learner, teacher, institute/leadership |
| Portfolio, community and careers | 15 | Learner, institute reviewers, recruiter |
| Rewards and rankings | 8 | Learner, institute reporting, super-admin configuration |
| Engagement and delivery | 18 | Learner, teacher, institute/leadership, super-admin |
| Governance, acquisition and sponsorship | 17 | Super-admin, public website; teacher submission and learner onboarding flows |
| Shared runtime | 3 | Backend support for all connected workspaces |
| **Total** | **123** | **Each table assigned exactly once** |

### 1. Identity and institute operations

**Features:** Accounts, sign-ins and passkeys; preferences and layouts; institutions and verified domains; joining, enrollment and invites; classes and membership history; departments, leadership roles and action ownership.

**Why grouped:** Shared foundation for learners, teachers, institute admins and leadership. Classes, enrollment and scoped staff roles determine the context used by academic and engagement workflows.

**Owned tables (20):** `users`, `user_emails`, `webauthn_user_credentials`, `user_sign_ins`, `user_preferences`, `user_dashboard_layouts`, `teacher_feature_onboarding`, `institutions`, `institution_domains`, `enrollments`, `groups`, `group_students`, `group_teachers`, `group_student_history`, `departments`, `staff_role_assignments`, `teacher_invites`, `student_invites`, `action_item_owners`, `referral_code_reset_requests`.

### 2. Teaching, learning and intervention

**Features:** Academic years and versioned curricula; chapters and concepts; quiz authoring, questions, options, versioning and libraries; assignment recipients; attempts, snapshots, responses, clues and behavior evidence; practice; concept mapping, misconceptions, reviews, teacher support, parent summaries and topic requests; mastery, recommendations and learning analytics.

**Why grouped:** Keep the complete curriculum → assessment → assignment → attempt → evidence → intervention loop together. Recommendations and analytics consume the same learner and assessment data. Depends on identity, institutes, classes and governance approvals.

**Owned tables (42):** `academic_years`, `curricula`, `curriculum_versions`, `curriculum_version_revisions`, `curriculum_chapters`, `curriculum_concepts`, `class_curricula`, `quiz_curriculum_units`, `quizzes`, `questions`, `question_options`, `question_versions`, `quiz_attempts`, `quiz_attempt_questions`, `question_responses`, `attempt_behavior_events`, `clue_reveals`, `offline_answer_exposures`, `saved_quizzes`, `teacher_quiz_favorites`, `featured_quizzes`, `domains`, `subdomains`, `question_lsh_buckets`, `learning_assignments`, `learning_assignment_recipients`, `question_concepts`, `misconceptions`, `question_misconception_options`, `question_misconception_suggestions`, `learning_evidence`, `learning_evidence_misconceptions`, `misconception_reviews`, `teacher_student_support`, `parent_summaries`, `topic_requests`, `practice_sessions`, `learner_ratings`, `learner_topic_mastery`, `recommendation_bandit_stats`, `quiz_read_stats`, `attempt_daily_metrics`.

### 3. Portfolio, community and careers

**Features:** Education, skills and portfolio entries; revisions and institute reviews; profile views and follows; parent–student links; study groups and membership; recruiter organisations, memberships, hiring roles, candidate states and recruiter audit.

**Why grouped:** Portfolio creation and review feed the candidate profile used by recruitment. Depends on shared users and institution-scoped reviews. Recruiter access remains limited to permitted, opt-in data; a shared platform does not grant unrestricted learner access.

**Owned tables (15):** `user_education`, `user_skills`, `user_profile_entries`, `user_profile_entry_revisions`, `user_profile_entry_reviews`, `profile_views`, `parent_student_links`, `user_follows`, `study_groups`, `study_group_members`, `recruiter_organisations`, `recruiter_memberships`, `recruiter_roles`, `recruiter_candidate_states`, `recruiter_audit_events`.

### 4. Rewards and rankings

**Features:** Points ledger, streaks, badges, leaderboard scores and snapshots, scorecard sharing, point-economy configuration and institute multiplier history.

**Why grouped:** Reward processing consumes learning activity; institution context and admin configuration control the economy. Keep reward rules and ranking calculations in one module. Event/reference dependencies may exist in application code without SQL foreign keys.

**Owned tables (8):** `points_ledger`, `streaks`, `badges`, `leaderboard_scores`, `leaderboard_snapshots`, `scorecard_share_days`, `point_economy_config`, `institution_multiplier_history`.

### 5. Engagement and delivery

**Features:** Forms and identified polls; audiences, versions, responses, history and audit; notices and audience targeting; announcements and promotional content; in-app notifications, device tokens, preferences, email/push delivery logs and content-delivery events.

**Why grouped:** All audience-based engagement depends on users, institutions, departments or classes. Other modules use delivery to reach participants. Anonymous research surveys belong to acquisition and governance below because they have a separate response model.

**Owned tables (18):** `activities`, `activity_audience_groups`, `activity_versions`, `activity_responses`, `activity_response_history`, `activity_audit_events`, `notices`, `notice_audience`, `user_notifications`, `notification_preferences`, `teacher_notification_preferences`, `notification_log`, `device_tokens`, `announcements`, `announcement_institutions`, `promotional_content`, `promo_institutions`, `content_delivery_events`.

### 6. Governance, acquisition and sponsorship

**Features:** Admin identities, sessions, passkeys and layouts; moderation, reports, audit, impersonation and settings; brands and quiz sponsorship requests; public contact handling, anonymous surveys, demo events and onboarding sessions; institution-application workflow input.

**Why grouped:** Admin decisions govern institutions, assessments, sponsorships and published content. Public demos and onboarding reuse quizzes and can connect to a claimed learner account. Brand sponsorship depends on both brands and assessments. Institution applications are workflow input, not an additional table in this inventory.

**Owned tables (17):** `admin_accounts`, `admin_sessions`, `webauthn_credentials`, `webauthn_challenges`, `admin_dashboard_layouts`, `impersonation_sessions`, `audit_log`, `reports`, `platform_settings`, `brands`, `sponsorship_requests`, `contact_submissions`, `anonymous_surveys`, `anonymous_survey_questions`, `anonymous_survey_responses`, `onboarding_sessions`, `demo_events`.

### 7. Shared runtime

**Features:** Search invalidation/versioning, background job execution and distributed rate limiting; shared API, PostgreSQL, authentication, S3 media and delivery adapters.

**Why grouped:** Common technical services support the business modules. Authentication, S3 and delivery providers are external integrations, not extra application-owned tables.

**Owned tables (3):** `internal_search_versions`, `background_jobs`, `distributed_rate_limits`.

### Cross-module workflows

| Workflow | Features that must work together |
| --- | --- |
| Institute setup → class participation | Institution verification and joining → enrollments/invites → classes and staff roles → curriculum and assignment access |
| Teaching → learner support | Curriculum/concept mapping → versioned assessment → class assignment/recipients → learner attempt/responses → learning evidence/mastery → misconception review → teacher support, follow-up and parent summary |
| Practice → motivation | Quiz discovery/recommendation → practice/attempt → configured points and achievements → rankings and scorecard sharing |
| Portfolio → recruitment | Learner profile/skills/entries → revision and institute review → permitted opt-in recruiter profile access → hiring role and candidate state |
| Communication → response | Audience membership → form/poll or notice publication → delivery/preferences → response/history or delivery tracking |
| Acquisition → governed content | Public contact/application handling and admin review; demo/onboarding → quiz and optional account claim; brand → sponsorship request → assessment and admin review |

Delivery and runtime support apply across these workflows. Role checks, institution scope, recruiter organisation scope and separate platform-admin authentication remain required when features share a platform. Parent links and summaries are existing backend features; this grouping does not assert an implemented parent workspace.

### Boundaries outside the connected platform

- **Coming-soon website:** its `Waitlist` and `Contact` MongoDB entities remain a separate acquisition store. There is no documented PostgreSQL synchronization bridge. Bringing them into the unified acquisition workflow would require an explicit integration.
- **Expo learner prototype:** a potential learner workspace, currently based on fixtures/local state with optional profile/insight reads and mock fallbacks. It is not a complete connected learner workflow.
- **Brand dashboard prototype:** a potential sponsorship workspace, currently using embedded programme/campaign data. Persisted sponsorship features are currently handled through backend/admin surfaces.
- **HR LeetCode monitor extension:** review material only; no implemented runtime integration or entity ownership is established.

The current implementation topology and detailed foreign-key diagrams below remain the evidence for this proposal.

## Platforms and persistence boundaries

```mermaid
flowchart TB
  subgraph platforms["Qwish platforms"]
    learner["Flutter learner app - numpie"]
    institute["Institute dashboard"]
    teacher["Teacher panel and leadership views"]
    admin["Super-admin console"]
    recruiter["Recruiter dashboard"]
    marketing["Brand website - public forms"]
    soon["Coming-soon website"]
    expo["Expo learner prototype"]
    brandui["Brand dashboard prototype"]
  end
  learner --> api["Shared Go API - /api/v1"]
  institute --> api
  teacher --> api
  admin --> api
  marketing --> api
  recruiter --> recruiterServer["Next.js recruiter server"] --> api
  api --> pg[("Shared PostgreSQL - 123 tables")]
  api --> auth["Supabase Auth"]
  api --> media["S3 media"]
  api --> delivery["Email and push delivery"]
  soon --> soonapi["Own Next.js API"] --> mongo[("MongoDB")]
  mongo --> waitlist["Waitlist"]
  mongo --> contact["Contact"]
  expo --> fixtures["Local state and fixture data"]
  brandui --> programmes["Embedded programme and campaign data"]
```

| Platform | Connected entities / integration |
| --- | --- |
| Flutter learner app (`numpie`) | Users, institute enrollment and classes, quizzes, assignments, attempts, profiles, rewards, learner insights, notices, activities, and notifications via the Go API. Parent relationships are a backend capability; no separate parent-dashboard project is present. |
| Institute dashboard | Institutions, joining and invites, users and classes, departments and staff roles, academic years and curricula, assessment approvals, learning support, portfolio reviews, notices, reports and analytics. |
| Teacher panel | Assigned classes and learners, reusable assessments and favorites, curriculum mapping, assignments, response evidence, misconceptions and support, topic requests, forms/polls, notices and preferences. Leadership access is granted by staff-role assignments. |
| Super-admin | Admin accounts, institutions, users, platform settings, quiz moderation, brands/sponsorships, content delivery, surveys, reports, audit and point-economy configuration. |
| Recruiter dashboard | Recruiter organisations and memberships, hiring roles, candidate states and audit; opt-in user/profile data through a Next.js server and the Go API. |
| Brand website | Public contact submissions and institution-application requests to the Go API. An application is onboarding workflow/input, not an extra table asserted by this map. |
| Coming-soon website | Own Next.js API and MongoDB Waitlist/Contact models. No checked-in FK or synchronization bridge to PostgreSQL. |
| Expo web/mobile prototype | Current shell uses fixtures/local state. A service module contains optional live profile/insight reads with mock fallbacks; this is not a complete shared-backend learner workflow. |
| Brand dashboard prototype | Embedded programme/campaign data. Persisted brands and sponsorships are managed by the backend/admin surfaces; no implemented brand-dashboard API integration was found. |
| HR LeetCode monitor extension | Review material only in this workspace; no implemented extension entities or runtime integration established. |

## Core business relationships

This is a semantic overview: some nodes combine tables and intermediary join tables are collapsed. Detailed diagrams below use exact SQL foreign keys.

```mermaid
flowchart LR
  subgraph identity["Identity and institution"]
    U["Users: learners, teachers, parents, institute admins"]
    I["Institutions"]
    E["Enrollments"]
    G["Classes - groups"]
    GM["Student and teacher class memberships"]
    D["Departments and staff roles"]
    E --> U
    E --> I
    G --> I
    GM --> U
    GM --> G
    D --> I
    D --> U
  end
  subgraph teaching["Assessment and curriculum"]
    Q["Quizzes / assessments"]
    QQ["Questions, options and versions"]
    C["Curriculum, versions, chapters and concepts"]
    CC["Class curriculum assignments"]
    A["Learning assignments"]
    R["Assignment recipients"]
    Q --> U
    Q --> I
    QQ --> Q
    C --> I
    CC --> G
    CC --> C
    A --> G
    A --> Q
    R --> A
    R --> U
  end
  subgraph learning["Attempts and learning support"]
    AT["Quiz attempts and delivered snapshots"]
    AN["Question responses"]
    LE["Learning evidence"]
    M["Misconceptions and reviews"]
    S["Teacher support and parent summaries"]
    AT --> U
    AT --> Q
    AN --> AT
    AN --> QQ
    LE --> AN
    LE --> C
    LE --> M
    S --> U
    R --> AT
  end
  subgraph outcomes["Other connected domains"]
    P["Portfolio entries, revisions and reviews"]
    RE["Recruiter organisations, roles and candidate states"]
    RW["Points, streaks, badges and leaderboards"]
    F["Forms, polls and responses"]
    N["Notices, notifications and delivery"]
    B["Brands and sponsorships"]
    AD["Admin accounts, moderation and audit"]
    P --> U
    RE --> U
    RW --> U
    F --> U
    F --> G
    N --> U
    B --> Q
    AD --> I
    AD --> Q
  end
```

## Reading the detailed diagrams

- Solid arrows point from the **referencing table to the referenced table**. Labels name the foreign-key column(s). They do not assert one-to-one cardinality or user access permissions.
- Composite foreign keys remain one labeled edge, preserving institution scope.
- Each table has one primary domain. Domain diagrams also show referenced tables from other domains, with dashed outlines.
- Tables without FK edges are still listed. JSON fields and polymorphic references are not automatically treated as SQL foreign keys.

## Important implementation distinctions

- Student, teacher, parent and institution-admin are roles on `users`, not separate person tables. Platform admin identities also have `admin_accounts`; do not assume every admin identity has an enforced FK to `users`.
- Students can have live enrollments in up to two institutes. For students, `users.institution_id` points to the active institute; `enrollments` captures memberships.
- Leadership is expressed by scoped `staff_role_assignments`. A teacher assigned HOD remains a teacher in `users.role`.
- `groups` means institution classes; `study_groups` is a distinct learner/social entity.
- A reusable quiz is distinct from a class learning assignment. Recipients connect assigned students to their attempts. Follow-up review fields live on `learning_assignments`, not a separate follow-up table.
- Published question versions and delivered attempt snapshots preserve historical scoring and concept/option context.
- `learning_evidence_misconceptions` supports multiple misconception tags per evidence row. The older nullable `learning_evidence.misconception_id` remains in the schema, so both edges appear.
- Forms and identified polls share `activities`; anonymous surveys are a separate model.
- Activity question definitions, response answers, options, snapshots and some audit/job metadata live in JSON fields, not separate tables.
- `admission_policies`, `admission_requests`, `admission_targets` `student_edit_requests`, `promotion_batches` and `promotion_batch_students` were dropped in migrations 084–086 and are excluded.
- Points `reference_id`, notification references, content target identifiers, audit targets and job payloads can connect entities in application code without enforced FK edges.
- Runtime tables and materialized/read-model tables are included; they are not additional frontend platforms.

## 1. Identity and preferences

7 primary tables; 8 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Identity and preferences"]
    users["users"]
    user_emails["user_emails"]
    webauthn_user_credentials["webauthn_user_credentials"]
    user_sign_ins["user_sign_ins"]
    user_preferences["user_preferences"]
    user_dashboard_layouts["user_dashboard_layouts"]
    teacher_feature_onboarding["teacher_feature_onboarding"]
  end
  institutions["institutions"]:::external
  teacher_feature_onboarding -->|"institution_id"| institutions
  teacher_feature_onboarding -->|"teacher_id"| users
  user_dashboard_layouts -->|"user_id"| users
  user_emails -->|"user_id"| users
  user_preferences -->|"user_id"| users
  user_sign_ins -->|"user_id"| users
  users -->|"institution_id"| institutions
  webauthn_user_credentials -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## 2. Institutions and memberships

13 primary tables; 33 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Institutions and memberships"]
    institutions["institutions"]
    institution_domains["institution_domains"]
    enrollments["enrollments"]
    groups["groups"]
    group_students["group_students"]
    group_teachers["group_teachers"]
    group_student_history["group_student_history"]
    departments["departments"]
    staff_role_assignments["staff_role_assignments"]
    teacher_invites["teacher_invites"]
    student_invites["student_invites"]
    action_item_owners["action_item_owners"]
    referral_code_reset_requests["referral_code_reset_requests"]
  end
  admin_accounts["admin_accounts"]:::external
  curriculum_concepts["curriculum_concepts"]:::external
  users["users"]:::external
  action_item_owners -->|"institution_id"| institutions
  action_item_owners -->|"owner_id"| users
  departments -->|"institution_id"| institutions
  enrollments -->|"institution_id"| institutions
  enrollments -->|"user_id"| users
  group_student_history -->|"group_id"| groups
  group_student_history -->|"user_id"| users
  group_students -->|"group_id"| groups
  group_students -->|"user_id"| users
  group_teachers -->|"group_id"| groups
  group_teachers -->|"user_id"| users
  groups -->|"source_concept_id"| curriculum_concepts
  groups -->|"department_id, institution_id"| departments
  groups -->|"source_group_id"| groups
  groups -->|"institution_id"| institutions
  groups -->|"created_by"| users
  institution_domains -->|"verified_by"| admin_accounts
  institution_domains -->|"institution_id"| institutions
  institutions -->|"verified_by"| admin_accounts
  referral_code_reset_requests -->|"institution_id"| institutions
  referral_code_reset_requests -->|"requested_by"| users
  referral_code_reset_requests -->|"resolved_by"| users
  staff_role_assignments -->|"department_id, institution_id"| departments
  staff_role_assignments -->|"institution_id"| institutions
  staff_role_assignments -->|"granted_by"| users
  staff_role_assignments -->|"revoked_by"| users
  staff_role_assignments -->|"user_id"| users
  student_invites -->|"group_id"| groups
  student_invites -->|"institution_id"| institutions
  student_invites -->|"accepted_by"| users
  student_invites -->|"invited_by"| users
  teacher_invites -->|"institution_id"| institutions
  teacher_invites -->|"invited_by"| users
  classDef external stroke-dasharray: 5 5
```

## 3. Curriculum

8 primary tables; 13 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Curriculum"]
    academic_years["academic_years"]
    curricula["curricula"]
    curriculum_versions["curriculum_versions"]
    curriculum_version_revisions["curriculum_version_revisions"]
    curriculum_chapters["curriculum_chapters"]
    curriculum_concepts["curriculum_concepts"]
    class_curricula["class_curricula"]
    quiz_curriculum_units["quiz_curriculum_units"]
  end
  groups["groups"]:::external
  institutions["institutions"]:::external
  quizzes["quizzes"]:::external
  users["users"]:::external
  academic_years -->|"institution_id"| institutions
  class_curricula -->|"academic_year_id, institution_id"| academic_years
  class_curricula -->|"version_id, curriculum_id, institution_id"| curriculum_versions
  class_curricula -->|"group_id, institution_id"| groups
  curricula -->|"institution_id"| institutions
  curriculum_chapters -->|"version_id"| curriculum_versions
  curriculum_concepts -->|"chapter_id"| curriculum_chapters
  curriculum_version_revisions -->|"version_id"| curriculum_versions
  curriculum_version_revisions -->|"actor_id"| users
  curriculum_versions -->|"curriculum_id, institution_id"| curricula
  curriculum_versions -->|"copied_from_version_id"| curriculum_versions
  quiz_curriculum_units -->|"unit_id"| curriculum_chapters
  quiz_curriculum_units -->|"quiz_id"| quizzes
  classDef external stroke-dasharray: 5 5
```

## 4. Assessments and attempts

16 primary tables; 32 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Assessments and attempts"]
    quizzes["quizzes"]
    questions["questions"]
    question_options["question_options"]
    question_versions["question_versions"]
    quiz_attempts["quiz_attempts"]
    quiz_attempt_questions["quiz_attempt_questions"]
    question_responses["question_responses"]
    attempt_behavior_events["attempt_behavior_events"]
    clue_reveals["clue_reveals"]
    offline_answer_exposures["offline_answer_exposures"]
    saved_quizzes["saved_quizzes"]
    teacher_quiz_favorites["teacher_quiz_favorites"]
    featured_quizzes["featured_quizzes"]
    domains["domains"]
    subdomains["subdomains"]
    question_lsh_buckets["question_lsh_buckets"]
  end
  admin_accounts["admin_accounts"]:::external
  curriculum_concepts["curriculum_concepts"]:::external
  groups["groups"]:::external
  institutions["institutions"]:::external
  users["users"]:::external
  attempt_behavior_events -->|"question_id"| questions
  attempt_behavior_events -->|"attempt_id"| quiz_attempts
  attempt_behavior_events -->|"user_id"| users
  clue_reveals -->|"question_id"| questions
  clue_reveals -->|"attempt_id"| quiz_attempts
  featured_quizzes -->|"quiz_id"| quizzes
  offline_answer_exposures -->|"quiz_id"| quizzes
  offline_answer_exposures -->|"user_id"| users
  question_lsh_buckets -->|"question_id"| questions
  question_options -->|"question_id"| questions
  question_responses -->|"option_id"| question_options
  question_responses -->|"question_id"| questions
  question_responses -->|"attempt_id"| quiz_attempts
  question_versions -->|"question_id"| questions
  questions -->|"quiz_id"| quizzes
  quiz_attempt_questions -->|"question_version_id"| question_versions
  quiz_attempt_questions -->|"question_id"| questions
  quiz_attempt_questions -->|"attempt_id"| quiz_attempts
  quiz_attempts -->|"quiz_id"| quizzes
  quiz_attempts -->|"user_id"| users
  quizzes -->|"approved_by"| admin_accounts
  quizzes -->|"curriculum_concept_id"| curriculum_concepts
  quizzes -->|"domain"| domains
  quizzes -->|"group_id"| groups
  quizzes -->|"institution_id"| institutions
  quizzes -->|"subdomain"| subdomains
  quizzes -->|"created_by"| users
  saved_quizzes -->|"quiz_id"| quizzes
  saved_quizzes -->|"user_id"| users
  subdomains -->|"domain_slug"| domains
  teacher_quiz_favorites -->|"quiz_id"| quizzes
  teacher_quiz_favorites -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## 5. Assignments and learning evidence

12 primary tables; 46 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Assignments and learning evidence"]
    learning_assignments["learning_assignments"]
    learning_assignment_recipients["learning_assignment_recipients"]
    question_concepts["question_concepts"]
    misconceptions["misconceptions"]
    question_misconception_options["question_misconception_options"]
    question_misconception_suggestions["question_misconception_suggestions"]
    learning_evidence["learning_evidence"]
    learning_evidence_misconceptions["learning_evidence_misconceptions"]
    misconception_reviews["misconception_reviews"]
    teacher_student_support["teacher_student_support"]
    parent_summaries["parent_summaries"]
    topic_requests["topic_requests"]
  end
  curriculum_concepts["curriculum_concepts"]:::external
  groups["groups"]:::external
  institutions["institutions"]:::external
  question_options["question_options"]:::external
  question_responses["question_responses"]:::external
  question_versions["question_versions"]:::external
  questions["questions"]:::external
  quiz_attempts["quiz_attempts"]:::external
  quizzes["quizzes"]:::external
  users["users"]:::external
  learning_assignment_recipients -->|"assignment_id"| learning_assignments
  learning_assignment_recipients -->|"attempt_id"| quiz_attempts
  learning_assignment_recipients -->|"student_id"| users
  learning_assignments -->|"source_concept_id"| curriculum_concepts
  learning_assignments -->|"group_id, institution_id"| groups
  learning_assignments -->|"institution_id"| institutions
  learning_assignments -->|"quiz_id"| quizzes
  learning_assignments -->|"created_by"| users
  learning_assignments -->|"follow_up_reviewed_by"| users
  learning_evidence -->|"concept_id"| curriculum_concepts
  learning_evidence -->|"institution_id"| institutions
  learning_evidence -->|"misconception_id"| misconceptions
  learning_evidence -->|"option_id"| question_options
  learning_evidence -->|"response_id"| question_responses
  learning_evidence -->|"question_version_id"| question_versions
  learning_evidence -->|"question_id"| questions
  learning_evidence -->|"attempt_id"| quiz_attempts
  learning_evidence -->|"user_id"| users
  learning_evidence_misconceptions -->|"evidence_id"| learning_evidence
  learning_evidence_misconceptions -->|"misconception_id"| misconceptions
  misconception_reviews -->|"institution_id"| institutions
  misconception_reviews -->|"misconception_id"| misconceptions
  misconception_reviews -->|"reviewed_by"| users
  misconception_reviews -->|"student_id"| users
  misconceptions -->|"concept_id"| curriculum_concepts
  misconceptions -->|"institution_id"| institutions
  misconceptions -->|"created_by"| users
  parent_summaries -->|"institution_id"| institutions
  parent_summaries -->|"student_id"| users
  parent_summaries -->|"teacher_id"| users
  question_concepts -->|"concept_id"| curriculum_concepts
  question_concepts -->|"question_id"| questions
  question_concepts -->|"mapped_by"| users
  question_misconception_options -->|"misconception_id"| misconceptions
  question_misconception_options -->|"option_id"| question_options
  question_misconception_options -->|"question_id"| questions
  question_misconception_options -->|"reviewed_by"| users
  question_misconception_suggestions -->|"question_id"| questions
  question_misconception_suggestions -->|"created_by"| users
  teacher_student_support -->|"institution_id"| institutions
  teacher_student_support -->|"student_id"| users
  teacher_student_support -->|"teacher_id"| users
  topic_requests -->|"institution_id"| institutions
  topic_requests -->|"resolved_quiz_id"| quizzes
  topic_requests -->|"assigned_to"| users
  topic_requests -->|"student_id"| users
  classDef external stroke-dasharray: 5 5
```

## 6. Portfolio and social

11 primary tables; 19 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Portfolio and social"]
    user_education["user_education"]
    user_skills["user_skills"]
    user_profile_entries["user_profile_entries"]
    user_profile_entry_revisions["user_profile_entry_revisions"]
    user_profile_entry_reviews["user_profile_entry_reviews"]
    profile_views["profile_views"]
    parent_student_links["parent_student_links"]
    user_follows["user_follows"]
    study_groups["study_groups"]
    study_group_members["study_group_members"]
    practice_sessions["practice_sessions"]
  end
  institutions["institutions"]:::external
  quizzes["quizzes"]:::external
  users["users"]:::external
  parent_student_links -->|"parent_id"| users
  parent_student_links -->|"student_id"| users
  practice_sessions -->|"quiz_id"| quizzes
  practice_sessions -->|"user_id"| users
  profile_views -->|"viewed_id"| users
  profile_views -->|"viewer_id"| users
  study_group_members -->|"group_id"| study_groups
  study_group_members -->|"user_id"| users
  study_groups -->|"owner_id"| users
  user_education -->|"user_id"| users
  user_follows -->|"followee_id"| users
  user_follows -->|"follower_id"| users
  user_profile_entries -->|"user_id"| users
  user_profile_entry_reviews -->|"institution_id"| institutions
  user_profile_entry_reviews -->|"revision_id"| user_profile_entry_revisions
  user_profile_entry_reviews -->|"reviewer_id"| users
  user_profile_entry_revisions -->|"institution_id"| institutions
  user_profile_entry_revisions -->|"entry_id"| user_profile_entries
  user_skills -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## 7. Recruitment

5 primary tables; 10 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Recruitment"]
    recruiter_organisations["recruiter_organisations"]
    recruiter_memberships["recruiter_memberships"]
    recruiter_roles["recruiter_roles"]
    recruiter_candidate_states["recruiter_candidate_states"]
    recruiter_audit_events["recruiter_audit_events"]
  end
  users["users"]:::external
  recruiter_audit_events -->|"organisation_id"| recruiter_organisations
  recruiter_audit_events -->|"actor_id"| users
  recruiter_candidate_states -->|"organisation_id"| recruiter_organisations
  recruiter_candidate_states -->|"role_id"| recruiter_roles
  recruiter_candidate_states -->|"candidate_id"| users
  recruiter_candidate_states -->|"updated_by"| users
  recruiter_memberships -->|"organisation_id"| recruiter_organisations
  recruiter_memberships -->|"user_id"| users
  recruiter_roles -->|"organisation_id"| recruiter_organisations
  recruiter_roles -->|"created_by"| users
  classDef external stroke-dasharray: 5 5
```

## 8. Rewards and rankings

8 primary tables; 9 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Rewards and rankings"]
    points_ledger["points_ledger"]
    streaks["streaks"]
    badges["badges"]
    leaderboard_scores["leaderboard_scores"]
    leaderboard_snapshots["leaderboard_snapshots"]
    scorecard_share_days["scorecard_share_days"]
    point_economy_config["point_economy_config"]
    institution_multiplier_history["institution_multiplier_history"]
  end
  admin_accounts["admin_accounts"]:::external
  institutions["institutions"]:::external
  users["users"]:::external
  badges -->|"user_id"| users
  institution_multiplier_history -->|"changed_by"| admin_accounts
  institution_multiplier_history -->|"institution_id"| institutions
  leaderboard_scores -->|"user_id"| users
  leaderboard_snapshots -->|"institution_id"| institutions
  point_economy_config -->|"updated_by"| admin_accounts
  points_ledger -->|"user_id"| users
  scorecard_share_days -->|"user_id"| users
  streaks -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## 9. Forms and polls

6 primary tables; 12 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Forms and polls"]
    activities["activities"]
    activity_audience_groups["activity_audience_groups"]
    activity_versions["activity_versions"]
    activity_responses["activity_responses"]
    activity_response_history["activity_response_history"]
    activity_audit_events["activity_audit_events"]
  end
  groups["groups"]:::external
  institutions["institutions"]:::external
  users["users"]:::external
  activities -->|"current_version_id"| activity_versions
  activities -->|"institution_id"| institutions
  activities -->|"created_by"| users
  activity_audience_groups -->|"activity_id"| activities
  activity_audience_groups -->|"group_id"| groups
  activity_audit_events -->|"activity_id"| activities
  activity_audit_events -->|"actor_id"| users
  activity_response_history -->|"response_id"| activity_responses
  activity_responses -->|"activity_id"| activities
  activity_responses -->|"version_id"| activity_versions
  activity_responses -->|"student_id"| users
  activity_versions -->|"activity_id"| activities
  classDef external stroke-dasharray: 5 5
```

## 10. Communications and content

12 primary tables; 20 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Communications and content"]
    notices["notices"]
    notice_audience["notice_audience"]
    user_notifications["user_notifications"]
    notification_preferences["notification_preferences"]
    teacher_notification_preferences["teacher_notification_preferences"]
    notification_log["notification_log"]
    device_tokens["device_tokens"]
    announcements["announcements"]
    announcement_institutions["announcement_institutions"]
    promotional_content["promotional_content"]
    promo_institutions["promo_institutions"]
    content_delivery_events["content_delivery_events"]
  end
  admin_accounts["admin_accounts"]:::external
  departments["departments"]:::external
  groups["groups"]:::external
  institutions["institutions"]:::external
  users["users"]:::external
  announcement_institutions -->|"announcement_id"| announcements
  announcement_institutions -->|"institution_id"| institutions
  announcements -->|"approved_by"| admin_accounts
  announcements -->|"created_by"| admin_accounts
  announcements -->|"institution_id"| institutions
  content_delivery_events -->|"user_id"| users
  device_tokens -->|"user_id"| users
  notice_audience -->|"department_id"| departments
  notice_audience -->|"group_id"| groups
  notice_audience -->|"notice_id"| notices
  notices -->|"institution_id"| institutions
  notices -->|"created_by"| users
  notification_preferences -->|"user_id"| users
  promo_institutions -->|"institution_id"| institutions
  promo_institutions -->|"promo_id"| promotional_content
  promotional_content -->|"approved_by"| admin_accounts
  promotional_content -->|"created_by"| admin_accounts
  promotional_content -->|"institution_id"| institutions
  teacher_notification_preferences -->|"user_id"| users
  user_notifications -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## 11. Brands and sponsorship

2 primary tables; 5 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Brands and sponsorship"]
    brands["brands"]
    sponsorship_requests["sponsorship_requests"]
  end
  admin_accounts["admin_accounts"]:::external
  quizzes["quizzes"]:::external
  brands -->|"approved_by"| admin_accounts
  brands -->|"created_by"| admin_accounts
  sponsorship_requests -->|"reviewed_by"| admin_accounts
  sponsorship_requests -->|"brand_id"| brands
  sponsorship_requests -->|"quiz_id"| quizzes
  classDef external stroke-dasharray: 5 5
```

## 12. Administration and moderation

9 primary tables; 13 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Administration and moderation"]
    admin_accounts["admin_accounts"]
    admin_sessions["admin_sessions"]
    webauthn_credentials["webauthn_credentials"]
    webauthn_challenges["webauthn_challenges"]
    admin_dashboard_layouts["admin_dashboard_layouts"]
    impersonation_sessions["impersonation_sessions"]
    audit_log["audit_log"]
    reports["reports"]
    platform_settings["platform_settings"]
  end
  institutions["institutions"]:::external
  questions["questions"]:::external
  quizzes["quizzes"]:::external
  users["users"]:::external
  admin_accounts -->|"created_by"| admin_accounts
  admin_dashboard_layouts -->|"admin_id"| admin_accounts
  admin_sessions -->|"admin_id"| admin_accounts
  admin_sessions -->|"revoked_by"| admin_accounts
  audit_log -->|"institution_id"| institutions
  impersonation_sessions -->|"admin_id"| admin_accounts
  impersonation_sessions -->|"user_id"| users
  platform_settings -->|"updated_by"| admin_accounts
  reports -->|"reviewed_by"| admin_accounts
  reports -->|"question_id"| questions
  reports -->|"quiz_id"| quizzes
  reports -->|"reporter_id"| users
  webauthn_credentials -->|"admin_id"| admin_accounts
  classDef external stroke-dasharray: 5 5
```

## 13. Public acquisition and anonymous research

6 primary tables; 8 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Public acquisition and anonymous research"]
    contact_submissions["contact_submissions"]
    anonymous_surveys["anonymous_surveys"]
    anonymous_survey_questions["anonymous_survey_questions"]
    anonymous_survey_responses["anonymous_survey_responses"]
    onboarding_sessions["onboarding_sessions"]
    demo_events["demo_events"]
  end
  admin_accounts["admin_accounts"]:::external
  quizzes["quizzes"]:::external
  users["users"]:::external
  anonymous_survey_questions -->|"survey_id"| anonymous_surveys
  anonymous_survey_responses -->|"survey_id"| anonymous_surveys
  anonymous_surveys -->|"created_by"| admin_accounts
  contact_submissions -->|"assignee_id"| admin_accounts
  contact_submissions -->|"resolved_by"| admin_accounts
  demo_events -->|"quiz_id"| quizzes
  onboarding_sessions -->|"quiz_id"| quizzes
  onboarding_sessions -->|"claimed_by"| users
  classDef external stroke-dasharray: 5 5
```

## 14. Analytics and runtime support

8 primary tables; 7 outbound FK relationships.

```mermaid
flowchart LR
  subgraph local["Analytics and runtime support"]
    learner_ratings["learner_ratings"]
    learner_topic_mastery["learner_topic_mastery"]
    recommendation_bandit_stats["recommendation_bandit_stats"]
    quiz_read_stats["quiz_read_stats"]
    attempt_daily_metrics["attempt_daily_metrics"]
    internal_search_versions["internal_search_versions"]
    background_jobs["background_jobs"]
    distributed_rate_limits["distributed_rate_limits"]
  end
  quizzes["quizzes"]:::external
  users["users"]:::external
  attempt_daily_metrics -->|"quiz_id"| quizzes
  attempt_daily_metrics -->|"user_id"| users
  learner_ratings -->|"user_id"| users
  learner_topic_mastery -->|"user_id"| users
  quiz_read_stats -->|"quiz_id"| quizzes
  recommendation_bandit_stats -->|"quiz_id"| quizzes
  recommendation_bandit_stats -->|"user_id"| users
  classDef external stroke-dasharray: 5 5
```

## Entity inventory and source migrations

| Domain | Entity | Defined in |
| --- | --- | --- |
| Identity and preferences | `users` | [001_initial.sql](../../migrations/001_initial.sql) |
| Identity and preferences | `user_emails` | [084_identity_and_joining.sql](../../migrations/084_identity_and_joining.sql) |
| Identity and preferences | `webauthn_user_credentials` | [025_user_passkeys.sql](../../migrations/025_user_passkeys.sql) |
| Identity and preferences | `user_sign_ins` | [078_institute_operations.sql](../../migrations/078_institute_operations.sql) |
| Identity and preferences | `user_preferences` | [080_teacher_panel_roadmap.sql](../../migrations/080_teacher_panel_roadmap.sql) |
| Identity and preferences | `user_dashboard_layouts` | [029_user_dashboard_layouts.sql](../../migrations/029_user_dashboard_layouts.sql) |
| Identity and preferences | `teacher_feature_onboarding` | [064_teacher_feature_onboarding.sql](../../migrations/064_teacher_feature_onboarding.sql) |
| Institutions and memberships | `institutions` | [001_initial.sql](../../migrations/001_initial.sql) |
| Institutions and memberships | `institution_domains` | [084_identity_and_joining.sql](../../migrations/084_identity_and_joining.sql) |
| Institutions and memberships | `enrollments` | [031_student_enrollment.sql](../../migrations/031_student_enrollment.sql) |
| Institutions and memberships | `groups` | [001_initial.sql](../../migrations/001_initial.sql) |
| Institutions and memberships | `group_students` | [001_initial.sql](../../migrations/001_initial.sql) |
| Institutions and memberships | `group_teachers` | [001_initial.sql](../../migrations/001_initial.sql) |
| Institutions and memberships | `group_student_history` | [086_classes_and_history.sql](../../migrations/086_classes_and_history.sql) |
| Institutions and memberships | `departments` | [083_leadership_roles.sql](../../migrations/083_leadership_roles.sql) |
| Institutions and memberships | `staff_role_assignments` | [083_leadership_roles.sql](../../migrations/083_leadership_roles.sql) |
| Institutions and memberships | `teacher_invites` | [009_teacher_invites_notification_log.sql](../../migrations/009_teacher_invites_notification_log.sql) |
| Institutions and memberships | `student_invites` | [084_identity_and_joining.sql](../../migrations/084_identity_and_joining.sql) |
| Institutions and memberships | `action_item_owners` | [078_institute_operations.sql](../../migrations/078_institute_operations.sql) |
| Institutions and memberships | `referral_code_reset_requests` | [036_referral_code_reset_requests.sql](../../migrations/036_referral_code_reset_requests.sql) |
| Curriculum | `academic_years` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `curricula` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `curriculum_versions` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `curriculum_version_revisions` | [077_curriculum_structure.sql](../../migrations/077_curriculum_structure.sql) |
| Curriculum | `curriculum_chapters` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `curriculum_concepts` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `class_curricula` | [058_curriculum_foundation.sql](../../migrations/058_curriculum_foundation.sql) |
| Curriculum | `quiz_curriculum_units` | [069_quiz_curriculum_units.sql](../../migrations/069_quiz_curriculum_units.sql) |
| Assessments and attempts | `quizzes` | [001_initial.sql](../../migrations/001_initial.sql) |
| Assessments and attempts | `questions` | [001_initial.sql](../../migrations/001_initial.sql) |
| Assessments and attempts | `question_options` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assessments and attempts | `question_versions` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assessments and attempts | `quiz_attempts` | [001_initial.sql](../../migrations/001_initial.sql) |
| Assessments and attempts | `quiz_attempt_questions` | [043_super_admin_quiz_delivery.sql](../../migrations/043_super_admin_quiz_delivery.sql) |
| Assessments and attempts | `question_responses` | [001_initial.sql](../../migrations/001_initial.sql) |
| Assessments and attempts | `attempt_behavior_events` | [044_attempt_behavior_events.sql](../../migrations/044_attempt_behavior_events.sql) |
| Assessments and attempts | `clue_reveals` | [026_attempt_anticheat.sql](../../migrations/026_attempt_anticheat.sql) |
| Assessments and attempts | `offline_answer_exposures` | [094_security_hardening.sql](../../migrations/094_security_hardening.sql) |
| Assessments and attempts | `saved_quizzes` | [001_initial.sql](../../migrations/001_initial.sql) |
| Assessments and attempts | `teacher_quiz_favorites` | [061_teacher_assessment_library.sql](../../migrations/061_teacher_assessment_library.sql) |
| Assessments and attempts | `featured_quizzes` | [046_featured_quizzes.sql](../../migrations/046_featured_quizzes.sql) |
| Assessments and attempts | `domains` | [020_quiz_domains.sql](../../migrations/020_quiz_domains.sql) |
| Assessments and attempts | `subdomains` | [020_quiz_domains.sql](../../migrations/020_quiz_domains.sql) |
| Assessments and attempts | `question_lsh_buckets` | [051_question_lsh.sql](../../migrations/051_question_lsh.sql) |
| Assignments and learning evidence | `learning_assignments` | [060_learning_assignments.sql](../../migrations/060_learning_assignments.sql) |
| Assignments and learning evidence | `learning_assignment_recipients` | [060_learning_assignments.sql](../../migrations/060_learning_assignments.sql) |
| Assignments and learning evidence | `question_concepts` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assignments and learning evidence | `misconceptions` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assignments and learning evidence | `question_misconception_options` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assignments and learning evidence | `question_misconception_suggestions` | [080_teacher_panel_roadmap.sql](../../migrations/080_teacher_panel_roadmap.sql) |
| Assignments and learning evidence | `learning_evidence` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assignments and learning evidence | `learning_evidence_misconceptions` | [075_misconception_evidence_integrity.sql](../../migrations/075_misconception_evidence_integrity.sql) |
| Assignments and learning evidence | `misconception_reviews` | [059_learning_evidence_foundation.sql](../../migrations/059_learning_evidence_foundation.sql) |
| Assignments and learning evidence | `teacher_student_support` | [065_teacher_student_support.sql](../../migrations/065_teacher_student_support.sql) |
| Assignments and learning evidence | `parent_summaries` | [080_teacher_panel_roadmap.sql](../../migrations/080_teacher_panel_roadmap.sql) |
| Assignments and learning evidence | `topic_requests` | [001_initial.sql](../../migrations/001_initial.sql) |
| Portfolio and social | `user_education` | [003_profile_features.sql](../../migrations/003_profile_features.sql) |
| Portfolio and social | `user_skills` | [003_profile_features.sql](../../migrations/003_profile_features.sql) |
| Portfolio and social | `user_profile_entries` | [031_student_enrollment.sql](../../migrations/031_student_enrollment.sql) |
| Portfolio and social | `user_profile_entry_revisions` | [081_student_portfolio.sql](../../migrations/081_student_portfolio.sql) |
| Portfolio and social | `user_profile_entry_reviews` | [081_student_portfolio.sql](../../migrations/081_student_portfolio.sql) |
| Portfolio and social | `profile_views` | [003_profile_features.sql](../../migrations/003_profile_features.sql) |
| Portfolio and social | `parent_student_links` | [001_initial.sql](../../migrations/001_initial.sql) |
| Portfolio and social | `user_follows` | [012_app_features.sql](../../migrations/012_app_features.sql) |
| Portfolio and social | `study_groups` | [012_app_features.sql](../../migrations/012_app_features.sql) |
| Portfolio and social | `study_group_members` | [012_app_features.sql](../../migrations/012_app_features.sql) |
| Portfolio and social | `practice_sessions` | [012_app_features.sql](../../migrations/012_app_features.sql) |
| Recruitment | `recruiter_organisations` | [047_recruiter_workspace.sql](../../migrations/047_recruiter_workspace.sql) |
| Recruitment | `recruiter_memberships` | [047_recruiter_workspace.sql](../../migrations/047_recruiter_workspace.sql) |
| Recruitment | `recruiter_roles` | [047_recruiter_workspace.sql](../../migrations/047_recruiter_workspace.sql) |
| Recruitment | `recruiter_candidate_states` | [047_recruiter_workspace.sql](../../migrations/047_recruiter_workspace.sql) |
| Recruitment | `recruiter_audit_events` | [047_recruiter_workspace.sql](../../migrations/047_recruiter_workspace.sql) |
| Rewards and rankings | `points_ledger` | [001_initial.sql](../../migrations/001_initial.sql) |
| Rewards and rankings | `streaks` | [001_initial.sql](../../migrations/001_initial.sql) |
| Rewards and rankings | `badges` | [001_initial.sql](../../migrations/001_initial.sql) |
| Rewards and rankings | `leaderboard_scores` | [052_incremental_leaderboard.sql](../../migrations/052_incremental_leaderboard.sql) |
| Rewards and rankings | `leaderboard_snapshots` | [001_initial.sql](../../migrations/001_initial.sql) |
| Rewards and rankings | `scorecard_share_days` | [056_achievements.sql](../../migrations/056_achievements.sql) |
| Rewards and rankings | `point_economy_config` | [001_initial.sql](../../migrations/001_initial.sql) |
| Rewards and rankings | `institution_multiplier_history` | [071_admin_console.sql](../../migrations/071_admin_console.sql) |
| Forms and polls | `activities` | [082_activities.sql](../../migrations/082_activities.sql) |
| Forms and polls | `activity_audience_groups` | [082_activities.sql](../../migrations/082_activities.sql) |
| Forms and polls | `activity_versions` | [082_activities.sql](../../migrations/082_activities.sql) |
| Forms and polls | `activity_responses` | [082_activities.sql](../../migrations/082_activities.sql) |
| Forms and polls | `activity_response_history` | [082_activities.sql](../../migrations/082_activities.sql) |
| Forms and polls | `activity_audit_events` | [082_activities.sql](../../migrations/082_activities.sql) |
| Communications and content | `notices` | [087_notices_and_remedial.sql](../../migrations/087_notices_and_remedial.sql) |
| Communications and content | `notice_audience` | [087_notices_and_remedial.sql](../../migrations/087_notices_and_remedial.sql) |
| Communications and content | `user_notifications` | [010_user_notifications.sql](../../migrations/010_user_notifications.sql) |
| Communications and content | `notification_preferences` | [012_app_features.sql](../../migrations/012_app_features.sql) |
| Communications and content | `teacher_notification_preferences` | [080_teacher_panel_roadmap.sql](../../migrations/080_teacher_panel_roadmap.sql) |
| Communications and content | `notification_log` | [009_teacher_invites_notification_log.sql](../../migrations/009_teacher_invites_notification_log.sql) |
| Communications and content | `device_tokens` | [011_device_tokens.sql](../../migrations/011_device_tokens.sql) |
| Communications and content | `announcements` | [001_initial.sql](../../migrations/001_initial.sql) |
| Communications and content | `announcement_institutions` | [055_content_targeting.sql](../../migrations/055_content_targeting.sql) |
| Communications and content | `promotional_content` | [001_initial.sql](../../migrations/001_initial.sql) |
| Communications and content | `promo_institutions` | [055_content_targeting.sql](../../migrations/055_content_targeting.sql) |
| Communications and content | `content_delivery_events` | [054_content_delivery_events.sql](../../migrations/054_content_delivery_events.sql) |
| Brands and sponsorship | `brands` | [006_brands_promos.sql](../../migrations/006_brands_promos.sql) |
| Brands and sponsorship | `sponsorship_requests` | [006_brands_promos.sql](../../migrations/006_brands_promos.sql) |
| Administration and moderation | `admin_accounts` | [001_initial.sql](../../migrations/001_initial.sql) |
| Administration and moderation | `admin_sessions` | [071_admin_console.sql](../../migrations/071_admin_console.sql) |
| Administration and moderation | `webauthn_credentials` | [017_webauthn_passkeys.sql](../../migrations/017_webauthn_passkeys.sql) |
| Administration and moderation | `webauthn_challenges` | [017_webauthn_passkeys.sql](../../migrations/017_webauthn_passkeys.sql) |
| Administration and moderation | `admin_dashboard_layouts` | [028_admin_dashboard_layouts.sql](../../migrations/028_admin_dashboard_layouts.sql) |
| Administration and moderation | `impersonation_sessions` | [001_initial.sql](../../migrations/001_initial.sql) |
| Administration and moderation | `audit_log` | [001_initial.sql](../../migrations/001_initial.sql) |
| Administration and moderation | `reports` | [001_initial.sql](../../migrations/001_initial.sql) |
| Administration and moderation | `platform_settings` | [071_admin_console.sql](../../migrations/071_admin_console.sql) |
| Public acquisition and anonymous research | `contact_submissions` | [008_contact_submissions.sql](../../migrations/008_contact_submissions.sql) |
| Public acquisition and anonymous research | `anonymous_surveys` | [053_anonymous_surveys.sql](../../migrations/053_anonymous_surveys.sql) |
| Public acquisition and anonymous research | `anonymous_survey_questions` | [053_anonymous_surveys.sql](../../migrations/053_anonymous_surveys.sql) |
| Public acquisition and anonymous research | `anonymous_survey_responses` | [053_anonymous_surveys.sql](../../migrations/053_anonymous_surveys.sql) |
| Public acquisition and anonymous research | `onboarding_sessions` | [041_onboarding_calibration.sql](../../migrations/041_onboarding_calibration.sql) |
| Public acquisition and anonymous research | `demo_events` | [023_demo_quiz_authoring_events.sql](../../migrations/023_demo_quiz_authoring_events.sql) |
| Analytics and runtime support | `learner_ratings` | [072_learner_ratings.sql](../../migrations/072_learner_ratings.sql) |
| Analytics and runtime support | `learner_topic_mastery` | [050_adaptive_learning.sql](../../migrations/050_adaptive_learning.sql) |
| Analytics and runtime support | `recommendation_bandit_stats` | [050_adaptive_learning.sql](../../migrations/050_adaptive_learning.sql) |
| Analytics and runtime support | `quiz_read_stats` | [091_quiz_read_models.sql](../../migrations/091_quiz_read_models.sql) |
| Analytics and runtime support | `attempt_daily_metrics` | [093_daily_attempt_metrics.sql](../../migrations/093_daily_attempt_metrics.sql) |
| Analytics and runtime support | `internal_search_versions` | [048_student_search_bloom_version.sql](../../migrations/048_student_search_bloom_version.sql) |
| Analytics and runtime support | `background_jobs` | [090_backend_reliability.sql](../../migrations/090_backend_reliability.sql) |
| Analytics and runtime support | `distributed_rate_limits` | [090_backend_reliability.sql](../../migrations/090_backend_reliability.sql) |
| Coming-soon MongoDB | `Waitlist` | [Waitlist.ts](https://github.com/suyog417/qwish-coming-soon-web/blob/main/src/models/Waitlist.ts) |
| Coming-soon MongoDB | `Contact` | [Contact.ts](https://github.com/suyog417/qwish-coming-soon-web/blob/main/src/models/Contact.ts) |

## Sources

- [Workspace architecture](ARCHITECTURE.MD)
- [Backend migrations](../../migrations)
- [API composition and route registration](../../cmd/api/main.go)
- [Backend API documentation](../../API_DOC.md)
- [Expo prototype service](https://github.com/qwish/qwish-web-app/blob/main/src/services/api.ts)
- [Coming-soon models](https://github.com/suyog417/qwish-coming-soon-web/tree/main/src/models)
