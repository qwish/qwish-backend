package onboardingsession

import (
	"context"
	"encoding/json"
	"log"

	"github.com/qwish/backend/internal/domain/attempt"
)

// Attempts is the slice of the attempt service the claim path needs. An
// interface rather than the concrete type so the dependency stays one-way and
// testable without a full attempt service.
type Attempts interface {
	Start(ctx context.Context, userID, quizID string) (*attempt.StartAttemptResp, error)
	ReplayAnswer(ctx context.Context, userID, attemptID string, req attempt.AnswerReq, elapsedMs int) (*attempt.AnswerResp, error)
	Complete(ctx context.Context, userID, attemptID string) (*attempt.CompleteResp, error)
}

// SetAttempts wires the attempt engine. Optional: without it a claim still
// applies preferences and skips the replay.
func (s *Service) SetAttempts(a Attempts) { s.attempts = a }

// Claim hands a session's contents to a freshly created user.
//
// Preferences are applied atomically with the claim marker, so a session is
// never half-applied and never applied twice. The quiz replay runs afterwards
// and outside that transaction: it walks the ordinary attempt engine, which
// manages its own transactions per answer, and a failure there must not cost
// the user their preferences or their account.
func (s *Service) Claim(ctx context.Context, sessionID, userID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var language string
	var topics []string
	var quizID *string
	var responses []byte
	err = tx.QueryRow(ctx,
		`SELECT language, topics, quiz_id, responses
		   FROM onboarding_sessions
		  WHERE id=$1 AND claimed_by IS NULL AND expires_at > now()
		  FOR UPDATE`,
		sessionID,
	).Scan(&language, &topics, &quizID, &responses)
	if err != nil {
		return ErrSession
	}

	if _, err := tx.Exec(ctx,
		`UPDATE users SET preferred_language=$2, interest_domains=$3, updated_at=now() WHERE id=$1`,
		userID, language, topics); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE onboarding_sessions SET claimed_by=$2, claimed_at=now() WHERE id=$1`,
		sessionID, userID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if s.attempts != nil && quizID != nil && len(responses) > 0 {
		s.replay(ctx, userID, *quizID, responses)
	}
	return nil
}

// replay turns the stored calibration answers into a real completed attempt,
// so score_pct, points, streak and the ledger all come out of the same engine
// that serves logged-in play. Failures are logged, never surfaced: the user has
// just created an account and must land on home either way.
func (s *Service) replay(ctx context.Context, userID, quizID string, responses []byte) {
	var answers []Answer
	if err := json.Unmarshal(responses, &answers); err != nil {
		log.Printf("onboarding claim: decode responses for user %s: %v", userID, err)
		return
	}

	started, err := s.attempts.Start(ctx, userID, quizID)
	if err != nil {
		log.Printf("onboarding claim: start attempt for user %s: %v", userID, err)
		return
	}
	for _, a := range answers {
		if _, err := s.attempts.ReplayAnswer(ctx, userID, started.AttemptID, attempt.AnswerReq{
			QuestionID: a.QuestionID,
			Answer:     a.Answer,
		}, a.ElapsedMs); err != nil {
			log.Printf("onboarding claim: replay answer %s for user %s: %v", a.QuestionID, userID, err)
		}
	}
	if _, err := s.attempts.Complete(ctx, userID, started.AttemptID); err != nil {
		log.Printf("onboarding claim: complete attempt for user %s: %v", userID, err)
	}
}
