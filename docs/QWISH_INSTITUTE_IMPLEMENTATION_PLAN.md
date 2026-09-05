# Qwish institute product implementation plan

Date: 5 September 2026

Status: Implementation started; see the progress note below.
Scope: All six proposed features, their shared foundations, and commercial readiness.

Implementation update, 5 September 2026: the first backend curriculum package is
pushed to backend `main` as `d458ee6`. It adds academic years, draft/published curriculum versions,
chapter/concept trees, class mappings, scoped reads and audit/immutability controls.
See [the implemented API contract](CURRICULUM_FOUNDATION.md).
The dashboard implementation includes institute academic years, curriculum editing,
publishing/copying and group assignment; teacher class-scoped curriculum reading.
Production builds and a synthetic browser workflow pass; deployment/live API
verification is still required. FND-02 remains partial: class offerings/terms remain.
Question/assessment versioning, FND-03–05, assignments and the six product modules
are not yet implemented. The roadmap below describes the target, not shipped scope.

## 1. Product decision

Position Qwish around a complete learning-improvement workflow:

**Assess understanding → identify a gap → assign help → reassess → show progress → reward retained learning.**

The institute owner buys visibility into academic delivery and evidence for parent conversations. The teacher gets a manageable next-action list. The student gets focused practice. The parent gets understandable progress and a next step.

Initial target hypothesis: tuition/coaching institutes teaching school subjects, with repeat assessments and regular parent interactions. Begin with one curriculum, one subject, and a small set of chapters. Support other institute types through configuration later; do not dilute the first pilot across schools, colleges, competitive exams, and corporate training simultaneously.

All six features belong in the roadmap, but the first paid release should combine misconception insights, remedial workflows, and parent reports. Admission diagnostics and learning missions follow. Syllabus mapping is a shared prerequisite; the full retention interface ships after reliable follow-up evidence exists.

## 2. Repository findings and reuse

These are findings from local source, not confirmation of deployed behavior or database state.

| Area | Existing foundation | Required extension |
|---|---|---|
| Institute operations | Enrollment/import/promotion, teachers, groups, audit log, settings | Academic years, scoped staff capabilities, learning operations, onboarding and entitlements |
| Teacher workflow | Seven question formats, authoring, CSV/XLSX import, publishing, class/student views | Reusable question bank, concept tagging, assignments, interventions, report review |
| Assessment evidence | Backend stores answer, correctness, confidence, response time, clues used | Versioned question evidence, institution/enrollment context, teacher-facing response insights |
| Learning scheduling | `learner_topic_mastery` and adaptive updates after completion | Question-level concepts, evidence sufficiency, institution-scoped projections, durable processing |
| Parents | Parent invite/link/accept/revoke and child overview endpoints | Verified recipient workflow, report snapshots, teacher approval, scoped delivery and revocation |
| Analytics | Server-defined metric catalogs and reusable dashboard widgets | Learning/outcome metrics, action queues, report completeness and operational health |
| Motivation | Points ledger, institution point rules, streaks, badges | Versioned missions, eligibility evidence, budgets, exactly-once rewards |
| Delivery | Scheduler, notifications, email/push infrastructure | Durable report/assignment jobs, recipient-level delivery status and retry handling |

Important correction to the initial dashboard-only assessment: confidence, selected answers, clues, topic review scheduling, and parent linking are already present in the backend. Reuse them. Their presence does not mean all six workflows are already implemented.

Source anchors:

- `qwish-institute-dashboard/PRODUCT.md` — current product constraints, including identical admin permissions and no report export in V1.
- `qwish-institute-dashboard/src/lib/api/institution.ts` — roster, enrollment, groups and reporting APIs.
- `qwish-teacher-panel/src/lib/api/types.ts` — question formats and currently exposed aggregate quiz results.
- `qwish-teacher-panel/src/components/ImportPanel.tsx` — import and external AI prompt workflow.
- `qwish-backend/internal/domain/attempt/service.go` — response persistence, completion, scoring and adaptive update.
- `qwish-backend/migrations/050_adaptive_learning.sql` — global user/topic mastery and review state.
- `qwish-backend/internal/domain/parent/handler.go` — existing parent relationship and overview flows.
- `qwish-backend/migrations/031_student_enrollment.sql` — enrollment ownership and current one-live-enrollment constraint.
- `qwish-backend/internal/scheduler/scheduler.go` — current scheduled delivery patterns.
- `qwish-backend/migrations/056_achievements.sql` — server-owned achievement foundation.

## 3. Architecture and product structure

### 3.1 Keep the Go backend as a modular monolith

Add focused domain packages under `internal/domain/`: `curriculum`, `assignment`, `learning`, `intervention`, `progressreport`, `admissions`, `mission`, and `entitlement`. Extend `parent`, `metrics`, `notification`, `points`, and `enrollment` rather than creating competing implementations.

Keep PostgreSQL as the system of record. Use a database-backed outbox and workers for derived learning evidence, scheduled checks, report generation, and delivery. Avoid introducing separate services or a new event platform before load requires them.

Completion transaction:

1. Validate and finalize the attempt using existing server-owned scoring.
2. Persist assessment-version and assignment/enrollment references.
3. Write an `attempt.completed` outbox event in the same transaction.
4. Commit and return the assessment result.
5. Workers update concept evidence, assignment status, intervention checks and mission eligibility idempotently.

Record event ID, schema version, institution context, occurred-at time and entity references. Consumers store deduplication keys and processing status. Use bounded retries, leases, failed-job inspection and replay. Do not claim learning projections are current while processing is delayed.

The existing adaptive update is best-effort after commit. Keep its current consumer behavior during migration; move or reconcile it behind one durable consumer before removing the old path. Do not double-apply the same completion to mastery or rewards.

### 3.2 Separate identity, academic membership and commercial access

- Keep `users` as identity and `enrollments` as the institute relationship.
- Add academic years/terms and class offerings that associate existing groups with grade, subject, curriculum version and assigned teachers.
- Scope academic evidence to the institution and enrollment at the time of the activity. A later transfer must not move old records into the new institute's dashboard.
- Preserve global personal learning separately from institute-owned learning records. Existing global topic mastery must not silently become an institute's measure of its teaching impact.
- Preserve the one-live-enrollment rule in the first release. Simultaneous school/coaching membership is a worthwhile later upgrade, but requires changes to token scope, switching, teacher permissions, and all institution-dependent queries.
- Model admissions leads separately from enrolled students. Conversion links to enrollment; it does not create a competing identity system.

### 3.3 Introduce staff capabilities

Proposed roles are presets over server-checked capabilities:

| Role | Main access |
|---|---|
| Owner | Commercial settings, staff permissions, all institute operations |
| Academic head | Curriculum, academic oversight, content/report approvals |
| Teacher | Assigned classes, their learning evidence, assignments and reports |
| Counsellor | Assigned leads, diagnostics and counselling follow-ups |
| Operations staff | Roster, scheduling and approved communications |
| Parent/student | Only authorized personal/child records |

Validate class membership and institute ownership on reads, mutations, exports, background jobs and recipient resolution. Browser navigation is not authorization. Existing admins migrate to an explicit compatibility role; owners can delegate narrower roles later.

### 3.4 Organize navigation around work

Teacher workspace:

- Today: due follow-ups, reports awaiting review and upcoming assignments.
- Classes: roster, learning map, interventions and syllabus within class context.
- Assessments: question bank, quizzes, assignments and results.
- Progress: student timelines and report drafts.
- Requests and settings.

Institute workspace:

- Overview: onboarding, academic actions and operating summary.
- Academics: classes, curriculum, learning gaps, intervention coverage, retention.
- People: students, teachers and guardian relationships.
- Admissions: campaigns, leads and conversion funnel.
- Engagement: missions, rewards and communication delivery.
- Reports: parent reports and institute outcomes, with custom analytics as an advanced view.
- Administration: permissions, plan/usage, settings and audit.

Keep existing URLs working through redirects or retained detail routes. Standardize the class/group terminology shown to customers. Embed report and analytics links in the relevant class/student views so teachers do not need to reconstruct context.

### 3.5 Reduce cross-panel duplication incrementally

The panels duplicate API types, metric helpers, widgets and UI components. Establish an API schema and generate TypeScript contracts; generate or validate Dart contracts against the same schema. Extract stable shared metric and UI utilities into a versioned package after the first vertical slice. Keep role-specific workflows in their apps. A monorepo migration is optional and is not a launch dependency.

## 4. Shared academic foundation

### 4.1 Curriculum and question bank

Proposed entities:

- `curricula`, `curriculum_versions`, `subjects`, `chapters`, `concepts`.
- `concept_prerequisites` with cycle validation; optional for the first pilot.
- `class_curricula` and `teaching_coverage` with taught date and actor.
- `question_bank_items` and immutable `question_versions`.
- `question_concepts`: concept, mapping weight, mapping provenance and reviewer.
- `question_misconception_options`: reviewed mappings from stable option IDs to possible misconceptions.
- `assessment_versions` and versioned assessment-item references.

Keep the existing quiz and question IDs compatible. Bank items produce versioned assessment items. Editing a published assessment creates a new version; past attempts resolve against the exact prompt, answer key, concept mapping and scoring policy they used. Support effective-dated mapping corrections through an explicit audited reprocessing operation.

Add tags to authoring and CSV/XLSX import: curriculum, subject, chapter, concept, teacher-rated difficulty, intended learning outcome, explanation, and optional misconception mapping. Validate imports and preview rejected mappings before publishing. Mapping weights must not multiply the contribution of a multi-concept question.

Treat difficulty as editorial metadata initially. Comparable follow-up forms should share a reviewed blueprint and item mix; later empirical calibration can improve this. Do not call arbitrary different quizzes equivalent.

Prepare a reviewed pilot content pack with distinct baseline, practice and follow-up questions per concept. Budget an academic content owner, not only engineers. Content quality and sufficient variant coverage are launch dependencies.

### 4.2 Evidence and measurement

Proposed entities: `learning_evidence`, `learner_concept_state`, `learning_rule_versions`, and `review_tasks`.

Evidence references user, enrollment, institution, concept, question version, response, assignment purpose and completion time. Store correctness, provided confidence, hints, comparable-form identifier and eligibility reason. Unique event/response/concept keys prevent duplicate updates.

Maintain separate values for demonstrated accuracy, confidence calibration, evidence count, last checked date and delayed-check result. Display an evidence-status label rather than a mysterious single score.

Rules for initial release:

- Missing confidence is unknown; never infer it from response speed.
- No attempts means not assessed, not weak.
- A wrong confident answer is a possible misconception, not a fixed label on the child.
- First, unassisted exposure and comparable follow-ups drive progress claims. Assisted/repeat practice remains visible as practice evidence.
- Configure a minimum distinct-item count and evidence window with the academic lead. Version these rules; proposed thresholds in later sections are pilot defaults only.
- Separate taught, demonstrated, retained, due-for-review and insufficient-evidence states.
- Report cohort denominators, baseline coverage, follow-up coverage and missing data. Pair the same learners across comparable checks when summarizing change.
- Never imply Qwish caused an observed gain without an appropriate evaluation design.

### 4.3 Assignment engine

Proposed entities: `assignments`, `assignment_recipients`, `assignment_attempts`, `assignment_item_versions`.

Support baseline, practice, reassessment, diagnostic and mission-linked purposes. States: draft, scheduled, open, closed, cancelled. Recipient states: assigned, started, submitted, overdue, excused.

Snapshot recipients and items when publishing. Make additions/removals explicit and auditable; do not silently change historical denominators when class membership changes. Include availability windows, timezone, deadline, attempt policy and accommodations. Reuse quiz delivery and attempt scoring with an assignment reference and server-enforced eligibility.

Student delivery in `numpie` is required: assigned work, due checks, resume state, instructions and completion feedback. Evaluate `qwish-web-app` as a lightweight browser surface during Phase 0 before choosing it for student diagnostics/report access. Do not promise parity before auditing its current routes.

## 5. Feature 1 — Misconception insights

**Buyer value:** Teachers can distinguish missing understanding from uncertainty and identify a specific next teaching action.

Implementation:

1. Extend results APIs beyond position/accuracy to expose permitted item-level answer distributions, confidence bands, hints and linked concepts.
2. Add a confidence/correctness matrix with an explicit unknown-confidence group. Keep `pretty_sure` visible as a middle band rather than forcing every answer into a binary confidence label.
3. Present possible misconceptions only where an academic reviewer mapped distractors or repeated evidence supports the signal. A single high-confidence error is a review flag.
4. Suggested pilot escalation: at least two distinct aligned questions show the same reviewed misconception within the configured window. Show the count and contradictory evidence.
5. Provide student and class detail views with evidence dates, questions and teacher notes.
6. Allow teacher confirmation/dismissal with a reason; preserve the underlying responses.
7. Add “Create remedial plan” with suggested recipients and concept selection.

Proposed API families: `/teacher/classes/{id}/learning-insights`, `/teacher/students/{id}/concepts`, `/teacher/quizzes/{id}/response-insights`, `/institution/learning-summary`. All live under the existing API version prefix.

Acceptance: mixed-confidence and missing-confidence fixtures classify correctly; teacher permissions restrict students; one response cannot overstate confidence; repeated imports/events do not duplicate evidence; every insight links to an inspectable source.

Later: calibrated misconception models, richer explanations and multilingual terminology after teacher agreement has been measured. AI may draft explanations; teachers own published academic interpretations.

## 6. Feature 2 — Remedial groups and follow-up checks

**Buyer value:** The institute can see which identified gaps received help and whether follow-up evidence improved.

Proposed entities: `interventions`, `intervention_members`, `intervention_actions`, `intervention_checks`, `intervention_notes`.

Workflow:

1. Teacher selects an insight or manually identifies a learning need.
2. Qwish proposes a temporary remedial cohort, responsible teacher and a reviewed practice pack.
3. Teacher edits recipients, goal, workload and follow-up date; activation creates assignments.
4. Students receive practice and a separate follow-up check using distinct questions.
5. Teacher marks each learner as improved, needs another action, not yet assessed or excused, with evidence.
6. Admin sees ownership, overdue work and completion coverage; no simplistic teacher league table.

Intervention states: draft → active → follow-up due → reviewed → closed; cancelled is separate. Completion of practice alone does not close the learning gap. Temporary intervention membership must not alter the student's main class roster.

Proposed API families: `/teacher/interventions`, `/teacher/assignments`, `/institution/interventions`, `/users/me/assignments`. Add explicit activate, review and close actions with idempotency keys.

Acceptance: a learner cannot receive accidental duplicate overlapping assignments from retries; overdue and excused states are distinct; transferred students stop receiving institution assignments; baseline and follow-up remain linked; no-follow-up students are not counted as improved.

Later: reusable teaching playbooks and evidence showing which approaches worked under comparable conditions. Describe correlations carefully because group selection is not random.

## 7. Feature 3 — Parent progress reports

**Buyer value:** Parents receive specific, credible progress information and understand the institute's next action.

Proposed entities: `progress_reports`, `progress_report_versions`, `report_recipients`, `report_deliveries`, `communication_preferences`. Extend existing parent relationships instead of creating a parallel guardian identity store.

Workflow:

1. Generate a weekly draft from an immutable evidence snapshot.
2. Include learning goals, comparable progress, evidence dates, unresolved needs, teacher action and one practical home activity.
3. Teacher reviews and edits narrative; numerical evidence remains derived and traceable.
4. Publishing freezes the version. Corrections create an explicitly superseding version.
5. Deliver through an authenticated parent view and downloadable PDF; email may notify the parent that a report is ready.
6. Add language templates and optional messaging-provider delivery in a later release.

Use existing parent invite/link/accept/revoke as the starting point, with tests of the full relationship lifecycle before relying on it. An imported guardian phone/email is contact data, not proof of an authorized relationship. Handle multiple guardians, adult learners, consent preferences, revoked access and institute transfers explicitly.

If secure report links are introduced, use short-lived, revocable opaque tokens with recipient verification as appropriate. Do not use guessable report IDs as authorization. Recheck access when viewing or delivering; keep reports institution-scoped even if the parent has a global child link. A downloaded PDF cannot be revoked, which should be clear in product behavior.

Proposed API families: `/teacher/progress-reports`, `/institution/progress-reports`, `/parent/children/{id}/progress-reports`. Separate preview, publish, supersede and delivery actions.

Acceptance: unsupported claims cannot appear in generated numerical summaries; no baseline produces an honest first-check report; revoked recipients lose online access; failed delivery is visible and retryable; one delivery retry cannot create a new report version.

Launch with deterministic templates and teacher notes. Later AI text must cite the supplied structured evidence internally, avoid invented numbers and remain reviewable. Provider-specific messaging requirements and applicable privacy obligations need verification before that integration ships.

## 8. Feature 4 — Admission diagnostics and bridge courses

**Buyer value:** Institutes turn an enquiry into a useful counselling conversation and can measure the resulting admissions funnel.

Proposed entities: `admission_campaigns`, `leads`, `lead_events`, `diagnostic_sessions`, `counselling_tasks`, `batch_recommendations`, `lead_enrollment_links`.

Workflow:

1. Admin chooses a reviewed curriculum-aligned diagnostic and publishes an institute-branded campaign link.
2. Prospect sees what the assessment measures and provides minimal necessary contact/preferences.
3. An expiring diagnostic session permits only that diagnostic; it does not grant institute access or require a fake enrolled account.
4. Diagnostic scoring reuses versioned question/scoring logic, while prospect attempts remain outside student points, leaderboards and enrolled-class metrics.
5. Results summarize demonstrated strengths, possible gaps and missing evidence.
6. Counsellor receives a follow-up task and editable batch/bridge-course recommendation with its criteria.
7. On confirmed enrollment, explicitly link the diagnostic history and assign the bridge pack, without inventing a payment or automatic admission decision.

Use a bounded diagnostic subject/session model, not unauthenticated calls to the existing student attempt endpoints. Session writes must bind to a server-validated campaign and immutable assessment version. Apply expiration, attempt limits and abuse controls.

Lead states: new, diagnostic started, diagnostic completed, contacted, counselling booked, enrolled, lost. Define funnel metrics on deduplicated leads and preserve event timestamps. Use human confirmation to resolve duplicate contacts; siblings can share a guardian phone number.

Proposed API families: `/institution/admissions/campaigns`, `/institution/admissions/leads`, `/diagnostics/sessions`. Public entry points resolve only published campaign metadata; private results require session or staff authorization.

Acceptance: campaign sessions cannot read other prospects; refreshing/resuming does not duplicate a lead; converted leads link to the right enrollment; public results do not expose answer keys prematurely; no inferred percentile, guaranteed exam outcome or automatic rejection.

Later: booking/payment integrations and CRM synchronization after institutes confirm the need. Validate provider APIs and commercial terms at implementation time.

## 9. Feature 5 — Syllabus and retention map

**Buyer value:** Academic heads distinguish content delivery from demonstrated understanding and delayed retention.

Reuse curriculum, evidence and review-task foundations. Extend the existing topic-level scheduling concept into a separate concept-level institutional projection; keep the global personal recommendation state compatible until consumer migration is complete.

Implementation:

1. Teacher records coverage for a class/concept with date and optional lesson reference.
2. Evidence workers compute demonstrated-understanding state from eligible responses.
3. Schedule a delayed review; suggested pilot intervals are 7 and 21 days, configurable around holidays and workload.
4. Use comparable unseen questions for delayed checks. Reschedule with an audit trail when necessary.
5. Show class chapter map, student concept detail and teacher due-review queue.
6. Admin sees taught coverage, assessment coverage, retention-check coverage and observed delayed accuracy separately.

A scheduled review date is not evidence of forgetting. A missed check is unknown retention. Strong immediate performance is not retained learning. Stale evidence is labelled with its age rather than silently replaced with a prediction.

Proposed API families: `/teacher/classes/{id}/syllabus`, `/teacher/review-tasks`, `/institution/retention`, `/users/me/reviews`.

Acceptance: mixed-concept quizzes allocate evidence correctly; holidays and timezones behave consistently; reassignment preserves history; due jobs do not double-create reviews; missing delayed checks remain unknown; changing curriculum versions does not relabel historical evidence without an explicit mapping.

Later: prerequisite-aware recommendations and scheduling models, evaluated against teacher judgment and completion workload before automatic assignment.

## 10. Feature 6 — Learning missions and rewards

**Buyer value:** Institutes connect Qwish's motivational features to verified practice, improvement and retained learning.

Proposed entities: `missions`, `mission_versions`, `mission_participants`, `mission_evidence`, `reward_budgets`, `reward_awards`. Extend the existing points ledger with a unique mission-award reference and required ledger reason support.

Initial mission templates:

- Complete an assigned remedial plan and its follow-up.
- Meet a reviewed improvement condition on comparable checks.
- Complete a delayed retention check with sufficient eligible evidence.
- Participate in a class learning goal; offer alternatives for excused learners.

Workflow:

1. Academic head selects goal, eligible cohort, dates and evidence rules.
2. Owner/admin previews maximum reward exposure and approves a capped budget.
3. Publish immutable mission terms and snapshot participants.
4. Completion events update progress; server-owned rules determine eligibility.
5. Award points atomically with budget consumption, award record and ledger entry.
6. Show pending verification, awarded, expired, cancelled and ineligible states with understandable reasons.

Choose bounded in-app point rewards for V1. Model the budget as points unless a funded redemption product is explicitly defined. Real goods, discounts or money introduce separate fulfillment and commercial work.

Prevent farming through one award per student/mission version, reviewed baseline eligibility, attempt limits and distinct equivalent forms. Do not base a reward solely on a freely repeated self-selected baseline. Permit accommodations so missed streaks or disability-related timing differences do not become punishment.

Proposed API families: `/institution/missions`, `/teacher/missions`, `/users/me/missions`. Teachers propose or activate within delegated limits; commercial budgets remain restricted.

Acceptance: parallel workers cannot overspend a budget or award twice; replay produces identical eligibility; changing point rules does not rewrite issued awards; cancellation preserves earned history; rollback disables new evaluations without deleting the ledger.

## 11. Commercial readiness and additional improvements

### Required for a paid pilot

1. **Guided activation:** curriculum selection → roster import → teacher invite → first assessment → first follow-up → first report. Show who owns each incomplete step and provide a reviewed starter pack.
2. **Outcome-oriented home screens:** prioritize work due today and uncovered learning needs; retain customizable analytics for deeper investigation.
3. **Institute branding:** name, logo, report footer and parent-facing identity. PDF output must print clearly; add language-ready report templates and accessible mobile views.
4. **Data portability:** scoped CSV/PDF exports with audit history and background generation for large requests. This intentionally updates the current no-report-export V1 scope. Use safe CSV encoding and short-lived file access.
5. **Plan entitlements:** server-side module flags, limits and usage records. Manual contract/invoice administration is adequate initially; payment automation is not a prerequisite.
6. **Support visibility:** failed jobs, missing mappings, delivery failures and processing freshness in restricted operational screens. Provide a documented support path and recovery runbooks.
7. **Trustworthy demo:** synthetic demo institute with labelled students, question evidence and a complete before/follow-up/report story. Keep demo data outside production outcome metrics.

### Next improvements after the learning workflow proves valuable

- Academic content packs and reusable intervention templates to reduce onboarding cost.
- Institute-controlled content review for shared bank items and official diagnostics, while retaining teacher publishing permissions where intended.
- Shared contract/UI package to reduce inconsistencies across panels.
- Multiple-institute membership and branch hierarchy, if paid customers require them; migrate all tenant assumptions deliberately.
- Integration API/import templates for existing institute software, after validating the most common systems in the pilot.
- Configurable theme and clear printable reports for classroom/projector use; validate with actual staff rather than treating visual polish as the core differentiator.

Do not make attendance, fee collection, video classes, a generic AI chatbot, or a full ERP a prerequisite for these six features. Add adjacent operations only when customer evidence supports the extra product surface.

## 12. Packaging and measurement

Proposed packages are hypotheses, not validated market pricing:

| Package | Included | Commercial basis to test |
|---|---|---|
| Core | Existing roster, teachers, assessments, standard reporting | Annual institute minimum with enrolled-seat allowance |
| Learning | Concepts/misconceptions, interventions, retention, parent reports, bounded missions | Annual institute minimum plus explicitly defined active learners |
| Admissions add-on | Campaigns, diagnostics, counselling pipeline, bridge-course handoff | Campaign/diagnostic allowance with transparent overage |

Define active learner, billing window, deduplication, suspended/transferred students and usage visibility before charging. Keep guardian access included in the learning package. Communication-provider costs and unusually large content-generation usage should have explicit allowances if introduced. Gate capabilities on the server; keep existing customer data exportable on downgrade within the agreed retention policy.

Customer-facing outcome measurements:

- Proportion of assigned learners completing baseline and comparable follow-up.
- Paired change in eligible check performance, with sample size and missingness.
- Proportion of identified needs receiving a teacher action and a follow-up.
- Time teachers spend planning follow-ups and preparing reports, measured in pilot interviews/time logs.
- Report delivery/access and parent usefulness feedback; do not treat tracking pixels as proof a report was read.
- Deduplicated lead-to-diagnostic and diagnostic-to-confirmed-enrollment conversion.
- Mission participation and delayed-check completion, separate from points issued.

Commercial measurements: activation time, weekly teacher usage, pilot-to-paid conversion, institute renewal intent, support time per institute and delivery/content cost. Do not promise admission or retention revenue uplift before measuring it.

## 13. Delivery sequence and effort

Planning range assumes two full-time engineers spanning backend/web/mobile, one shared QA/product contributor, and an academic content reviewer available each week. It is a capacity estimate, not a deadline. Mobile release lead time, content readiness and parent-link verification can extend it. A solo implementation should expect materially longer.

| Phase | Indicative effort window | Deliverable and exit condition |
|---|---|---|
| 0. Confirm foundations | 1–2 weeks | Trace deployed schema/routes, run a real test attempt, verify parent flow, choose pilot curriculum, agree evidence rules, review student web delivery |
| 1. Academic foundation | 3–4 weeks | Versioned bank/tags, class curriculum, scoped evidence, assignment MVP, outbox and capability groundwork; one assessment works end-to-end |
| 2. Insights and intervention | 3–4 weeks | Feature 1 and Feature 2 with student practice/follow-up delivery; a teacher can complete the full loop |
| 3. Parent reports and paid-pilot readiness | 2–3 weeks | Feature 3, report access, branding, exports, activation flow and entitlements; approved reports reach verified recipients |
| 4. Retention map | 2–3 weeks | Feature 5 full interface and scheduled checks; delayed outcomes display with honest coverage |
| 5. Admissions | 3–4 weeks | Feature 4 campaign-to-enrollment workflow and bridge-course assignment |
| 6. Missions | 2–3 weeks | Feature 6 eligibility, budgeted awards and student progress; concurrency/replay tests pass |
| 7. Broader rollout | 1–2 weeks | Pilot fixes, support/runbooks, load checks, onboarding content, agreed commercial package |

Sequential engineering range: approximately 17–25 weeks under these assumptions. The first paid-pilot candidate is around weeks 9–13, subject to release gates. Recruit design partners during Phase 0. Academic preparation, interviews and commercial setup can run alongside engineering. Delayed-retention evaluation requires actual elapsed learning time and may extend beyond engineering completion.

The release order differs from feature numbering because curriculum/evidence feeds everything, remedial work supplies parent-report value, and missions should reward a verified workflow.

## 14. Implementation work packages

Each package should become a small set of reviewable changes spanning schema, API, UI and verification; do not ship disconnected mock screens as completed features.

| ID | Deliverable | Depends on |
|---|---|---|
| FND-01 | Runtime/schema/route inventory, compatibility decisions and pilot evidence rubric | None |
| FND-02 | Curriculum, academic year and class mapping | FND-01 |
| FND-03 | Bank/versioning and import mapping | FND-02 |
| FND-04 | Staff capabilities and institution-scoped academic access | FND-01 |
| FND-05 | Completion outbox, workers, evidence projection and replay | FND-03, FND-04 |
| ASN-01 | Assignment publishing, recipients and attempt eligibility | FND-03, FND-04 |
| ASN-02 | Student inbox, practice and follow-up delivery | ASN-01 |
| INS-01 | Response insights and teacher evidence detail | FND-05 |
| INT-01 | Intervention lifecycle and admin oversight | INS-01, ASN-02 |
| PAR-01 | Parent relationship verification/access lifecycle | FND-04 |
| REP-01 | Evidence snapshots, teacher review, PDF and recipient delivery | INT-01, PAR-01 |
| RET-01 | Coverage, concept review schedule and retention views | FND-05, ASN-02 |
| ADM-01 | Lead pipeline, campaign sessions and prospect diagnostics | FND-03, FND-04 |
| ADM-02 | Counselling conversion and bridge handoff | ADM-01, INT-01 |
| MIS-01 | Mission rules, enrollment and progress | INT-01, RET-01 |
| MIS-02 | Atomic budget/ledger awards and operational controls | MIS-01 |
| COM-01 | Branding, entitlements, usage, exports and activation | FND-04; completes with REP-01 |
| OPS-01 | Delivery health, support tools and rollout controls | Incremental across all packages |

## 15. Migration, verification and rollout

### Migration strategy

Use additive migrations with the next available sequence at implementation time; do not reserve numbers against the currently inspected migration list. Add nullable compatibility fields first, backfill in batches, verify coverage, then tighten constraints where valid.

Preserve existing quiz publishing, attempt scoring, points and student enrollment semantics until explicit cutovers. New feature flags enable per-institute pilots. Feature flags and paid entitlements serve different purposes and both are checked server-side.

For historical data, only backfill what can be established. Existing responses joined to mutable questions may not reconstruct the exact historical prompt or answer key. Mark unresolved version provenance and exclude it from comparable progress claims. Do not invent historical confidence, concept tags or enrollment attribution.

Introduce new institution-concept state alongside global topic state, reconcile sampled outcomes, switch institute consumers, then separately evaluate personal recommendation migration. Maintain a mapping/version audit so learning rules can be recalculated without rewriting raw responses.

### Required verification

- Unit tests of evidence classifications, sufficiency, confidence unknowns, comparable-form eligibility and mission rules.
- Integration tests with real PostgreSQL for completion/outbox atomicity, deduplication, concurrency, award budgets and access scope.
- Contract checks for Go responses and TypeScript/Dart consumers, including old clients with missing new fields.
- End-to-end path: publish tagged assessment → student baseline → teacher insight → intervention → follow-up → approved parent report → eligible mission reward.
- Admission path: campaign → prospect attempt → counsellor follow-up → confirmed conversion → bridge assignment.
- Scope fixtures for two institutes, overlapping staff roles, former enrollments, revoked parents and unclaimed roster rows.
- Failure cases: worker crash, duplicate completion, missed cron, provider timeout, revoked delivery recipient, edited assessment and archived class.
- Query/load checks at pilot-sized and expected next-stage rosters; page large evidence lists and generate exports asynchronously.
- Accessibility and mobile checks for student tasks, report reading, keyboard interaction, non-color status cues and printable PDFs.

### Release gates

Do not launch a module until its end-to-end workflow passes, historical compatibility is checked, content is reviewed, operator recovery is documented, and the pilot institute can complete it without developer assistance.

Rollback disables the module and stops new jobs/assignments safely while retaining attempts, evidence, published reports and ledger records. Reward corrections use audited compensating entries. Parent revocation is honored even for already queued deliveries. Expose a degraded-processing state during worker failures rather than showing stale outcomes as current.

## 16. First concrete implementation slice

Begin with one class and one concept:

1. Create a reviewed baseline, practice set and comparable follow-up with immutable item versions.
2. Assign the baseline through the current student app.
3. Capture existing confidence/answer evidence and process it through the durable projection.
4. Show a teacher an evidence-backed possible misconception.
5. Let the teacher assign a remedial pack and follow-up.
6. Generate one reviewed parent progress report from the paired evidence.

This validates the core purchase story before broad curriculum coverage. Once it works, expand the same foundation to syllabus retention, admission diagnostics and missions according to the work packages above.
