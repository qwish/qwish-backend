package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/qwish/backend/internal/config"
	"github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/domain/admin"
	"github.com/qwish/backend/internal/domain/attempt"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/domain/avatar"
	"github.com/qwish/backend/internal/domain/contact"
	"github.com/qwish/backend/internal/domain/demo"
	"github.com/qwish/backend/internal/domain/editrequest"
	"github.com/qwish/backend/internal/domain/enrollment"
	"github.com/qwish/backend/internal/domain/institution"
	"github.com/qwish/backend/internal/domain/leaderboard"
	"github.com/qwish/backend/internal/domain/metrics"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/offline"
	"github.com/qwish/backend/internal/domain/onboarding"
	"github.com/qwish/backend/internal/domain/onboardingsession"
	"github.com/qwish/backend/internal/domain/parent"
	"github.com/qwish/backend/internal/domain/points"
	"github.com/qwish/backend/internal/domain/push"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/domain/streak"
	"github.com/qwish/backend/internal/domain/studygroup"
	"github.com/qwish/backend/internal/domain/teacher"
	"github.com/qwish/backend/internal/domain/topicrequest"
	"github.com/qwish/backend/internal/domain/upload"
	"github.com/qwish/backend/internal/domain/user"
	mw "github.com/qwish/backend/internal/middleware"
	"github.com/qwish/backend/internal/scheduler"
	"github.com/qwish/backend/internal/storage"
)

func main() {
	cfg := config.Load()
	pool := db.Connect(cfg.DatabaseURL)
	defer pool.Close()

	db.RunMigrations(pool)

	// Services
	authSvc := auth.NewService(pool, cfg)
	userSvc := user.NewService(pool)
	quizSvc := quiz.NewService(pool)
	streakSvc := streak.NewService(pool)
	demoSvc := demo.NewService(pool, quizSvc)
	obSessionSvc := onboardingsession.NewService(pool, quizSvc)
	attemptSvc := attempt.NewService(pool, quizSvc, streakSvc)
	pushSvc := push.NewService(pool, cfg.FCMProjectID, cfg.FCMCredentialsJSON)
	notifSvc := notification.NewService(pool, cfg.ResendAPIKey, cfg.InstituteURL, cfg.SuperAdminURL)
	notifSvc.SetPusher(func(ctx context.Context, userID, title, body string, data map[string]string) {
		pushSvc.SendToUser(ctx, userID, push.Payload{Title: title, Body: body, Data: data})
	})
	attemptSvc.SetNotifier(notifSvc)
	obSessionSvc.SetAttempts(attemptSvc)
	r2Client := storage.NewR2Client(cfg)
	offlineSvc := offline.NewService(pool)
	studyGroupSvc := studygroup.NewService(pool)
	sched := scheduler.New(pool, streakSvc, pushSvc, notifSvc, userSvc)

	// Handlers
	authH := auth.NewHandler(authSvc)
	avatarH := avatar.NewHandler()
	userH := user.NewHandler(userSvc)
	quizH := quiz.NewHandler(quizSvc)
	demoH := demo.NewHandler(demoSvc)
	obSessionH := onboardingsession.NewHandler(obSessionSvc)
	authH.SetOnboardingClaimer(obSessionSvc)
	attemptH := attempt.NewHandler(attemptSvc)
	pointsH := points.NewHandler(pool)
	streakH := streak.NewHandler(streakSvc)
	leaderboardH := leaderboard.NewHandler(pool)
	parentH := parent.NewHandler(pool)
	topicH := topicrequest.NewHandler(pool)
	uploadH := upload.NewHandler(r2Client)
	enrollmentSvc := enrollment.NewService(pool)
	institutionH := institution.NewHandler(pool, notifSvc, enrollmentSvc, cfg.AppURL, cfg.TeacherURL)
	teacherH := teacher.NewHandler(pool)
	adminH := admin.NewHandler(pool, cfg, notifSvc)
	metricsH := metrics.NewHandler(pool, admin.MetricsScopeResolver(pool))
	instMetricsH := metrics.NewHandler(pool, institution.MetricsScopeResolver())
	teacherMetricsH := metrics.NewHandler(pool, teacher.MetricsScopeResolver(pool))
	layoutsH := admin.NewLayoutsHandler(pool, "admin_dashboard_layouts", "admin_id", mw.GetAdminID)
	userLayoutsH := admin.NewLayoutsHandler(pool, "user_dashboard_layouts", "user_id", mw.GetUserID)
	onboardingH := onboarding.NewHandler(pool, cfg.TurnstileSecret)
	contactH := contact.NewHandler(pool, notifSvc, cfg.BrandURL, cfg.TurnstileSecret)
	notifH := notification.NewHandler(notifSvc)
	pushH := push.NewHandler(pool)
	offlineH := offline.NewHandler(offlineSvc)
	studyGroupH := studygroup.NewHandler(studyGroupSvc)

	enrollmentStudentH := enrollment.NewStudentHandler(enrollmentSvc)
	enrollmentInstH := enrollment.NewInstitutionHandler(enrollmentSvc, pool)
	enrollmentTeacherH := enrollment.NewTeacherHandler(enrollmentSvc)
	editRequestH := editrequest.NewHandler(editrequest.NewService(pool))
	studentAdminH := admin.NewStudentAdminHandler(pool)
	profileEntryH := user.NewProfileEntryHandler(pool)

	_ = notifSvc
	_ = scoring.LoadConfig // referenced by services

	// Router
	r := chi.NewRouter()
	r.Use(mw.RequestLog)
	r.Use(chimw.Recoverer)
	allowedOrigins := buildOriginSet(cfg.AllowedOrigins)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			origin := req.Header.Get("Origin")
			if allowedOrigins == nil {
				// wildcard: allow all
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && allowedOrigins[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			if req.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, req)
		})
	})

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		mw.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	// ==========================================
	// API v1
	// ==========================================
	r.Route("/api/v1", func(r chi.Router) {

		// Stream route must bypass the 30s timeout middleware
		r.Group(func(r chi.Router) {
			r.Use(mw.Authenticate(cfg.SupabaseJWTSecret, cfg.SupabaseURL, pool))
			r.Get("/users/me/notifications/stream", notifH.Stream)
		})

		// ---- Query profiling (EXPLAIN ANALYZE on the real list queries) ----
		// Authenticated by CRON_SECRET only, so it sits outside the Supabase
		// auth group — an ops/cron caller has no JWT. Runs in production too,
		// since that is where the data lives, and is registered ONLY when
		// CRON_SECRET is non-empty: RequireCronSecret compares a missing
		// header against the configured value, so an empty secret would leave
		// the endpoint unauthenticated.
		// Outside the 30s timeout group as well — EXPLAIN ANALYZE executes
		// every query and the handler enforces its own 60s budget.
		if cfg.CronSecret != "" {
			r.Route("/internal/profile", func(r chi.Router) {
				r.Use(mw.RequireCronSecret(cfg.CronSecret))
				r.Get("/quiz-list", quizH.Profile)
			})
		}

		// ---- Internal cron endpoints ----
		// Triggered by the Render cron services declared in render.yaml (see
		// runInProcessCron removal): scheduling lives outside the process, so a
		// restart or deploy can no longer swallow a run. Authenticated by
		// CRON_SECRET only — a cron caller has no Supabase JWT — and registered
		// ONLY when the secret is non-empty, since RequireCronSecret compares a
		// missing header against the configured value and an empty secret would
		// leave these open. Outside the 30s timeout group: nightly sweeps over
		// the whole ledger take longer than a request.
		if cfg.CronSecret != "" {
			r.Route("/internal/cron", func(r chi.Router) {
				r.Use(mw.RequireCronSecret(cfg.CronSecret))
				r.Post("/expire-points", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.ExpirePoints(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/reset-streaks", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.ResetStreaks(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/snapshot-leaderboard", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.SnapshotLeaderboard(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/close-expired-quizzes", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.CloseExpiredQuizzes(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/abandon-stale-attempts", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.AbandonStaleAttempts(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/purge-onboarding-sessions", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.PurgeOnboardingSessions(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/purge-attempt-behavior", func(w http.ResponseWriter, r *http.Request) {
					deleted, err := attemptSvc.PurgeExpiredBehavior(r.Context())
					if err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]int64{"deleted": deleted})
				})
				r.Post("/streak-nudges", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.SendStreakNudges(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/weekly-digests", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.SendWeeklyDigests(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/rank-change-alerts", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.SendRankChangeAlerts(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/weekly-insights-email", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.SendWeeklyInsightsEmail(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
				r.Post("/recompute-question-difficulty", func(w http.ResponseWriter, r *http.Request) {
					if err := sched.RecomputeQuestionDifficulty(r.Context()); err != nil {
						mw.InternalError(w)
						return
					}
					mw.JSON(w, http.StatusOK, map[string]string{"message": "done"})
				})
			})
		}

		// Normal endpoints subject to 30s timeout
		r.Group(func(r chi.Router) {
			r.Use(chimw.Timeout(30 * time.Second))

			// ------ Public Institution Onboarding ------
			r.Route("/onboarding", func(r chi.Router) {
				// Public + unauthenticated registration — rate-limit per IP.
				r.With(mw.RateLimit(5, 10*time.Minute)).Post("/institution", onboardingH.RegisterInstitution)
				r.Get("/institution/status", onboardingH.CheckStatus)
				r.Get("/taxonomy", obSessionH.Taxonomy)

				// Pre-signup calibration. Public and unauthenticated: the
				// session id is the only credential, so rate-limit per IP.
				r.Route("/session", func(r chi.Router) {
					r.With(mw.RateLimit(10, 10*time.Minute)).Post("/", obSessionH.Create)
					r.Patch("/{sessionId}", obSessionH.UpdatePrefs)
					r.Get("/{sessionId}/recommendations", obSessionH.Recommendations)
					r.Get("/{sessionId}/quizzes/{quizId}", obSessionH.Questions)
					r.With(mw.RateLimit(30, 10*time.Minute)).Post("/{sessionId}/submit", obSessionH.Submit)
				})
			})

			// ------ Public Avatars (deterministic SVG, no auth) ------
			r.Get("/avatars/options", avatarH.Meta)
			r.Get("/avatars/{seed}", avatarH.Get)

			// ------ Public Contact Form ------
			// Public + unauthenticated, so rate-limit per IP to curb spam/abuse.
			r.With(mw.RateLimit(5, 10*time.Minute)).Post("/contact", contactH.Submit)

			// ------ Public Demo Quizzes (onboarding, no auth) ------
			// Curated demo quizzes played before login; graded statelessly.
			r.Route("/demo", func(r chi.Router) {
				r.Get("/quizzes", demoH.List)
				r.Get("/quizzes/{quizId}", demoH.Questions)
				r.With(mw.RateLimit(30, 10*time.Minute)).Post("/quizzes/{quizId}/score", demoH.Score)
			})

			// ------ AUTH (public) ------
			r.Route("/auth", func(r chi.Router) {
				// Sends an email per call — limit per-IP (burst/abuse) and per-email
				// (targeted spam to one inbox) to curb OTP spam.
				r.With(
					mw.RateLimit(5, 15*time.Minute),
					mw.RateLimitByJSONField(3, 15*time.Minute, "email"),
				).Post("/send-otp", authH.SendOTP)
				r.Post("/verify-otp", authH.VerifyOTP)
				// Sign-up forms check an address before spending an OTP on it.
				// Answers only taken/free — never which surface holds it.
				r.With(mw.RateLimit(20, 15*time.Minute)).Get("/email-availability", authH.EmailAvailability)
				r.Post("/refresh", authH.Refresh)
				r.Get("/teacher-invite", authH.GetTeacherInvite)

				// Passkey (WebAuthn) login — alternative to OTP. Public; the
				// begin step is rate-limited per IP and per email to curb abuse.
				r.With(
					mw.RateLimit(10, 15*time.Minute),
					mw.RateLimitByJSONField(5, 15*time.Minute, "email"),
				).Post("/passkey/login/begin", authH.PasskeyLoginBegin)
				r.Post("/passkey/login/finish", authH.PasskeyLoginFinish)

				// Usernameless (conditional-UI / autofill) login — no email.
				r.With(
					mw.RateLimit(10, 15*time.Minute),
				).Post("/passkey/login/begin-discoverable", authH.PasskeyLoginBeginDiscoverable)
				r.Post("/passkey/login/finish-discoverable", authH.PasskeyLoginFinishDiscoverable)

				// User (teacher) passkey login — same shapes as the admin routes
				// above, but resolved against the users table. Public.
				r.With(
					mw.RateLimit(10, 15*time.Minute),
					mw.RateLimitByJSONField(5, 15*time.Minute, "email"),
				).Post("/passkey/user/login/begin", authH.UserPasskeyLoginBegin)
				r.Post("/passkey/user/login/finish", authH.UserPasskeyLoginFinish)
				r.With(
					mw.RateLimit(10, 15*time.Minute),
				).Post("/passkey/user/login/begin-discoverable", authH.UserPasskeyLoginBeginDiscoverable)
				r.Post("/passkey/user/login/finish-discoverable", authH.UserPasskeyLoginFinishDiscoverable)

				// JWT-only (user may not exist in DB yet)
				r.Group(func(r chi.Router) {
					r.Use(mw.AuthenticateJWTOnly(cfg.SupabaseJWTSecret, cfg.SupabaseURL))
					r.Post("/create-profile", authH.CreateProfile)
				})

				// Protected auth routes
				r.Group(func(r chi.Router) {
					r.Use(mw.Authenticate(cfg.SupabaseJWTSecret, cfg.SupabaseURL, pool))
					r.Post("/logout", authH.Logout)
					// Ends every session on every device. Distinct from /logout,
					// which only forwards to Supabase and is a no-op for a
					// passkey session.
					r.Post("/sessions/revoke-all", authH.RevokeAllSessions)
					r.Patch("/referral-code", authH.UpdateReferralCode)

					// Passkey enrolment & management for the signed-in admin.
					r.Post("/passkey/register/begin", authH.PasskeyRegisterBegin)
					r.Post("/passkey/register/finish", authH.PasskeyRegisterFinish)
					r.Get("/passkey/credentials", authH.PasskeyList)
					r.Patch("/passkey/credentials/{id}", authH.PasskeyRename)
					r.Post("/passkey/credentials/{id}/primary", authH.PasskeySetPrimary)
					r.Delete("/passkey/credentials/{id}", authH.PasskeyDelete)

					// User (teacher) passkey enrolment & management for the
					// signed-in user (users table).
					r.Post("/passkey/user/register/begin", authH.UserPasskeyRegisterBegin)
					r.Post("/passkey/user/register/finish", authH.UserPasskeyRegisterFinish)
					r.Get("/passkey/user/credentials", authH.UserPasskeyList)
					r.Patch("/passkey/user/credentials/{id}", authH.UserPasskeyRename)
					r.Post("/passkey/user/credentials/{id}/primary", authH.UserPasskeySetPrimary)
					r.Delete("/passkey/user/credentials/{id}", authH.UserPasskeyDelete)
				})
			})

			// ------ Protected routes (all require auth) ------
			r.Group(func(r chi.Router) {
				r.Use(mw.Authenticate(cfg.SupabaseJWTSecret, cfg.SupabaseURL, pool))

				// Users (self)
				r.Get("/users/me", userH.GetMe)
				r.Patch("/users/me", userH.UpdateMe)
				r.Delete("/users/me", userH.DeleteMe)
				r.Get("/users/me/stats", userH.GetMyStats)
				r.Get("/users/me/badges", userH.GetMyBadges)
				r.Get("/users/me/attempts", userH.GetMyAttempts)
				r.Get("/users/me/points", pointsH.GetBalance)
				r.Get("/users/me/points/ledger", pointsH.GetLedger)
				r.Get("/users/me/streak", streakH.GetStreak)
				r.Get("/users/me/rank", userH.GetMyRank)
				r.Get("/users/me/profile-views", userH.GetMyProfileViews)
				r.Get("/users/me/milestones", userH.GetMyMilestones)
				r.Get("/users/me/education", userH.GetMyEducation)
				r.Post("/users/me/education", userH.AddMyEducation)
				r.Delete("/users/me/education/{id}", userH.DeleteMyEducation)
				r.Get("/users/me/notifications", notifH.List)
				r.Get("/users/me/notifications/unread-count", notifH.UnreadCount)
				r.Patch("/users/me/notifications/read-all", notifH.MarkAllRead)
				r.Patch("/users/me/notifications/{id}/read", notifH.MarkRead)
				r.Post("/users/me/devices", pushH.Register)
				r.Delete("/users/me/devices/{token}", pushH.Unregister)
				r.Get("/users/me/skills", userH.GetMySkills)
				r.Post("/users/me/skills", userH.AddMySkill)
				r.Delete("/users/me/skills/{skill}", userH.DeleteMySkill)

				// The rest of the CV. Education and skills have their own
				// tables already; experience, certifications, achievements and
				// courses share one shape, so they share one endpoint.
				r.Get("/users/me/profile-entries", profileEntryH.List)
				r.Post("/users/me/profile-entries", profileEntryH.Create)
				r.Patch("/users/me/profile-entries/{entryId}", profileEntryH.Update)
				r.Delete("/users/me/profile-entries/{entryId}", profileEntryH.Delete)
				r.Patch("/users/me/domain", userH.UpdateMyDomain)
				r.Get("/users/me/recommendations", userH.GetMyRecommendations)
				r.Get("/users/me/learning-preferences", userH.GetMyLearningPreferences)
				r.Patch("/users/me/learning-preferences", userH.UpdateMyLearningPreferences)
				r.Get("/users/me/report-card", userH.GetMyReportCardPDF)
				r.Get("/users/{userId}/profile", userH.GetPublicProfile)

				// Settings: dark mode (theme) + privacy (private-by-default / recruiter visibility)
				r.Get("/users/me/settings", userH.GetMySettings)
				r.Patch("/users/me/settings", userH.UpdateMySettings)

				// Notification preferences (push alerts opt-in/out)
				r.Get("/users/me/notification-preferences", userH.GetMyNotifPrefs)
				r.Patch("/users/me/notification-preferences", userH.UpdateMyNotifPrefs)

				// Weekly score insights
				r.Get("/users/me/insights/weekly", userH.GetMyWeeklyInsights)
				r.Get("/users/me/insights/breakdown", userH.GetMyInsightsBreakdown)
				r.Get("/users/me/insights/trend", userH.GetMyScoreTrend)

				// Offline mode: prefetch practice pack + sync offline results
				r.Get("/offline/pack", offlineH.GetPack)
				r.Post("/offline/sync", offlineH.Sync)

				// Enrollment: claim a roster row, or join a class directly.
				// A student with neither is institution-less and stays valid.
				r.Post("/students/claim", enrollmentStudentH.Claim)
				r.Post("/students/join-class", enrollmentStudentH.JoinClass)
				r.Get("/users/me/enrollment", enrollmentStudentH.Mine)

				// Social: batchmate follows
				r.Post("/users/{userId}/follow", studyGroupH.Follow)
				r.Delete("/users/{userId}/follow", studyGroupH.Unfollow)
				r.Get("/users/me/following", studyGroupH.Following)
				r.Get("/users/me/followers", studyGroupH.Followers)

				// Study groups (private leagues)
				r.Post("/study-groups", studyGroupH.Create)
				r.Get("/study-groups", studyGroupH.ListMine)
				r.Post("/study-groups/join", studyGroupH.Join)
				r.Get("/study-groups/{groupId}", studyGroupH.Get)
				r.Delete("/study-groups/{groupId}", studyGroupH.Archive)
				r.Post("/study-groups/{groupId}/leave", studyGroupH.Leave)
				r.Get("/study-groups/{groupId}/leaderboard", studyGroupH.Leaderboard)

				// Quiz browser (student / teacher)
				// Must precede /quizzes/{quizId}, otherwise chi treats "featured"
				// as a quiz id.
				r.Get("/quizzes/featured", userH.GetFeaturedQuizzes)
				r.Get("/quizzes", quizH.List)
				r.Get("/quizzes/{quizId}", quizH.Get)
				r.Post("/quizzes/{quizId}/save", quizH.Save)
				r.Delete("/quizzes/{quizId}/save", quizH.Unsave)
				r.Get("/quizzes/{quizId}/share", quizH.Share)
				r.Post("/quizzes/{quizId}/reports", quizH.ReportQuiz)
				r.Post("/quizzes/{quizId}/questions/{questionId}/reports", quizH.ReportQuestion)

				// Attempts. Rate limited per user: these routes mint points, so a
				// script hammering them is the cheapest attack surface here.
				r.Group(func(r chi.Router) {
					r.Use(mw.RateLimitByUser(300, time.Minute))
					r.Post("/quizzes/{quizId}/attempts", attemptH.Start)
					r.Post("/attempts/{attemptId}/answers", attemptH.SubmitAnswer)
					r.With(mw.RateLimit(300, time.Minute)).Post("/attempts/{attemptId}/behavior", attemptH.RecordBehavior)
					r.Post("/attempts/{attemptId}/questions/{questionId}/clue", attemptH.RevealClue)
					r.Post("/attempts/{attemptId}/complete", attemptH.Complete)
				})
				r.Get("/attempts/{attemptId}", attemptH.GetResult)

				// Leaderboard
				r.Get("/leaderboard", leaderboardH.Get)

				// Topic requests (student)
				r.Post("/topic-requests", topicH.Create)
				r.Get("/topic-requests/mine", topicH.ListMine)

				// Parent
				r.Post("/parent/link-invite", parentH.GenerateInvite)
				r.Post("/parent/link", parentH.Link)
				r.Post("/parent/link/{linkId}/accept", parentH.Accept)
				r.Delete("/parent/link/{linkId}", parentH.Revoke)
				r.Get("/parent/children", parentH.ListChildren)
				r.Get("/parent/children/{studentId}/overview", parentH.ChildOverview)

				// ---- Teacher routes ----
				r.Route("/teacher", func(r chi.Router) {
					r.Use(mw.RequireRole("teacher"))
					r.Get("/overview", teacherH.Overview)
					r.Get("/quizzes/taxonomy", quizH.GetTaxonomy)
					r.Get("/quizzes", quizH.TeacherList)
					r.Post("/quizzes", quizH.TeacherCreate)
					r.Patch("/quizzes/{quizId}", quizH.TeacherUpdate)
					r.Delete("/quizzes/{quizId}", quizH.TeacherDelete)
					r.Post("/quizzes/{quizId}/publish", quizH.TeacherPublish)
					r.Post("/quizzes/{quizId}/unpublish", quizH.TeacherUnpublish)
					r.Get("/quizzes/{quizId}/results", quizH.TeacherResults)
					r.Get("/quizzes/{quizId}/questions", quizH.TeacherGetQuestions)
					r.Post("/quizzes/{quizId}/questions", quizH.TeacherAddQuestion)
					r.Patch("/quizzes/{quizId}/questions/order", quizH.TeacherReorderQuestions)
					r.Patch("/quizzes/{quizId}/questions/{questionId}", quizH.TeacherUpdateQuestion)
					r.Delete("/quizzes/{quizId}/questions/{questionId}", quizH.TeacherDeleteQuestion)
					r.Get("/students", teacherH.ListStudents)
					r.Get("/students/{userId}", teacherH.GetStudent)
					r.Get("/classes", teacherH.ListClasses)
					// Roster writes, bounded to classes the teacher is assigned
					// to. Identity fields stay institution-owned.
					r.Post("/classes/{classId}/students", enrollmentTeacherH.AddStudent)
					r.Delete("/classes/{classId}/students/{userId}", enrollmentTeacherH.RemoveStudent)
					// Corrections to institution-owned fields are proposals,
					// not writes; an admin decides them.
					r.Post("/enrollments/{enrollmentId}/edit-requests", editRequestH.Propose)
					r.Get("/edit-requests", editRequestH.ListMine)
					r.Get("/classes/{classId}", teacherH.GetClass)
					r.Get("/reports/quiz-analytics", teacherH.QuizAnalyticsReport)
					r.Get("/reports/student-performance", teacherH.StudentPerformanceReport)
					r.Get("/topic-requests", topicH.TeacherList)
					r.Patch("/topic-requests/{requestId}", topicH.TeacherUpdate)

					// Analytics. `scope` selects classes or quizzes; the id it
					// filters on always comes from the token.
					// /metrics/catalog precedes /metrics so chi does not read
					// "catalog" as a wildcard segment.
					r.Get("/metrics/catalog", teacherMetricsH.Catalog)
					r.Get("/metrics", teacherMetricsH.Metrics)
					r.Get("/distributions", teacherMetricsH.Distributions)
					r.Get("/points-liability", teacherMetricsH.PointsLiability)

					// Dashboard layouts — private to the calling user.
					// /order precedes {layoutId} so chi does not capture
					// "order" as an id.
					r.Get("/dashboard-layouts", userLayoutsH.List)
					r.Post("/dashboard-layouts", userLayoutsH.Create)
					r.Put("/dashboard-layouts/order", userLayoutsH.Reorder)
					r.Patch("/dashboard-layouts/{layoutId}", userLayoutsH.Update)
					r.Delete("/dashboard-layouts/{layoutId}", userLayoutsH.Delete)
				})

				// Upload
				r.Route("/upload", func(r chi.Router) {
					r.Use(mw.RequireRole("teacher", "super_admin", "moderator"))
					r.Post("/image", uploadH.UploadImage)
					r.Post("/presign", uploadH.PresignUpload)
				})

				// ---- Institution Admin routes ----
				r.Route("/institution", func(r chi.Router) {
					r.Use(mw.RequireRole("institution_admin"))
					r.Get("/overview", institutionH.Overview)
					r.Get("/students", institutionH.ListStudents)
					r.Post("/students", enrollmentInstH.CreateStudent)
					// Enrollment-addressed routes stay off /students/... so they
					// cannot collide with the existing /students/{userId}/status.
					r.Patch("/enrollments/{enrollmentId}", enrollmentInstH.UpdateStudent)
					r.Post("/students/import", enrollmentInstH.ImportStudents)
					r.Patch("/enrollments/{enrollmentId}/status", enrollmentInstH.SetStudentStatus)
					r.Post("/enrollments/promote", enrollmentInstH.PromoteStudents)
					// Class-based promotion: pick a class, pick students, pick
					// where they go. Recorded as a batch so it can be undone.
					r.Post("/promotions", enrollmentInstH.CreatePromotion)
					r.Get("/promotions", enrollmentInstH.ListPromotions)
					r.Post("/promotions/{batchId}/revert", enrollmentInstH.RevertPromotion)
					r.Get("/edit-requests", editRequestH.ListForReview)
					r.Patch("/edit-requests/{requestId}", editRequestH.Review)
					r.Get("/students/{userId}", institutionH.GetStudent)
					r.Patch("/students/{userId}/status", institutionH.UpdateStudentStatus)
					r.Get("/teachers", institutionH.ListTeachers)
					r.Get("/teachers/{userId}", institutionH.GetTeacher)
					r.Patch("/teachers/{userId}/status", institutionH.UpdateTeacherStatus)
					r.Delete("/teachers/{userId}", institutionH.RemoveTeacher)
					r.Post("/teachers/invite", institutionH.InviteTeacher)
					r.Get("/groups", institutionH.ListGroups)
					r.Post("/groups", institutionH.CreateGroup)
					r.Get("/groups/{groupId}", institutionH.GetGroup)
					r.Patch("/groups/{groupId}", institutionH.UpdateGroup)
					r.Delete("/groups/{groupId}", institutionH.ArchiveGroup)
					r.Post("/groups/{groupId}/students", institutionH.AddStudentToGroup)
					r.Delete("/groups/{groupId}/students/{userId}", institutionH.RemoveStudentFromGroup)
					r.Post("/groups/{groupId}/teachers", institutionH.AddTeacherToGroup)
					r.Delete("/groups/{groupId}/teachers/{userId}", institutionH.RemoveTeacherFromGroup)
					r.Get("/quizzes", quizH.InstitutionList)
					r.Get("/quizzes/{quizId}", quizH.Get)
					r.Get("/topic-requests", topicH.TeacherList)
					r.Patch("/topic-requests/{requestId}", topicH.InstitutionUpdate)
					r.Get("/reports/student-performance", institutionH.StudentPerformanceReport)
					r.Get("/reports/teacher-activity", institutionH.TeacherActivityReport)
					r.Get("/reports/quiz-analytics", institutionH.QuizAnalyticsReport)
					r.Get("/reports/streak-health", institutionH.StreakHealthReport)
					r.Get("/reports/points-summary", institutionH.PointsSummaryReport)
					r.Get("/quizzes/{quizId}/results", institutionH.QuizResults)
					r.Get("/settings", institutionH.GetSettings)
					r.Patch("/settings", institutionH.UpdateSettings)
					r.Patch("/settings/point-rules", institutionH.UpdatePointRules)
					r.Get("/audit-log", institutionH.AuditLog)
					r.Get("/setup-checklist", institutionH.SetupChecklist)
					r.Post("/referral-code-reset-request", institutionH.RequestReferralCodeReset)

					// Analytics. Scope is pinned to the caller's institution by
					// the resolver — there is no institution_id parameter here.
					// /metrics/catalog precedes /metrics so chi does not read
					// "catalog" as a wildcard segment.
					r.Get("/metrics/catalog", instMetricsH.Catalog)
					r.Get("/metrics", instMetricsH.Metrics)
					r.Get("/distributions", instMetricsH.Distributions)
					r.Get("/points-liability", instMetricsH.PointsLiability)

					// Dashboard layouts — private to the calling user, so no
					// extra role gate. /order precedes {layoutId} so chi does
					// not capture "order" as an id.
					r.Get("/dashboard-layouts", userLayoutsH.List)
					r.Post("/dashboard-layouts", userLayoutsH.Create)
					r.Put("/dashboard-layouts/order", userLayoutsH.Reorder)
					r.Patch("/dashboard-layouts/{layoutId}", userLayoutsH.Update)
					r.Delete("/dashboard-layouts/{layoutId}", userLayoutsH.Delete)
				})

				// ---- Super Admin routes ----
				r.Route("/admin", func(r chi.Router) {
					r.Use(mw.RequireRole("super_admin", "moderator", "support_agent"))

					// Overview (all roles)
					r.Get("/overview", adminH.Overview)
					r.Get("/activity-feed", adminH.ActivityFeed)

					// Analytics (all roles, read-only).
					// /metrics/catalog is registered before /metrics so chi does
					// not read "catalog" as a wildcard segment.
					r.Get("/metrics/catalog", metricsH.Catalog)
					r.Get("/metrics", metricsH.Metrics)
					r.Get("/distributions", metricsH.Distributions)
					r.Get("/points-liability", metricsH.PointsLiability)

					// Dashboard layouts — private to the calling admin, so no
					// extra role gate. /order precedes {layoutId} so chi does not
					// capture "order" as an id.
					r.Get("/dashboard-layouts", layoutsH.List)
					r.Post("/dashboard-layouts", layoutsH.Create)
					r.Put("/dashboard-layouts/order", layoutsH.Reorder)
					r.Patch("/dashboard-layouts/{layoutId}", layoutsH.Update)
					r.Delete("/dashboard-layouts/{layoutId}", layoutsH.Delete)

					// Demo quizzes (super_admin only): author + play analytics
					r.With(mw.RequireRole("super_admin")).Get("/demo/quizzes", demoH.AdminList)
					r.With(mw.RequireRole("super_admin")).Post("/demo/quizzes", demoH.AdminCreate)
					r.With(mw.RequireRole("super_admin")).Delete("/demo/quizzes/{quizId}", demoH.AdminDelete)
					r.With(mw.RequireRole("super_admin")).Get("/demo/quizzes/{quizId}/analytics", demoH.AdminAnalytics)

					// Cross-institution student tooling. Search is read-only;
					// merge and purge are irreversible, so super_admin only.
					r.Get("/students/search", studentAdminH.Search)
					r.With(mw.RequireRole("super_admin")).Post("/students/merge", studentAdminH.Merge)
					r.With(mw.RequireRole("super_admin")).Delete("/students/{userId}/purge", studentAdminH.Purge)

					// Institutions (Super Admin + Moderator read)
					r.Get("/institutions", adminH.ListInstitutions)
					r.Get("/institutions/queue", adminH.InstitutionQueue)
					r.Get("/institutions/{institutionId}", adminH.GetInstitution)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/approve", adminH.ApproveInstitution)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reject", adminH.RejectInstitution)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/suspend", adminH.SuspendInstitution)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reactivate", adminH.ReactivateInstitution)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reset-referral-codes", adminH.ResetReferralCodes)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/resend-credentials", adminH.ResendInstitutionCredentials)
					r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/provision-admin", adminH.ProvisionAdmin)

					// Users
					r.Get("/users", adminH.ListUsers)
					r.Get("/users/{userId}", adminH.GetUser)
					r.Patch("/users/{userId}/suspend", adminH.SuspendUser)
					r.Patch("/users/{userId}/reactivate", adminH.ReactivateUser)
					r.With(mw.RequireRole("super_admin")).Delete("/users/{userId}", adminH.DeleteUser)
					r.With(mw.RequireRole("super_admin")).Post("/users/{userId}/points", adminH.AdjustPoints)
					r.Post("/users/{userId}/impersonate", adminH.Impersonate)
					r.Post("/impersonation/{sessionId}/end", adminH.EndImpersonation)
					r.With(mw.RequireRole("super_admin")).Post("/users/{userId}/reset-password", adminH.ResetPassword)

					// Quizzes moderation
					// /moderation-queue precedes the list so chi does not read
					// "moderation-queue" as a quiz id.
					r.Get("/quizzes/moderation-queue", adminH.ModerationQueue)
					r.Get("/quizzes", adminH.ListQuizzes)
					r.With(mw.RequireRole("super_admin")).Get("/featured-quizzes", userH.GetFeaturedQuizzes)
					r.With(mw.RequireRole("super_admin")).Put("/featured-quizzes", userH.SetFeaturedQuizzes)
					r.Get("/quizzes/taxonomy", quizH.GetTaxonomy)
					// Platform-authored quizzes are always attributed to the Qwish
					// system account. The service owns author, visibility, publish
					// status, scheduling and question-delivery validation.
					r.With(mw.RequireRole("super_admin")).Post("/quizzes", quizH.AdminCreate)
					r.With(mw.RequireRole("super_admin")).Get("/quizzes/{quizId}", quizH.AdminGet)
					r.With(mw.RequireRole("super_admin")).Patch("/quizzes/{quizId}", quizH.AdminUpdate)
					r.With(mw.RequireRole("super_admin", "moderator")).Get("/quizzes/{quizId}/behavior", attemptH.BehaviorSummary)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/quizzes/{quizId}/approve", adminH.ApproveQuiz)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/quizzes/{quizId}/reject", adminH.RejectQuiz)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/quizzes/{quizId}/request-edits", adminH.RequestEdits)
					r.With(mw.RequireRole("super_admin")).Post("/quizzes/{quizId}/unpublish", adminH.UnpublishQuiz)

					// Reports
					r.Get("/reports", adminH.ListReports)
					r.Post("/reports/{reportId}/resolve", adminH.ResolveReport)

					// Point economy (super_admin only)
					r.With(mw.RequireRole("super_admin")).Get("/point-economy", adminH.GetPointEconomy)
					r.With(mw.RequireRole("super_admin")).Patch("/point-economy/{key}", adminH.UpdatePointEconomy)

					// Announcements
					r.Get("/announcements", adminH.ListAnnouncements)
					r.Post("/announcements", adminH.CreateAnnouncement)
					r.With(mw.RequireRole("super_admin", "moderator")).Patch("/announcements/{announcementId}/retract", adminH.RetractAnnouncement)

					// Promos
					r.Get("/promos", adminH.ListPromos)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/promos", adminH.CreatePromo)
					r.With(mw.RequireRole("super_admin", "moderator")).Patch("/promos/{promoId}", adminH.UpdatePromoStatus)
					r.With(mw.RequireRole("super_admin")).Delete("/promos/{promoId}", adminH.DeletePromo)

					// Brands
					r.Get("/brands", adminH.ListBrands)
					r.With(mw.RequireRole("super_admin")).Post("/brands", adminH.CreateBrand)
					r.With(mw.RequireRole("super_admin")).Post("/brands/{brandId}/approve", adminH.ApproveBrand)
					r.With(mw.RequireRole("super_admin")).Post("/brands/{brandId}/suspend", adminH.SuspendBrand)
					r.With(mw.RequireRole("super_admin")).Post("/brands/{brandId}/reactivate", adminH.ReactivateBrand)
					r.Get("/brands/{brandId}/sponsorship-requests", adminH.ListSponsorshipRequests)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/sponsorship-requests/{requestId}/approve", adminH.ApproveSponsorshipRequest)
					r.With(mw.RequireRole("super_admin", "moderator")).Post("/sponsorship-requests/{requestId}/reject", adminH.RejectSponsorshipRequest)

					// Contact form submissions
					r.Get("/contact-submissions", contactH.List)
					r.Post("/contact-submissions/{id}/resolve", contactH.Resolve)
					r.Post("/contact-submissions/{id}/invite", contactH.InviteToApply)

					// Notification log (super_admin only)
					r.With(mw.RequireRole("super_admin")).Get("/notification-log", adminH.ListNotificationLog)

					// Audit log (super_admin only)
					r.With(mw.RequireRole("super_admin")).Get("/audit-log", adminH.AuditLog)

					// Admin account management (super_admin only)
					r.With(mw.RequireRole("super_admin")).Get("/admin-accounts", adminH.ListAdminAccounts)
					r.With(mw.RequireRole("super_admin")).Post("/admin-accounts", adminH.CreateAdminAccount)
					r.With(mw.RequireRole("super_admin")).Patch("/admin-accounts/{adminId}", adminH.UpdateAdminAccount)
					r.With(mw.RequireRole("super_admin")).Delete("/admin-accounts/{adminId}", adminH.DeleteAdminAccount)
					r.With(mw.RequireRole("super_admin")).Post("/admin-accounts/{adminId}/resend", adminH.ResendAdminInvite)
				})
			})
		})
	})

	addr := ":" + cfg.Port
	log.Printf("qwish-backend listening on %s", addr)

	srv := &http.Server{
		Addr:         addr,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Scheduling lives outside this process: the Render cron services in
	// render.yaml POST the /api/v1/internal/cron/* endpoints. The old
	// in-process ticker loop lost any run that a restart or deploy landed on,
	// which silently froze streak resets.

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// buildOriginSet parses a comma-separated ALLOWED_ORIGINS value.
// Returns nil when the value is "*" (wildcard).
func buildOriginSet(raw string) map[string]bool {
	if raw == "" || raw == "*" {
		return nil
	}
	set := map[string]bool{}
	for _, o := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(o); trimmed != "" {
			set[trimmed] = true
		}
	}
	return set
}
