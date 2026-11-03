package fixture

import (
	"context"
	"time"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func (p *Provider) FetchGuild(ctx context.Context, key guild.Key, realm character.Realm) (guild.Bundle, error) {
	if err := ctx.Err(); err != nil {
		return guild.Bundle{}, character.NewFault("PROVIDER_UNAVAILABLE", true)
	}
	canonical, err := guild.NormalizeKey(key.Region, key.Realm, key.Slug)
	if err != nil || canonical != key || realm.Region != key.Region || realm.Slug != key.Realm {
		return guild.Bundle{}, character.NewFault("INVALID_ARGUMENT", false)
	}
	if key.Region != "eu" || key.Realm != "гордунни" || key.Slug != "kipet-lab" {
		return guild.Bundle{}, character.NewFault("GUILD_UNAVAILABLE", false)
	}
	count, level, leader, memberRank := 3, 80, 0, 5
	dk, mage := "Death Knight", "Mage"
	id := "00000000-0000-4000-8000-000000000000"
	created := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC)
	profile := guild.Profile{ID: id, ProviderID: "900007001", Region: key.Region, Realm: realm.DTO("ru-RU"), Slug: key.Slug, Name: "Synthetic Kipet Laboratory", Faction: "alliance", MemberCount: &count, CreatedAt: &created}
	roster := guild.Roster{GuildID: id, ReportedCount: &count, Members: []guild.Member{
		{Key: guild.CharacterKey{Region: "eu", Realm: "гордунни", Name: "Кипет"}, ProviderID: "900000011", Class: &guild.NamedID{ID: "6", Name: &dk}, Level: &level, Rank: &leader},
		{Key: guild.CharacterKey{Region: "eu", Realm: "гордунни", Name: "Ледник"}, ProviderID: "900000013", Class: &guild.NamedID{ID: "6", Name: &dk}, Level: &level, Rank: &memberRank},
		{Key: guild.CharacterKey{Region: "eu", Realm: "гордунни", Name: "Пропавший"}, ProviderID: "900000099", Class: &guild.NamedID{ID: "8", Name: &mage}, Level: &level, Rank: &memberRank},
	}}
	return guild.Bundle{Profile: profile, Roster: roster}, nil
}
