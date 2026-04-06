package prefix

import (
	"math/rand/v2"
	"strconv"
)

// Generator produces human-like email prefixes.
//
// Unique prefix capacity per domain (conservative estimate):
//   - Personal patterns: ~420 first x ~420 last x 15 patterns x ~1200 suffixes ≈ 3.2 billion
//   - Nickname patterns: ~170 nicks x 8 patterns x ~1200 suffixes + adj*noun combos ≈ 1.8 billion
//   - Business patterns: ~50 prefixes x ~35 depts x 4 patterns x ~100 suffixes ≈ 700 thousand
//   - Total: > 5 billion unique prefixes per domain
type Generator struct{}

// New creates a new prefix generator.
func New() *Generator {
	return &Generator{}
}

// Generate returns a random human-like email prefix (local part).
// All results are at least 3 characters long.
func (g *Generator) Generate() string {
	for range 10 {
		p := g.generate()
		if len(p) >= 3 {
			return p
		}
	}
	// Fallback: always >= 3 chars
	return firstNames[rand.IntN(len(firstNames))] + "." + lastNames[rand.IntN(len(lastNames))]
}

// GenerateN returns n unique random prefixes. Uniqueness is best-effort;
// with billions of possible outputs, collisions are extremely rare.
func (g *Generator) GenerateN(n int) []string {
	seen := make(map[string]struct{}, n)
	result := make([]string, 0, n)
	attempts := n * 3
	for range attempts {
		if len(result) >= n {
			break
		}
		p := g.Generate()
		if _, dup := seen[p]; !dup {
			seen[p] = struct{}{}
			result = append(result, p)
		}
	}
	return result
}

// GenerateForDomain returns a full email address with a random prefix.
func (g *Generator) GenerateForDomain(domain string) string {
	return g.Generate() + "@" + domain
}

func (g *Generator) generate() string {
	r := rand.IntN(100)
	switch {
	case r < 55: // 55% personal name patterns
		return g.personal()
	case r < 85: // 30% nickname patterns
		return g.nickname()
	default: // 15% business patterns
		return g.business()
	}
}

// --- Personal name patterns ---

func (g *Generator) personal() string {
	first := firstNames[rand.IntN(len(firstNames))]
	last := lastNames[rand.IntN(len(lastNames))]
	suffix := personalSuffix()

	switch rand.IntN(15) {
	case 0, 1, 2, 3: // first.last (most common, ~27%)
		return first + "." + last + suffix
	case 4, 5: // firstlast (~13%)
		return first + last + suffix
	case 6: // first_last (~7%)
		return first + "_" + last + suffix
	case 7: // first-last (~7%)
		return first + "-" + last + suffix
	case 8, 9: // flast - initial+last (~13%)
		return string(first[0]) + last + suffix
	case 10: // f.last (~7%)
		return string(first[0]) + "." + last + suffix
	case 11: // first.l (~7%)
		return first + "." + string(last[0]) + suffix
	case 12: // last.first (~7%)
		return last + "." + first + suffix
	case 13: // lastfirst (~7%)
		return last + first + suffix
	default: // firstname + forced suffix (~7%)
		if suffix == "" {
			suffix = strconv.Itoa(rand.IntN(1000))
		}
		return first + suffix
	}
}

func personalSuffix() string {
	r := rand.IntN(100)
	switch {
	case r < 30: // no suffix
		return ""
	case r < 50: // 1-2 digits: 0-99
		return strconv.Itoa(rand.IntN(100))
	case r < 65: // birth year 2-digit: 50-09 (wrapping)
		y := 50 + rand.IntN(60)
		return twoDigits(y % 100)
	case r < 75: // birth year 4-digit: 1960-2026
		return strconv.Itoa(1960 + rand.IntN(67))
	case r < 88: // 3 digits: 100-999
		return strconv.Itoa(100 + rand.IntN(900))
	default: // 4 digits: 1000-9999
		return strconv.Itoa(1000 + rand.IntN(9000))
	}
}

// --- Nickname patterns ---

func (g *Generator) nickname() string {
	r := rand.IntN(100)
	switch {
	case r < 35: // nickname + number
		nick := nicknames[rand.IntN(len(nicknames))]
		return nick + nickSuffix()
	case r < 55: // adj + noun (joined)
		adj := adjectives[rand.IntN(len(adjectives))]
		noun := nouns[rand.IntN(len(nouns))]
		return adj + noun + nickSuffix()
	case r < 70: // adj_noun
		adj := adjectives[rand.IntN(len(adjectives))]
		noun := nouns[rand.IntN(len(nouns))]
		return adj + "_" + noun + nickSuffix()
	case r < 82: // adj.noun
		adj := adjectives[rand.IntN(len(adjectives))]
		noun := nouns[rand.IntN(len(nouns))]
		return adj + "." + noun + nickSuffix()
	case r < 92: // prefix + nickname
		pre := nickPrefixWords[rand.IntN(len(nickPrefixWords))]
		nick := nicknames[rand.IntN(len(nicknames))]
		return pre + nick
	default: // nickname + tag
		nick := nicknames[rand.IntN(len(nicknames))]
		tag := nickSuffixTags[rand.IntN(len(nickSuffixTags))]
		seps := [2]string{".", "_"}
		return nick + seps[rand.IntN(2)] + tag
	}
}

func nickSuffix() string {
	r := rand.IntN(100)
	switch {
	case r < 20: // no suffix
		return ""
	case r < 45: // 1-2 digits
		return strconv.Itoa(rand.IntN(100))
	case r < 65: // 3 digits
		return strconv.Itoa(100 + rand.IntN(900))
	case r < 80: // 4 digits (year-like)
		return strconv.Itoa(1000 + rand.IntN(9000))
	default: // 4-digit year
		return strconv.Itoa(1960 + rand.IntN(67))
	}
}

// --- Business patterns ---

func (g *Generator) business() string {
	biz := businessPrefixes[rand.IntN(len(businessPrefixes))]

	// Short prefixes always combine with department
	if len(biz) < 3 {
		dept := departments[rand.IntN(len(departments))]
		seps := [2]string{".", "_"}
		return biz + seps[rand.IntN(2)] + dept
	}

	r := rand.IntN(100)
	switch {
	case r < 35: // standalone
		return biz
	case r < 60: // biz.dept
		dept := departments[rand.IntN(len(departments))]
		return biz + "." + dept
	case r < 80: // biz_dept
		dept := departments[rand.IntN(len(departments))]
		return biz + "_" + dept
	default: // biz + number
		return biz + strconv.Itoa(rand.IntN(100))
	}
}

func twoDigits(n int) string {
	if n < 0 {
		n = -n
	}
	n %= 100
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
