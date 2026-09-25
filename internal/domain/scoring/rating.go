package scoring

import (
	"context"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Skill rating (the Qwish Score).
//
// Each learner has an ability estimate Theta with uncertainty Sigma; each
// question has a difficulty B on the same 100–900 scale. The expected chance
// of a correct answer is a logistic curve in (Theta-B), with a guessing floor
// for option-based questions. After every first-time answer, Theta moves by
// the surprise (correct - expected) scaled by the current uncertainty, and the
// uncertainty shrinks by how informative that answer was (a Glicko-style
// Gaussian update of a Rasch model).
//
// The published score is the conservative estimate Theta - 2*Sigma. It rises
// gradually as evidence accumulates, easy wins barely move it, hard wins move
// it a lot, and streaks, speed and activity have no say — those feed XP.
const (
	RatingStart      = 500.0
	RatingSigmaStart = 150.0
	ratingSigmaFloor = 25.0  // keeps a veteran's rating able to follow real improvement
	ratingScale      = 100.0 // rating points per logit
	ratingIdleTau    = 5.0   // sigma added (in quadrature) per idle day
	ratingMin        = 100.0
	ratingMax        = 900.0
)

// GuessFloorSQL is the chance of a blind guess being right for question alias q.
// Ordering questions have n! arrangements, so their floor is treated as zero.
const GuessFloorSQL = `CASE WHEN q.type='arrange_order' OR jsonb_typeof(q.options)<>'array'
  OR jsonb_array_length(q.options)<2 THEN 0.0 ELSE 1.0/jsonb_array_length(q.options) END`

type Rating struct {
	Theta float64
	Sigma float64
	N     int
}

func NewRating() Rating { return Rating{Theta: RatingStart, Sigma: RatingSigmaStart} }

// Score is the published 100–900 value.
func (r Rating) Score() float64 {
	return math.Max(ratingMin, math.Min(ratingMax, r.Theta-2*r.Sigma))
}

// Age widens the uncertainty after idle days, so stale evidence counts for
// less and the score dips slightly until the learner answers again.
func (r Rating) Age(days float64) Rating {
	if days <= 0 || r.N == 0 {
		return r
	}
	r.Sigma = math.Min(RatingSigmaStart, math.Sqrt(r.Sigma*r.Sigma+ratingIdleTau*ratingIdleTau*days))
	return r
}

// SeedDifficulty maps the 0–1 questions.difficulty (≈ share of learners who
// miss it) onto the rating scale: an average learner (Theta=500) answers a
// question of difficulty d correctly with probability 1-d.
func SeedDifficulty(d float64) float64 {
	d = math.Max(0.05, math.Min(0.95, d))
	return RatingStart + ratingScale*math.Log(d/(1-d))
}

// Update folds one first-time answer into the rating. b is the question's
// difficulty, guess its blind-guess floor, qN how many answers already shaped
// b. It returns the new rating and how far b should move.
func (r Rating) Update(b, guess float64, correct bool, qN int) (Rating, float64) {
	l := 1 / (1 + math.Exp(-(r.Theta-b)/ratingScale))
	p := guess + (1-guess)*l
	p = math.Max(1e-6, math.Min(1-1e-6, p))
	y := 0.0
	if correct {
		y = 1
	}
	slope := (1 - guess) * l * (1 - l) / ratingScale // dp/dTheta
	info := slope * slope / (p * (1 - p))
	grad := (y - p) / (p * (1 - p)) * slope

	v := 1 / (1/(r.Sigma*r.Sigma) + info)
	r.Theta += v * grad
	r.Sigma = math.Max(ratingSigmaFloor, math.Sqrt(v))
	r.N++

	// ponytail: fixed decaying step for questions; a full per-question sigma
	// is only worth it once questions are compared across cohorts.
	k := math.Max(4, 40/math.Sqrt(1+float64(qN)))
	return r, -k * (y - p)
}

// RatingObs is one first-time answer. Callers must drop repeats: seeing the
// same question again measures memory of it, not ability.
type RatingObs struct {
	QuestionID string
	Correct    bool
	Guess      float64
	B          float64 // current question difficulty on the rating scale
	QN         int
}

// ApplyRatings folds obs into the learner's stored rating inside tx and moves
// each question's difficulty. It returns the published score before and after.
func ApplyRatings(ctx context.Context, tx pgx.Tx, userID string, obs []RatingObs) (before, after float64, err error) {
	r := NewRating()
	var updatedAt *time.Time
	err = tx.QueryRow(ctx,
		`SELECT theta, sigma, n, updated_at FROM learner_ratings WHERE user_id=$1 FOR UPDATE`, userID,
	).Scan(&r.Theta, &r.Sigma, &r.N, &updatedAt)
	if err != nil && err != pgx.ErrNoRows {
		return 0, 0, err
	}
	before = ratingMin // no row yet: nothing measured
	if err == nil {
		before = r.Score()
	}
	if len(obs) == 0 {
		return before, before, nil
	}
	if updatedAt != nil {
		r = r.Age(time.Since(*updatedAt).Hours() / 24)
	}

	ids := make([]string, len(obs))
	deltas := make([]float64, len(obs))
	seeds := make([]float64, len(obs))
	for i, o := range obs {
		var d float64
		r, d = r.Update(o.B, o.Guess, o.Correct, o.QN)
		ids[i], deltas[i], seeds[i] = o.QuestionID, d, o.B
	}

	// Deltas rather than absolute values so concurrent attempts on the same
	// question never overwrite each other's evidence.
	if _, err = tx.Exec(ctx,
		`UPDATE questions q SET rating_b = COALESCE(q.rating_b, t.seed) + t.delta, rating_n = q.rating_n + 1
		   FROM unnest($1::uuid[], $2::float8[], $3::float8[]) AS t(id, delta, seed)
		  WHERE q.id = t.id`, ids, deltas, seeds); err != nil {
		return 0, 0, err
	}
	if _, err = tx.Exec(ctx,
		`INSERT INTO learner_ratings (user_id, theta, sigma, n, score, updated_at)
		 VALUES ($1, $2, $3, $4, $5, now())
		 ON CONFLICT (user_id) DO UPDATE SET theta=EXCLUDED.theta, sigma=EXCLUDED.sigma,
		   n=EXCLUDED.n, score=EXCLUDED.score, updated_at=now()`,
		userID, r.Theta, r.Sigma, r.N, r.Score()); err != nil {
		return 0, 0, err
	}
	return before, r.Score(), nil
}

// BackfillRatings replays every learner's first-time answers in submission
// order. With learner_ratings empty it seeds ratings and question difficulty;
// either way it fills quiz_attempts.qwish_score_after for completed attempts
// that lack it. A no-op once both are done, and the advisory lock stops two
// booting replicas from replaying at once.
// ponytail: whole history in memory, fine to ~millions of responses; batch by
// user if it ever outgrows the boot window. The replay orders answers, not
// completions, so historical points can differ slightly from what a learner
// saw live.
func BackfillRatings(ctx context.Context, db *pgxpool.Pool) (int, error) {
	tx, err := db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('backfill_learner_ratings'))`); err != nil {
		return 0, err
	}
	var haveRatings, needHistory bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM learner_ratings),
		EXISTS (SELECT 1 FROM quiz_attempts WHERE status='completed' AND qwish_score_after IS NULL)`,
	).Scan(&haveRatings, &needHistory); err != nil || (haveRatings && !needHistory) {
		return 0, err
	}

	rows, err := tx.Query(ctx, `
		SELECT user_id, attempt_id, question_id, is_correct, guess, difficulty, submitted_at FROM (
		  SELECT DISTINCT ON (a.user_id, qr.question_id)
		         a.user_id::text, a.id::text attempt_id, qr.question_id::text, COALESCE(qr.is_correct,false) is_correct,
		         `+GuessFloorSQL+` guess, q.difficulty, qr.submitted_at
		    FROM question_responses qr
		    JOIN quiz_attempts a ON a.id=qr.attempt_id AND a.status='completed'
		    JOIN questions q ON q.id=qr.question_id
		   ORDER BY a.user_id, qr.question_id, qr.submitted_at
		) first ORDER BY submitted_at`)
	if err != nil {
		return 0, err
	}
	type qstate struct {
		b     float64
		n     int
		delta float64
	}
	type ustate struct {
		r    Rating
		last time.Time
	}
	users := map[string]*ustate{}
	questions := map[string]*qstate{}
	after := map[string]float64{} // attempt → score after its last counted answer
	for rows.Next() {
		var uid, aid, qid string
		var correct bool
		var guess, diff float64
		var at time.Time
		if err := rows.Scan(&uid, &aid, &qid, &correct, &guess, &diff, &at); err != nil {
			return 0, err
		}
		q := questions[qid]
		if q == nil {
			q = &qstate{b: SeedDifficulty(diff)}
			questions[qid] = q
		}
		u := users[uid]
		if u == nil {
			u = &ustate{r: NewRating()}
			users[uid] = u
		} else {
			u.r = u.r.Age(at.Sub(u.last).Hours() / 24)
		}
		var d float64
		u.r, d = u.r.Update(q.b+q.delta, guess, correct, q.n)
		q.delta += d
		q.n++
		u.last = at
		after[aid] = u.r.Score()
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	rows.Close()

	aids := make([]string, 0, len(after))
	aScores := make([]float64, 0, len(after))
	for id, v := range after {
		aids, aScores = append(aids, id), append(aScores, v)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE quiz_attempts a SET qwish_score_after=t.s
		   FROM unnest($1::uuid[], $2::float8[]) AS t(id, s)
		  WHERE a.id=t.id AND a.qwish_score_after IS NULL`, aids, aScores); err != nil {
		return 0, err
	}
	// Attempts with no first-time answers (retakes) keep the score they started
	// with. The subquery sees the pre-statement snapshot, so runs of retakes all
	// resolve to the last scored attempt before them.
	if _, err := tx.Exec(ctx,
		`UPDATE quiz_attempts a SET qwish_score_after = COALESCE((
		   SELECT p.qwish_score_after FROM quiz_attempts p
		    WHERE p.user_id=a.user_id AND p.status='completed' AND p.qwish_score_after IS NOT NULL
		      AND p.completed_at <= a.completed_at
		    ORDER BY p.completed_at DESC LIMIT 1), 100)
		  WHERE a.status='completed' AND a.qwish_score_after IS NULL`); err != nil {
		return 0, err
	}
	if haveRatings {
		return len(aids), tx.Commit(ctx)
	}

	var uids []string
	var thetas, sigmas, scores []float64
	var ns []int32
	var lasts []time.Time
	for id, u := range users {
		// Idle time since the last answer counts too.
		r := u.r.Age(time.Since(u.last).Hours() / 24)
		uids, thetas, sigmas, ns, scores, lasts = append(uids, id), append(thetas, r.Theta),
			append(sigmas, r.Sigma), append(ns, int32(r.N)), append(scores, r.Score()), append(lasts, time.Now())
	}
	var qids []string
	var bs []float64
	var qns []int32
	for id, q := range questions {
		qids, bs, qns = append(qids, id), append(bs, q.b+q.delta), append(qns, int32(q.n))
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO learner_ratings (user_id, theta, sigma, n, score, updated_at)
		 SELECT * FROM unnest($1::uuid[], $2::float8[], $3::float8[], $4::int[], $5::float8[], $6::timestamptz[])`,
		uids, thetas, sigmas, ns, scores, lasts); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE questions q SET rating_b=t.b, rating_n=t.n
		   FROM unnest($1::uuid[], $2::float8[], $3::int[]) AS t(id, b, n) WHERE q.id=t.id`,
		qids, bs, qns); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT refresh_leaderboard_score(user_id) FROM leaderboard_scores`); err != nil {
		return 0, err
	}
	return len(uids), tx.Commit(ctx)
}
