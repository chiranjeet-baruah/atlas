package utils

import "strings"

// aliasMap maps known variant spellings to one canonical form.
// Extend this map as new variants show up in real extraction output.
var aliasMap = map[string]string{
	"python3":    "python",
	"golang":     "go",
	"postgresql": "postgres",
}

// NormalizeSkills lowercases, trims, de-aliases, and de-duplicates a raw
// skills list so the `skills @>` AND filter isn't defeated by casing or
// variant-spelling noise. Order of first occurrence is preserved.
func NormalizeSkills(raw []string) []string {
	seen := make(map[string]bool)
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			continue
		}
		if canonical, ok := aliasMap[s]; ok {
			s = canonical
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
