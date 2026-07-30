package main

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/qwish/backend/internal/config"
	"github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/domain/admin"
	"github.com/qwish/backend/internal/domain/attempt"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/domain/avatar"
	"github.com/qwish/backend/internal/domain/contact"
	"github.com/qwish/backend/internal/domain/demo"
	"github.com/qwish/backend/internal/domain/institution"
	"github.com/qwish/backend/internal/domain/leaderboard"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/offline"
	"github.com/qwish/backend/internal/domain/onboarding"
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
	attemptSvc := attempt.NewService(pool, quizSvc, streakSvc)
	pushSvc := push.NewService(pool, cfg.FCMProjectID, cfg.FCMCredentialsJSON)
	notifSvc := notification.NewService(pool, cfg.ResendAPIKey, cfg.InstituteURL, cfg.SuperAdminURL)
	notifSvc.SetPusher(func(ctx context.Context, userID, title, body string, data map[string]string) {
		pushSvc.SendToUser(ctx, userID, push.Payload{Title: title, Body: body, Data: data})
	})
	attemptSvc.SetNotifier(notifSvc)
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
	attemptH := attempt.NewHandler(attemptSvc)
	pointsH := points.NewHandler(pool)
	streakH := streak.NewHandler(streakSvc)
	leaderboardH := leaderboard.NewHandler(pool)
	parentH := parent.NewHandler(pool)
	topicH := topicrequest.NewHandler(pool)
	uploadH := upload.NewHandler(r2Client)
	institutionH := institution.NewHandler(pool, notifSvc, cfg.AppURL, cfg.TeacherURL)
	teacherH := teacher.NewHandler(pool)
	adminH := admin.NewHandler(pool, cfg, notifSvc)
	onboardingH := onboarding.NewHandler(pool, cfg.TurnstileSecret)
	contactH := contact.NewHandler(pool, notifSvc, cfg.BrandURL, cfg.TurnstileSecret)
	notifH := notification.NewHandler(notifSvc)
	pushH := push.NewHandler(pool)
	offlineH := offline.NewHandler(offlineSvc)
	studyGroupH := studygroup.NewHandler(studyGroupSvc)

	_ = notifSvc
	_ = scoring.LoadConfig // referenced by services

	// Router
	r := chi.NewRouter()
	r.Use(chimw.Logger)
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
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
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

		// Normal endpoints subject to 30s timeout
		r.Group(func(r chi.Router) {
			r.Use(chimw.Timeout(30 * time.Second))

			// ------ Public Institution Onboarding ------
			r.Route("/onboarding", func(r chi.Router) {
				// Public + unauthenticated registration — rate-limit per IP.
				r.With(mw.RateLimit(5, 10*time.Minute)).Post("/institution", onboardingH.RegisterInstitution)
				r.Get("/institution/status", onboardingH.CheckStatus)
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
				r.Patch("/users/me/domain", userH.UpdateMyDomain)
				r.Get("/users/me/recommendations", userH.GetMyRecommendations)
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
					r.Get("/classes/{classId}", teacherH.GetClass)
					r.Get("/reports/quiz-analytics", teacherH.QuizAnalyticsReport)
					r.Get("/reports/student-performance", teacherH.StudentPerformanceReport)
					r.Get("/topic-requests", topicH.TeacherList)
					r.Patch("/topic-requests/{requestId}", topicH.TeacherUpdate)
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
					r.Get("/quizzes", quizH.List)
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
				})

				// ---- Super Admin routes ----
				r.Route("/admin", func(r chi.Router) {
					r.Use(mw.RequireRole("super_admin", "moderator", "support_agent"))

					// Overview (all roles)
					r.Get("/overview", adminH.Overview)
					r.Get("/activity-feed", adminH.ActivityFeed)

					// Demo quizzes (super_admin only): author + play analytics
					r.With(mw.RequireRole("super_admin")).Get("/demo/quizzes", demoH.AdminList)
					r.With(mw.RequireRole("super_admin")).Post("/demo/quizzes", demoH.AdminCreate)
					r.With(mw.RequireRole("super_admin")).Delete("/demo/quizzes/{quizId}", demoH.AdminDelete)
					r.With(mw.RequireRole("super_admin")).Get("/demo/quizzes/{quizId}/analytics", demoH.AdminAnalytics)

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
					r.Get("/quizzes/moderation-queue", adminH.ModerationQueue)
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

			// ---- Internal cron endpoints (development/manual trigger only) ----
			if cfg.AppEnv != "production" {
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

	// Optionally run in-process cron on Render (no separate worker)
	if cfg.AppEnv == "production" {
		go runInProcessCron(pool, sched)
	}

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

// runInProcessCron runs scheduled jobs using Go tickers (alternative to external cron triggers).
// It acquires a PostgreSQL advisory lock so only one instance runs cron when scaled horizontally.
func runInProcessCron(pool *pgxpool.Pool, sched *scheduler.Scheduler) {
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		log.Printf("[cron] failed to acquire db connection: %v", err)
		return
	}
	// Advisory lock key — unique fixed integer for this service's cron scheduler.
	// The lock is held for the lifetime of conn (i.e. the process).
	const lockKey = 7654321
	var acquired bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", lockKey).Scan(&acquired); err != nil {
		log.Printf("[cron] advisory lock query failed: %v", err)
		conn.Release()
		return
	}
	if !acquired {
		log.Println("[cron] another instance holds the scheduler lock — skipping in-process cron")
		conn.Release()
		return
	}
	// conn intentionally not released: the advisory lock is tied to this session.
	log.Println("[cron] in-process scheduler started")

	// Close expired quizzes every hour
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			sched.CloseExpiredQuizzes(context.Background())
		}
	}()

	// Reset streaks daily at 00:05 UTC
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 5, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.ResetStreaks(context.Background())
		}
	}()

	// Expire points nightly at midnight UTC
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.ExpirePoints(context.Background())
		}
	}()

	// Recompute derived question difficulty nightly at 00:20 UTC
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 20, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.RecomputeQuestionDifficulty(context.Background())
		}
	}()

	// Snapshot leaderboard every Monday 00:01 UTC
	go func() {
		for {
			now := time.Now().UTC()
			daysUntilMonday := (8 - int(now.Weekday())) % 7
			if daysUntilMonday == 0 {
				daysUntilMonday = 7
			}
			next := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 0, 1, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.SnapshotLeaderboard(context.Background())
		}
	}()

	// Rank-change alerts daily at 00:10 UTC (after streaks/points settle)
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 10, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.SendRankChangeAlerts(context.Background())
		}
	}()

	// Streak nudges daily at 14:00 UTC (evening across IST users)
	go func() {
		for {
			now := time.Now().UTC()
			next := time.Date(now.Year(), now.Month(), now.Day(), 14, 0, 0, 0, time.UTC)
			if !next.After(now) {
				next = next.AddDate(0, 0, 1)
			}
			time.Sleep(time.Until(next))
			sched.SendStreakNudges(context.Background())
		}
	}()

	// Weekly digest push + insights email every Monday 08:00 UTC
	go func() {
		for {
			now := time.Now().UTC()
			daysUntilMonday := (8 - int(now.Weekday())) % 7
			if daysUntilMonday == 0 {
				daysUntilMonday = 7
			}
			next := time.Date(now.Year(), now.Month(), now.Day()+daysUntilMonday, 8, 0, 0, 0, time.UTC)
			time.Sleep(time.Until(next))
			sched.SendWeeklyDigests(context.Background())
			sched.SendWeeklyInsightsEmail(context.Background())
		}
	}()
}
