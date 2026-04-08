

**QuizApp**  
**User PRD**  
Product Requirements — Student Layer

Version 1.0  •  February 2026

| Status | Draft |
| :---- | :---- |
| **Layer** | User (Student) |
| **Platforms** | Flutter (iOS & Android), Next.js Web |
| **Depends On** | QuizApp PRD v1.1 |
| **Out of Scope** | Teacher creation tools, institution management, admin controls |

# **1\. Purpose & Scope**

This document defines the product requirements for the Student layer of QuizApp — the experience for any user who joins the platform to solve quizzes, earn points, compete on leaderboards, and track their progress. This PRD covers everything the student sees, does, and controls within the app. Teacher-facing and admin-facing features are covered in their respective PRDs.

## **1.1 Who Is This User?**

* A student enrolled at a school, college, or tuition that has been onboarded to QuizApp.  
* They join via a referral code issued by their institution.  
* Their primary goal is to solve quizzes, maintain streaks, earn points, and climb the leaderboard.  
* They interact exclusively via the Flutter mobile app or the Next.js web app.

## **1.2 Goals for V1**

* Give students a fast, engaging quiz-taking experience across 7 question types.  
* Motivate daily engagement through a streak system with a grace window.  
* Reward performance with a points economy that feels meaningful.  
* Surface competitive context via institution and global leaderboards.  
* Allow students to manage their experience: save quizzes, report issues, share, and request topics.  
* Provide a public profile with badges and stats that reflect effort and achievement.

## **1.3 Out of Scope for V1**

* Student-to-student duels (Phase 2\)  
* Gift card redemption (Phase 2\)  
* Brand-sponsored rewards (Phase 2\)  
* Push notification delivery (design only in V1; FCM integration Phase 2\)

# **2\. Onboarding & Account Setup**

## **2.1 Sign Up**

* Student provides: full name, email address, password, and institution referral code.  
* The referral code determines institution membership. If the code is a student code, the role is set to Student automatically.  
* Before submitting, the app shows a confirmation badge: "You’re joining as a Student at \[Institution Name\]."  
* On successful signup, the student is taken directly to the Home screen. No email verification gate in V1.  
* If the referral code is invalid, signup is blocked with an inline error message.

## **2.2 Log In**

* Email and password login.  
* Forgot password flow: student enters email, receives a one-time password (OTP) via email, sets a new password.  
* OTP is valid for 15 minutes and single-use.

## **2.3 Changing Institution**

* A student can update their referral code from Settings at any time.  
* Changing institution transfers the student’s account to the new institution.  
* Points, streaks, and history are retained. Leaderboard position resets to the new institution’s scope.  
* A warning is shown before confirming: "You will leave \[Current Institution\] and join \[New Institution\]."

## **2.4 Parent Linking**

* A student can generate a link invite code from their profile settings.  
* The parent uses this code to link their account to the student.  
* Student sees a pending request notification and must accept before the link is active.  
* Student can revoke a parent link at any time from Settings.

# **3\. Home Screen & Navigation**

## **3.1 Bottom Navigation**

| Tab | Icon | Content |
| :---- | :---- | :---- |
| Home | House icon | Streak, points, active Play & Win banner, recent activity |
| Quizzes | Quiz/list icon | Browse all available quizzes by type |
| Leaderboard | Trophy icon | Institution and global rankings |
| Profile | Person icon | Stats, badges, history, settings |

## **3.2 Home Screen**

* Greeting: first name \+ current date.  
* Streak card: flame icon, current streak count, progress bar toward next milestone (e.g. “5 / 7 days”). Amber highlight if streak is in the grace window.  
* Points card: total points balance. Amber warning note if any points expire within 30 days.  
* Active Play & Win banner: shown only when an active Play & Win quiz exists. Displays quiz title, end date countdown, and a “Play Now” CTA.  
* Recent activity: last 3 quiz attempts — quiz name, score badge, and points delta. Tappable to view full result.  
* Saved quizzes shortcut: shown only if the student has saved quizzes. Displays count and links to the saved list.

# **4\. Quiz Browser**

## **4.1 Quiz List**

* Segmented control: Knowledge Check • Play & Win.  
* Each quiz card displays: title, teacher name, question count, estimated time, and a status chip.  
* Status chips: New • Completed • Saved • Ends in \[countdown\] (Play & Win) • Closed.  
* Students can tap the bookmark icon on any quiz card to save it for later.  
* Saved quizzes appear in a dedicated “Saved” filter within the quiz browser.

## **4.2 Quiz Detail Screen**

* Quiz title, type badge, teacher name, and description.  
* Question count and total estimated time.  
* Row of question-type icons previewing the types present in the quiz.  
* For Play & Win: end date, one-attempt warning.  
* CTAs: Start Quiz (primary) • Save for Later (secondary) • Share (icon button).

## **4.3 Saving a Quiz**

* Student taps the bookmark icon on a quiz card or the “Save for Later” button on the quiz detail screen.  
* Saved quizzes are stored per-user and persist across sessions.  
* Student can remove a saved quiz at any time.  
* Play & Win quizzes that have closed are removed from the saved list automatically.

## **4.4 Sharing a Quiz**

* Student taps the share icon on a quiz card or detail screen.  
* A deep link to the quiz is generated and opened in the native share sheet (iOS/Android) or copied to clipboard (web).  
* The recipient must be a student in the same institution to access an institution quiz.  
* Public quizzes can be shared with anyone; recipients are prompted to sign up if not already a user.

## **4.5 Reporting a Quiz or Question**

* Available from the quiz detail screen (Report Quiz) and during an active attempt (Report Question, accessible via a … menu on each question).  
* Report reasons: Incorrect answer • Offensive content • Unclear question • Technical issue • Other.  
* Student can optionally add a text description (max 300 characters).  
* Reports are submitted to the admin moderation queue. The student sees a confirmation: “Thanks — we’ll review this."  
* Students cannot see the status of their reports in V1.

## **4.6 Requesting a Quiz Topic**

* Accessible from the quiz browser via a “Request a Topic” button at the bottom of the list.  
* Student enters: topic name (required), subject area (optional), and a short description of what they’d like covered (optional, max 200 characters).  
* Requests are visible to teachers within the same institution in their dashboard.  
* Students see a list of their own past requests and their status (Pending • In Progress • Done).

# **5\. Quiz Flow**

## **5.1 Starting a Quiz**

* Tapping “Start Quiz” on the detail screen begins the attempt immediately.  
* For Play & Win: a confirmation modal is shown — “This quiz can only be attempted once. Are you sure?”  
* The quiz enters full-screen focus mode (dark background, bottom nav hidden).

## **5.2 Question Timing**

* Each question has an individual time limit of 10–15 seconds (set by the teacher).  
* The timer is displayed as a countdown in the top bar.  
* Timer colour transitions: white → amber at 5 seconds → coral at 3 seconds.  
* If the timer reaches 0 before an answer is submitted, the question is marked incorrect and the quiz advances automatically.

## **5.3 Question Types**

| Type | Student Interaction |
| :---- | :---- |
| Multiple Choice | Tap one option from 2–6 choices. Selected state: accent border. Result revealed immediately after tap or timeout. |
| Confidence-Based | Answer the MCQ first, then rate confidence: Not Sure • Pretty Sure • Very Confident. Points are scaled by confidence accuracy. |
| Eliminate the Wrong One | Tap options to eliminate incorrect ones one at a time. Last remaining option is submitted as the answer. |
| Puzzle | View an image (pinch to zoom). Answer via MCQ options below. |
| Speed Chain (Combo) | Rapid-fire questions. Consecutive correct answers build a combo multiplier (x1 → x2 → x3+). Chain breaks on wrong answer or timeout. |
| Arrange in Order | Drag and drop items into the correct sequence. Confirm button submits. |
| Clue Reveal | Answer a hidden question. Tap “Reveal Clue” to expose clues one at a time. Fewer clues used \= more points earned. |

## **5.4 Between Questions**

* A brief full-screen flash: green for correct, red for incorrect.  
* Points delta shown large and centre: “+120 pts” or “−40 pts”.  
* Speed Chain: current combo multiplier displayed.  
* Flash duration: 1.5 seconds. Auto-advances to next question.

## **5.5 Completing a Quiz**

* After the last question, the attempt is submitted automatically and scoring runs server-side.  
* The student is taken to the Results screen.

## **5.6 Results Screen**

* Score displayed as a large percentage with an animated arc/ring graphic.  
* Performance badge: Excellent (≥75%) • Good (50–74%) • Needs Work (\<50%).  
* Points earned or deducted shown as a large delta with direction indicator.  
* Combo bonus shown separately if Speed Chain was part of the quiz.  
* Scrollable question-by-question breakdown: question snippet, student’s answer, correct answer, points per question.  
* For Knowledge Check: “Try Again” and “Back to Quizzes” CTAs.  
* For Play & Win: score shown, rank hidden behind a lock until the quiz end date. CTA: “View Leaderboard”.

# **6\. Points & Rewards**

## **6.1 How Points Are Earned**

| Event | Points Action |
| :---- | :---- |
| Score ≥ 75% | Base points \+ performance bonus |
| Score 50–74% | Base points only |
| Score \< 50% | Points deducted (cannot go below 0\) |
| Speed Chain combo | Bonus per combo level, added to attempt total |
| Clue Reveal (early answer) | Tiered bonus — fewer clues \= more points |
| 7-day streak milestone | One-time streak bonus (resets each cycle) |
| 15-day streak milestone | One-time streak bonus (higher) |
| 30-day streak milestone | One-time streak bonus (highest) |

## **6.2 Points Rules**

* Points cannot go below 0\. Deductions are capped at the student’s current balance.  
* Points expire 6 months after they are earned (rolling expiry per batch, not account-wide).  
* Students see an expiry warning on the Home screen and profile if points expire within 30 days.  
* The points ledger (full history of transactions) is visible in the Profile screen.

## **6.3 Points Display**

* Total balance shown on Home screen and Profile.  
* After each quiz, a points delta animation plays on the Results screen (count-up or count-down).  
* Points ledger in Profile shows: date, quiz name, amount, reason, and expiry date per transaction.

# **7\. Streak System**

A streak increments by 1 each calendar day (institution timezone) the student completes at least one quiz.

## **7.1 Streak Rules**

| Scenario | Outcome |
| :---- | :---- |
| Completes a quiz today (first completion today) | Streak increments by 1\. last\_completed\_date \= today. |
| Completes another quiz today (already counted) | No change to streak. |
| Yesterday was missed, completes within 12-hour grace window today | Streak preserved and increments. Grace window cleared. |
| Grace window expires without a completion | Streak resets to 0\. |
| Reaches 7-day milestone | One-time bonus points awarded. Milestone flag set. |
| Reaches 15-day milestone | Higher bonus points awarded. |
| Reaches 30-day milestone | Highest bonus points awarded. |
| Streak resets | All milestone flags reset — student can earn them again on the next cycle. |

## **7.2 Streak Display**

* Streak card on Home: flame icon, current count, progress bar to next milestone.  
* Amber glow on the streak card when the grace window is active (“Streak at risk\!” label).  
* Longest streak shown on Profile.

# **8\. Leaderboard**

## **8.1 Scopes**

| Scope | Ranked By | Who Can See It |
| :---- | :---- | :---- |
| My Institution | Total points within the institution | All students and teachers in the institution |
| Global | Total points across all institutions | All authenticated users |

## **8.2 Leaderboard Display**

* Toggle between “My Institution” and “Global” at the top of the screen.  
* Top 3 displayed with gold, silver, and bronze treatment.  
* Each row: rank, display name, total points, current streak flame.  
* The current student’s rank is pinned as a floating card at the bottom of the screen, always visible while scrolling.  
* Rankings refresh every Monday at 00:01 UTC.  
* On the global leaderboard, only display\_name is shown — no institution name or full name.

## **8.3 Viewing Another Student’s Profile from Leaderboard**

* Tapping a student on the institution leaderboard opens their public profile.  
* Public profile shows: display name, institution, total points, current streak, longest streak, badges earned, and number of quizzes completed.  
* Full name, email, and quiz-level history are not shown on public profiles.

# **9\. Profile**

## **9.1 Own Profile**

* Avatar: initials-based, colour derived from display name. Not customisable in V1.  
* Display name, institution name, member since date.  
* Stats row: Total Points • Quizzes Taken • Average Score • Current Streak.  
* Badges section: earned badges displayed as a grid. Unearned badges shown as locked silhouettes to motivate progress.  
* Points expiry warning card if any points expire within 30 days.  
* Points ledger: paginated transaction history (date, quiz, points, reason, expiry).  
* Recent attempts: last 10 quiz attempts with score and points delta. Tappable for full result.  
* Saved quizzes shortcut.  
* Settings gear icon top-right.

## **9.2 Public Profile (Visible to Others)**

* Accessible from leaderboard or institution-scoped search.  
* Visible to any authenticated user within the same institution, and to all users for the global leaderboard.  
* Shows: display name, institution, total points, current streak, longest streak, quizzes completed, and badges earned.  
* Does not show: full name, email, quiz-level history, points ledger, or saved quizzes.

## **9.3 Badges & Achievements**

| Badge | Trigger |
| :---- | :---- |
| First Quiz | Complete your first quiz |
| On a Roll | Reach a 7-day streak for the first time |
| Unstoppable | Reach a 30-day streak |
| Top 10 | Appear in institution leaderboard top 10 |
| Perfect Score | Score 100% on any quiz |
| Speed Demon | Complete a Speed Chain quiz with a x3 combo or higher |
| Sharp Mind | Score 100% on a Confidence-Based quiz with all Very Confident selections correct |
| Explorer | Complete one quiz of each of the 7 question types |

## **9.4 Settings**

* Account: edit display name, change password.  
* Institution: current institution, “Change Referral Code” option with confirmation warning.  
* Parent link: view linked parents, revoke links, generate new invite codes.  
* Notifications: toggle for streak reminders (UI only in V1; delivery via FCM in Phase 2).  
* Log Out: bottom of list, red label, confirmation required.  
* Delete Account: available in V1 for compliance. Soft-deletes the account. Data retained per retention policy.

# **10\. Phase 2 Preview (Student Layer)**

| Feature | Description | V1 Preparation |
| :---- | :---- | :---- |
| Duels | Challenge another student to the same quiz simultaneously. 8-character challenge code used to connect. | challenge\_code stored on users table from V1. |
| Gift Card Redemption | Redeem accumulated points for gift cards from sponsor brands. | Points ledger with per-entry expiry tracking is in V1. FIFO redemption logic ready. |
| Brand-Sponsored Play & Win | Play & Win quizzes with real prizes funded by brands. | Play & Win quiz type and end-date announcement flow built in V1. |
| Push Notifications | Streak reminders, quiz announcements, duel challenges delivered as push notifications. | Notification preference toggles exist in Settings in V1. |
| Duel Win Margin Bonus | Win a duel by 80% or more margin for extra bonus points. | Scoring engine extensible; no V1 changes needed. |

# **11\. Open Questions**

| \# | Question | Owner | Status |
| :---- | :---- | :---- | :---- |
| 1 | Exact base point values per quiz type and question? | Product | Open |
| 2 | Exact deduction amount for scores \< 50%? | Product | Open |
| 3 | Streak milestone bonus point values (7, 15, 30 days)? | Product | Open |
| 4 | Confidence-based scoring multiplier table? | Product | Open |
| 5 | Maximum number of quizzes a student can save? | Product | Open |
| 6 | Should topic requests be visible to all teachers or only assigned teachers? | Product | Open |
| 7 | Can a student un-report a quiz/question after submission? | Product | Open |
| 8 | Attempt recovery: if app crashes mid-quiz, can the student resume? | Engineering | Open |

