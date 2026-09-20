package attempt

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
)

// shuffleQuestionOptions creates the one option order used for a question in
// an attempt. Both legacy options and ID-backed option_choices are rearranged
// together, so submitting an option ID continues to identify the right label.
func shuffleQuestionOptions(options, choices json.RawMessage) (json.RawMessage, json.RawMessage, json.RawMessage, error) {
	var optionValues []json.RawMessage
	if err := json.Unmarshal(options, &optionValues); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid question options: %w", err)
	}
	if len(optionValues) < 2 {
		return options, choices, json.RawMessage("[]"), nil
	}

	order := make([]int, len(optionValues))
	for i := range order {
		order[i] = i
	}
	for i := len(order) - 1; i > 0; i-- {
		j, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("shuffle question options: %w", err)
		}
		order[i], order[j.Int64()] = order[j.Int64()], order[i]
	}
	return applyOptionOrder(options, choices, order)
}

// applyOptionOrder replays a snapshot order when an in-progress attempt is
// resumed. Invalid or legacy snapshots leave the authored order untouched.
func applyOptionOrder(options, choices json.RawMessage, order []int) (json.RawMessage, json.RawMessage, json.RawMessage, error) {
	var optionValues []json.RawMessage
	if err := json.Unmarshal(options, &optionValues); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid question options: %w", err)
	}
	if !validOptionOrder(order, len(optionValues)) {
		return options, choices, json.RawMessage("[]"), nil
	}

	var choiceValues []json.RawMessage
	if err := json.Unmarshal(choices, &choiceValues); err != nil {
		return nil, nil, nil, fmt.Errorf("invalid option choices: %w", err)
	}
	shuffledOptions := reorder(optionValues, order)
	if len(choiceValues) == len(optionValues) {
		choiceValues = reorder(choiceValues, order)
	}
	encodedOptions, err := json.Marshal(shuffledOptions)
	if err != nil {
		return nil, nil, nil, err
	}
	encodedChoices, err := json.Marshal(choiceValues)
	if err != nil {
		return nil, nil, nil, err
	}
	encodedOrder, err := json.Marshal(order)
	if err != nil {
		return nil, nil, nil, err
	}
	return encodedOptions, encodedChoices, encodedOrder, nil
}

func validOptionOrder(order []int, length int) bool {
	if len(order) != length {
		return false
	}
	seen := make([]bool, length)
	for _, index := range order {
		if index < 0 || index >= length || seen[index] {
			return false
		}
		seen[index] = true
	}
	return true
}

func reorder[T any](values []T, order []int) []T {
	result := make([]T, len(values))
	for outputIndex, sourceIndex := range order {
		result[outputIndex] = values[sourceIndex]
	}
	return result
}
