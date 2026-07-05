// Package avatar generates deterministic, Bauhaus-style abstract SVG avatars
// from a seed.
//
// One seed -> one exact SVG, forever. No stored assets, stdlib only, thread-safe
// (every call gets its own rand source). Scales to any resolution (viewBox).
//
// Style: flat geometric color-blocks, hard-edged shapes (circles, arcs,
// triangles, bars), constrained Bauhaus palette, playful asymmetry. No
// gradients, no realism — pure abstract composition.
package avatar

import (
	"fmt"
	"hash/fnv"
	"math/rand"
	"regexp"
	"strings"
)

// GenerateAvatar returns a complete, standalone SVG string for the given seed.
// Same seed always yields byte-identical output.
func GenerateAvatar(seed string) string { return GenerateAvatarCustom(seed, Options{}) }

// GenerateAvatarInt is the integer-seed variant.
func GenerateAvatarInt(seed int64) string { return GenerateAvatarIntCustom(seed, Options{}) }

// GenerateAvatarCustom starts from the seeded random avatar, then applies any
// non-empty Options overrides. Same (seed, opts) -> identical output.
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
	return render(d, r)
}

// --- customization ---------------------------------------------------------

// Options are user-facing overrides. Every field is optional: leave it "" to
// keep the seed's random choice. Give a frontend the vocab slices below to
// build simple pickers.
type Options struct {
	Skin       string // SkinTones, or a #RRGGBB hex
	HairStyle  string // HairStyles
	HairColor  string // PaletteNames, or a #RRGGBB hex
	Background string // PaletteNames, or a #RRGGBB hex
	Expression string // Expressions
	Accessory  string // Accessories
}

// Vocabularies — the valid values for each Options field.
var (
	SkinTones    = []string{"cream", "peach", "tan", "brown"}
	HairStyles   = []string{"cap", "helmet", "swept", "afro"}
	Expressions  = []string{"happy", "neutral", "sad"}
	Accessories  = []string{"none", "circle", "triangle", "bar"}
	PaletteNames = []string{"cobalt", "vermilion", "yellow", "teal", "terracotta", "violet", "deepteal", "sand"}
)

var (
	skinHex      = map[string]string{"cream": cream, "peach": "#F2D7B6", "tan": "#E8C6A0", "brown": "#C79A6B"}
	hairStyleIdx = map[string]int{"cap": 0, "helmet": 1, "swept": 2, "afro": 3}
	exprVal      = map[string]float64{"happy": 0.9, "neutral": 0.5, "sad": 0.1}
	accIdx       = map[string]int{"none": 0, "circle": 1, "triangle": 2, "bar": 3}
	paletteHex   = map[string]string{"cobalt": "#2A4BA0", "vermilion": "#C93F2E", "yellow": "#E8B02A",
		"teal": "#2A9D8F", "terracotta": "#E07A5F", "violet": "#7B6CB3", "deepteal": "#1F6F78", "sand": "#E9C46A"}
	hexRe = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`) // trust boundary: only clean hex reaches the SVG
)

func (o Options) apply(d *dna) {
	if c := resolve(o.Skin, skinHex); c != "" {
		d.face = c
	}
	if i, ok := hairStyleIdx[o.HairStyle]; ok {
		d.hairStyle = i
	}
	if c := resolve(o.HairColor, paletteHex); c != "" {
		d.hair = c
	}
	if c := resolve(o.Background, paletteHex); c != "" {
		d.bgA = c
	}
	if e, ok := exprVal[o.Expression]; ok {
		d.expr = e
	}
	if i, ok := accIdx[o.Accessory]; ok {
		d.accessory = i
	}
}

// resolve maps a named preset to hex, else accepts a validated #RRGGBB literal,
// else "" (ignore — keep random). Never lets arbitrary input into the SVG.
func resolve(v string, names map[string]string) string {
	if v == "" {
		return ""
	}
	if hex, ok := names[v]; ok {
		return hex
	}
	if hexRe.MatchString(v) {
		return v
	}
	return ""
}

// --- palette ---------------------------------------------------------------

// Bauhaus swatches: cobalt, vermilion, sun-yellow, teal, terracotta, violet,
// deep teal, sand. Constrained + bold on purpose.
var swatches = []string{
	"#2A4BA0", "#C93F2E", "#E8B02A", "#2A9D8F",
	"#E07A5F", "#7B6CB3", "#1F6F78", "#E9C46A",
}

const (
	cream = "#EFE7D6"
	ink   = "#211E1C"
)

// --- DNA -------------------------------------------------------------------

type dna struct {
	bgA, bgB, halo, face, hair, accent, mouthCol string

	bgStyle, haloOn                            int
	hairStyle, eyeStyle, noseStyle, mouthStyle int
	eyeSpace, eyeR, eyeAsym                    float64
	eyeYoff                                    float64
	foreheadFrac                               float64
	expr                                       float64 // 0..1 -> mouth curve
	cheeks                                     bool
	accessory                                  int
}

func newDNA(r *rand.Rand) dna {
	// distinct palette roles via seeded shuffle
	pool := append([]string(nil), swatches...)
	r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })

	d := dna{
		bgA:    pool[0],
		bgB:    pool[1],
		halo:   pool[2],
		face:   pick(r, []string{cream, "#F2D7B6", "#E8C6A0", pool[3]}),
		hair:   pool[4],
		accent: pool[5],

		bgStyle:    r.Intn(4),
		haloOn:     r.Intn(3), // 0 -> no halo
		hairStyle:  r.Intn(4),
		eyeStyle:   r.Intn(3),
		noseStyle:  r.Intn(3),
		mouthStyle: r.Intn(3),

		eyeSpace:     lerp(36, 46, r.Float64()),
		eyeR:         lerp(12, 19, r.Float64()),
		eyeAsym:      lerp(0.7, 1.0, r.Float64()), // right eye scale (asymmetry)
		eyeYoff:      lerp(-4, 4, r.Float64()),
		foreheadFrac: lerp(0.10, 0.34, r.Float64()),
		expr:         r.Float64(),
		cheeks:       r.Float64() < 0.4,
		accessory:    r.Intn(4), // 0 -> none
	}
	d.mouthCol = pick(r, []string{ink, d.accent, "#C93F2E"})
	return d
}

// --- render ----------------------------------------------------------------

const (
	cx, cy    = 200.0, 214.0
	headR     = 112.0
	headCyTop = cy - headR
)

func render(d dna, r *rand.Rand) string {
	var b strings.Builder
	b.Grow(3072)

	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 400 400" width="400" height="400">`)
	fmt.Fprintf(&b, `<defs><clipPath id="frame"><rect width="400" height="400" rx="40"/></clipPath></defs>`)
	fmt.Fprintf(&b, `<g clip-path="url(#frame)">`)

	drawBackground(&b, d)

	if d.haloOn != 0 {
		fmt.Fprintf(&b, `<circle cx="200" cy="214" r="150" fill="%s"/>`, d.halo)
	}

	drawHairBack(&b, d)                                                               // afro / big shapes behind
	fmt.Fprintf(&b, `<circle cx="200" cy="214" r="%s" fill="%s"/>`, p(headR), d.face) // head
	drawHairFront(&b, d)                                                              // cap / fringe / bars

	eyeY := cy - 28 + d.eyeYoff
	drawEye(&b, d, cx-d.eyeSpace, eyeY, 1.0)
	drawEye(&b, d, cx+d.eyeSpace, eyeY, d.eyeAsym)
	if d.cheeks {
		fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="7" fill="%s" opacity="0.55"/>`, p(cx-d.eyeSpace-6), p(eyeY+30), d.accent)
		fmt.Fprintf(&b, `<circle cx="%s" cy="%s" r="7" fill="%s" opacity="0.55"/>`, p(cx+d.eyeSpace+6), p(eyeY+30), d.accent)
	}
	drawNose(&b, d, eyeY)
	drawMouth(&b, d, cy+64)
	drawAccessory(&b, d)

	fmt.Fprintf(&b, `</g></svg>`)
	return b.String()
}

func drawBackground(b *strings.Builder, d dna) {
	fmt.Fprintf(b, `<rect width="400" height="400" fill="%s"/>`, d.bgA)
	switch d.bgStyle {
	case 0: // diagonal split
		fmt.Fprintf(b, `<path d="M0,0 L400,0 L0,400 Z" fill="%s"/>`, d.bgB)
	case 1: // vertical half
		fmt.Fprintf(b, `<rect x="200" width="200" height="400" fill="%s"/>`, d.bgB)
	case 2: // horizontal band
		fmt.Fprintf(b, `<rect y="260" width="400" height="140" fill="%s"/>`, d.bgB)
	default: // quarter circle corner
		fmt.Fprintf(b, `<path d="M400,0 A400,400 0 0 1 0,400 L0,0 Z" fill="%s" opacity="0.9"/>`, d.bgB)
	}
}

// Hair behind the head: only the "afro" style needs a big backing circle.
func drawHairBack(b *strings.Builder, d dna) {
	if d.hairStyle == 3 {
		fmt.Fprintf(b, `<circle cx="200" cy="204" r="%s" fill="%s"/>`, p(headR+18), d.hair)
	}
}

// Hair over the head: a hard geometric cap sitting on a chord across the head.
func drawHairFront(b *strings.Builder, d dna) {
	if d.hairStyle == 3 {
		return // afro already drawn as backing circle
	}
	chord := headCyTop + 2*headR*d.foreheadFrac // lower chord = more forehead covered
	switch d.hairStyle {
	case 0: // rounded cap (top half-disc)
		fmt.Fprintf(b, `<path d="M%s,%s A%s,%s 0 0 0 %s,%s Z" fill="%s"/>`,
			p(cx-headR), p(chord), p(headR), p(headR), p(cx+headR), p(chord), d.hair)
	case 1: // cap + two side bars (helmet)
		fmt.Fprintf(b, `<path d="M%s,%s A%s,%s 0 0 0 %s,%s Z" fill="%s"/>`,
			p(cx-headR), p(chord), p(headR), p(headR), p(cx+headR), p(chord), d.hair)
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="16" height="70" rx="8" fill="%s"/>`, p(cx-headR-2), p(chord), d.hair)
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="16" height="70" rx="8" fill="%s"/>`, p(cx+headR-14), p(chord), d.hair)
	default: // angled slab (side-swept)
		fmt.Fprintf(b, `<path d="M%s,%s A%s,%s 0 0 0 %s,%s L%s,%s Z" fill="%s"/>`,
			p(cx-headR), p(chord+14), p(headR), p(headR), p(cx+headR), p(chord-10), p(cx+headR), p(chord+30), d.hair)
	}
}

func drawEye(b *strings.Builder, d dna, x, y, scale float64) {
	r := d.eyeR * scale
	switch d.eyeStyle {
	case 0: // solid ink dot
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`, p(x), p(y), p(r), ink)
	case 1: // white disc + ink pupil
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="#fff"/>`, p(x), p(y), p(r))
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`, p(x), p(y), p(r*0.5), ink)
	default: // ink ring (outline)
		fmt.Fprintf(b, `<circle cx="%s" cy="%s" r="%s" fill="none" stroke="%s" stroke-width="%s"/>`,
			p(x), p(y), p(r), ink, p(r*0.4))
	}
}

func drawNose(b *strings.Builder, d dna, eyeY float64) {
	ny := eyeY + 18
	switch d.noseStyle {
	case 0: // downward triangle
		fmt.Fprintf(b, `<path d="M%s,%s L%s,%s L%s,%s Z" fill="%s"/>`,
			p(cx-9), p(ny), p(cx+9), p(ny), p(cx), p(ny+22), d.accent)
	case 1: // vertical bar
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="7" height="26" rx="3.5" fill="%s"/>`, p(cx-3.5), p(ny), ink)
	default: // quarter-circle (nostril flick)
		fmt.Fprintf(b, `<path d="M%s,%s A18,18 0 0 1 %s,%s" fill="none" stroke="%s" stroke-width="6" stroke-linecap="round"/>`,
			p(cx-2), p(ny), p(cx+8), p(ny+20), ink)
	}
}

func drawMouth(b *strings.Builder, d dna, y float64) {
	w := 34.0
	curve := lerp(6, 30, d.expr) // higher expr -> deeper smile
	switch d.mouthStyle {
	case 0: // smile / frown arc (stroke)
		sweep := 1 // smile (bulge down)
		if d.expr < 0.4 {
			sweep = 0 // frown
		}
		fmt.Fprintf(b, `<path d="M%s,%s A%s,%s 0 0 %d %s,%s" fill="none" stroke="%s" stroke-width="9" stroke-linecap="round"/>`,
			p(cx-w), p(y), p(w), p(curve), sweep, p(cx+w), p(y), d.mouthCol)
	case 1: // filled semicircle (open mouth)
		fmt.Fprintf(b, `<path d="M%s,%s A%s,%s 0 0 0 %s,%s Z" fill="%s"/>`,
			p(cx-w*0.7), p(y), p(w*0.7), p(w*0.7), p(cx+w*0.7), p(y), d.mouthCol)
	default: // simple bar (neutral)
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="8" rx="4" fill="%s"/>`, p(cx-w*0.7), p(y), p(w*1.4), d.mouthCol)
	}
}

// A floating geometric accent — the Bauhaus "extra shape".
func drawAccessory(b *strings.Builder, d dna) {
	switch d.accessory {
	case 1: // circle top-right
		fmt.Fprintf(b, `<circle cx="330" cy="70" r="30" fill="%s"/>`, d.accent)
	case 2: // triangle top-left
		fmt.Fprintf(b, `<path d="M40,40 L96,40 L40,96 Z" fill="%s"/>`, d.accent)
	case 3: // bar bottom-right
		fmt.Fprintf(b, `<rect x="300" y="350" width="90" height="18" rx="9" fill="%s"/>`, d.accent)
	}
}

// --- helpers ---------------------------------------------------------------

func pick(r *rand.Rand, pool []string) string { return pool[r.Intn(len(pool))] }
func p(f float64) string                      { return fmt.Sprintf("%.1f", f) }
func lerp(a, b, t float64) float64            { return a + (b-a)*t }
