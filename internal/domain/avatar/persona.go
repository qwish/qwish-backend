package avatar

import (
	"fmt"
	"math"
	"strings"
)

// Personas are the domain outfits a learner earns by mastering a topic
// (mathematician for maths, director for entertainment, ...). Each is one
// strong wearable plus one supporting detail, drawn on the learner's own
// seeded character. Higher tiers (2..MaxTier) add memorable extras, so an
// outfit visibly grows as the learner keeps mastering its domain.
//
// The DB's domains.persona / subdomains.persona columns (migration 108) name
// these; an unknown name renders the learner in their plain clothes.
var Personas = []string{
	"mathematician", "detective", "strategist", "author", "coder", "executive",
	"director", "scientist", "historian", "explorer", "journalist", "linguist",
	"athlete", "artist", "musician", "chef", "doctor", "naturalist", "pilot",
	"philosopher", "judge", "engineer", "grandmaster", "gamer", "astronaut",
	"trendsetter", "topper", "scholar",
}

type layer func(b *strings.Builder, d dna, tier int)

// Each persona tells its story in four ways: a lived-in detail (chalk dust,
// flour, ink), an asymmetric silhouette (something tucked behind one ear, a
// tilted hat, a prop on one side), structural growth across tiers (T1 garment
// + signature headwear, T2 functional gear, T3 mastery artifact in gold), and
// the shared warm palette with #2B2120 strokes.
//
// Layers slot into the render stack (see avatar.go): back = 2 (behind the
// head), body = 4 (collar and neckwear), face = 6 (smudges on the skin),
// eyes = 7 (eyewear), head = 8 (headgear and held props). Props stay out of
// the brow-to-chin band so the expression is always the focus.
type persona struct {
	outfit                       string // torso colour; "" = the learner's signature colour
	hat                          bool   // covers the crown: the sprout tuft tucks away
	back, body, face, eyes, head layer
}

const (
	white     = "#FFFFFF"
	paper     = "#FBF6EA"
	coat      = "#F4F1EA"
	gold      = "#E8B02A"
	goldDeep  = "#C99520"
	red       = "#C93F2E"
	cobalt    = "#2A4BA0"
	teal      = "#2A9D8F"
	green     = "#4C9A5B"
	greenDeep = "#2F6B3E"
	wood      = "#B07A4A"
	brown     = "#7A4A2C"
	khaki     = "#D8C08A"
	navy      = "#1F2A44"
	charcoal  = "#3A3F4B"
	silver    = "#C9CED6"
	pink      = "#E56B9F"
	orange    = "#F08A24"
)

var personas = map[string]persona{
	"mathematician": {
		back: func(b *strings.Builder, d dna, t int) { // pencil tucked behind the right ear
			w(b, `<g transform="rotate(24 312 180)"><rect x="304" y="112" width="14" height="96" rx="3" fill="#F2C14E" stroke="`+ink+`" stroke-width="3"/>`+
				`<rect x="304" y="104" width="14" height="14" rx="3" fill="#E98A9A" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M304,208 L318,208 L311,228 Z" fill="#EBC9A0" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/></g>`)
		},
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			if t >= 2 { // burgundy V-neck cardigan vest
				w(b, `<path d="M84,400 C88,350 116,318 156,306 L200,356 L244,306 C284,318 312,350 316,400 Z" fill="#7A2E3B"/>`+
					`<path d="M200,356 L200,400" stroke="#5E2230" stroke-width="3"/><circle cx="208" cy="372" r="3.5" fill="`+paper+`"/><circle cx="208" cy="390" r="3.5" fill="`+paper+`"/>`)
			}
			// chalk dust on the sleeve
			w(b, `<path d="M66,372 q10,-8 20,-2 q8,5 16,-2 M74,386 q8,-5 16,0" fill="none" stroke="#FFFFFF" stroke-opacity="0.65" stroke-width="5" stroke-linecap="round"/>`+
				`<circle cx="98" cy="360" r="3" fill="#FFFFFF" fill-opacity="0.6"/><circle cx="62" cy="388" r="2.5" fill="#FFFFFF" fill-opacity="0.6"/>`)
			if t >= 3 { // protractor in the pocket + golden-ratio spiral pin
				w(b, `<path d="M236,350 A22,22 0 0 1 280,350 Z" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/><path d="M258,350 L258,334" stroke="`+ink+`" stroke-width="2"/>`+
					`<path d="M130,342 a3,3 0 0 1 3,3 a6,6 0 0 1 -6,6 a10,10 0 0 1 -10,-10 a16,16 0 0 1 16,-16" fill="none" stroke="`+gold+`" stroke-width="3" stroke-linecap="round"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // notebook of geometric sketches
			w(b, `<g transform="rotate(-8 92 352)"><rect x="50" y="316" width="84" height="70" rx="6" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M60,316 v-6 M74,316 v-6 M88,316 v-6 M102,316 v-6 M116,316 v-6" stroke="`+ink+`" stroke-width="3" stroke-linecap="round"/>`+
				`<path d="M62,374 L88,334 L112,374 Z" fill="none" stroke="`+cobalt+`" stroke-width="3" stroke-linejoin="round"/>`+
				`<circle cx="88" cy="360" r="11" fill="none" stroke="`+red+`" stroke-width="3"/></g>`)
		},
	},
	"detective": {
		outfit: "#C9A66B",
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			lapels(b, "#A9874E")
			if t >= 2 { // inverness cape
				w(b, `<path d="M52,372 C66,330 118,306 200,306 C282,306 334,330 348,372 Q200,394 52,372 Z" fill="#8C6A43"/>`+
					`<path d="M90,330 Q200,350 310,330 M70,356 Q200,378 330,356" fill="none" stroke="#6B4F2E" stroke-width="3"/>`+
					`<path d="M110,318 L104,382 M160,308 L158,388 M240,308 L242,388 M290,318 L296,382" stroke="#6B4F2E" stroke-width="3"/>`+
					`<path d="M80,342 Q200,362 320,342" fill="none" stroke="`+red+`" stroke-opacity="0.7" stroke-width="2"/>`)
			}
			if t >= 3 { // gold pocket watch on its chain + a clue-string pin
				w(b, `<path d="M228,380 Q256,398 280,376" fill="none" stroke="`+gold+`" stroke-width="3"/><circle cx="282" cy="372" r="9" fill="`+gold+`" stroke="`+ink+`" stroke-width="2"/><path d="M282,372 l0,-5 M282,372 l4,2" stroke="`+ink+`" stroke-width="1.5"/>`+
					`<circle cx="130" cy="330" r="5" fill="`+red+`" stroke="`+ink+`" stroke-width="2"/><path d="M130,330 L146,346 L134,356 L152,368" fill="none" stroke="`+red+`" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) {
			dome(b, 138, 112, "#9C6B3F", "")
			hw := chordW(138, 112)
			f(b, `<path d="M%s,136 L%s,124 L%s,146 Z M%s,136 L%s,124 L%s,146 Z" fill="#7A5230"/>`,
				p(cx-hw+6), p(cx-hw-24), p(cx-hw+12), p(cx+hw-6), p(cx+hw+24), p(cx+hw-12))
			w(b, `<ellipse cx="200" cy="138" rx="90" ry="8" fill="#7A5230"/><circle cx="200" cy="86" r="6" fill="#7A5230"/>`+
				`<path d="M150,96 Q146,120 140,136 M250,96 Q254,120 260,136" fill="none" stroke="#7A5230" stroke-width="3"/>`)
			// magnifying glass
			w(b, `<path d="M332,358 L360,390" stroke="`+brown+`" stroke-width="12" stroke-linecap="round"/>`+
				`<circle cx="312" cy="336" r="26" fill="#DDEFF2" fill-opacity="0.8" stroke="#B8862B" stroke-width="7"/>`)
		},
	},
	"strategist": {
		outfit: navy,
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			f(b, `<path d="M168,300 L200,330 L232,300 Z" fill="%s"/>`, coat)
			w(b, `<circle cx="178" cy="346" r="5" fill="`+gold+`"/><circle cx="178" cy="374" r="5" fill="`+gold+`"/>`+
				`<circle cx="222" cy="346" r="5" fill="`+gold+`"/><circle cx="222" cy="374" r="5" fill="`+gold+`"/>`)
			if t >= 2 { // epaulettes
				w(b, `<rect x="62" y="330" width="50" height="14" rx="5" fill="`+gold+`" transform="rotate(-24 87 337)"/>`+
					`<rect x="288" y="330" width="50" height="14" rx="5" fill="`+gold+`" transform="rotate(24 313 337)"/>`)
			}
			if t >= 3 { // medal ribbons
				w(b, `<rect x="104" y="350" width="14" height="10" fill="`+red+`"/><rect x="118" y="350" width="14" height="10" fill="`+cobalt+`"/><rect x="132" y="350" width="14" height="10" fill="`+gold+`"/>`+
					`<rect x="111" y="360" width="14" height="10" fill="`+teal+`"/><rect x="125" y="360" width="14" height="10" fill="`+white+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // peaked cap, worn at an angle + battle-plan clipboard
			tilt(b, -8, 200, 120, func() {
				f(b, `<path d="M112,134 Q116,88 200,82 Q284,88 288,134 Z" fill="%s"/>`, navy)
				f(b, `<rect x="112" y="118" width="176" height="16" fill="%s"/><circle cx="200" cy="106" r="9" fill="%s" stroke="%s" stroke-width="2"/>`, d.sig, gold, ink)
				w(b, `<path d="M110,132 Q200,152 290,132 L290,130 L110,130 Z" fill="`+ink+`"/>`)
			})
			w(b, `<rect x="54" y="320" width="72" height="72" rx="6" fill="`+brown+`"/><rect x="62" y="330" width="56" height="56" fill="`+paper+`"/>`+
				`<rect x="78" y="314" width="24" height="12" rx="3" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/>`+
				`<path d="M70,374 Q86,350 104,346 M98,340 L106,346 L98,352" fill="none" stroke="`+red+`" stroke-width="3" stroke-linecap="round"/>`+
				`<circle cx="74" cy="346" r="4" fill="`+cobalt+`"/>`)
		},
	},
	"author": {
		back: func(b *strings.Builder, d dna, t int) { // quill behind the left ear
			w(b, `<g transform="rotate(-22 96 180)"><path d="M96,88 C124,116 118,170 98,216 C80,170 72,120 96,88 Z" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M98,216 L96,104" stroke="`+ink+`" stroke-width="2"/></g>`)
		},
		body: func(b *strings.Builder, d dna, t int) { // cardigan, ink-stained from use
			vNeck(b, coat)
			cardigan(b, d)
			w(b, `<path d="M256,352 q6,-8 12,-2 q8,-2 6,6 q6,6 -2,10 q-4,8 -10,2 q-8,2 -6,-6 q-6,-6 0,-10 Z" fill="`+cobalt+`" fill-opacity="0.75"/><circle cx="278" cy="372" r="2.5" fill="`+cobalt+`" fill-opacity="0.75"/>`)
			if t >= 2 { // cozy draped knit scarf
				scarf(b, shade(d.sig, 0.75))
			}
		},
		eyes: func(b *strings.Builder, d dna, t int) {
			if t >= 3 { // round wire-rim reading glasses
				roundGlasses(b, d, gold)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // stack of books
			w(b, `<rect x="48" y="366" width="90" height="22" rx="3" fill="`+cobalt+`"/><rect x="56" y="344" width="80" height="22" rx="3" fill="`+red+`"/>`+
				`<rect x="50" y="322" width="84" height="22" rx="3" fill="`+teal+`"/>`+
				`<path d="M58,377 h70 M66,355 h62 M60,333 h66" stroke="`+paper+`" stroke-width="3" opacity="0.7"/>`)
			if t >= 3 { // inkwell
				w(b, `<path d="M300,352 L340,352 L346,388 L294,388 Z" fill="`+navy+`" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/><rect x="310" y="340" width="20" height="14" rx="3" fill="`+gold+`" stroke="`+ink+`" stroke-width="3"/>`)
			}
		},
	},
	"coder": {
		outfit: charcoal,
		eyes: func(b *strings.Builder, d dna, t int) {
			if t >= 3 { // glasses catching the dark-mode terminal glow
				roundGlasses(b, d, ink)
				for _, x := range []float64{cx - d.eyeSpace, cx + d.eyeSpace} {
					f(b, `<path d="M%s,%s h10 M%s,%s h6" stroke="#5EE6C2" stroke-width="3" stroke-linecap="round"/>`,
						p(x-8), p(eyeY(d)+d.eyeR), p(x-8), p(eyeY(d)+d.eyeR+5))
				}
			}
		},
		back: func(b *strings.Builder, d dna, t int) { // hood
			f(b, `<circle cx="200" cy="204" r="132" fill="%s"/>`, "#2F333D")
		},
		body: func(b *strings.Builder, d dna, t int) {
			w(b, `<path d="M182,302 L178,352 M218,302 L222,352" stroke="`+coat+`" stroke-width="5" stroke-linecap="round"/>`+
				`<rect x="140" y="368" width="120" height="40" rx="12" fill="#2F333D"/>`)
		},
		head: func(b *strings.Builder, d dna, t int) { // headset + tiny laptop
			band := ink
			if t >= 3 {
				band = gold
			}
			headset(b, band, "#4A4F5A", false)
			w(b, `<rect x="270" y="316" width="88" height="64" rx="6" fill="#9AA1AB" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M258,392 L370,392 L360,380 L268,380 Z" fill="`+silver+`" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/>`+
				`<circle cx="314" cy="348" r="7" fill="`+white+`"/>`)
			if t >= 2 { // the first stickers
				w(b, `<circle cx="286" cy="330" r="7" fill="`+red+`"/><rect x="334" y="358" width="16" height="12" rx="3" fill="`+gold+`"/>`)
			}
			if t >= 3 { // sticker-bombed
				w(b, `<path d="M338,326 l8,10 l-14,0 Z" fill="`+teal+`"/><rect x="280" y="356" width="18" height="14" rx="4" fill="`+cobalt+`"/><circle cx="346" cy="340" r="5" fill="`+pink+`"/><path d="M298,326 h14 v8 h-14 Z" fill="`+green+`"/>`)
			}
		},
	},
	"executive": {
		outfit: navy,
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			f(b, `<path d="M193,306 L207,306 L213,364 L200,382 L187,364 Z" fill="%s"/>`, d.sig)
			lapels(b, "#16203A")
			if t >= 2 { // pocket square
				f(b, `<path d="M112,352 L136,352 L130,342 L124,350 L118,342 Z" fill="%s"/>`, coat)
			}
			if t >= 3 { // tie clip + lapel pin
				w(b, `<rect x="186" y="344" width="28" height="4" fill="`+gold+`"/><circle cx="252" cy="334" r="5" fill="`+gold+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // briefcase
			w(b, `<rect x="276" y="336" width="84" height="56" rx="7" fill="`+brown+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M304,336 v-10 a4,4 0 0 1 4,-4 h20 a4,4 0 0 1 4,4 v10" fill="none" stroke="`+ink+`" stroke-width="4"/>`+
				`<rect x="312" y="352" width="12" height="9" rx="2" fill="`+gold+`"/>`)
		},
	},
	"director": {
		outfit: "#2E2826",
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			w(b, `<rect x="164" y="284" width="72" height="30" rx="14" fill="#3E3532"/><path d="M172,296 h56 M172,304 h56" stroke="#2E2826" stroke-width="2"/>`) // turtleneck roll
			if t >= 3 {                                                                                                                                          // red-carpet scarf + gold film-reel pin
				scarf(b, red)
				w(b, `<circle cx="122" cy="344" r="12" fill="`+gold+`" stroke="`+ink+`" stroke-width="3"/><circle cx="122" cy="338" r="2.5" fill="`+ink+`"/><circle cx="116" cy="347" r="2.5" fill="`+ink+`"/><circle cx="128" cy="347" r="2.5" fill="`+ink+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // beret + megaphone
			w(b, `<ellipse cx="192" cy="112" rx="104" ry="28" fill="`+ink+`" transform="rotate(-8 192 112)"/><circle cx="196" cy="82" r="8" fill="`+ink+`"/>`)
			w(b, `<path d="M46,318 L46,390 L118,364 L118,344 Z" fill="`+paper+`" stroke="`+ink+`" stroke-width="4" stroke-linejoin="round"/>`+
				`<path d="M70,330 L70,380" stroke="`+red+`" stroke-width="8"/><rect x="116" y="344" width="16" height="20" rx="3" fill="`+ink+`"/>`)
			if t >= 2 { // clapperboard
				w(b, `<rect x="274" y="344" width="84" height="48" rx="3" fill="`+ink+`"/>`+
					`<g transform="rotate(-12 274 340)"><rect x="274" y="326" width="84" height="14" fill="`+ink+`"/><path d="M284,326 l10,0 l-6,14 l-10,0 Z M306,326 l10,0 l-6,14 l-10,0 Z M328,326 l10,0 l-6,14 l-10,0 Z" fill="`+white+`"/></g>`+
					`<path d="M284,362 h64 M284,376 h40" stroke="`+white+`" stroke-width="3" opacity="0.6"/>`)
			}
		},
	},
	"scientist": {
		outfit: coat,
		body: func(b *strings.Builder, d dna, t int) {
			f(b, `<path d="M168,300 L200,346 L232,300 Z" fill="%s"/>`, d.sig)
			w(b, `<path d="M168,300 L200,346 L184,400 M232,300 L200,346 L216,400" fill="none" stroke="`+silver+`" stroke-width="4"/>`+
				`<path d="M146,306 L170,300 L186,332 Z M254,306 L230,300 L214,332 Z" fill="`+coat+`" stroke="`+silver+`" stroke-width="3" stroke-linejoin="round"/>`)
			if t >= 3 { // pocket protector + ID badge
				w(b, `<rect x="100" y="346" width="30" height="20" rx="3" fill="`+silver+`"/><path d="M106,346 v-14 M115,346 v-18 M124,346 v-12" stroke-width="4" stroke-linecap="round" stroke="`+cobalt+`"/>`+
					`<rect x="262" y="344" width="30" height="40" rx="4" fill="`+white+`" stroke="`+teal+`" stroke-width="3"/><rect x="270" y="352" width="14" height="14" fill="`+silver+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // oversized safety goggles, pushed up askew
			tilt(b, -6, 200, 118, func() {
				hw := chordW(120, headR)
				f(b, `<path d="M%s,120 L%s,120" stroke="%s" stroke-width="9"/>`, p(cx-hw), p(cx+hw), ink)
				w(b, `<rect x="136" y="98" width="54" height="40" rx="16" fill="#BFE3E8" fill-opacity="0.9" stroke="`+ink+`" stroke-width="6"/>`+
					`<rect x="210" y="98" width="54" height="40" rx="16" fill="#BFE3E8" fill-opacity="0.9" stroke="`+ink+`" stroke-width="6"/>`+
					`<rect x="188" y="112" width="24" height="10" fill="`+ink+`"/><path d="M146,108 l10,-4 M220,108 l10,-4" stroke="#fff" stroke-width="4" stroke-linecap="round"/>`)
			})
			if t >= 2 { // flask
				w(b, `<path d="M308,316 L324,316 L324,336 L346,380 Q348,388 340,388 L292,388 Q284,388 286,380 L308,336 Z" fill="`+white+`" stroke="`+ink+`" stroke-width="4" stroke-linejoin="round"/>`+
					`<path d="M298,362 L334,362 L344,382 Q345,384 341,384 L291,384 Q287,384 288,382 Z" fill="`+teal+`"/><circle cx="312" cy="348" r="3" fill="`+teal+`"/><circle cx="320" cy="300" r="4" fill="`+teal+`"/>`)
			}
		},
	},
	"historian": {
		outfit: "#7A5C45",
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			lapels(b, "#5E4533")
			if t >= 2 { // crimson bow tie
				w(b, `<path d="M178,306 L200,314 L222,306 L222,326 L200,318 L178,326 Z" fill="#9E2A2B"/><circle cx="200" cy="316" r="5" fill="#7E1F20"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // newsboy cap, tipped + ancient scroll
			tilt(b, 8, 200, 118, func() {
				w(b, `<path d="M106,134 Q108,90 200,84 Q292,90 294,134 Z" fill="#8C6E52"/><circle cx="200" cy="88" r="6" fill="#6B523B"/>`+
					`<path d="M108,130 Q200,148 292,130 L292,124 Q200,140 108,124 Z" fill="#6B523B"/>`)
			})
			w(b, `<rect x="60" y="332" width="72" height="44" fill="#EFE0BC" stroke="`+ink+`" stroke-width="3"/>`+
				`<rect x="50" y="326" width="14" height="56" rx="7" fill="#D9C08E" stroke="`+ink+`" stroke-width="3"/><rect x="128" y="326" width="14" height="56" rx="7" fill="#D9C08E" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M74,346 h44 M74,358 h34" stroke="#A88A5A" stroke-width="3"/>`)
			if t >= 3 { // hourglass
				w(b, `<rect x="290" y="320" width="56" height="8" rx="3" fill="`+brown+`"/><rect x="290" y="384" width="56" height="8" rx="3" fill="`+brown+`"/>`+
					`<path d="M298,328 L338,328 L318,356 L338,384 L298,384 L318,356 Z" fill="`+gold+`" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/>`)
			}
		},
	},
	"explorer": {
		outfit: "#C9B37E",
		hat:    true,
		back: func(b *strings.Builder, d dna, t int) {
			if t >= 3 { // well-travelled backpack
				w(b, `<rect x="70" y="262" width="260" height="80" rx="28" fill="`+brown+`"/><circle cx="96" cy="292" r="10" fill="`+red+`"/><rect x="292" y="282" width="20" height="16" rx="3" fill="`+teal+`"/>`)
			}
		},
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, "#B39A62")
			w(b, `<path d="M120,312 L140,400 M280,312 L260,400" stroke="#6B4429" stroke-width="16"/>`+
				`<rect x="124" y="350" width="20" height="12" rx="2" fill="`+silver+`"/><rect x="256" y="350" width="20" height="12" rx="2" fill="`+silver+`"/>`)
			if t >= 2 { // neckerchief
				f(b, `<path d="M174,300 L226,300 L200,332 Z" fill="%s"/>`, red)
			}
			if t >= 3 { // compass on the strap
				w(b, `<circle cx="136" cy="380" r="12" fill="`+white+`" stroke="`+ink+`" stroke-width="3"/><path d="M136,370 L140,380 L132,380 Z" fill="`+red+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // sun helmet, set at an angle
			tilt(b, -8, 200, 128, func() {
				dome(b, 132, 112, khaki, "")
				hw := chordW(126, 112)
				f(b, `<rect x="%s" y="120" width="%s" height="12" fill="#8C7444"/>`, p(cx-hw), p(2*hw))
				w(b, `<ellipse cx="200" cy="134" rx="134" ry="10" fill="#B39A62"/>`)
			})
		},
	},
	"journalist": {
		hat: true,
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			lapels(b, shade(d.sig, 0.75))
			if t >= 2 { // press pass on a lanyard
				f(b, `<path d="M180,302 L194,350 M220,302 L206,350" stroke="%s" stroke-width="3"/>`, red)
				w(b, `<rect x="184" y="346" width="32" height="38" rx="4" fill="`+white+`" stroke="`+ink+`" stroke-width="3"/><path d="M191,370 h18 M191,377 h12" stroke="`+ink+`" stroke-width="2"/><rect x="191" y="352" width="18" height="12" fill="`+red+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // fedora + microphone
			w(b, `<path d="M128,132 L140,80 Q200,62 260,80 L272,132 Z" fill="`+ink+`"/>`)
			f(b, `<rect x="130" y="112" width="140" height="14" fill="%s"/>`, d.sig)
			w(b, `<rect x="232" y="86" width="26" height="18" fill="`+white+`" transform="rotate(12 245 95)"/><ellipse cx="200" cy="132" rx="128" ry="9" fill="`+ink+`"/>`)
			w(b, `<rect x="312" y="342" width="14" height="50" rx="4" fill="`+ink+`"/><rect x="309" y="340" width="20" height="10" fill="`+red+`"/>`+
				`<circle cx="319" cy="324" r="18" fill="`+ink+`"/><path d="M307,318 h24 M305,326 h28 M309,334 h20" stroke="#5A5A5A" stroke-width="2"/>`)
			if t >= 3 { // camera
				w(b, `<rect x="48" y="338" width="80" height="52" rx="8" fill="`+ink+`"/><rect x="60" y="330" width="22" height="10" rx="2" fill="`+ink+`"/>`+
					`<circle cx="92" cy="364" r="17" fill="#5A5A5A" stroke="`+silver+`" stroke-width="4"/><circle cx="92" cy="364" r="7" fill="#9AB6D8"/>`)
			}
		},
	},
	"linguist": {
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			scarf(b, teal)
			w(b, `<path d="M208,340 h28 M210,354 h28 M212,368 h28" stroke="`+coat+`" stroke-width="5"/>`)
		},
		head: func(b *strings.Builder, d dna, t int) { // speech bubbles in two languages
			w(b, `<path d="M294,92 L286,110 L310,94" fill="`+cobalt+`" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/><rect x="282" y="44" width="78" height="50" rx="16" fill="`+cobalt+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M298,62 h46 M298,76 h30" stroke="`+white+`" stroke-width="5" stroke-linecap="round"/>`+
				`<path d="M92,100 L104,116 L84,101" fill="`+red+`" stroke="`+ink+`" stroke-width="3" stroke-linejoin="round"/><rect x="38" y="58" width="66" height="44" rx="14" fill="`+red+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M54,74 h34 M54,86 h20" stroke="`+white+`" stroke-width="5" stroke-linecap="round"/>`)
			if t >= 2 { // phrasebook
				w(b, `<rect x="52" y="330" width="66" height="60" rx="5" fill="`+red+`" stroke="`+ink+`" stroke-width="3"/><circle cx="85" cy="360" r="15" fill="none" stroke="`+gold+`" stroke-width="3"/><path d="M70,360 h30 M85,345 v30" stroke="`+gold+`" stroke-width="2"/>`)
			}
			if t >= 3 { // translator earpiece
				w(b, `<circle cx="302" cy="208" r="8" fill="`+gold+`" stroke="`+ink+`" stroke-width="2"/>`)
			}
		},
	},
	"athlete": {
		body: func(b *strings.Builder, d dna, t int) { // jersey
			w(b, `<path d="M170,300 L200,332 L230,300" fill="none" stroke="`+coat+`" stroke-width="7"/>`+
				`<path d="M92,350 L118,318 M308,350 L282,318" stroke="`+coat+`" stroke-width="7"/>`)
			if t < 3 {
				w(b, `<rect x="182" y="346" width="36" height="36" rx="4" fill="`+coat+`"/><rect x="196" y="352" width="8" height="24" fill="`+ink+`"/>`)
			}
			if t >= 2 { // whistle on a cord
				w(b, `<path d="M180,302 Q160,340 150,362" fill="none" stroke="`+red+`" stroke-width="3"/><rect x="134" y="360" width="26" height="14" rx="7" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/>`)
			}
			if t >= 3 { // gold medal
				w(b, `<path d="M182,302 L200,348 L218,302" fill="none" stroke="`+red+`" stroke-width="8"/><circle cx="200" cy="360" r="18" fill="`+gold+`" stroke="`+ink+`" stroke-width="3"/><path d="M200,350 l3,7 l7,0 l-6,4 l2,7 l-6,-4 l-6,4 l2,-7 l-6,-4 l7,0 Z" fill="`+goldDeep+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // sweatband + ball
			h1, h2 := chordW(118, headR+1), chordW(138, headR+1)
			f(b, `<path d="M%s,118 L%s,118 L%s,138 L%s,138 Z" fill="%s"/><path d="M%s,128 L%s,128" stroke="%s" stroke-width="4"/>`,
				p(cx-h1), p(cx+h1), p(cx+h2), p(cx-h2), coat, p(cx-h2+2), p(cx+h2-2), d.sig)
			w(b, `<circle cx="320" cy="350" r="32" fill="`+white+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M320,338 L331,346 L327,359 L313,359 L309,346 Z" fill="`+ink+`"/>`+
				`<path d="M320,338 L320,318 M331,346 L350,340 M327,359 L338,376 M313,359 L302,376 M309,346 L290,340" stroke="`+ink+`" stroke-width="3"/>`)
		},
	},
	"artist": {
		hat: true,
		back: func(b *strings.Builder, d dna, t int) { // brush behind the right ear
			w(b, `<g transform="rotate(26 312 180)"><rect x="306" y="96" width="10" height="104" rx="4" fill="`+wood+`" stroke="`+ink+`" stroke-width="2"/>`+
				`<rect x="304" y="196" width="14" height="14" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/><path d="M304,210 L318,210 L311,232 Z" fill="`+cobalt+`"/></g>`)
		},
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			if t >= 3 { // paint splatters on the smock
				w(b, `<circle cx="120" cy="352" r="8" fill="`+red+`"/><circle cx="138" cy="372" r="5" fill="`+gold+`"/><circle cx="270" cy="344" r="7" fill="`+teal+`"/><circle cx="288" cy="368" r="5" fill="`+cobalt+`"/><circle cx="246" cy="382" r="4" fill="`+red+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // paint-stained beret
			w(b, `<ellipse cx="208" cy="110" rx="102" ry="28" fill="`+red+`" transform="rotate(10 208 110)"/><circle cx="212" cy="80" r="8" fill="`+red+`"/>`+
				`<circle cx="160" cy="104" r="7" fill="`+gold+`"/><circle cx="174" cy="96" r="4" fill="`+gold+`"/><circle cx="246" cy="116" r="6" fill="`+cobalt+`"/><circle cx="268" cy="122" r="4" fill="`+teal+`"/>`)
			if t >= 2 { // palette
				w(b, `<path d="M44,360 C42,334 72,320 100,326 C124,332 132,352 118,360 C108,366 112,384 92,388 C66,392 46,384 44,360 Z" fill="#D9B382" stroke="`+ink+`" stroke-width="3"/>`+
					`<circle cx="66" cy="344" r="6" fill="`+red+`"/><circle cx="86" cy="338" r="6" fill="`+cobalt+`"/><circle cx="106" cy="346" r="6" fill="`+gold+`"/><circle cx="62" cy="366" r="6" fill="`+teal+`"/><circle cx="86" cy="366" r="6" fill="`+coat+`"/>`)
			}
		},
	},
	"musician": {
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, ink)
			if t >= 3 { // studded collar
				w(b, `<circle cx="140" cy="318" r="4" fill="`+gold+`"/><circle cx="156" cy="310" r="4" fill="`+gold+`"/><circle cx="244" cy="310" r="4" fill="`+gold+`"/><circle cx="260" cy="318" r="4" fill="`+gold+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // big headphones + guitar
			band := ink
			if t >= 3 {
				band = gold
			}
			headset(b, band, ink, false)
			w(b, `<g transform="rotate(28 330 320)"><rect x="324" y="200" width="14" height="110" fill="`+brown+`"/><rect x="320" y="190" width="22" height="20" rx="4" fill="`+ink+`"/></g>`+
				`<circle cx="306" cy="352" r="34" fill="#C8743A" stroke="`+ink+`" stroke-width="3"/><circle cx="292" cy="320" r="24" fill="#C8743A" stroke="`+ink+`" stroke-width="3"/>`+
				`<circle cx="300" cy="340" r="9" fill="`+ink+`"/><rect x="290" y="366" width="24" height="6" rx="2" fill="`+ink+`"/>`)
			if t >= 2 { // humming
				w(b, `<circle cx="52" cy="104" r="8" fill="`+ink+`"/><rect x="56" y="62" width="5" height="42" fill="`+ink+`"/><path d="M56,62 q14,4 16,18" fill="none" stroke="`+ink+`" stroke-width="5"/>`+
					`<circle cx="86" cy="70" r="6" fill="`+ink+`"/><rect x="89" y="40" width="4" height="30" fill="`+ink+`"/>`)
			}
		},
	},
	"chef": {
		outfit: coat,
		hat:    true,
		face: func(b *strings.Builder, d dna, t int) { // a flour smudge on one cheek
			f(b, `<path d="M%s,%s q8,-6 18,-2 q6,4 -2,8 q-10,4 -16,-6 Z" fill="#FFFFFF" fill-opacity="0.7"/><circle cx="%s" cy="%s" r="2" fill="#FFFFFF" fill-opacity="0.7"/>`,
				p(cx-d.eyeSpace-14), p(eyeY(d)+40), p(cx-d.eyeSpace+10), p(eyeY(d)+48))
		},
		body: func(b *strings.Builder, d dna, t int) {
			f(b, `<path d="M176,300 L224,300 L200,328 Z" fill="%s"/>`, red)
			w(b, `<circle cx="178" cy="344" r="5" fill="`+ink+`"/><circle cx="178" cy="372" r="5" fill="`+ink+`"/><circle cx="222" cy="344" r="5" fill="`+ink+`"/><circle cx="222" cy="372" r="5" fill="`+ink+`"/>`+
				`<path d="M150,304 L176,300 M250,304 L224,300" stroke="`+silver+`" stroke-width="3"/>`)
			if t >= 3 { // gold star pin
				w(b, `<path d="M118,334 l4,9 l10,0 l-8,6 l3,10 l-9,-6 l-9,6 l3,-10 l-8,-6 l10,0 Z" fill="`+gold+`" stroke="`+ink+`" stroke-width="2"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // toque
			for _, c := range [][3]float64{{152, 92, 32}, {248, 92, 32}, {200, 70, 40}} {
				f(b, `<circle cx="%s" cy="%s" r="%s" fill="%s" stroke="%s" stroke-width="3"/>`, p(c[0]), p(c[1]), p(c[2]), white, ink)
			}
			w(b, `<rect x="124" y="98" width="152" height="40" rx="6" fill="`+white+`" stroke="`+ink+`" stroke-width="3"/>`)
			if t >= 3 {
				w(b, `<path d="M156,104 v28 M180,104 v28 M204,104 v28 M228,104 v28 M252,104 v28" stroke="`+silver+`" stroke-width="3"/>`)
			}
			if t >= 2 { // wooden spoon
				w(b, `<g transform="rotate(18 330 350)"><ellipse cx="330" cy="318" rx="14" ry="20" fill="`+wood+`" stroke="`+ink+`" stroke-width="3"/><rect x="325" y="336" width="10" height="60" rx="4" fill="`+wood+`" stroke="`+ink+`" stroke-width="3"/></g>`)
			}
		},
	},
	"doctor": {
		outfit: coat,
		body: func(b *strings.Builder, d dna, t int) {
			f(b, `<path d="M168,300 L200,346 L232,300 Z" fill="%s"/>`, teal)
			w(b, `<path d="M168,300 L200,346 L182,400 M232,300 L200,346 L218,400" fill="none" stroke="`+silver+`" stroke-width="4"/>`+
				`<path d="M146,304 Q142,372 200,376 Q258,372 254,304" fill="none" stroke="`+ink+`" stroke-width="6"/>`+
				`<path d="M200,376 L200,384" stroke="`+ink+`" stroke-width="5"/><circle cx="200" cy="388" r="10" fill="`+silver+`" stroke="`+ink+`" stroke-width="4"/>`)
			w(b, `<path d="M110,350 h26 v12 h-26 Z" fill="none" stroke="`+silver+`" stroke-width="3"/><path d="M118,350 v-16" stroke="`+cobalt+`" stroke-width="4" stroke-linecap="round"/>`) // pen in the coat pocket
			if t >= 3 {                                                                                                                                                                       // gold caduceus pin
				w(b, `<path d="M278,328 v20 M272,334 q6,4 12,0 M272,342 q6,-4 12,0" fill="none" stroke="`+gold+`" stroke-width="3" stroke-linecap="round"/><circle cx="278" cy="327" r="3" fill="`+gold+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) {
			if t >= 3 { // medical chart on a clipboard
				w(b, `<rect x="290" y="320" width="66" height="72" rx="6" fill="`+brown+`" stroke="`+ink+`" stroke-width="3"/><rect x="298" y="330" width="50" height="56" fill="`+paper+`"/>`+
					`<rect x="312" y="314" width="22" height="12" rx="3" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/><path d="M304,358 l8,0 l4,-10 l6,20 l5,-10 l15,0" fill="none" stroke="`+red+`" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"/>`)
			}
			if t >= 2 { // head mirror
				hw := chordW(120, headR)
				f(b, `<path d="M%s,120 L%s,120" stroke="%s" stroke-width="6"/>`, p(cx-hw), p(cx+hw), ink)
				w(b, `<circle cx="200" cy="116" r="18" fill="`+silver+`" stroke="`+ink+`" stroke-width="4"/><circle cx="200" cy="116" r="5" fill="`+ink+`"/>`)
			}
		},
	},
	"naturalist": {
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			// butterfly on the left shoulder
			w(b, `<ellipse cx="104" cy="318" rx="14" ry="10" fill="`+orange+`" transform="rotate(-30 104 318)"/><ellipse cx="124" cy="318" rx="14" ry="10" fill="`+orange+`" transform="rotate(30 124 318)"/>`+
				`<ellipse cx="106" cy="334" rx="9" ry="7" fill="`+gold+`"/><ellipse cx="122" cy="334" rx="9" ry="7" fill="`+gold+`"/><rect x="112" y="312" width="4" height="28" rx="2" fill="`+ink+`"/>`)
			if t >= 2 { // binoculars
				w(b, `<path d="M180,302 L176,340 M220,302 L224,340" stroke="`+brown+`" stroke-width="3"/>`+
					`<rect x="166" y="338" width="28" height="34" rx="8" fill="`+ink+`"/><rect x="206" y="338" width="28" height="34" rx="8" fill="`+ink+`"/><rect x="190" y="346" width="20" height="10" fill="`+ink+`"/>`+
					`<circle cx="180" cy="366" r="7" fill="#7FA8C9"/><circle cx="220" cy="366" r="7" fill="#7FA8C9"/>`)
			}
			if t >= 3 { // a bird on the right shoulder
				w(b, `<path d="M276,318 L258,326 L274,332 Z" fill="`+cobalt+`"/><ellipse cx="296" cy="318" rx="22" ry="14" fill="`+cobalt+`"/><circle cx="314" cy="304" r="10" fill="`+cobalt+`"/>`+
					`<path d="M322,302 L334,306 L322,310 Z" fill="`+gold+`"/><circle cx="316" cy="302" r="2.5" fill="`+white+`"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // leaf crown
			for i, deg := range []float64{160, 138, 116, 98, 82, 64, 42, 20} {
				fill := green
				if i%2 == 1 {
					fill = greenDeep
				}
				leaf(b, deg, headR+2, 18, 8, fill)
			}
			w(b, `<circle cx="136" cy="118" r="7" fill="`+white+`"/><circle cx="136" cy="118" r="3" fill="`+gold+`"/><circle cx="262" cy="114" r="7" fill="`+white+`"/><circle cx="262" cy="114" r="3" fill="`+gold+`"/>`)
		},
	},
	"pilot": {
		outfit: brown,
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			if t >= 3 { // fur collar
				w(b, `<path d="M118,318 Q130,300 150,302 Q166,296 180,306 L200,326 L220,306 Q234,296 250,302 Q270,300 282,318 Q240,324 200,350 Q160,324 118,318 Z" fill="`+coat+`"/>`)
			}
			// flowing white scarf
			w(b, `<path d="M140,298 Q200,332 260,298 L266,318 Q200,352 134,318 Z" fill="`+white+`"/><path d="M236,322 C276,336 318,326 352,346 C320,352 290,358 244,344 Z" fill="`+white+`"/>`)
			if t >= 2 { // wings pin
				w(b, `<path d="M106,352 Q122,344 132,352 Q142,344 158,352 Q142,358 132,356 Q122,358 106,352 Z" fill="`+gold+`" stroke="`+goldDeep+`" stroke-width="2"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // aviator cap with goggles up
			w(b, `<rect x="82" y="150" width="26" height="88" rx="13" fill="`+brown+`"/><rect x="292" y="150" width="26" height="88" rx="13" fill="`+brown+`"/>`)
			dome(b, 156, 110, brown, "")
			hw := chordW(122, 110)
			f(b, `<path d="M%s,122 L%s,122" stroke="%s" stroke-width="7"/>`, p(cx-hw), p(cx+hw), ink)
			w(b, `<circle cx="170" cy="118" r="20" fill="`+teal+`" fill-opacity="0.85" stroke="#B8862B" stroke-width="6"/><circle cx="230" cy="118" r="20" fill="`+teal+`" fill-opacity="0.85" stroke="#B8862B" stroke-width="6"/>`)
		},
	},
	"philosopher": {
		outfit: "#F1EBDD",
		body: func(b *strings.Builder, d dna, t int) { // draped toga with a crimson sash over one shoulder
			w(b, `<path d="M92,334 L176,300 L304,400 L234,400 Z" fill="#9E2A2B"/><path d="M120,326 L250,400 M150,314 L276,400" stroke="#7E1F20" stroke-width="2"/>`)
		},
		head: func(b *strings.Builder, d dna, t int) { // laurel wreath
			c := "#7A9A3A"
			if t >= 3 {
				c = gold
			}
			for _, deg := range []float64{205, 186, 167, 148, 129, 111, -25, -6, 13, 32, 51, 69} {
				leaf(b, deg, headR-2, 15, 7, c)
			}
			if t >= 2 { // a well-thumbed book
				w(b, `<rect x="50" y="332" width="74" height="58" rx="5" fill="`+brown+`" stroke="`+ink+`" stroke-width="3"/><rect x="58" y="340" width="58" height="42" fill="none" stroke="`+gold+`" stroke-width="2"/>`)
			}
			if t >= 3 { // column
				w(b, `<rect x="290" y="324" width="56" height="10" rx="2" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/><rect x="298" y="334" width="40" height="48" fill="`+coat+`" stroke="`+ink+`" stroke-width="2"/>`+
					`<path d="M310,336 v44 M318,336 v44 M326,336 v44" stroke="`+silver+`" stroke-width="2"/><rect x="290" y="382" width="56" height="10" rx="2" fill="`+silver+`" stroke="`+ink+`" stroke-width="2"/>`)
			}
		},
	},
	"judge": {
		outfit: ink,
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) {
			w(b, `<rect x="189" y="302" width="10" height="36" fill="`+white+`"/><rect x="201" y="302" width="10" height="36" fill="`+white+`"/>`)
		},
		head: func(b *strings.Builder, d dna, t int) { // white curled wig
			const wig = "#F4F1EA"
			for _, y := range []float64{150, 174, 198, 222, 246} {
				for _, x := range []float64{cx - headR - 2, cx + headR + 2} {
					f(b, `<circle cx="%s" cy="%s" r="15" fill="%s" stroke="#B9B2A6" stroke-width="3"/>`, p(x), p(y), wig)
				}
			}
			dome(b, 128, 108, wig, ` stroke="#B9B2A6" stroke-width="3"`)
			for x := cx - 72.0; x <= cx+72; x += 24 {
				f(b, `<circle cx="%s" cy="128" r="12" fill="%s" stroke="#B9B2A6" stroke-width="3"/>`, p(x), wig)
			}
			if t >= 2 { // gavel
				w(b, `<g transform="rotate(-30 318 352)"><rect x="290" y="332" width="56" height="24" rx="6" fill="`+wood+`" stroke="`+ink+`" stroke-width="3"/><rect x="312" y="356" width="12" height="44" rx="4" fill="`+wood+`" stroke="`+ink+`" stroke-width="3"/></g>`)
			}
			if t >= 3 { // scales
				w(b, `<path d="M88,322 L88,384 M68,388 h40 M56,330 h64" stroke="`+gold+`" stroke-width="4" stroke-linecap="round"/>`+
					`<path d="M56,330 L48,352 M56,330 L64,352 M120,330 L112,352 M120,330 L128,352" stroke="`+gold+`" stroke-width="2"/>`+
					`<path d="M46,352 A10,10 0 0 0 66,352 Z M110,352 A10,10 0 0 0 130,352 Z" fill="`+gold+`"/>`)
			}
		},
	},
	"engineer": {
		outfit: orange,
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) { // hi-vis vest
			w(b, `<path d="M168,300 L200,346 L232,300" fill="none" stroke="`+coat+`" stroke-width="10"/><rect x="70" y="360" width="260" height="10" fill="`+coat+`"/>`)
		},
		head: func(b *strings.Builder, d dna, t int) { // ribbed hard hat, tipped back
			tilt(b, -6, 200, 128, func() {
				dome(b, 132, 110, gold, ` stroke="`+ink+`" stroke-width="4"`)
				w(b, `<path d="M150,128 Q156,100 176,90 M250,128 Q244,100 224,90" fill="none" stroke="`+goldDeep+`" stroke-width="4" stroke-linecap="round"/>`+
					`<rect x="190" y="88" width="20" height="44" rx="6" fill="`+goldDeep+`" stroke="`+ink+`" stroke-width="3"/>`+
					`<rect x="84" y="128" width="232" height="12" rx="6" fill="`+goldDeep+`" stroke="`+ink+`" stroke-width="3"/>`)
			})
			if t >= 2 { // wrench
				w(b, `<g transform="rotate(35 322 352)"><rect x="315" y="320" width="14" height="72" rx="5" fill="`+silver+`" stroke="`+ink+`" stroke-width="3"/>`+
					`<path d="M308,300 a16,16 0 1 0 28,0 l-6,12 l-16,0 Z" fill="`+silver+`" stroke="`+ink+`" stroke-width="3"/></g>`)
			}
			if t >= 3 { // blueprint roll
				w(b, `<rect x="44" y="340" width="96" height="30" rx="15" fill="#3DA5D9" stroke="`+ink+`" stroke-width="3"/><path d="M60,350 h50 M60,360 h36" stroke="`+white+`" stroke-width="2"/><circle cx="125" cy="355" r="8" fill="#2C84B0"/>`)
			}
		},
	},
	"grandmaster": {
		hat: true,
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, coat)
			if t >= 2 { // checkered collar
				for i := 0; i < 6; i++ {
					c := ink
					if i%2 == 1 {
						c = white
					}
					f(b, `<rect x="%d" y="304" width="14" height="14" fill="%s"/>`, 158+i*14, c)
				}
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // crown + knight
			tilt(b, 10, 200, 110, func() {
				w(b, `<path d="M150,126 L144,88 L172,106 L200,76 L228,106 L256,88 L250,126 Z" fill="`+gold+`" stroke="`+ink+`" stroke-width="4" stroke-linejoin="round"/>`+
					`<circle cx="200" cy="112" r="6" fill="`+red+`"/><circle cx="168" cy="116" r="4" fill="`+cobalt+`"/><circle cx="232" cy="116" r="4" fill="`+cobalt+`"/>`)
			})
			w(b, `<path d="M296,392 L362,392 L358,378 L352,378 C354,348 346,322 328,310 C318,302 304,304 296,312 L286,326 L294,334 L308,330 C304,344 298,360 302,378 L298,378 Z" fill="`+ink+`"/><circle cx="318" cy="320" r="3" fill="`+white+`"/>`)
			if t >= 3 { // trophy
				w(b, `<path d="M58,326 L118,326 L114,354 Q110,368 88,368 Q66,368 62,354 Z" fill="`+gold+`" stroke="`+ink+`" stroke-width="3"/>`+
					`<path d="M60,334 Q44,334 48,348 Q52,356 62,354 M116,334 Q132,334 128,348 Q124,356 114,354" fill="none" stroke="`+ink+`" stroke-width="3"/>`+
					`<rect x="82" y="368" width="12" height="12" fill="`+ink+`"/><rect x="66" y="380" width="44" height="10" rx="2" fill="`+ink+`"/>`)
			}
		},
	},
	"gamer": {
		body: func(b *strings.Builder, d dna, t int) {
			vNeck(b, ink)
			if t >= 2 { // esports jersey stripes
				w(b, `<path d="M70,380 L130,320 M90,400 L150,340 M330,380 L270,320 M310,400 L250,340" stroke="`+coat+`" stroke-width="6"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // cat-ear headset + controller
			headset(b, ink, d.sig, true)
			f(b, `<path d="M118,112 L126,64 L160,94 Z M282,112 L274,64 L240,94 Z" fill="%s" stroke="%s" stroke-width="4" stroke-linejoin="round"/>`, d.sig, ink)
			w(b, `<path d="M282,334 L346,334 Q362,334 362,352 L360,368 Q356,386 340,378 L332,370 L296,370 L288,378 Q272,386 268,368 L266,352 Q266,334 282,334 Z" fill="`+ink+`"/>`+
				`<path d="M286,344 v16 M278,352 h16" stroke="`+white+`" stroke-width="4"/><circle cx="336" cy="346" r="4" fill="`+red+`"/><circle cx="346" cy="356" r="4" fill="`+gold+`"/>`)
			if t >= 3 { // trophy
				w(b, `<path d="M62,326 L114,326 L110,352 Q106,364 88,364 Q70,364 66,352 Z" fill="`+gold+`" stroke="`+ink+`" stroke-width="3"/><rect x="82" y="364" width="12" height="14" fill="`+ink+`"/><rect x="68" y="378" width="40" height="10" rx="2" fill="`+ink+`"/>`)
			}
		},
	},
	"astronaut": {
		outfit: coat,
		body: func(b *strings.Builder, d dna, t int) {
			w(b, `<path d="M118,310 Q200,340 282,310 L288,332 Q200,364 112,332 Z" fill="`+silver+`"/>`+
				`<rect x="102" y="352" width="32" height="20" fill="`+cobalt+`"/><rect x="102" y="352" width="32" height="6" fill="`+red+`"/>`+
				`<rect x="178" y="356" width="44" height="26" rx="4" fill="`+charcoal+`"/><circle cx="190" cy="369" r="4" fill="`+red+`"/><circle cx="210" cy="369" r="4" fill="`+gold+`"/>`)
			if t >= 2 { // mission patch
				f(b, `<circle cx="282" cy="356" r="16" fill="%s" stroke="%s" stroke-width="3"/><path d="M282,346 l3,7 l7,0 l-6,4 l2,7 l-6,-4 l-6,4 l2,-7 l-6,-4 l7,0 Z" fill="%s"/>`, d.sig, gold, gold)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // helmet framing the face
			f(b, `<circle cx="200" cy="196" r="%s" fill="%s" fill-opacity="0.12" stroke="%s" stroke-width="16"/>`, p(headR+24), white, coat)
			f(b, `<path d="M124,140 A98,98 0 0 1 168,104" fill="none" stroke="%s" stroke-width="8" stroke-linecap="round" opacity="0.8"/>`, white)
			if t >= 3 { // gold visor rim + a star
				f(b, `<circle cx="200" cy="196" r="%s" fill="none" stroke="%s" stroke-width="4"/>`, p(headR+16), gold)
				w(b, `<path d="M340,52 l5,12 l13,1 l-10,8 l3,13 l-11,-7 l-11,7 l3,-13 l-10,-8 l13,-1 Z" fill="`+gold+`"/>`)
			}
		},
	},
	"trendsetter": {
		body: func(b *strings.Builder, d dna, t int) { // popped collar
			vNeck(b, coat)
			f(b, `<path d="M136,302 L168,298 L188,344 Z M264,302 L232,298 L212,344 Z" fill="%s"/>`, shade(d.sig, 0.75))
			if t >= 3 { // gold chain
				w(b, `<path d="M164,306 Q200,356 236,306" fill="none" stroke="`+gold+`" stroke-width="4" stroke-dasharray="6 3"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // sunglasses up in the hair + hoops
			tilt(b, -7, 200, 120, func() {
				f(b, `<path d="M146,112 L190,112 L186,132 Q170,140 150,130 Z M254,112 L210,112 L214,132 Q230,140 250,130 Z" fill="%s" stroke="%s" stroke-width="4" stroke-linejoin="round"/>`, ink, pink)
				w(b, `<path d="M190,116 Q200,110 210,116" fill="none" stroke="`+pink+`" stroke-width="4"/>`)
			})
			w(b, `<circle cx="98" cy="236" r="11" fill="none" stroke="`+gold+`" stroke-width="5"/><circle cx="302" cy="236" r="11" fill="none" stroke="`+gold+`" stroke-width="5"/>`)
			if t >= 2 { // shopping bag
				w(b, `<path d="M306,330 q0,-20 18,-20 q18,0 18,20" fill="none" stroke="`+ink+`" stroke-width="4"/><rect x="290" y="328" width="68" height="62" rx="4" fill="`+pink+`" stroke="`+ink+`" stroke-width="3"/>`)
			}
			if t >= 3 { // sparkle
				w(b, `<path d="M58,70 Q61,84 74,86 Q61,88 58,102 Q55,88 42,86 Q55,84 58,70 Z M346,70 Q348,78 356,80 Q348,82 346,90 Q344,82 336,80 Q344,78 346,70 Z" fill="`+gold+`"/>`)
			}
		},
	},
	"topper": {
		outfit: ink,
		hat:    true,
		body: func(b *strings.Builder, d dna, t int) { // stole
			f(b, `<path d="M168,302 L146,400 L174,400 L190,310 Z M232,302 L254,400 L226,400 L210,310 Z" fill="%s"/>`, d.sig)
			if t >= 3 { // gold medal
				w(b, `<circle cx="200" cy="364" r="16" fill="`+gold+`" stroke="`+goldDeep+`" stroke-width="3"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // mortarboard
			w(b, `<path d="M140,126 L140,102 Q200,92 260,102 L260,126 Q200,138 140,126 Z" fill="`+ink+`"/>`+
				`<path d="M200,58 L302,86 L200,114 L98,86 Z" fill="`+ink+`"/>`+
				`<path d="M200,86 L290,94 L290,134" fill="none" stroke="`+gold+`" stroke-width="4"/><rect x="284" y="132" width="12" height="20" rx="3" fill="`+gold+`"/>`)
			if t >= 2 { // diploma
				w(b, `<rect x="48" y="350" width="88" height="26" rx="13" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/><rect x="86" y="348" width="12" height="30" fill="`+red+`"/>`)
			}
		},
	},
	"scholar": {
		hat: true,
		body: func(b *strings.Builder, d dna, t int) { // cardigan
			vNeck(b, coat)
			w(b, `<path d="M168,300 L200,352 L200,400 M232,300 L200,352" fill="none" stroke="`+ink+`" stroke-opacity="0.25" stroke-width="4"/>`+
				`<circle cx="210" cy="366" r="4" fill="`+ink+`" opacity="0.5"/><circle cx="210" cy="388" r="4" fill="`+ink+`" opacity="0.5"/>`)
			if t >= 2 { // owl friend on the shoulder
				w(b, `<ellipse cx="100" cy="316" rx="20" ry="24" fill="#8C6A43"/><circle cx="92" cy="308" r="7" fill="`+white+`"/><circle cx="108" cy="308" r="7" fill="`+white+`"/>`+
					`<circle cx="92" cy="308" r="3" fill="`+ink+`"/><circle cx="108" cy="308" r="3" fill="`+ink+`"/><path d="M96,316 L104,316 L100,322 Z" fill="`+gold+`"/><path d="M84,294 L88,284 L94,294 M106,294 L112,284 L116,294" fill="#8C6A43"/>`)
			}
		},
		head: func(b *strings.Builder, d dna, t int) { // slouchy knit beanie + open book
			tilt(b, -10, 200, 120, func() {
				dome(b, 136, 108, shade(d.sig, 0.8), "")
				hw := chordW(136, 108)
				f(b, `<rect x="%s" y="124" width="%s" height="22" rx="11" fill="%s"/><circle cx="200" cy="82" r="14" fill="%s"/>`,
					p(cx-hw-4), p(2*hw+8), d.sig, d.sig)
				if t >= 3 {
					w(b, `<path d="M240,104 l4,8 l9,1 l-7,6 l2,9 l-8,-5 l-8,5 l2,-9 l-7,-6 l9,-1 Z" fill="`+gold+`"/>`)
				}
			})
			w(b, `<path d="M276,342 Q300,332 320,342 L320,388 Q300,378 276,388 Z" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M320,342 Q340,332 364,342 L364,388 Q340,378 320,388 Z" fill="`+paper+`" stroke="`+ink+`" stroke-width="3"/>`+
				`<path d="M284,354 h28 M284,364 h22 M328,354 h28 M328,364 h22" stroke="#B9B2A6" stroke-width="3"/>`)
		},
	},
}

// --- shared pieces ----------------------------------------------------------

func w(b *strings.Builder, s string)                { b.WriteString(s) }
func f(b *strings.Builder, format string, a ...any) { fmt.Fprintf(b, format, a...) }

// chordW is the half-width of a head-centred circle of radius r at height y.
func chordW(y, r float64) float64 { return math.Sqrt(r*r - (cy-y)*(cy-y)) }

// dome fills the cap of a head-centred circle of radius r above height y: a hat crown.
func dome(b *strings.Builder, y, r float64, fill, extra string) {
	hw := chordW(y, r)
	f(b, `<path d="M%s,%s A%s,%s 0 0 1 %s,%s Z" fill="%s"%s/>`, p(cx-hw), p(y), p(r), p(r), p(cx+hw), p(y), fill, extra)
}

func vNeck(b *strings.Builder, fill string) {
	f(b, `<path d="M166,300 L200,348 L234,300 Z" fill="%s"/>`, fill)
}

func lapels(b *strings.Builder, fill string) {
	f(b, `<path d="M162,301 L200,350 L182,382 L136,318 Z M238,301 L200,350 L218,382 L264,318 Z" fill="%s"/>`, fill)
}

// tilt draws inside a rotation: hats and glasses worn at a jaunty angle.
func tilt(b *strings.Builder, deg, x, y float64, draw func()) {
	f(b, `<g transform="rotate(%s %s %s)">`, p(deg), p(x), p(y))
	draw()
	w(b, `</g>`)
}

// roundGlasses frames both eyes with round rims.
func roundGlasses(b *strings.Builder, d dna, rim string) {
	ey, r := eyeY(d), d.eyeR+8
	for _, x := range []float64{cx - d.eyeSpace, cx + d.eyeSpace} {
		f(b, `<circle cx="%s" cy="%s" r="%s" fill="#FFFFFF" fill-opacity="0.15" stroke="%s" stroke-width="3"/>`, p(x), p(ey), p(r), rim)
	}
	f(b, `<path d="M%s,%s Q200,%s %s,%s M%s,%s L100,%s M%s,%s L300,%s" fill="none" stroke="%s" stroke-width="3" stroke-linecap="round"/>`,
		p(cx-d.eyeSpace+r), p(ey), p(ey-8), p(cx+d.eyeSpace-r), p(ey),
		p(cx-d.eyeSpace-r), p(ey), p(ey-4), p(cx+d.eyeSpace+r), p(ey), p(ey-4), rim)
}

// cardigan opens over the shirt: two front edges and a button.
func cardigan(b *strings.Builder, d dna) {
	f(b, `<path d="M166,300 L200,352 L196,400 M234,300 L200,352" fill="none" stroke="%s" stroke-width="5"/><circle cx="208" cy="372" r="4" fill="%s"/>`,
		shade(d.sig, 0.7), shade(d.sig, 0.6))
}

func scarf(b *strings.Builder, fill string) {
	f(b, `<path d="M136,296 Q200,334 264,296 L270,322 Q200,360 130,322 Z" fill="%s"/><rect x="204" y="320" width="32" height="74" rx="8" fill="%s" transform="rotate(6 220 357)"/>`, fill, fill)
}

// headset: a band over the head with ear cups; mic adds a boom to the mouth's side.
func headset(b *strings.Builder, band, cup string, mic bool) {
	const r = headR + 8
	f(b, `<path d="M%s,196 A%s,%s 0 0 1 %s,196" fill="none" stroke="%s" stroke-width="13"/>`, p(cx-r), p(r), p(r), p(cx+r), band)
	if mic {
		f(b, `<path d="M%s,224 Q%s,276 150,262" fill="none" stroke="%s" stroke-width="5"/><circle cx="152" cy="262" r="7" fill="%s"/>`, p(cx-r), p(cx-r+16), ink, ink)
	}
	for _, x := range []float64{cx - r - 14, cx + r - 14} {
		f(b, `<rect x="%s" y="168" width="28" height="62" rx="12" fill="%s" stroke="%s" stroke-width="4"/>`, p(x), cup, ink)
	}
}

// leaf draws one leaf on the head-centred circle of radius r at angle deg
// (degrees, 0 = right, counter-clockwise), aligned with the circle's tangent.
func leaf(b *strings.Builder, deg, r, rx, ry float64, fill string) {
	t := deg * math.Pi / 180
	x, y := cx+r*math.Cos(t), cy-r*math.Sin(t)
	f(b, `<ellipse cx="%s" cy="%s" rx="%s" ry="%s" fill="%s" transform="rotate(%s %s %s)"/>`,
		p(x), p(y), p(rx), p(ry), fill, p(90-deg), p(x), p(y))
}
