package onboardingsession

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
)

// The sampler is the first-run quiz: the Topic & Sub-Topic Discovery quiz
// seeded in migration 106. It has no right answers. Q1-Q5 score the 12
// topics; the top two become the user's tracks. Q6-Q10 pick sub-topics, which
// are what the session stores as interests and recommendations rank by.
//
// A sampler session never stores a quiz_id, so signup applies the inferred
// interests but has no single quiz to replay into an attempt.

// ErrNoSampler means the discovery quiz is missing or deleted.
var ErrNoSampler = errors.New("no sampler questions available")

// discoveryQuizID is the quiz seeded by migration 106.
const discoveryQuizID = "b0a7d1e5-0000-4000-8000-000000000106"

const (
	// macroQuestions is how many leading questions score topics.
	macroQuestions = 5
	// topTopics is how many topics become the user's tracks.
	topTopics = 2
	// flatTopics topics within flatMargin of the top score make the result
	// too flat to call; the user is routed to the mixed sub-topics instead.
	flatTopics = 4
	flatMargin = 1
	// samplerRecommendLimit caps the list on the result screen.
	samplerRecommendLimit = 5
)

type points struct {
	Domain string
	N      int
}

type tag struct{ Domain, Sub string }

type choice struct {
	Points []points
	Tags   []tag
}

// profileQuiz is the scoring sheet, [question position-1][option position].
// It must match the option order seeded in migration 106.
var profileQuiz = [][]choice{
	{ // 1. main goal
		{Points: []points{{"aptitude", 2}, {"quantitative", 2}, {"logical", 2}}},
		{Points: []points{{"computer_science", 3}, {"science", 1}}},
		{Points: []points{{"corporate", 3}, {"verbal", 1}}},
		{Points: []points{{"social_studies", 2}, {"current_affairs", 2}, {"science", 2}}},
		{Points: []points{{"entertainment", 3}, {"language_learning", 2}, {"general", 1}}},
	},
	{ // 2. satisfying challenge
		{Points: []points{{"quantitative", 3}, {"aptitude", 2}}, Tags: []tag{{"aptitude", "apt_di"}}},
		{Points: []points{{"logical", 3}, {"aptitude", 1}}, Tags: []tag{{"aptitude", "apt_analytical"}}},
		{Points: []points{{"computer_science", 3}}},
		{Points: []points{{"verbal", 2}, {"language_learning", 2}}},
		{Points: []points{{"corporate", 3}}},
	},
	{ // 3. stop scrolling
		{Points: []points{{"current_affairs", 3}, {"social_studies", 1}}, Tags: []tag{{"social_studies", "social_economics"}}},
		{Points: []points{{"science", 3}}},
		{Points: []points{{"entertainment", 3}}},
		{Points: []points{{"social_studies", 3}}},
		{Points: []points{{"general", 3}, {"entertainment", 1}}, Tags: []tag{{"general", "general_mixed"}, {"entertainment", "ent_general"}}},
	},
	{ // 4. team role
		{Points: []points{{"aptitude", 2}, {"quantitative", 2}, {"logical", 2}}},
		{Points: []points{{"computer_science", 3}}},
		{Points: []points{{"verbal", 2}, {"language_learning", 2}, {"corporate", 1}}, Tags: []tag{{"corporate", "corp_sales"}}},
		{Points: []points{{"corporate", 3}}},
		{Points: []points{{"general", 2}, {"entertainment", 1}, {"science", 1}, {"social_studies", 1}, {"current_affairs", 1}}},
	},
	{ // 5. workout structure
		{Points: []points{{"quantitative", 2}, {"logical", 2}, {"aptitude", 2}}},
		{Points: []points{{"computer_science", 2}, {"corporate", 2}}},
		{Points: []points{{"verbal", 2}, {"language_learning", 2}}},
		{Points: []points{{"social_studies", 2}, {"current_affairs", 2}, {"science", 2}}},
		{Points: []points{{"entertainment", 3}, {"general", 2}}},
	},
	{ // 6. numbers, logic, data
		{Tags: []tag{{"quantitative", "quant_arithmetic"}, {"quantitative", "quant_percentages"}, {"quantitative", "quant_tsw"}, {"quantitative", "quant_number_system"}}},
		{Tags: []tag{{"quantitative", "quant_algebra"}, {"quantitative", "quant_geometry"}, {"quantitative", "quant_probability"}}},
		{Tags: []tag{{"aptitude", "apt_di"}, {"aptitude", "apt_analytical"}, {"aptitude", "apt_mixed"}}},
		{Tags: []tag{{"logical", "logical_seating"}, {"logical", "logical_puzzles"}, {"logical", "logical_syllogisms"}, {"logical", "logical_blood_relations"}}},
		{Tags: []tag{{"logical", "logical_series"}, {"logical", "logical_coding_decoding"}}},
	},
	{ // 7. technology and science
		{Tags: []tag{{"computer_science", "cs_dsa"}, {"computer_science", "cs_oop"}, {"computer_science", "cs_programming"}}},
		{Tags: []tag{{"computer_science", "cs_dbms"}, {"computer_science", "cs_os"}, {"computer_science", "cs_networks"}}},
		{Tags: []tag{{"science", "science_physics"}, {"science", "science_chemistry"}, {"science", "science_space"}}},
		{Tags: []tag{{"science", "science_biology"}, {"science", "science_environment"}}},
		{}, // skip
	},
	{ // 8. language and communication
		{Tags: []tag{{"verbal", "verbal_grammar"}, {"verbal", "verbal_vocabulary"}, {"verbal", "verbal_sentence_correction"}}},
		{Tags: []tag{{"verbal", "verbal_rc"}, {"verbal", "verbal_para_jumbles"}}},
		{Tags: []tag{{"language_learning", "lang_english"}}},
		{Tags: []tag{{"language_learning", "lang_hindi"}}},
		{Tags: []tag{{"language_learning", "lang_marathi"}}},
	},
	{ // 9. professional world and global affairs
		{Tags: []tag{{"corporate", "corp_onboarding"}, {"corporate", "corp_policies"}, {"corporate", "corp_hr"}, {"corporate", "corp_safety"}}},
		{Tags: []tag{{"corporate", "corp_security"}, {"corporate", "corp_compliance"}}},
		{Tags: []tag{{"corporate", "corp_leadership"}, {"corporate", "corp_sales"}, {"corporate", "corp_customer_service"}, {"corporate", "corp_product"}}},
		{Tags: []tag{{"current_affairs", "current_india"}, {"current_affairs", "current_world"}, {"current_affairs", "current_business_tech"}, {"current_affairs", "current_sports"}}},
		{Tags: []tag{{"social_studies", "social_history"}, {"social_studies", "social_geography"}, {"social_studies", "social_civics"}, {"social_studies", "social_economics"}}},
	},
	{ // 10. fun vibe: the wildcard, always kept
		{Tags: []tag{{"entertainment", "ent_movies"}, {"entertainment", "ent_tv"}, {"entertainment", "ent_music"}}},
		{Tags: []tag{{"entertainment", "ent_anime"}, {"entertainment", "ent_games"}, {"entertainment", "ent_comics"}, {"entertainment", "ent_scifi_fantasy"}}},
		{Tags: []tag{{"entertainment", "ent_novels"}, {"entertainment", "ent_mythology"}}},
		{Tags: []tag{{"entertainment", "ent_general"}, {"general", "general_mixed"}, {"aptitude", "apt_mixed"}}},
	},
}

// profile turns the option picked per question (-1 for none) into the top
// topics and the sub-topics to recommend from, best first:
//   - primary: any tag in a top topic;
//   - secondary: a Q6-Q9 tag in another topic that scored at all;
//   - wildcard: every Q10 tag.
//
// A flat score (flatTopics topics within flatMargin of the top) routes to
// General and Aptitude, Mixed.
func profile(picks []int) (top, subs []string) {
	score := map[string]int{}
	var ranked []string // topics in the order they first scored
	for q := 0; q < macroQuestions && q < len(picks); q++ {
		c, ok := choiceAt(q, picks[q])
		if !ok {
			continue
		}
		for _, p := range c.Points {
			if score[p.Domain] == 0 {
				ranked = append(ranked, p.Domain)
			}
			score[p.Domain] += p.N
		}
	}
	sort.SliceStable(ranked, func(i, j int) bool { return score[ranked[i]] > score[ranked[j]] })
	near := 0
	for _, d := range ranked {
		if score[d] >= score[ranked[0]]-flatMargin {
			near++
		}
	}
	if len(ranked) == 0 || near >= flatTopics {
		return []string{"general", "aptitude"}, []string{"general_mixed", "apt_mixed"}
	}
	top = ranked[:min(len(ranked), topTopics)]

	var primary, secondary, wildcard []string
	for q, pick := range picks {
		c, ok := choiceAt(q, pick)
		if !ok {
			continue
		}
		for _, t := range c.Tags {
			switch {
			case slices.Contains(top, t.Domain):
				primary = append(primary, t.Sub)
			case q == len(profileQuiz)-1:
				wildcard = append(wildcard, t.Sub)
			case q >= macroQuestions && score[t.Domain] > 0:
				secondary = append(secondary, t.Sub)
			}
		}
	}
	subs = []string{}
	for _, s := range slices.Concat(primary, secondary, wildcard) {
		if !slices.Contains(subs, s) {
			subs = append(subs, s)
		}
	}
	return top, subs
}

func choiceAt(q, pick int) (choice, bool) {
	if q >= len(profileQuiz) || pick < 0 || pick >= len(profileQuiz[q]) {
		return choice{}, false
	}
	return profileQuiz[q][pick], true
}

type samplerItem struct {
	QuestionID string `json:"question_id"`
	QuizID     string `json:"quiz_id"`
	Domain     string `json:"domain,omitempty"`
}

// SamplerQuestion is one sampler question without its correct answer.
type SamplerQuestion struct {
	ID               string          `json:"id"`
	QuizID           string          `json:"quiz_id"`
	Position         int             `json:"position"`
	Type             string          `json:"type"`
	Prompt           string          `json:"prompt"`
	MediaURL         *string         `json:"media_url,omitempty"`
	Options          json.RawMessage `json:"options"`
	TimeLimitSeconds int             `json:"time_limit_seconds"`
}

// Interest is a top topic, strongest first.
type Interest struct {
	Slug  string `json:"slug"`
	Label string `json:"label"`
}

// SamplerResult reports how many questions were answered, not how many were
// right; Profile tells the client to word it that way.
type SamplerResult struct {
	SubmitResult
	Profile         bool          `json:"profile"`
	Topics          []struct{}    `json:"topics"`
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
// Later calls return the same set.
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

// drawSampler takes every question of the discovery quiz, in order, and
// stores the set. The first writer wins, so two concurrent first calls still
// hand out one set.
func (s *Service) drawSampler(ctx context.Context, sessionID string) ([]samplerItem, error) {
	rows, err := s.db.Query(ctx,
		`SELECT qn.id, qn.quiz_id FROM questions qn
		   JOIN quizzes q ON q.id = qn.quiz_id
		  WHERE q.id = $1 AND q.deleted_at IS NULL
		  ORDER BY qn.position`, discoveryQuizID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []samplerItem
	for rows.Next() {
		var it samplerItem
		if err := rows.Scan(&it.QuestionID, &it.QuizID); err != nil {
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

// samplerQuestions loads the drawn questions in drawn order, with their seeded
// positions, which the scoring sheet is keyed by.
func (s *Service) samplerQuestions(ctx context.Context, items []samplerItem) ([]SamplerQuestion, error) {
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.QuestionID
	}
	rows, err := s.db.Query(ctx,
		`SELECT qn.id, qn.quiz_id, qn.position, qn.type, qn.prompt, qn.media_url, qn.options,
		        qn.time_limit_seconds
		   FROM questions qn
		   JOIN quizzes q ON q.id = qn.quiz_id
		  WHERE qn.id = ANY($1::uuid[]) AND q.deleted_at IS NULL`,
		ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := map[string]SamplerQuestion{}
	for rows.Next() {
		var q SamplerQuestion
		if err := rows.Scan(&q.ID, &q.QuizID, &q.Position, &q.Type, &q.Prompt, &q.MediaURL, &q.Options,
			&q.TimeLimitSeconds); err != nil {
			return nil, err
		}
		byID[q.ID] = q
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SamplerQuestion, 0, len(items))
	for _, it := range items {
		if q, ok := byID[it.QuestionID]; ok {
			out = append(out, q)
		}
	}
	if len(out) == 0 {
		return nil, ErrNoSampler
	}
	return out, nil
}

// SubmitSampler profiles the answers, stores them on the session (answers in
// responses, sub-topics in topics, where signup picks them up), and returns
// the top topics with recommendations. Single-use, like Submit.
func (s *Service) SubmitSampler(ctx context.Context, sessionID string, answers []Answer) (*SamplerResult, error) {
	items, err := s.storedSampler(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if items == nil {
		return nil, ErrNoSampler
	}
	questions, err := s.samplerQuestions(ctx, items)
	if err != nil {
		return nil, err
	}

	given := make(map[string]json.RawMessage, len(answers))
	for _, a := range answers {
		given[a.QuestionID] = a.Answer
	}
	picks := make([]int, len(profileQuiz))
	for i := range picks {
		picks[i] = -1
	}
	answered := 0
	for _, q := range questions {
		if i := optionIndex(q.Options, given[q.ID]); i >= 0 && q.Position >= 1 && q.Position <= len(picks) {
			picks[q.Position-1] = i
			answered++
		}
	}
	top, subs := profile(picks)

	result := &SamplerResult{
		SubmitResult: SubmitResult{
			TotalCorrect:   answered,
			TotalQuestions: len(questions),
			Review:         []ReviewItem{},
		},
		Profile: true,
		Topics:  []struct{}{},
	}
	if result.Interests, err = s.topicLabels(ctx, top); err != nil {
		return nil, err
	}

	stored, err := json.Marshal(answers)
	if err != nil {
		return nil, err
	}
	ct, err := s.db.Exec(ctx,
		`UPDATE onboarding_sessions
		    SET topics=$2, responses=$3, submitted_at=now()
		  WHERE id=$1 AND claimed_by IS NULL AND submitted_at IS NULL AND expires_at > now()`,
		sessionID, subs, stored)
	if err != nil {
		return nil, err
	}
	if ct.RowsAffected() == 0 {
		return nil, ErrAlreadySubmitted
	}

	if result.Recommendations, err = s.recommendForInterests(ctx, subs); err != nil {
		return nil, err
	}
	return result, nil
}

// optionIndex is the position of answer among options, or -1 when it is not
// one of them.
func optionIndex(options, answer json.RawMessage) int {
	var opts []string
	var a string
	if json.Unmarshal(options, &opts) != nil || json.Unmarshal(answer, &a) != nil {
		return -1
	}
	return slices.Index(opts, a)
}

// topicLabels names the topics, in the given order.
func (s *Service) topicLabels(ctx context.Context, slugs []string) ([]Interest, error) {
	rows, err := s.db.Query(ctx,
		`SELECT slug, label FROM domains WHERE slug = ANY($1::text[])
		  ORDER BY array_position($1::text[], slug)`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Interest{}
	for rows.Next() {
		var i Interest
		if err := rows.Scan(&i.Slug, &i.Label); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// recommendForInterests lists quizzes to play after signup. Quizzes in a
// picked sub-topic come first, the most-played of each sub-topic in pick
// order, then the second of each, so one sub-topic does not crowd out the
// rest. Quizzes elsewhere in those sub-topics' topics fill any gap. Question
// type is not filtered: these are played in the full player once the account
// exists.
func (s *Service) recommendForInterests(ctx context.Context, subs []string) ([]QuizSummary, error) {
	rows, err := s.db.Query(ctx,
		`WITH ranked AS (
		   SELECT q.id, q.title, q.description, q.domain, q.question_count,
		          COALESCE(q.subdomain = ANY($1::text[]), false) AS exact,
		          array_position($1::text[], q.subdomain) AS interest_rank,
		          COALESCE(st.completions, 0) AS completions, q.published_at,
		          ROW_NUMBER() OVER (
		            PARTITION BY COALESCE(q.subdomain, q.domain)
		            ORDER BY COALESCE(st.completions, 0) DESC, q.published_at DESC NULLS LAST, q.id
		          ) AS bucket_rank
		     FROM quizzes q
		     LEFT JOIN quiz_read_stats st ON st.quiz_id = q.id
		    WHERE `+eligibleQuiz+`
		      AND q.question_count > 0
		      AND (q.subdomain = ANY($1::text[])
		           OR q.domain IN (SELECT domain_slug FROM subdomains WHERE slug = ANY($1::text[])))
		 )
		 SELECT id, title, description, domain, question_count FROM ranked
		  ORDER BY exact DESC, bucket_rank, interest_rank NULLS LAST, completions DESC,
		           published_at DESC NULLS LAST, id
		  LIMIT $2`, subs, samplerRecommendLimit)
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
