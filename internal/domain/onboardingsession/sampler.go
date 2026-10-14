package onboardingsession

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"sort"

	"github.com/qwish/backend/internal/domain/scoring"
)

// The mixed-topic sampler is the first-run quiz: one question from each of a
// few subjects, so the result says something about what the guest is good at
// rather than how they did on a single quiz. The subjects they answer best
// become their interests, which is what recommendations rank by.
//
// A sampler session never stores a quiz_id, so signup applies the inferred
// interests but has no single quiz to replay into an attempt.

// ErrNoSampler means no subject has a quiz an anonymous user may play.
var ErrNoSampler = errors.New("no sampler questions available")

const (
	// samplerSize is how many subjects one sampler covers, one question each.
	samplerSize = 5
	// maxInterests caps the subjects carried over as interests.
	maxInterests = 3
	// defaultLimitMs stands in for questions with no time limit when speed is
	// weighed.
	defaultLimitMs = 60_000
	// samplerRecommendLimit caps the list on the result screen.
	samplerRecommendLimit = 5
)

type samplerItem struct {
	QuestionID string `json:"question_id"`
	QuizID     string `json:"quiz_id"`
	Domain     string `json:"domain"`
}

// SamplerQuestion is one sampler question without its correct answer, tagged
// with the subject it was drawn from.
type SamplerQuestion struct {
	ID               string          `json:"id"`
	QuizID           string          `json:"quiz_id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
	Domain           string          `json:"domain"`
	DomainLabel      string          `json:"domain_label"`
}

// TopicResult is how the guest did on one subject.
type TopicResult struct {
	Domain    string `json:"domain"`
	Label     string `json:"label"`
	Correct   bool   `json:"correct"`
	ElapsedMs int    `json:"elapsed_ms"`
}

// Interest is a subject inferred from the result, strongest first.
type Interest struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

type SamplerResult struct {
	SubmitResult
	Topics          []TopicResult `json:"topics"`
	Interests       []Interest    `json:"interests"`
	Recommendations []QuizSummary `json:"recommendations"`
}

// eligibleQuiz is the anonymous-play predicate shared with Recommendations and
// assertEligible, over a quizzes row aliased q.
const eligibleQuiz = `q.visibility = 'public'
	AND q.status = 'published'
	AND q.deleted_at IS NULL
	AND (q.starts_at IS NULL OR q.starts_at <= now())
	AND (q.ends_at IS NULL OR q.ends_at > now())`

// Sampler returns the session's sampler questions, drawing them on first call.
// Later calls return the same set so a restart does not reshuffle the quiz.
func (s *Service) Sampler(ctx context.Context, sessionID string) ([]SamplerQuestion, error) {
	items, err := s.storedSampler(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		if items, err = s.drawSampler(ctx, sessionID); err != nil {
			return nil, err
		}
	}
	return s.samplerQuestions(ctx, items)
}

// storedSampler reads the drawn set of a live, unsubmitted session; nil when
// nothing has been drawn yet.
func (s *Service) storedSampler(ctx context.Context, sessionID string) ([]samplerItem, error) {
	var raw []byte
	var submitted bool
	if err := s.db.QueryRow(ctx,
		`SELECT sampler, submitted_at IS NOT NULL FROM onboarding_sessions
		  WHERE id=$1 AND claimed_by IS NULL AND expires_at > now()`,
		sessionID,
	).Scan(&raw, &submitted); err != nil {
		return nil, ErrSession
	}
	if submitted {
		return nil, ErrAlreadySubmitted
	}
	if raw == nil {
		return nil, nil
	}
	var items []samplerItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// drawSampler picks samplerSize subjects at random among those with a playable
// quiz, then one random question from each, and stores the set. The first
// writer wins, so two concurrent first calls still hand out one set.
func (s *Service) drawSampler(ctx context.Context, sessionID string) ([]samplerItem, error) {
	rows, err := s.db.Query(ctx,
		`WITH eligible AS (
		   SELECT q.id, q.domain FROM quizzes q
		    WHERE `+eligibleQuiz+`
		      AND q.question_count > 0
		      AND COALESCE(q.domain, '') <> ''
		      AND NOT EXISTS (
		            SELECT 1 FROM questions qn
		             WHERE qn.quiz_id = q.id AND qn.type <> 'multiple_choice')
		 ), drawn AS (
		   SELECT domain FROM (SELECT DISTINCT domain FROM eligible) d
		    ORDER BY random() LIMIT $1
		 )
		 SELECT DISTINCT ON (e.domain) qn.id, e.id, e.domain
		   FROM eligible e
		   JOIN drawn ON drawn.domain = e.domain
		   JOIN questions qn ON qn.quiz_id = e.id
		  ORDER BY e.domain, random()`, samplerSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []samplerItem
	for rows.Next() {
		var it samplerItem
		if err := rows.Scan(&it.QuestionID, &it.QuizID, &it.Domain); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, ErrNoSampler
	}
	// DISTINCT ON returns subjects alphabetically; play them in random order.
	rand.Shuffle(len(items), func(i, j int) { items[i], items[j] = items[j], items[i] })

	stored, err := json.Marshal(items)
	if err != nil {
		return nil, err
	}
	var raw []byte
	if err := s.db.QueryRow(ctx,
		`UPDATE onboarding_sessions SET sampler = COALESCE(sampler, $2)
		  WHERE id=$1 AND claimed_by IS NULL AND submitted_at IS NULL AND expires_at > now()
		  RETURNING sampler`,
		sessionID, stored,
	).Scan(&raw); err != nil {
		return nil, ErrSession
	}
	var kept []samplerItem
	if err := json.Unmarshal(raw, &kept); err != nil {
		return nil, err
	}
	return kept, nil
}

type samplerRow struct {
	SamplerQuestion
	CorrectAnswer json.RawMessage
}

// samplerRows loads the drawn questions, in drawn order, with their answers.
// A question whose quiz stopped being playable since the draw is dropped.
func (s *Service) samplerRows(ctx context.Context, items []samplerItem) ([]samplerRow, error) {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.QuestionID
	}
	rows, err := s.db.Query(ctx,
		`SELECT qn.id, qn.quiz_id, qn.type, qn.prompt, qn.media_url, qn.options,
		        qn.correct_answer, qn.time_limit_seconds, COALESCE(d.label, q.domain)
		   FROM questions qn
		   JOIN quizzes q ON q.id = qn.quiz_id
		   LEFT JOIN domains d ON d.slug = q.domain
		  WHERE qn.id = ANY($1::uuid[]) AND `+eligibleQuiz,
		ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]samplerRow{}
	for rows.Next() {
		var r samplerRow
		if err := rows.Scan(&r.ID, &r.QuizID, &r.Type, &r.Prompt, &r.MediaURL, &r.Options,
			&r.CorrectAnswer, &r.TimeLimitSeconds, &r.DomainLabel); err != nil {
			return nil, err
		}
		byID[r.ID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]samplerRow, 0, len(items))
	for _, it := range items {
		r, ok := byID[it.QuestionID]
		if !ok {
			continue
		}
		r.Domain = it.Domain
		r.Position = len(out) + 1
		out = append(out, r)
	}
	return out, nil
}

func (s *Service) samplerQuestions(ctx context.Context, items []samplerItem) ([]SamplerQuestion, error) {
	rows, err := s.samplerRows(ctx, items)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNoSampler
	}
	out := make([]SamplerQuestion, len(rows))
	for i, r := range rows {
		out[i] = r.SamplerQuestion
	}
	return out, nil
}

// SubmitSampler grades the sampler, infers interests from it, and stores both
// on the session: answers in responses, interests in topics, where signup
// picks them up. Like Submit it is single-use and returns no score.
func (s *Service) SubmitSampler(ctx context.Context, sessionID string, answers []Answer) (*SamplerResult, error) {
	items, err := s.storedSampler(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return nil, ErrNoSampler
	}
	questions, err := s.samplerRows(ctx, items)
	if err != nil {
		return nil, err
	}
	cfg, err := scoring.LoadConfig(ctx, s.db)
	if err != nil {
		return nil, err
	}

	given := make(map[string]Answer, len(answers))
	for _, a := range answers {
		given[a.QuestionID] = a
	}

	result := &SamplerResult{
		SubmitResult: SubmitResult{
			TotalQuestions: len(questions),
			Review:         make([]ReviewItem, 0, len(questions)),
		},
		Topics:    make([]TopicResult, 0, len(questions)),
		Interests: []Interest{},
	}
	scored := make([]topicScore, 0, len(questions))
	labels := map[string]string{}
	for _, q := range questions {
		a := given[q.ID]
		ok, _ := scoring.ScoreQuestion(scoring.QuestionResponse{
			QuestionType:  q.Type,
			CorrectAnswer: q.CorrectAnswer,
			StudentAnswer: a.Answer,
		}, cfg)
		if ok {
			result.TotalCorrect++
		}
		result.Review = append(result.Review, ReviewItem{
			QuestionID: q.ID, Correct: ok, CorrectAnswer: q.CorrectAnswer,
		})
		result.Topics = append(result.Topics, TopicResult{
			Domain: q.Domain, Label: q.DomainLabel, Correct: ok, ElapsedMs: a.ElapsedMs,
		})
		scored = append(scored, topicScore{
			Domain: q.Domain, Correct: ok, ElapsedMs: a.ElapsedMs,
			LimitMs: q.TimeLimitSeconds * 1000,
		})
		labels[q.Domain] = q.DomainLabel
	}

	interests := inferInterests(scored)
	for _, slug := range interests {
		result.Interests = append(result.Interests, Interest{Slug: slug, Label: labels[slug]})
	}

	stored, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	ct, err := s.db.Exec(ctx,
		`UPDATE onboarding_sessions
		    SET topics=$2, responses=$3, submitted_at=now()
		  WHERE id=$1 AND claimed_by IS NULL AND submitted_at IS NULL AND expires_at > now()`,
		sessionID, interests, stored)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrAlreadySubmitted
	}

	if result.Recommendations, err = s.recommendForInterests(ctx, interests); err != nil {
		return nil, err
	}
	return result, nil
}

type topicScore struct {
	Domain    string
	Correct   bool
	ElapsedMs int
	LimitMs   int
}

// inferInterests ranks the subjects answered correctly, faster answers first,
// and keeps the top maxInterests. A wrong or skipped answer carries no signal:
// a fast wrong answer is more likely a guess than an interest. With nothing
// correct the list is empty and recommendations fall back to popularity.
func inferInterests(scores []topicScore) []string {
	type ranked struct {
		domain string
		weight float64
	}
	var list []ranked
	seen := map[string]bool{}
	for _, sc := range scores {
		if !sc.Correct || seen[sc.Domain] {
			continue
		}
		seen[sc.Domain] = true
		limit := sc.LimitMs
		if limit <= 0 {
			limit = defaultLimitMs
		}
		elapsed := min(max(sc.ElapsedMs, 0), limit)
		list = append(list, ranked{sc.Domain, 2 - float64(elapsed)/float64(limit)})
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].weight > list[j].weight })
	out := []string{}
	for i := 0; i < len(list) && i < maxInterests; i++ {
		out = append(out, list[i].domain)
	}
	return out
}

// recommendForInterests lists quizzes to play after signup: the most-played
// quiz of each interest in strength order, then the second of each, and so on,
// so one strong subject does not crowd out the others. With no interests it is
// the same round-robin across every subject. Question type is not filtered:
// these are played in the full player once the account exists.
func (s *Service) recommendForInterests(ctx context.Context, interests []string) ([]QuizSummary, error) {
	rows, err := s.db.Query(ctx,
		`WITH ranked AS (
		   SELECT q.id, q.title, q.description, q.domain, q.question_count,
		          array_position($1::text[], q.domain) AS interest_rank,
		          COALESCE(st.completions, 0) AS completions, q.published_at,
		          ROW_NUMBER() OVER (
		            PARTITION BY q.domain
		            ORDER BY COALESCE(st.completions, 0) DESC, q.published_at DESC NULLS LAST, q.id
		          ) AS domain_rank
		     FROM quizzes q
		     LEFT JOIN quiz_read_stats st ON st.quiz_id = q.id
		    WHERE `+eligibleQuiz+`
		      AND q.question_count > 0
		      AND (cardinality($1::text[]) = 0 OR q.domain = ANY($1::text[]))
		 )
		 SELECT id, title, description, domain, question_count FROM ranked
		  ORDER BY domain_rank, interest_rank NULLS LAST, completions DESC,
		           published_at DESC NULLS LAST, id
		  LIMIT $2`, interests, samplerRecommendLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuizSummary{}
	for rows.Next() {
		var q QuizSummary
		if err := rows.Scan(&q.ID, &q.Title, &q.Description, &q.Domain, &q.QuestionCount); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
