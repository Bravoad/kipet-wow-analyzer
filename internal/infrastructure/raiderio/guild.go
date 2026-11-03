package raiderio

import (
	"encoding/json"
	"strings"

	"kipet-wow-analyzer/internal/domain/raiderio"
)

const maxGuildMembers = 1000

type wireGuildProfile struct {
	Name          string          `json:"name"`
	Region        string          `json:"region"`
	Realm         string          `json:"realm"`
	ProfileURL    string          `json:"profile_url"`
	Faction       *string         `json:"faction"`
	LastCrawledAt *string         `json:"last_crawled_at"`
	Members       json.RawMessage `json:"members"`
	Raids         json.RawMessage `json:"raid_progression"`
	Rankings      json.RawMessage `json:"raid_rankings"`
}

type wireGuildMember struct {
	Rank      *int         `json:"rank"`
	Character *wireProfile `json:"character"`
}

func normalizeGuildProfile(key raiderio.Key, body []byte) (*raiderio.GuildProfile, error) {
	var raw wireGuildProfile
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, invalid()
	}
	realm, err := raiderio.GuildRealm(key, raw.Region, raw.Name, raw.Realm, raw.ProfileURL)
	if err != nil {
		return nil, err
	}
	guild := &raiderio.GuildProfile{
		Name: strings.TrimSpace(raw.Name), Region: raw.Region, Realm: realm, ProfileURL: raw.ProfileURL,
		Faction: textValue(raw.Faction, 32), LastCrawledAt: timestamp(raw.LastCrawledAt),
		Coverage: make(map[string]raiderio.Coverage, 4),
	}
	if guild.Faction != nil {
		faction := strings.ToLower(*guild.Faction)
		guild.Faction = &faction
	}
	guild.Coverage["profile"] = coverage("complete", "", guild.LastCrawledAt)
	guild.Members, guild.Coverage["members"] = normalizeMembers(guild.Region, raw.Members)
	guild.Raids, guild.Coverage["raids"] = normalizeRaids(raw.Raids)
	guild.Rankings, guild.Coverage["rankings"] = normalizeRankings(raw.Rankings)
	return guild, nil
}

func normalizeMembers(region string, body json.RawMessage) ([]raiderio.GuildMember, raiderio.Coverage) {
	raw, status := decodeOptional[[]json.RawMessage](body)
	if raw == nil {
		return nil, status
	}
	limit := min(len(*raw), maxGuildMembers)
	members := make([]raiderio.GuildMember, 0, limit)
	seen := make(map[raiderio.Key]bool, limit)
	for _, item := range (*raw)[:limit] {
		var rawMember wireGuildMember
		if json.Unmarshal(item, &rawMember) != nil || rawMember.Character == nil {
			continue
		}
		character := rawMember.Character
		if character.Region != region {
			continue
		}
		key, err := raiderio.NormalizeKey(character.Region, character.Realm, character.Name)
		if err != nil {
			continue
		}
		realm, err := raiderio.ProfileRealm(key, character.Region, character.Name, character.Realm, character.ProfileURL)
		if err != nil {
			continue
		}
		key.Realm = realm.Slug
		if seen[key] {
			continue
		}
		seen[key] = true
		key.Name = strings.TrimSpace(character.Name)
		members = append(members, raiderio.GuildMember{
			Key: key, Rank: count(rawMember.Rank, 100), Class: textValue(character.Class, 80),
			Specialization: textValue(character.Specialization, 80), Role: textValue(character.Role, 32),
		})
	}
	if len(members) < len(*raw) {
		status = coverage("partial", "incomplete", nil)
		if len(members) == 0 {
			members = nil
		}
	}
	return members, status
}

func normalizeRankings(body json.RawMessage) (map[string]map[string]raiderio.Rank, raiderio.Coverage) {
	raw, status := decodeOptional[map[string]map[string]*raiderio.Rank](body)
	if raw == nil {
		return nil, status
	}
	const maxRank = 10_000_000
	tiers := sortedKeys(*raw)
	limit := min(len(tiers), 100)
	partial := limit < len(tiers)
	rankings := make(map[string]map[string]raiderio.Rank, limit)
	for _, tier := range tiers[:limit] {
		difficulties := (*raw)[tier]
		if difficulties == nil || !identifier(tier, 100, false) {
			partial = true
			continue
		}
		keys := sortedKeys(difficulties)
		countLimit := min(len(keys), 16)
		byDifficulty := make(map[string]raiderio.Rank, countLimit)
		for _, difficulty := range keys[:countLimit] {
			rank := difficulties[difficulty]
			if rank == nil || !identifier(difficulty, 32, false) {
				continue
			}
			clean := raiderio.Rank{World: count(rank.World, maxRank), Region: count(rank.Region, maxRank), Realm: count(rank.Realm, maxRank)}
			partial = partial || clean != *rank
			byDifficulty[difficulty] = clean
		}
		partial = partial || len(byDifficulty) < len(keys)
		if len(keys) == 0 || len(byDifficulty) > 0 {
			rankings[tier] = byDifficulty
		}
	}
	if partial {
		status = coverage("partial", "incomplete", nil)
		if len(rankings) == 0 {
			rankings = nil
		}
	}
	return rankings, status
}
