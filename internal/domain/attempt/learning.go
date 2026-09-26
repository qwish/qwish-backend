package attempt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Resolve legacy label-only answers against the delivered choices, never the
// current option catalogue. ID-backed answers still identify their original label.
func resolveAnswerOption(kind string, rawChoices, answer json.RawMessage, id *string) (json.RawMessage, *string, error) {
	var choices []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	}
	if err := json.Unmarshal(rawChoices, &choices); err != nil {
		return nil, nil, fmt.Errorf("invalid option snapshot")
	}
	if kind == "arrange_order" {
		if id != nil {
			return nil, nil, fmt.Errorf("ordered answers cannot use a single option_id")
		}
		var values []string
		if err := json.Unmarshal(answer, &values); err != nil || values == nil {
			return nil, nil, fmt.Errorf("answer must be an ordered list")
		}
		return answer, nil, nil
	}
	if id != nil {
		for _, choice := range choices {
			if choice.ID == *id {
				value, _ := json.Marshal(choice.Label)
				return value, id, nil
			}
		}
		return nil, nil, fmt.Errorf("option was not delivered in this attempt")
	}
	var label string
	if err := json.Unmarshal(answer, &label); err != nil || string(answer) == "null" {
		return nil, nil, fmt.Errorf("answer must be a string")
	}
	var matched *string
	for _, choice := range choices {
		if choice.Label == label {
			if matched != nil {
				return nil, nil, fmt.Errorf("ambiguous option; supply option_id")
			}
			value := choice.ID
			matched = &value
		}
	}
	// Empty/free-text responses can be graded but never acquire an invented tag.
	return answer, matched, nil
}

func recordLearningEvidence(ctx context.Context, tx pgx.Tx, response, user, attempt, question string, option *string, correct, timedOut bool, confidence *string, clues int) error {
	_, err := tx.Exec(ctx, `WITH inserted AS (
 INSERT INTO learning_evidence(response_id,institution_id,user_id,attempt_id,question_id,question_revision,question_version_id,concept_id,option_id,is_correct,confidence_level,clues_used,timed_out)
 SELECT $1,e.institution_id,$2,$3,$4,aq.question_revision,aq.question_version_id,(mapping->>'concept_id')::uuid,$5,$6,$7,$8,$9
 FROM quiz_attempt_questions aq
 CROSS JOIN LATERAL jsonb_array_elements(aq.learning_map) mapping
 JOIN curriculum_concepts c ON c.id=(mapping->>'concept_id')::uuid
 JOIN curriculum_chapters ch ON ch.id=c.chapter_id
 JOIN curriculum_versions cv ON cv.id=ch.version_id
 JOIN enrollments e ON e.user_id=$2 AND e.institution_id=cv.institution_id AND e.status='active'
 WHERE aq.attempt_id=$3 AND aq.question_id=$4
 ON CONFLICT(response_id,concept_id) DO NOTHING RETURNING id,concept_id
 )
 INSERT INTO learning_evidence_misconceptions(evidence_id,misconception_id)
 SELECT ins.id,(tag->>'misconception_id')::uuid FROM inserted ins
 JOIN quiz_attempt_questions aq ON aq.attempt_id=$3 AND aq.question_id=$4
 CROSS JOIN LATERAL jsonb_array_elements(aq.learning_map) mapping
 CROSS JOIN LATERAL jsonb_array_elements(mapping->'misconceptions') tag
 WHERE ins.concept_id=(mapping->>'concept_id')::uuid AND tag->>'option_id'=$5::text
 AND NOT $6 AND NOT $9
 ON CONFLICT DO NOTHING`, response, user, attempt, question, option, correct, confidence, clues, timedOut)
	return err
}
