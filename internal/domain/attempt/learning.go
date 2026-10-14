package attempt

import (
	"encoding/json"
	"fmt"
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

// submitAnswerSQL writes one answer in a single round trip. Every write hangs
// off the inserted response, so an answer that already exists (ON CONFLICT DO
// NOTHING returns no row) records no evidence and leaves the combo untouched.
// Evidence is kept only for active enrolments in the concept's institution;
// misconception tags only for a wrong, in-time choice of a tagged option.
//
// $1 attempt, $2 question, $3 answer, $4 is_correct (after server gates),
// $5 time_taken_ms, $6 clues_used, $7 confidence, $8 combo_level at answer,
// $9 points, $10 option, $11 user, $12 content-correct, $13 timed_out,
// $14 next combo level.
const submitAnswerSQL = `WITH resp AS (
 INSERT INTO question_responses (attempt_id, question_id, answer, is_correct, time_taken_ms, clues_used, confidence_level, combo_level, points_earned, option_id)
 VALUES ($1::uuid, $2::uuid, $3::jsonb, $4::bool, $5::int, $6::int, $7::text, $8::int, $9::bigint, $10::uuid)
 ON CONFLICT (attempt_id, question_id) DO NOTHING RETURNING id
), evidence AS (
 INSERT INTO learning_evidence(response_id,institution_id,user_id,attempt_id,question_id,question_revision,question_version_id,concept_id,option_id,is_correct,confidence_level,clues_used,timed_out)
 SELECT resp.id,e.institution_id,$11::uuid,$1::uuid,$2::uuid,aq.question_revision,aq.question_version_id,(mapping->>'concept_id')::uuid,$10::uuid,$12::bool,$7::text,$6::int,$13::bool
 FROM resp
 JOIN quiz_attempt_questions aq ON aq.attempt_id=$1::uuid AND aq.question_id=$2::uuid
 CROSS JOIN LATERAL jsonb_array_elements(aq.learning_map) mapping
 JOIN curriculum_concepts c ON c.id=(mapping->>'concept_id')::uuid
 JOIN curriculum_chapters ch ON ch.id=c.chapter_id
 JOIN curriculum_versions cv ON cv.id=ch.version_id
 JOIN enrollments e ON e.user_id=$11::uuid AND e.institution_id=cv.institution_id AND e.status='active'
 ON CONFLICT(response_id,concept_id) DO NOTHING RETURNING id,concept_id
), tags AS (
 INSERT INTO learning_evidence_misconceptions(evidence_id,misconception_id)
 SELECT ins.id,(tag->>'misconception_id')::uuid FROM evidence ins
 JOIN quiz_attempt_questions aq ON aq.attempt_id=$1::uuid AND aq.question_id=$2::uuid
 CROSS JOIN LATERAL jsonb_array_elements(aq.learning_map) mapping
 CROSS JOIN LATERAL jsonb_array_elements(mapping->'misconceptions') tag
 WHERE ins.concept_id=(mapping->>'concept_id')::uuid AND tag->>'option_id'=$10::uuid::text
 AND NOT $12::bool AND NOT $13::bool
 ON CONFLICT DO NOTHING
), advanced AS (
 UPDATE quiz_attempts SET combo_level=$14::int, last_answer_at=now()
 WHERE id=$1::uuid AND EXISTS (SELECT 1 FROM resp)
)
SELECT id FROM resp`
