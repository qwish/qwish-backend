package quiz

import (
	"encoding/json"
	"testing"
)

func TestQuestionDuplicates(t *testing.T) {
	cases := []struct {
		a, aAns, b, bAns string
		want             bool
		aMedia, bMedia   string
	}{
		{"Which protocol is used to securely load web pages?", `"HTTPS"`, "Which protocol is used for securely loading web pages?", `"TLS"`, true, "", ""},
		{"What is the capital of France?", `"Paris"`, "Name the capital city of France.", `"Lyon"`, true, "", ""},
		{"Who wrote Romeo and Juliet?", `"Shakespeare"`, "Romeo and Juliet was written by whom?", `" shakespeare"`, true, "", ""},
		{"How many continents are there?", `"7"`, "How many continents are there on Earth?", `"7"`, true, "", ""},
		{"What is the largest planet in our solar system?", `"Jupiter"`, "Which planet is the largest in the solar system?", `"Saturn"`, true, "", ""},
		{"What is 12 x 4?", `"48"`, "What is 12 x 5?", `"60"`, false, "", ""},
		{"Which planet is closest to the Sun?", `"Mercury"`, "Which planet has no moons and is smallest?", `"Mercury"`, false, "", ""},
		{"Calculate the area of a triangle", `"12"`, "Which protocol is used to securely load web pages?", `"12"`, false, "", ""},
		{"What is this?", `"Cat"`, "What is this?", `"Dog"`, false, "https://r2/cat.png", "https://r2/dog.png"},
		{"What is this?", `"Cat"`, "What is this?", `"Cat"`, true, "https://r2/cat.png", "https://r2/cat.png"},
		{"What is the capital of France?", `"Paris"`, "What is the capital of France?", `"Paris"`, true, "https://r2/map.png", ""},
	}
	for _, c := range cases {
		got := signature(c.a, json.RawMessage(c.aAns), &c.aMedia).duplicates(signature(c.b, json.RawMessage(c.bAns), &c.bMedia))
		if got != c.want {
			t.Errorf("%q vs %q: got %v want %v", c.a, c.b, got, c.want)
		}
	}
}
