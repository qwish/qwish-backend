package cron

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRunnerPreservesChainsAndSecret(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is required for the deployment runner")
	}
	for group, jobs := range map[string][]string{
		"hourly":          {"close-expired-quizzes", "abandon-stale-attempts", "assignment-reminders", "teacher-notifications"},
		"nightly":         {"expire-points", "reset-streaks", "rank-change-alerts", "recompute-question-difficulty", "purge-onboarding-sessions", "purge-attempt-behavior", "end-inactive-enrollments"},
		"streak-nudges":   {"streak-nudges"},
		"announcements":   {"dispatch-announcements"},
		"weekly-snapshot": {"snapshot-leaderboard"},
		"weekly-digests":  {"weekly-digests", "weekly-insights-email"},
	} {
		t.Run(group, func(t *testing.T) {
			var mu sync.Mutex
			var calls []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/ready" {
					if r.Method != http.MethodGet {
						t.Error("readiness must be GET")
					}
					return
				}
				if r.Method != http.MethodPost || r.Header.Get("X-Cron-Secret") != "test-runner-secret" {
					t.Error("missing cron method or secret")
				}
				mu.Lock()
				calls = append(calls, strings.TrimPrefix(r.URL.Path, "/api/v1/internal/cron/"))
				mu.Unlock()
			}))
			defer server.Close()
			cmd := exec.Command("bash", "run-cron.sh", group)
			cmd.Env = append(os.Environ(), "CRON_SECRET=test-runner-secret", "CRON_API_URL="+server.URL)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("runner: %v: %s", err, out)
			}
			if strings.Contains(string(out), "test-runner-secret") {
				t.Fatal("runner logged the secret")
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(calls, jobs) {
				t.Fatalf("got %v, want %v", calls, jobs)
			}
		})
	}
}

func TestRunnerStopsOnFailedPostWithoutRetry(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl is required for the deployment runner")
	}
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			return
		}
		job := strings.TrimPrefix(r.URL.Path, "/api/v1/internal/cron/")
		mu.Lock()
		calls = append(calls, job)
		mu.Unlock()
		if job == "abandon-stale-attempts" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	cmd := exec.Command("bash", "run-cron.sh", "hourly")
	cmd.Env = append(os.Environ(), "CRON_SECRET=test", "CRON_API_URL="+server.URL)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("runner succeeded after a failed POST: %s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(calls, []string{"close-expired-quizzes", "abandon-stale-attempts"}) {
		t.Fatalf("runner retried or continued after failure: %v", calls)
	}
}
