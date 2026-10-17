// Package avatar generates deterministic, illustrative SVG avatars from a seed.
//
// One seed -> one exact SVG, forever. No stored assets, stdlib only, thread-safe
// (every call gets its own rand source). Scales to any resolution (viewBox).
//
// The seed fixes a learner's identity: head shape, skin, hair, nose, a small
// character mark (freckles, a band-aid, rosy cheeks) and a signature colour.
// Everything that changes over time is layered on top without touching that
// identity: a mood (smile, engaged, delighted, resting) and an earned domain
// outfit (persona + tier, see persona.go). Features are deliberately
// gender-neutral and illustrative.
//
// Layer stack, back to front (every persona slots into it):
//  1. background + tier aura   5. ears + head silhouette
//  2. back props + back hair   6. face features + character marks
//  3. torso, neck, chin shadow 7. front hair + eyewear
//  4. collar / neckwear        8. headgear + foreground props
package avatar

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
)

// GenerateAvatar returns a complete, standalone SVG string for the given seed.
// Same seed always yields byte-identical output.
func GenerateAvatar(seed string) string { return GenerateAvatarCustom(seed, Options{}) }

// GenerateAvatarInt is the integer-seed variant.
func GenerateAvatarInt(seed int64) string { return GenerateAvatarIntCustom(seed, Options{}) }

// GenerateAvatarCustom starts from the seeded avatar, then applies any
// non-empty Options. Same (seed, opts) -> identical output.
func GenerateAvatarCustom(seed string, o Options) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return GenerateAvatarIntCustom(int64(h.Sum64()), o)
}

// GenerateAvatarIntCustom is the integer-seed variant of GenerateAvatarCustom.
func GenerateAvatarIntCustom(seed int64, o Options) string {
	r := rand.New(rand.NewSource(seed))
	d := newDNA(r)
	o.apply(&d)
	return render(d)
}

// --- customization ---------------------------------------------------------

// Options are user-facing overrides. Every field is optional: leave it zero to
// keep the seed's choice. Invalid values are ignored, never rendered.
type Options struct {
	Skin       string // SkinTones, or a #RRGGBB hex
	HairStyle  string // HairStyles
	HairColor  string // HairColors, or a #RRGGBB hex
	Background string // Backgrounds, or a #RRGGBB hex
	Signature  string // PaletteNames, or a #RRGGBB hex: the outfit colour
	Mood       string // Moods
	Persona    string // Personas: the earned domain outfit
	Tier       int    // 1..MaxTier: how far the outfit has been upgraded

	// Deprecated: use Mood. happy -> delighted, neutral -> smile, sad -> resting.
	Expression string
}

// MaxTier is the most upgraded an outfit gets.
const MaxTier = 3

// Vocabularies — the valid values for each Options field.
var (
	SkinTones    = []string{"cream", "peach", "tan", "brown", "deep"}
	HairStyles   = []string{"cloud", "sprout", "wavy"}
	HairColors   = []string{"black", "brown", "auburn", "blond", "cobalt", "violet", "deepteal", "vermilion"}
	Backgrounds  = []string{"cream", "sky", "sage", "blush", "lilac", "sand", "mint", "stone"}
	PaletteNames = []string{"cobalt", "vermilion", "yellow", "teal", "terracotta", "violet", "deepteal", "sand"}
	Moods        = []string{"smile", "engaged", "delighted", "resting"}
)

var (
	skinHex = map[string]string{"cream": "#F4DFC8", "peach": "#F0C9A4", "tan": "#D9A877", "brown": "#A86F45", "deep": "#6E4529"}
	hairHex = map[string]string{"black": "#2B2523", "brown": "#5C3B28", "auburn": "#9A4A2C", "blond": "#D9A95B",
		"cobalt": "#2A4BA0", "violet": "#7B6CB3", "deepteal": "#1F6F78", "vermilion": "#C93F2E"}
	paletteHex = map[string]string{"cobalt": "#2A4BA0", "vermilion": "#C93F2E", "yellow": "#E8B02A",
		"teal": "#2A9D8F", "terracotta": "#E07A5F", "violet": "#7B6CB3", "deepteal": "#1F6F78", "sand": "#E9C46A"}
	// quiet grounds: [base, the backdrop circle on it]
	bgHex = map[string][2]string{"cream": {"#F3EDE2", "#E6DBC7"}, "sky": {"#E3ECF3", "#CFDDEA"},
		"sage": {"#E5EEE3", "#D0E1CC"}, "blush": {"#F3E5DF", "#E8CFC5"}, "lilac": {"#ECE7F4", "#D9D1EA"},
		"sand": {"#F4EBD5", "#E8D9B2"}, "mint": {"#E0EFEC", "#C9E2DD"}, "stone": {"#ECEAE6", "#DAD6CF"}}
	legacyMood = map[string]string{"happy": "delighted", "neutral": "smile", "sad": "resting"}
	legacyHair = map[string]string{"afro": "cloud", "cap": "sprout", "swept": "wavy", "helmet": "wavy"}
	hexRe      = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`) // trust boundary: only clean hex reaches the SVG
)

func (o Options) apply(d *dna) {
	if c := resolve(o.Skin, skinHex); c != "" {
		d.skin = c
	}
	style := o.HairStyle
	if s, ok := legacyHair[style]; ok {
		style = s
	}
	if i := indexOf(HairStyles, style); i >= 0 {
		d.hairStyle = i
	}
	if c := resolve(o.HairColor, hairHex); c != "" {
		d.hair = c
	}
	if bg, ok := bgHex[o.Background]; ok {
		d.bg, d.bgShape = bg[0], bg[1]
	} else if hexRe.MatchString(o.Background) {
		d.bg, d.bgShape = o.Background, shade(o.Background, 0.92)
	}
	if c := resolve(o.Signature, paletteHex); c != "" {
		d.sig = c
	}
	if m, ok := legacyMood[o.Expression]; ok {
		d.mood = m
	}
	if indexOf(Moods, o.Mood) >= 0 {
		d.mood = o.Mood
	}
	if _, ok := personas[o.Persona]; ok {
		d.persona = o.Persona
	}
	if o.Tier >= 1 && o.Tier <= MaxTier {
		d.tier = o.Tier
	}
}

// resolve maps a named preset to hex, else accepts a validated #RRGGBB literal,
// else "" (ignore — keep the seed's). Never lets arbitrary input into the SVG.
func resolve(v string, names map[string]string) string {
	if hex, ok := names[v]; ok {
		return hex
	}
	if hexRe.MatchString(v) {
		return v
	}
	return ""
}

// --- DNA -------------------------------------------------------------------

const ink = "#2B2120" // warm charcoal-brown: every stroke, never pure black

// Head silhouettes, all inside x 96..304, y 92..294 so every hat fits every head.
var headShapes = []string{
	`<rect x="96" y="94" width="208" height="198" rx="76" fill="%s"/>`,                                                                 // squircle
	`<path d="M104,168 C104,110 142,92 200,92 C258,92 296,110 296,168 C314,225 294,292 200,292 C106,292 86,225 104,168 Z" fill="%s"/>`, // pebble
	`<rect x="102" y="92" width="196" height="202" rx="94" fill="%s"/>`,                                                                // capsule
}

// Character marks: identity details that stay with the learner.
const (
	markFreckles = iota
	markBandAid
	markRosy
	markCount
)

type dna struct {
	bg, bgShape, skin, hair, sig string // sig = signature colour, worn by outfits

	headShape, hairStyle, noseStyle, mark int
	eyeStyle                              int
	eyeSpace, eyeR, eyeYoff               float64

	mood    string // one of Moods
	persona string // "" = no outfit; key of personas
	tier    int    // 1..MaxTier
}

func newDNA(r *rand.Rand) dna {
	bg := bgHex[pick(r, Backgrounds)]
	d := dna{
		bg: bg[0], bgShape: bg[1],
		skin: skinHex[pick(r, SkinTones)],
		hair: hairHex[pick(r, HairColors)],
		sig:  paletteHex[pick(r, PaletteNames)],

		headShape: r.Intn(len(headShapes)),
		hairStyle: r.Intn(len(HairStyles)),
		noseStyle: r.Intn(4),
		mark:      r.Intn(markCount),
		eyeStyle:  r.Intn(3),

		eyeSpace: lerp(34, 42, r.Float64()),
		eyeR:     lerp(11, 15, r.Float64()),
		eyeYoff:  lerp(-2, 2, r.Float64()),

		mood: "smile",
		tier: 1,
	}
	if d.hair == d.sig { // hair and outfit would merge into one blob
		d.hair = hairHex["black"]
	}
	return d
}

// --- render ----------------------------------------------------------------

// Anchors. headR is the head envelope hats are fitted to: a circle centred at
// (cx, cy) touching the head top (y 92) and both temples (x 96 / 304).
// The band between the brows and the chin (y ~140..294) stays clear for the face.
const (
	cx, cy  = 200.0, 196.0
	headR   = 104.0
	headTop = cy - headR
	eyeBase = 176.0
	torsoD  = "M44,400 C48,338 108,300 200,300 C292,300 352,338 356,400 Z"
)

func eyeY(d dna) float64 { return eyeBase + d.eyeYoff }

func render(d dna) string {
	var b strings.Builder
	b.Grow(8192)
	pc, dressed := personas[d.persona]

	b.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400" width="400" height="400">`)
	b.WriteString(`<defs><clipPath id="frame"><rect width="400" height="400" rx="40"/></clipPath></defs><g clip-path="url(#frame)">`)

	// 1. background + tier aura
	fmt.Fprintf(&b, `<rect width="400" height="400" fill="%s"/><circle cx="200" cy="196" r="150" fill="%s"/>`, d.bg, d.bgShape)
	if dressed {
		drawAura(&b, d)
	}
	// 2. back props + back hair
	if dressed && pc.back != nil {
		pc.back(&b, d, d.tier)
	}
	drawHairBack(&b, d)
	// 3. torso, neck, chin shadow
	outfit := d.sig
	if dressed && pc.outfit != "" {
		outfit = pc.outfit
	}
	fmt.Fprintf(&b, `<path d="%s" fill="%s"/>`, torsoD, outfit)
	fmt.Fprintf(&b, `<rect x="174" y="268" width="52" height="50" rx="20" fill="%s"/><path d="M174,284 Q200,304 226,284 Z" fill="%s"/>`,
		shade(d.skin, 0.92), shade(d.skin, 0.8))
	// 4. collar / neckwear
	if dressed && pc.body != nil {
		pc.body(&b, d, d.tier)
	} else {
		fmt.Fprintf(&b, `<path d="M170,302 L200,336 L230,302 Z" fill="%s"/>`, shade(outfit, 0.8))
	}
	// 5. ears + head silhouette
	for _, x := range []float64{96, 304} {
		fmt.Fprintf(&b, `<circle cx="%s" cy="204" r="15" fill="%s"/><circle cx="%s" cy="204" r="7" fill="%s"/>`,
			p(x), d.skin, p(x), shade(d.skin, 0.85))
	}
	fmt.Fprintf(&b, headShapes[d.headShape], d.skin)
	// 6. face + character marks
	drawFace(&b, d)
	drawMark(&b, d)
	if dressed && pc.face != nil {
		pc.face(&b, d, d.tier) // lived-in smudges: flour, chalk, ink
	}
	// 7. front hair + eyewear
	drawHairFront(&b, d, dressed && pc.hat)
	if dressed && pc.eyes != nil {
		pc.eyes(&b, d, d.tier)
	}
	// 8. headgear + foreground props
	if dressed && pc.head != nil {
		pc.head(&b, d, d.tier)
	}
	if d.mood == "resting" {
		fmt.Fprintf(&b, `<path d="M318,64 h18 l-18,20 h18 M346,36 h12 l-12,14 h12" fill="none" stroke="#8A8178" stroke-width="4" stroke-linecap="round" stroke-linejoin="round"/>`)
	}
	b.WriteString(`</g></svg>`)
	return b.String()
}

// The aura grows with the outfit: a signature-colour ring once practised,
// a gold double ring at mastery.
func drawAura(b *strings.Builder, d dna) {
	switch d.tier {
	case 2:
		fmt.Fprintf(b, `<circle cx="200" cy="196" r="150" fill="none" stroke="%s" stroke-opacity="0.3" stroke-width="6"/>`, d.sig)
	case MaxTier:
		b.WriteString(`<circle cx="200" cy="196" r="150" fill="none" stroke="#E8B02A" stroke-opacity="0.7" stroke-width="6"/>` +
			`<circle cx="200" cy="196" r="164" fill="none" stroke="#E8B02A" stroke-opacity="0.45" stroke-width="2" stroke-dasharray="2 10" stroke-linecap="round"/>`)
	}
}

func drawHairBack(b *strings.Builder, d dna) {
	if d.hairStyle == 0 { // cloud curls: volume behind the head
		fmt.Fprintf(b, `<g fill="%s"><circle cx="135" cy="105" r="38"/><circle cx="200" cy="90" r="42"/><circle cx="265" cy="105" r="38"/><circle cx="102" cy="150" r="34"/><circle cx="298" cy="150" r="34"/></g>`, d.hair)
	}
}

// Front hair. Under a hat the sprout tuft is tucked away; everything else
// stays, so the learner is recognisable in any outfit.
func drawHairFront(b *strings.Builder, d dna, hat bool) {
	switch d.hairStyle {
	case 0: // cloud curls
		fmt.Fprintf(b, `<g fill="%s"><circle cx="160" cy="102" r="22"/><circle cx="205" cy="96" r="24"/><circle cx="245" cy="104" r="20"/></g>`, d.hair)
	case 1: // sprout tuft crop
		fmt.Fprintf(b, `<path d="M104,154 C110,104 148,86 200,86 C252,86 290,104 296,154 Q250,126 200,126 Q150,126 104,154 Z" fill="%s"/>`, d.hair)
		if !hat {
			fmt.Fprintf(b, `<path d="M195,90 Q185,62 168,68 Q182,82 188,92 Z M202,90 Q215,58 232,66 Q216,80 210,92 Z" fill="%s"/>`, d.hair)
		}
	default: // wavy side-swoop
		fmt.Fprintf(b, `<path d="M100,170 C96,110 140,86 200,86 C260,86 304,110 300,170 C270,136 225,148 180,134 C135,120 118,148 100,170 Z" fill="%s"/>`, d.hair)
	}
}

// mood -> brow lift, brow arch.
var browShape = map[string][2]float64{"smile": {0, 4}, "engaged": {5, 7}, "delighted": {8, 8}, "resting": {-2, 1}}

func drawFace(b *strings.Builder, d dna) {
	ey := eyeY(d)
	brow := browShape[d.mood]
	for _, x := range []float64{cx - d.eyeSpace, cx + d.eyeSpace} {
		by := ey - d.eyeR - 12 - brow[0]
		fmt.Fprintf(b, `<path d="M%s,%s Q%s,%s %s,%s" fill="none" stroke="%s" stroke-width="7" stroke-linecap="round"/>`,
			p(x-14), p(by+2), p(x), p(by-brow[1]), p(x+14), p(by+2), shade(d.hair, 0.8))
		drawEye(b, d, x, ey)
	}
	if d.mood == "delighted" && d.mark != markRosy {
		drawRosy(b, d)
	}
	nose := shade(d.skin, 0.8)
	switch d.noseStyle {
	case 0: // wide button
		fmt.Fprintf(b, `<rect x="190" y="202" width="20" height="14" rx="7" fill="%s"/>`, nose)
	case 1: // round berry
		fmt.Fprintf(b, `<circle cx="200" cy="210" r="8" fill="%s"/>`, nose)
	case 2: // L-bridge
		fmt.Fprintf(b, `<path d="M198,196 L194,220 L206,220" fill="none" stroke="%s" stroke-width="6" stroke-linecap="round" stroke-linejoin="round"/>`, nose)
	default: // vertical pill
		fmt.Fprintf(b, `<rect x="196" y="202" width="8" height="24" rx="4" fill="%s"/>`, nose)
	}
	const my = 248.0
	switch d.mood {
	case "engaged": // eager, mid-thought
		fmt.Fprintf(b, `<path d="M180,%s Q200,%s 220,%s Z" fill="%s" stroke="%s" stroke-width="3" stroke-linejoin="round"/>`, p(my-4), p(my+22), p(my-4), ink, ink)
	case "delighted": // proud open grin
		fmt.Fprintf(b, `<path d="M168,%s Q200,%s 232,%s Z" fill="%s" stroke="%s" stroke-width="3" stroke-linejoin="round"/><ellipse cx="200" cy="%s" rx="11" ry="5" fill="#E07A7A"/>`,
			p(my-8), p(my+40), p(my-8), ink, ink, p(my+8))
	case "resting":
		fmt.Fprintf(b, `<path d="M186,%s Q200,%s 214,%s" fill="none" stroke="%s" stroke-width="6" stroke-linecap="round"/>`, p(my), p(my+7), p(my), ink)
	default: // soft smile
		fmt.Fprintf(b, `<path d="M176,%s Q200,%s 224,%s" fill="none" stroke="%s" stroke-width="7" stroke-linecap="round"/>`, p(my-4), p(my+14), p(my-4), ink)
	}
}

func drawEye(b *strings.Builder, d dna, x, y float64) {
	r := d.eyeR
	switch d.mood {
	case "delighted": // closed, smiling
		fmt.Fprintf(b, `<path d="M%s,%s Q%s,%s %s,%s" fill="none" stroke="%s" stroke-width="6" stroke-linecap="round"/>`,
			p(x-r), p(y+4), p(x), p(y-r), p(x+r), p(y+4), ink)
		return
	case "resting": // closed, relaxed
		fmt.Fprintf(b, `<path d="M%s,%s Q%s,%s %s,%s" fill="none" stroke="%s" stroke-width="6" stroke-linecap="round"/>`,
			p(x-r), p(y), p(x), p(y+r*0.7), p(x+r), p(y), ink)
		return
	}
	switch d.eyeStyle {
	case 0:
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`, p(x), p(y), p(r*0.8), ink)
	case 1:
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="#fff"/><circle cx="%s" cy="%s" r="%s" fill="%s"/>`,
			p(x), p(y), p(r), p(x), p(y), p(r*0.5), ink)
	default:
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="none" stroke="%s" stroke-width="%s"/>`,
			p(x), p(y), p(r*0.8), ink, p(r*0.4))
	}
	if d.mood == "engaged" { // a bright glint
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="#fff"/>`, p(x+r*0.25), p(y-r*0.3), p(r*0.22))
	}
}

func drawRosy(b *strings.Builder, d dna) {
	for _, x := range []float64{cx - d.eyeSpace - 6, cx + d.eyeSpace + 6} {
		fmt.Fprintf(b, `<ellipse cx="%s" cy="%s" rx="13" ry="8" fill="#F08A7E" opacity="0.4"/>`, p(x), p(eyeY(d)+36))
	}
}

func drawMark(b *strings.Builder, d dna) {
	cheek := eyeY(d) + 34
	switch d.mark {
	case markFreckles:
		c := shade(d.skin, 0.72)
		for _, x := range []float64{cx - d.eyeSpace, cx + d.eyeSpace} {
			fmt.Fprintf(b, `<g fill="%s"><circle cx="%s" cy="%s" r="2.6"/><circle cx="%s" cy="%s" r="2.6"/><circle cx="%s" cy="%s" r="2.6"/></g>`,
				c, p(x-8), p(cheek), p(x+1), p(cheek-5), p(x+9), p(cheek+1))
		}
	case markBandAid:
		x := cx + d.eyeSpace + 2
		fmt.Fprintf(b, `<g transform="rotate(-12 %s %s)"><rect x="%s" y="%s" width="28" height="12" rx="4" fill="#F2D3A6" stroke="%s" stroke-width="2"/><rect x="%s" y="%s" width="8" height="8" rx="2" fill="#E5BE8A"/></g>`,
			p(x), p(cheek), p(x-14), p(cheek-6), ink, p(x-4), p(cheek-4))
	default:
		drawRosy(b, d)
	}
}

// --- helpers ---------------------------------------------------------------

func pick(r *rand.Rand, pool []string) string { return pool[r.Intn(len(pool))] }
func p(f float64) string                      { return fmt.Sprintf("%.1f", f) }
func lerp(a, b, t float64) float64            { return a + (b-a)*t }

func indexOf(xs []string, v string) int {
	for i, x := range xs {
		if x == v {
			return i
		}
	}
	return -1
}

// shade scales a validated #RRGGBB colour toward black (f < 1).
func shade(hex string, f float64) string {
	n, _ := strconv.ParseUint(hex[1:], 16, 32)
	c := func(v uint64) uint64 { return uint64(float64(v) * f) }
	return fmt.Sprintf("#%02X%02X%02X", c(n>>16&0xFF), c(n>>8&0xFF), c(n&0xFF))
}
