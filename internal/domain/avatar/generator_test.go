package avatar

import (
	"strings"
	"testing"
)

func TestDeterministic(t *testing.T) {
	a := GenerateAvatar("user_123")
	b := GenerateAvatar("user_123")
	if a != b {
		t.Fatal("same seed produced different SVG")
	}
	if GenerateAvatar("user_123") == GenerateAvatar("user_124") {
		t.Fatal("different seeds produced identical SVG")
	}
}

func TestOptionsOverride(t *testing.T) {
	base := GenerateAvatar("u")
	custom := GenerateAvatarCustom("u", Options{HairStyle: "afro", Expression: "happy"})
	if base == custom {
		t.Fatal("override had no effect")
	}
	// same seed + same opts must still be deterministic
	if custom != GenerateAvatarCustom("u", Options{HairStyle: "afro", Expression: "happy"}) {
		t.Fatal("custom output not deterministic")
	}
	// a named palette color must actually appear
	if !strings.Contains(GenerateAvatarCustom("u", Options{Background: "vermilion"}), "#C93F2E") {
		t.Fatal("background override not applied")
	}
}

func TestColorInjectionRejected(t *testing.T) {
	evil := `red"/><script>x</script>`
	s := GenerateAvatarCustom("u", Options{HairColor: evil, Background: evil, Skin: evil})
	if strings.Contains(s, "<script>") {
		t.Fatal("unsanitized color reached the SVG")
	}
	// bad input is ignored -> identical to the plain seeded avatar
	if s != GenerateAvatar("u") {
		t.Fatal("invalid options should be ignored, not applied")
	}
}

func TestWellFormed(t *testing.T) {
	s := GenerateAvatar("x")
	if !strings.HasPrefix(s, "<svg") || !strings.HasSuffix(s, "</svg>") {
		t.Fatal("not a full svg")
	}
	// balanced-ish sanity: every <g opens a </g>
	if strings.Count(s, "<g") != strings.Count(s, "</g>") {
		t.Fatalf("unbalanced groups: %d open vs %d close", strings.Count(s, "<g"), strings.Count(s, "</g>"))
	}
}
