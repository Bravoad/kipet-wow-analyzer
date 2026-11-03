package guild

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"kipet-wow-analyzer/internal/domain/character"
)

// NormalizeKey accepts an already canonical provider guild slug. It never
// transliterates a display name or guesses the provider's slug punctuation.
func NormalizeKey(region, realm, slug string) (Key, error) {
	base, err := character.NormalizeKey(region, realm, "Example")
	if err != nil || !utf8.ValidString(slug) {
		return Key{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	slug = norm.NFC.String(strings.ToLower(norm.NFC.String(strings.TrimSpace(slug))))
	if n := utf8.RuneCountInString(slug); n < 2 || n > 100 || strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		return Key{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	for i, r := range slug {
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || r == '-') || i == 0 && unicode.IsMark(r) {
			return Key{}, character.NewFault("INVALID_ARGUMENT", false)
		}
	}
	return Key{Region: region, Realm: base.Realm, Slug: slug}, nil
}
