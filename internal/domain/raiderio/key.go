package raiderio

import (
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"kipet-wow-analyzer/internal/domain/character"
)

// NormalizeKey keeps the spelling needed by the source lookup. The provider
// accepts a realm slug or a realm title. Realm resolution belongs to Raider.IO.
func NormalizeKey(region, realm, name string) (Key, error) {
	region = strings.ToLower(strings.TrimSpace(region))
	identity, err := character.NormalizeKey(region, "profile", name)
	if err != nil {
		return Key{}, err
	}
	realm, ok := normalizeRealm(realm, true)
	if !ok {
		return Key{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	return Key{
		Region: region,
		Realm:  realm,
		Name:   norm.NFC.String(strings.ToLower(identity.LookupName)),
	}, nil
}

// NormalizeGuildKey accepts a guild's display name and a Raider.IO realm alias.
func NormalizeGuildKey(region, realm, name string) (Key, error) {
	region = strings.ToLower(strings.TrimSpace(region))
	realm, realmOK := normalizeRealm(realm, true)
	name, nameOK := normalizeGuildName(name)
	if (region != "eu" && region != "us") || !realmOK || !nameOK {
		return Key{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	return Key{Region: region, Realm: realm, Name: name, Kind: "guild"}, nil
}

func normalizeGuildName(value string) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = norm.NFC.String(strings.TrimSpace(value))
	if n := utf8.RuneCountInString(value); n < 2 || n > 100 {
		return "", false
	}
	hasWord := false
	for _, ch := range value {
		if strings.ContainsRune("/\\%?#", ch) {
			return "", false
		}
		switch {
		case unicode.IsLetter(ch), unicode.IsDigit(ch):
			hasWord = true
		case ch == ' ', unicode.IsMark(ch), unicode.IsPunct(ch), unicode.IsSymbol(ch):
		default:
			return "", false
		}
	}
	return norm.NFC.String(strings.ToLower(value)), hasWord
}

func normalizeRealm(value string, allowSpaces bool) (string, bool) {
	if !utf8.ValidString(value) {
		return "", false
	}
	value = norm.NFC.String(strings.TrimSpace(value))
	if n := utf8.RuneCountInString(value); n < 1 || n > 80 {
		return "", false
	}
	hasWord := false
	for _, ch := range value {
		switch {
		case unicode.IsLetter(ch), unicode.IsDigit(ch):
			hasWord = true
		case unicode.IsMark(ch), ch == '-', ch == '\'', ch == '’':
		case allowSpaces && ch == ' ':
		default:
			return "", false
		}
	}
	if !hasWord {
		return "", false
	}
	return norm.NFC.String(strings.ToLower(strings.Join(strings.Fields(value), " "))), true
}

// ProfileRealm verifies the source profile's region and name in both the body
// and the official link. The provider supplies the resolved realm slug.
func ProfileRealm(key Key, region, name, title, profileURL string) (Realm, error) {
	invalid := character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	requested, err := character.NormalizeKey(key.Region, "profile", key.Name)
	if err != nil || region != key.Region {
		return Realm{}, invalid
	}
	observed, err := character.NormalizeKey(region, "profile", name)
	if err != nil || observed.Name != requested.Name {
		return Realm{}, invalid
	}
	realm, linkName, err := profileLink("characters", region, title, profileURL)
	if err != nil {
		return Realm{}, err
	}
	linked, err := character.NormalizeKey(region, "profile", linkName)
	if err != nil || linked.Name != requested.Name {
		return Realm{}, invalid
	}
	return realm, nil
}

// GuildRealm checks the guild's display identity and canonical source link.
func GuildRealm(key Key, region, name, title, profileURL string) (Realm, error) {
	invalid := character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	requested, validRequest := normalizeGuildName(key.Name)
	observed, validObserved := normalizeGuildName(name)
	if (region != "eu" && region != "us") || region != key.Region ||
		!validRequest || !validObserved || observed != requested {
		return Realm{}, invalid
	}
	realm, linkName, err := profileLink("guilds", region, title, profileURL)
	if err != nil {
		return Realm{}, err
	}
	linked, validLink := normalizeGuildName(linkName)
	if !validLink || linked != requested {
		return Realm{}, invalid
	}
	return realm, nil
}

func profileLink(collection, region, title, profileURL string) (Realm, string, error) {
	invalid := character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	if _, ok := normalizeRealm(title, true); !ok || len(profileURL) > 1024 {
		return Realm{}, "", invalid
	}
	link, err := url.Parse(profileURL)
	if err != nil || link.Scheme != "https" || link.Host != "raider.io" ||
		link.User != nil || link.RawQuery != "" || link.ForceQuery || link.Fragment != "" {
		return Realm{}, "", invalid
	}
	path := strings.Split(link.EscapedPath(), "/")
	if len(path) != 5 || path[0] != "" || path[1] != collection || path[2] != region {
		return Realm{}, "", invalid
	}
	realmSlug, slugErr := url.PathUnescape(path[3])
	linkName, nameErr := url.PathUnescape(path[4])
	slug, slugOK := normalizeRealm(realmSlug, false)
	if slugErr != nil || nameErr != nil || !slugOK ||
		strings.TrimSpace(realmSlug) != realmSlug || strings.TrimSpace(linkName) != linkName {
		return Realm{}, "", invalid
	}
	return Realm{Name: norm.NFC.String(strings.TrimSpace(title)), Slug: slug}, linkName, nil
}

const (
	FreshFor = 15 * time.Minute
	KeepFor  = 24 * time.Hour
)

// ValidateSnapshot also protects callers that persist or publish normalized data.
func ValidateSnapshot(key Key, value Snapshot) error {
	invalid := character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	if value.FetchedAt.IsZero() ||
		!value.FreshUntil.After(value.FetchedAt) || value.FreshUntil.After(value.FetchedAt.Add(FreshFor)) ||
		!value.ValidUntil.After(value.FreshUntil) || value.ValidUntil.After(value.FetchedAt.Add(KeepFor)) {
		return invalid
	}
	switch key.Kind {
	case "":
		p := value.Profile
		realm, err := ProfileRealm(key, p.Region, p.Name, p.Realm.Name, p.ProfileURL)
		if err != nil || realm != p.Realm || value.Guild != nil {
			return invalid
		}
	case "guild":
		p := value.Guild
		if p == nil || value.Profile.Name != "" {
			return invalid
		}
		realm, err := GuildRealm(key, p.Region, p.Name, p.Realm.Name, p.ProfileURL)
		if err != nil || realm != p.Realm {
			return invalid
		}
	default:
		return invalid
	}
	return nil
}
