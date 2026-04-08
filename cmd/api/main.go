package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"

	"github.com/qwish/backend/internal/config"
	"github.com/qwish/backend/internal/db"
	"github.com/qwish/backend/internal/domain/admin"
	"github.com/qwish/backend/internal/domain/attempt"
	"github.com/qwish/backend/internal/domain/auth"
	"github.com/qwish/backend/internal/domain/institution"
	"github.com/qwish/backend/internal/domain/leaderboard"
	"github.com/qwish/backend/internal/domain/notification"
	"github.com/qwish/backend/internal/domain/parent"
	"github.com/qwish/backend/internal/domain/points"
	"github.com/qwish/backend/internal/domain/quiz"
	"github.com/qwish/backend/internal/domain/scoring"
	"github.com/qwish/backend/internal/domain/streak"
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

	// Services
	authSvc := auth.NewService(pool, cfg)
	userSvc := user.NewService(pool)
	quizSvc := quiz.NewService(pool)
	streakSvc := streak.NewService(pool)
	attemptSvc := attempt.NewService(pool, quizSvc, streakSvc)
	notifSvc := notification.NewService(cfg.ResendAPIKey)
	r2Client := storage.NewR2Client(cfg)
	sched := scheduler.New(pool, streakSvc)

	// Handlers
	authH := auth.NewHandler(authSvc)
	userH := user.NewHandler(userSvc)
	quizH := quiz.NewHandler(quizSvc)
	attemptH := attempt.NewHandler(attemptSvc)
	pointsH := points.NewHandler(pool)
	streakH := streak.NewHandler(streakSvc)
	leaderboardH := leaderboard.NewHandler(pool)
	parentH := parent.NewHandler(pool)
	topicH := topicrequest.NewHandler(pool)
	uploadH := upload.NewHandler(r2Client)
	institutionH := institution.NewHandler(pool)
	adminH := admin.NewHandler(pool)

	_ = notifSvc
	_ = scoring.LoadConfig // referenced by services

	// Router
	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(chimw.Timeout(30 * time.Second))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
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

		// ------ AUTH (public) ------
		r.Route("/auth", func(r chi.Router) {
			r.Post("/signup", authH.Signup)
			r.Post("/login", authH.Login)
			r.Post("/refresh", authH.Refresh)
			r.Post("/forgot-password", authH.ForgotPassword)

			// Protected auth routes
			r.Group(func(r chi.Router) {
				r.Use(mw.Authenticate(cfg.SupabaseJWTSecret, pool))
				r.Post("/logout", authH.Logout)
				r.Patch("/referral-code", authH.UpdateReferralCode)
			})
		})

		// ------ Protected routes (all require auth) ------
		r.Group(func(r chi.Router) {
			r.Use(mw.Authenticate(cfg.SupabaseJWTSecret, pool))

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
			r.Get("/users/{userId}/profile", userH.GetPublicProfile)

			// Quiz browser (student / teacher)
			r.Get("/quizzes", quizH.List)
			r.Get("/quizzes/{quizId}", quizH.Get)
			r.Post("/quizzes/{quizId}/save", quizH.Save)
			r.Delete("/quizzes/{quizId}/save", quizH.Unsave)
			r.Get("/quizzes/{quizId}/share", quizH.Share)
			r.Post("/quizzes/{quizId}/reports", quizH.ReportQuiz)
			r.Post("/quizzes/{quizId}/questions/{questionId}/reports", quizH.ReportQuestion)

			// Attempts
			r.Post("/quizzes/{quizId}/attempts", attemptH.Start)
			r.Post("/attempts/{attemptId}/answers", attemptH.SubmitAnswer)
			r.Post("/attempts/{attemptId}/complete", attemptH.Complete)
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
				r.Get("/quizzes", quizH.TeacherList)
				r.Post("/quizzes", quizH.TeacherCreate)
				r.Patch("/quizzes/{quizId}", quizH.TeacherUpdate)
				r.Post("/quizzes/{quizId}/questions", quizH.TeacherAddQuestion)
				r.Patch("/quizzes/{quizId}/questions/{questionId}", quizH.TeacherUpdateQuestion)
				r.Delete("/quizzes/{quizId}/questions/{questionId}", quizH.TeacherDeleteQuestion)
				r.Post("/quizzes/{quizId}/publish", quizH.TeacherPublish)
				r.Get("/quizzes/{quizId}/results", quizH.TeacherResults)
				r.Get("/topic-requests", topicH.TeacherList)
				r.Patch("/topic-requests/{requestId}", topicH.TeacherUpdate)
			})

			// Upload
			r.Route("/upload", func(r chi.Router) {
				r.Use(mw.RequireRole("teacher", "super_admin", "moderator"))
				r.Post("/image", uploadH.UploadImage)
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

				// Institutions (Super Admin + Moderator read)
				r.Get("/institutions", adminH.ListInstitutions)
				r.Get("/institutions/queue", adminH.InstitutionQueue)
				r.Get("/institutions/{institutionId}", adminH.GetInstitution)
				r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/approve", adminH.ApproveInstitution)
				r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reject", adminH.RejectInstitution)
				r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/suspend", adminH.SuspendInstitution)
				r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reactivate", adminH.ReactivateInstitution)
				r.With(mw.RequireRole("super_admin")).Post("/institutions/{institutionId}/reset-referral-codes", adminH.ResetReferralCodes)

				// Users
				r.Get("/users", adminH.ListUsers)
				r.Get("/users/{userId}", adminH.GetUser)
				r.Patch("/users/{userId}/suspend", adminH.SuspendUser)
				r.Patch("/users/{userId}/reactivate", adminH.ReactivateUser)
				r.With(mw.RequireRole("super_admin")).Delete("/users/{userId}", adminH.DeleteUser)
				r.With(mw.RequireRole("super_admin")).Post("/users/{userId}/points", adminH.AdjustPoints)
				r.Post("/users/{userId}/impersonate", adminH.Impersonate)
				r.Post("/impersonation/{sessionId}/end", adminH.EndImpersonation)

				// Quizzes moderation
				r.Get("/quizzes/moderation-queue", adminH.ModerationQueue)
				r.With(mw.RequireRole("super_admin", "moderator")).Post("/quizzes/{quizId}/approve", adminH.ApproveQuiz)
				r.With(mw.RequireRole("super_admin", "moderator")).Post("/quizzes/{quizId}/reject", adminH.RejectQuiz)
				r.With(mw.RequireRole("super_admin")).Post("/quizzes/{quizId}/unpublish", adminH.UnpublishQuiz)

				// Reports
				r.Get("/reports", adminH.ListReports)
				r.Post("/reports/{reportId}/resolve", adminH.ResolveReport)

				// Point economy (super_admin only)
				r.With(mw.RequireRole("super_admin")).Get("/point-economy", adminH.GetPointEconomy)
				r.With(mw.RequireRole("super_admin")).Patch("/point-economy/{key}", adminH.UpdatePointEconomy)

				// Announcements
				r.Post("/announcements", adminH.CreateAnnouncement)

				// Audit log (super_admin only)
				r.With(mw.RequireRole("super_admin")).Get("/audit-log", adminH.AuditLog)

				// Admin account management (super_admin only)
				r.With(mw.RequireRole("super_admin")).Get("/admin-accounts", adminH.ListAdminAccounts)
				r.With(mw.RequireRole("super_admin")).Post("/admin-accounts", adminH.CreateAdminAccount)
				r.With(mw.RequireRole("super_admin")).Patch("/admin-accounts/{adminId}", adminH.UpdateAdminAccount)
				r.With(mw.RequireRole("super_admin")).Delete("/admin-accounts/{adminId}", adminH.DeleteAdminAccount)
			})
		})

		// ---- Internal cron endpoints ----
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
		go runInProcessCron(sched)
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// runInProcessCron runs scheduled jobs using Go tickers (alternative to external cron triggers).
func runInProcessCron(sched *scheduler.Scheduler) {
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
}
