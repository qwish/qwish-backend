# Teacher and institute dashboard workflows

Implemented on 21 October 2026. These changes reorganize existing authenticated workflows; they do not establish a production deployment.

## Navigation

Teacher navigation has six daily destinations: Today, My Classes, Assessments, Communication, Reviews and Reports. Notices and identified Forms & Polls sit under Communication. Student search remains available in the header, mobile header and utilities. Leadership remains permission-scoped.

Institute navigation has six daily destinations: Overview, Classes, People, Academics, Communication and Reports. Contextual navigation exposes students/teachers/access, academic setup and report types. Setup guide, audit history and settings are secondary destinations. Existing URLs remain supported.

## Class learning workflow

Open `/classes/detail?id=<class-id>&tab=learning` in the teacher panel:

1. Choose a concept and optional evidence dates.
2. Inspect student evidence and its question counts. Missing, insufficient and stale evidence remain distinct.
3. Adjust the initial student selection, which includes current support signals and excludes stale signals.
4. Open a student's support plan in the class workspace. Existing private notes are loaded before editing; save the plan, status and review date through the existing support API.
5. Prepare a follow-up. The builder carries class, concept, selected students and date context. Its publication screen allows editing individual recipients and intersects the URL selection with students returned for the current class/concept. The backend continues to enforce membership and teacher access.
6. Publish and return to the class's Assigned Work. Open Review follow-up outcomes from Learning to inspect comparison evidence and record a decision; the report provides a return link to class learning.

Date controls filter rows by their latest evidence date; concept summary totals still cover all available evidence and are labelled accordingly. These controls do not recompute a historical mastery model. Archived classes expose learning read-only.

## Institute setup and oversight

`/setup` and the Overview setup section derive progress from saved group data. Missing list metrics remain unknown. Existing classes resume through `/groups/new?class=<id>&step=teachers|curriculum|students`; resumption does not create a second class. Joining state is shown independently from student participation.

The guided flow allocates teachers, attaches curriculum, then handles students/joining. School setup omits requests for college programmes, cohorts and terms. Colleges retain these optional structures. A department is optional and no longer keeps an otherwise configured school class in unfinished setup.

Support follow-through explicitly shows overdue review totals, teacher ownership/workload and links from follow-up items back to their classes. Existing class/student attention queues retain evidence and overdue-review details. Teacher workload is not a teaching-quality ranking.

## Validation

Run the focused unit checks from the workspace root:

```sh
bun test qwish-institute-dashboard/src/lib/class-setup.test.ts qwish-teacher-panel/src/lib/workspace-context.test.ts
```

Run `npm run build` in each dashboard. For browser regression checks, start the teacher panel on port 3211 and institute dashboard on port 3212, then run:

```sh
node scripts/dashboard-workflows.browser.cjs
```

The browser runner uses an installed Playwright package or the workspace's `qwish-platform-site` installation. `PLAYWRIGHT_MODULE`, `TEACHER_TEST_URL`, `INSTITUTE_TEST_URL` and `DASHBOARD_SCREENSHOTS` override defaults. API calls are intercepted with illustrative test data; this does not verify deployed backend behavior. Screenshots default to `/tmp/qwish-dashboard-workflows`.
