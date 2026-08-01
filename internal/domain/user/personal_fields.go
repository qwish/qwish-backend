package user

import (
	"fmt"
	"strings"
)

// personalFields are the student-owned columns on users. Pointers distinguish
// "not supplied" from "supplied as empty", so a partial patch never blanks a
// field the client did not mention.
//
// These are deliberately not on enrollments: everything an institution owns
// lives there and has no student-facing write path, and everything here is the
// student's own to change, institution or not.
type personalFields struct {
	DateOfBirth          *string `json:"date_of_birth"`
	Gender               *string `json:"gender"`
	Phone                *string `json:"phone"`
	Address              *string `json:"address"`
	GuardianName         *string `json:"guardian_name"`
	GuardianPhone        *string `json:"guardian_phone"`
	GuardianEmail        *string `json:"guardian_email"`
	HighestQualification *string `json:"highest_qualification"`
}

// buildUserPatch returns the SET clause and its arguments for the supplied
// fields, numbered from $1.
func buildUserPatch(req personalFields) (string, []interface{}) {
	cols := []struct {
		name string
		val  *string
	}{
		{"date_of_birth", req.DateOfBirth},
		{"gender", req.Gender},
		{"phone", req.Phone},
		{"address", req.Address},
		{"guardian_name", req.GuardianName},
		{"guardian_phone", req.GuardianPhone},
		{"guardian_email", req.GuardianEmail},
		{"highest_qualification", req.HighestQualification},
	}
	var set []string
	var args []interface{}
	for _, c := range cols {
		if c.val == nil {
			continue
		}
		args = append(args, *c.val)
		set = append(set, fmt.Sprintf("%s=$%d", c.name, len(args)))
	}
	return strings.Join(set, ", "), args
}
