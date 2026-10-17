package quiz

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrDuplicateQuestion = errors.New("a near-duplicate question already exists")

var wordPattern = regexp.MustCompile(`[\pL\pN]+`)

// Function and question words carry no topic signal; left in, they let
// "What is the capital of France?" and "Name the capital city of France."
// score below threshold while "What is 12 x 4?" matches "What is 12 x 5?".
var stopwords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true, "was": true, "were": true,
	"be": true, "been": true, "of": true, "in": true, "on": true, "at": true, "to": true,
	"for": true, "from": true, "by": true, "with": true, "and": true, "or": true,
	"what": true, "which": true, "who": true, "whom": true, "whose": true, "how": true,
	"when": true, "where": true, "why": true, "name": true, "this": true, "that": true,
	"these": true, "those": true, "it": true, "its": true, "our": true, "your": true,
	"there": true, "do": true, "does": true, "did": true, "has": true, "have": true,
	"had": true, "many": true, "much": true, "s": true,
}

type questionSig struct {
	tokens map[string]struct{}
	answer string
	media  string
}

func signature(prompt string, answer json.RawMessage, media *string) questionSig {
	all := wordPattern.FindAllString(strings.ToLower(prompt), -1)
	tokens := map[string]struct{}{}
	for _, t := range all {
		if !stopwords[t] {
			tokens[t] = struct{}{}
		}
	}
	if len(tokens) == 0 { // "What is this?" — nothing but stopwords; compare raw words.
		for _, t := range all {
			tokens[t] = struct{}{}
		}
	}
	sig := questionSig{tokens: tokens, answer: answerKey(answer)}
	if media != nil {
		sig.media = strings.TrimSpace(*media)
	}
	return sig
}

func answerKey(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.ToLower(strings.TrimSpace(s))
	}
	if k := strings.TrimSpace(string(raw)); k != "null" {
		return strings.ToLower(k)
	}
	return ""
}

func (a questionSig) jaccard(b questionSig) float64 {
	inter := 0
	for t := range b.tokens {
		if _, ok := a.tokens[t]; ok {
			inter++
		}
	}
	union := len(a.tokens) + len(b.tokens) - inter
	if union == 0 {
		return 1
	}
	return float64(inter) / float64(union)
}

// Same answer lowers the bar: rephrasings ("Who wrote X?" / "X was written by
// whom?") share few words but always share the answer.
// Two different images/videos make "What is this?" two different questions.
func (a questionSig) duplicates(b questionSig) bool {
	if a.media != "" && b.media != "" && a.media != b.media {
		return false
	}
	j := a.jaccard(b)
	return j >= 0.60 || (a.answer != "" && a.answer == b.answer && j >= 0.40)
}

// ponytail: full scan of the quiz (max 200 questions); index if quizzes grow far past that.
func findNearDuplicate(ctx context.Context, db *pgxpool.Pool, quizID, prompt string, answer json.RawMessage, media *string, excludeID string) (bool, error) {
	sig := signature(prompt, answer, media)
	rows, err := db.Query(ctx, `
		SELECT prompt, correct_answer, media_url FROM questions
		WHERE quiz_id=$1 AND ($2='' OR id<>$2::uuid)`, quizID, excludeID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate string
		var candidateAnswer json.RawMessage
		var candidateMedia *string
		if err := rows.Scan(&candidate, &candidateAnswer, &candidateMedia); err != nil {
			return false, err
		}
		if sig.duplicates(signature(candidate, candidateAnswer, candidateMedia)) {
			return true, nil
		}
	}
	return false, rows.Err()
}
