package scoring

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds point economy config values loaded at attempt start.
type Config struct {
	BasePointsPerQuestion  float64            `json:"base_points_per_question"`
	PerformanceBonusPct75  float64            `json:"performance_bonus_pct_75"`
	DeductionPctBelow50    float64            `json:"deduction_pct_below_50"`
	StreakBonus7Day        float64            `json:"streak_bonus_7_day"`
	StreakBonus15Day       float64            `json:"streak_bonus_15_day"`
	StreakBonus30Day       float64            `json:"streak_bonus_30_day"`
	ComboMultiplierStep    float64            `json:"combo_multiplier_step"`
	ClueRevealDeductionPer float64            `json:"clue_reveal_deduction_per_clue"`
	PointsExpiryMonths     float64            `json:"points_expiry_months"`
	ConfidenceMultipliers  ConfidenceTable    `json:"confidence_multiplier_table"`
}

type ConfidenceTable struct {
	VeryConfidentCorrect float64 `json:"very_confident_correct"`
	PrettySureCorrect    float64 `json:"pretty_sure_correct"`
	NotSureCorrect       float64 `json:"not_sure_correct"`
	VeryConfidentWrong   float64 `json:"very_confident_wrong"`
	PrettySureWrong      float64 `json:"pretty_sure_wrong"`
	NotSureWrong         float64 `json:"not_sure_wrong"`
}

// LoadConfig reads all point economy config keys from DB and returns a Config.
func LoadConfig(ctx context.Context, db *pgxpool.Pool) (*Config, error) {
	rows, err := db.Query(ctx, `SELECT key, value FROM point_economy_config`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	kv := map[string]json.RawMessage{}
	for rows.Next() {
		var k string
		var v json.RawMessage
		rows.Scan(&k, &v)
		kv[k] = v
	}

	cfg := &Config{
		BasePointsPerQuestion: parseFloat(kv["base_points_per_question"], 10),
		PerformanceBonusPct75: parseFloat(kv["performance_bonus_pct_75"], 20),
		DeductionPctBelow50:   parseFloat(kv["deduction_pct_below_50"], 50),
		StreakBonus7Day:       parseFloat(kv["streak_bonus_7_day"], 50),
		StreakBonus15Day:      parseFloat(kv["streak_bonus_15_day"], 100),
		StreakBonus30Day:      parseFloat(kv["streak_bonus_30_day"], 250),
		ComboMultiplierStep:   parseFloat(kv["combo_multiplier_step"], 0.5),
		ClueRevealDeductionPer: parseFloat(kv["clue_reveal_deduction_per_clue"], 0.25),
		PointsExpiryMonths:    parseFloat(kv["points_expiry_months"], 6),
	}

	// Confidence table
	ct := ConfidenceTable{
		VeryConfidentCorrect: 1.5,
		PrettySureCorrect:    1.0,
		NotSureCorrect:       0.5,
		VeryConfidentWrong:   -0.5,
		PrettySureWrong:      0,
		NotSureWrong:         0,
	}
	if v, ok := kv["confidence_multiplier_table"]; ok {
		json.Unmarshal(v, &ct)
	}
	cfg.ConfidenceMultipliers = ct
	return cfg, nil
}

func parseFloat(raw json.RawMessage, fallback float64) float64 {
	if raw == nil {
		return fallback
	}
	var v float64
	if err := json.Unmarshal(raw, &v); err != nil {
		return fallback
	}
	return v
}

// QuestionResponse is a minimal struct for scoring.
type QuestionResponse struct {
	QuestionID      string
	QuestionType    string
	CorrectAnswer   json.RawMessage
	StudentAnswer   json.RawMessage
	ConfidenceLevel string
	CluesUsed       int
	ComboLevel      int
}

// ScoreQuestion calculates points for a single question response.
// Returns (isCorrect, pointsEarned).
func ScoreQuestion(resp QuestionResponse, cfg *Config) (bool, int64) {
	isCorrect := checkCorrectness(resp.QuestionType, resp.CorrectAnswer, resp.StudentAnswer)
	base := cfg.BasePointsPerQuestion

	switch resp.QuestionType {
	case "multiple_choice", "eliminate_wrong", "puzzle":
		if isCorrect {
			return true, int64(base)
		}
		return false, 0

	case "confidence_based":
		return scoreConfidence(isCorrect, resp.ConfidenceLevel, base, cfg.ConfidenceMultipliers)

	case "speed_chain":
		if isCorrect {
			multiplier := 1.0 + cfg.ComboMultiplierStep*float64(resp.ComboLevel)
			return true, int64(base * multiplier)
		}
		return false, 0

	case "arrange_order":
		if isCorrect {
			return true, int64(base)
		}
		return false, 0

	case "clue_reveal":
		if isCorrect {
			multiplier := 2.0 - cfg.ClueRevealDeductionPer*float64(resp.CluesUsed)
			if multiplier < 0.5 {
				multiplier = 0.5
			}
			return true, int64(base * multiplier)
		}
		return false, 0
	}
	return false, 0
}

func scoreConfidence(correct bool, level string, base float64, ct ConfidenceTable) (bool, int64) {
	var mult float64
	if correct {
		switch level {
		case "very_confident":
			mult = ct.VeryConfidentCorrect
		case "pretty_sure":
			mult = ct.PrettySureCorrect
		default:
			mult = ct.NotSureCorrect
		}
		return true, int64(base * mult)
	}
	// wrong
	switch level {
	case "very_confident":
		mult = ct.VeryConfidentWrong
	default:
		mult = 0
	}
	pts := int64(base * mult)
	return false, pts // pts can be negative
}

// checkCorrectness compares student answer to correct answer for a question type.
func checkCorrectness(qtype string, correct, student json.RawMessage) bool {
	switch qtype {
	case "multiple_choice", "confidence_based", "eliminate_wrong", "puzzle", "speed_chain", "clue_reveal":
		var c, s string
		json.Unmarshal(correct, &c)
		json.Unmarshal(student, &s)
		return c == s

	case "arrange_order":
		var c, st []string
		json.Unmarshal(correct, &c)
		json.Unmarshal(student, &st)
		return sliceEqual(c, st)
	}
	return false
}

func sliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// CalculateFinalScore computes the overall quiz result.
// Returns (scorePct, totalPoints after multiplier).
func CalculateFinalScore(totalCorrect, totalQuestions int, rawPoints int64, scorePct float64,
	cfg *Config, instMultiplier float64) int64 {

	var finalPts int64

	// Performance bonus/deduction on BASE points (separate from per-question combos)
	baseTotal := int64(cfg.BasePointsPerQuestion) * int64(totalCorrect)
	if scorePct >= 75 {
		bonus := int64(float64(baseTotal) * cfg.PerformanceBonusPct75 / 100)
		finalPts = rawPoints + bonus
	} else if scorePct >= 50 {
		finalPts = rawPoints
	} else {
		deduction := int64(float64(baseTotal) * cfg.DeductionPctBelow50 / 100)
		finalPts = rawPoints - deduction
	}

	// Apply institution multiplier
	finalPts = int64(float64(finalPts) * instMultiplier)
	return finalPts
}

// ConfigJSON returns the config as JSON for snapshotting.
func (c *Config) JSON() ([]byte, error) {
	return json.Marshal(c)
}

// ConfigFromSnapshot rehydrates a Config from a JSONB snapshot stored on the attempt.
func ConfigFromSnapshot(raw json.RawMessage) (*Config, error) {
	if raw == nil {
		return nil, fmt.Errorf("no config snapshot")
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
