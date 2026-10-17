package avatar

import (
	"encoding/xml"
	"fmt"
	"io"
	"math/rand"
	"os"
	"regexp"
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
	if !strings.Contains(GenerateAvatarCustom("u", Options{Background: "sky"}), "#E3ECF3") {
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

func TestPersonas(t *testing.T) {
	if len(Personas) != len(personas) {
		t.Fatalf("Personas lists %d, drawers define %d", len(Personas), len(personas))
	}
	base := GenerateAvatar("u")
	seen := map[string]bool{}
	for _, name := range Personas {
		if _, ok := personas[name]; !ok {
			t.Fatalf("persona %q has no drawer", name)
		}
		prev := base
		for tier := 1; tier <= MaxTier; tier++ { // every upgrade adds something visible
			s := GenerateAvatarCustom("u", Options{Persona: name, Tier: tier})
			if s == prev || seen[s] {
				t.Fatalf("persona %q tier %d renders nothing new", name, tier)
			}
			seen[s] = true
			prev = s
			if s != GenerateAvatarCustom("u", Options{Persona: name, Tier: tier}) {
				t.Fatalf("persona %q not deterministic", name)
			}
		}
	}
	if GenerateAvatarCustom("u", Options{Persona: `x"/><script>`, Tier: 9}) != base {
		t.Fatal("unknown persona / tier should be ignored")
	}
}

// Every combination must be valid XML: one bad path breaks the whole image.
func TestAllCombinationsWellFormed(t *testing.T) {
	for _, name := range append([]string{""}, Personas...) {
		for tier := 1; tier <= MaxTier; tier++ {
			for _, mood := range Moods {
				for _, seed := range []string{"a", "b", "c", "d"} {
					s := GenerateAvatarCustom(seed, Options{Persona: name, Tier: tier, Mood: mood})
					dec := xml.NewDecoder(strings.NewReader(s))
					for {
						if _, err := dec.Token(); err == io.EOF {
							break
						} else if err != nil {
							t.Fatalf("%s/%d/%s/%s: %v", name, tier, mood, seed, err)
						}
					}
				}
			}
		}
	}
}

// Outfits and moods change around the learner, never the learner: the same
// head shape in the same skin under every costume — and never a raw circle.
func TestIdentityPersists(t *testing.T) {
	d := newDNA(rand.New(rand.NewSource(42)))
	plain := GenerateAvatarIntCustom(42, Options{})
	for _, name := range Personas {
		s := GenerateAvatarIntCustom(42, Options{Persona: name, Tier: MaxTier})
		head := fmt.Sprintf(headShapes[d.headShape], d.skin)
		if !strings.Contains(s, head) || !strings.Contains(plain, head) {
			t.Fatalf("%s: head/skin changed", name)
		}
	}
	for i := range headShapes {
		if strings.HasPrefix(headShapes[i], "<circle") {
			t.Fatal("heads are never raw circles")
		}
	}
	moods := map[string]bool{}
	for _, m := range Moods {
		moods[GenerateAvatarIntCustom(42, Options{Mood: m})] = true
	}
	if len(moods) != len(Moods) {
		t.Fatal("moods are not visually distinct")
	}
	if GenerateAvatarCustom("u", Options{Expression: "happy"}) != GenerateAvatarCustom("u", Options{Mood: "delighted"}) {
		t.Fatal("legacy expression=happy should map to delighted")
	}
}

// Every persona the taxonomy migration assigns must be one the generator draws.
func TestMigrationPersonasExist(t *testing.T) {
	sql, err := os.ReadFile("../../../migrations/108_domain_expansion_and_avatar_personas.sql")
	if err != nil {
		t.Fatal(err)
	}
	// persona is the last quoted value of every tuple in that migration
	found := regexp.MustCompile(`, '([a-z_]+)'\)`).FindAllStringSubmatch(string(sql), -1)
	if len(found) < 30 {
		t.Fatalf("only %d persona assignments parsed; regex out of date?", len(found))
	}
	for _, m := range found {
		if _, ok := personas[m[1]]; !ok {
			t.Errorf("migration assigns unknown persona %q", m[1])
		}
	}
}
