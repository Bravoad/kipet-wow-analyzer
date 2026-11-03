package blizzard

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func (c *Client) FetchGuild(ctx context.Context, key guild.Key, realm character.Realm) (guild.Bundle, error) {
	canonical, err := guild.NormalizeKey(key.Region, key.Realm, key.Slug)
	if err != nil || canonical != key || realm.Region != key.Region || realm.Slug != key.Realm || realm.ProviderID == "" {
		return guild.Bundle{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	path := "/data/wow/guild/" + url.PathEscape(realm.Slug) + "/" + url.PathEscape(key.Slug)
	body, _, err := c.get(ctx, key.Region, "profile", path, "profile")
	if err != nil {
		var provider *Error
		if errors.As(err, &provider) && provider.Code == "CHARACTER_UNAVAILABLE" {
			return guild.Bundle{}, character.NewFault("GUILD_UNAVAILABLE", false)
		}
		return guild.Bundle{}, fault(err)
	}
	var raw object
	if decode(body, &raw) != nil {
		return guild.Bundle{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	profile, err := normalizeGuildProfile(raw, key, realm)
	if err != nil {
		return guild.Bundle{}, err
	}
	out := guild.Bundle{Profile: profile, Roster: guild.Roster{GuildID: profile.ID, ReportedCount: profile.MemberCount}}
	body, _, err = c.get(ctx, key.Region, "profile", path+"/roster", "section")
	if err != nil {
		var provider *Error
		if errors.As(err, &provider) && provider.Code == "SECTION_UNAVAILABLE" {
			return out, nil
		}
		return guild.Bundle{}, fault(err)
	}
	if decode(body, &raw) != nil {
		return guild.Bundle{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	roster, err := normalizeGuildRoster(raw, key, realm, profile.ProviderID)
	if err != nil {
		return guild.Bundle{}, err
	}
	roster.GuildID = profile.ID
	if roster.ReportedCount == nil {
		roster.ReportedCount = profile.MemberCount
	}
	out.Roster = roster
	return out, nil
}
func normalizeGuildProfile(raw object, key guild.Key, realm character.Realm) (guild.Profile, error) {
	bad := func() (guild.Profile, error) {
		return guild.Profile{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	id := positiveID(raw["id"])
	name := label(raw["name"])
	sourceRealm := asObject(raw["realm"])
	if id == "" || name == nil || sourceRealm == nil || positiveID(sourceRealm["id"]) != realm.ProviderID {
		return bad()
	}
	if slug, present := sourceRealm["slug"]; present && text(slug) != realm.Slug {
		return bad()
	}
	if slug, present := raw["slug"]; present {
		actual, err := guild.NormalizeKey(key.Region, key.Realm, text(slug))
		if err != nil || actual != key {
			return bad()
		}
	}
	faction := strings.ToLower(text(asObject(raw["faction"])["type"]))
	if faction != "alliance" && faction != "horde" && faction != "neutral" {
		faction = "unknown"
	}
	count := guildInteger(raw["member_count"], 1000)
	if value, present := raw["member_count"]; present && value != nil && count == nil {
		return bad()
	}
	var created *time.Time
	if millis := progressInteger(raw["created_timestamp"]); millis != nil && *millis > 0 && *millis <= 253402300799999 {
		at := time.UnixMilli(*millis).UTC()
		created = &at
	}
	return guild.Profile{ID: "00000000-0000-4000-8000-000000000000", ProviderID: id, Region: key.Region, Realm: realm.DTO("en-US"), Slug: key.Slug, Name: *name, Faction: faction, MemberCount: count, CreatedAt: created}, nil
}
func normalizeGuildRoster(raw object, key guild.Key, realm character.Realm, providerID string) (guild.Roster, error) {
	bad := func() (guild.Roster, error) {
		return guild.Roster{}, character.NewFault("PROVIDER_INVALID_RESPONSE", false)
	}
	if source, present := raw["guild"]; present {
		g := asObject(source)
		if g == nil || positiveID(g["id"]) != providerID {
			return bad()
		}
		if sourceRealm, present := g["realm"]; present {
			r := asObject(sourceRealm)
			if r == nil || positiveID(r["id"]) != realm.ProviderID {
				return bad()
			}
			if slug, present := r["slug"]; present && text(slug) != realm.Slug {
				return bad()
			}
		}
		if slug, present := g["slug"]; present {
			actual, err := guild.NormalizeKey(key.Region, key.Realm, text(slug))
			if err != nil || actual != key {
				return bad()
			}
		}
	}
	out := guild.Roster{ReportedCount: guildInteger(raw["member_count"], 1000)}
	if value, present := raw["member_count"]; present && value != nil && out.ReportedCount == nil {
		return bad()
	}
	value, present := raw["members"]
	if !present || value == nil {
		return out, nil
	}
	list, ok := value.([]any)
	if !ok || len(list) > 1000 {
		return bad()
	}
	out.Members = make([]guild.Member, 0, len(list))
	keys, ids := map[string]bool{}, map[string]bool{}
	for _, value := range list {
		entry := asObject(value)
		source := asObject(entry["character"])
		r := asObject(source["realm"])
		id := positiveID(source["id"])
		realmID := positiveID(r["id"])
		slug := text(r["slug"])
		actual, err := character.NormalizeKey(key.Region, slug, text(source["name"]))
		if id == "" || realmID == "" || err != nil || ids[id] || realmID == realm.ProviderID && slug != realm.Slug || realmID != realm.ProviderID && slug == realm.Slug {
			return bad()
		}
		lookup := actual.Region + "\x00" + actual.Realm + "\x00" + actual.Name
		if keys[lookup] {
			return bad()
		}
		keys[lookup] = true
		ids[id] = true
		class := asObject(source["playable_class"])
		if class == nil {
			class = asObject(source["character_class"])
		}
		var named *guild.NamedID
		if cid := positiveID(class["id"]); cid != "" {
			named = &guild.NamedID{ID: cid, Name: label(class["name"])}
		}
		level := guildInteger(source["level"], 1000)
		if level != nil && *level == 0 {
			level = nil
		}
		out.Members = append(out.Members, guild.Member{Key: guild.CharacterKey{Region: actual.Region, Realm: actual.Realm, Name: actual.LookupName}, ProviderID: id, Class: named, Level: level, Rank: guildInteger(entry["rank"], 1000)})
	}
	return out, nil
}
func guildInteger(value any, max int) *int {
	n := progressInteger(value)
	if n == nil || *n > int64(max) {
		return nil
	}
	v := int(*n)
	return &v
}
