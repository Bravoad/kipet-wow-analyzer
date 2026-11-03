import { raiderSourceLink } from './raider.ts';
import type { RaiderProfile } from './raider.ts';
import { characterKeyID, exportable } from './social.ts';
import type { Envelope } from './types.ts';

type ProfileResult = Envelope<RaiderProfile | null> | null;

export function raiderComparisonContext(profiles: readonly ProfileResult[]) {
  const observed = profiles.flatMap(profile =>
    profile?.data && profile.meta.source === 'raiderio' &&
    (profile.meta.status === 'fresh' || profile.meta.status === 'stale') ? [profile.data] : []
  );
  const identities = observed.map(profile =>
    characterKeyID({ region: profile.region, realm: profile.realm.slug, name: profile.name })
  );
  const duplicateProfiles = new Set(identities).size < identities.length;
  if (observed.length < 2 || observed.length !== profiles.length || duplicateProfiles) {
    return { sameSpecialization: null, sameMythicSeason: null, duplicateProfiles };
  }

  const [first] = observed;
  const sameSpecialization = observed.every(profile => profile.class?.trim() && profile.specialization?.trim())
    ? observed.every(profile => profile.class === first.class && profile.specialization === first.specialization)
    : null;
  const sameMythicSeason = observed.every(profile => profile.mythic_plus?.season?.trim())
    ? observed.every(profile => profile.mythic_plus?.season === first.mythic_plus?.season)
    : null;
  return { sameSpecialization, sameMythicSeason, duplicateProfiles };
}

export function makeRaiderComparisonExport(
  profiles: readonly ProfileResult[],
  expectedSnapshotIDs: readonly (string | null)[],
  now = Date.now(),
) {
  if (profiles.length < 1 || profiles.length > 5 || profiles.length !== expectedSnapshotIDs.length) return null;

  const context = raiderComparisonContext(profiles);
  if (context.duplicateProfiles) return null;

  const characters = [];
  for (const [index, profile] of profiles.entries()) {
    if (!profile?.data) return null;
    const { data, meta } = profile;
    const snapshotID = expectedSnapshotIDs[index];
    if (!snapshotID || meta.source !== 'raiderio' || meta.status !== 'fresh' || meta.job_id !== null ||
        meta.snapshot_id !== snapshotID || !exportable(meta, now) ||
        !meta.fetched_at || !Number.isFinite(Date.parse(meta.fetched_at))) return null;

    const link = raiderSourceLink(data.profile_url, 'characters');
    if (!link) return null;
    characters.push({
      name: data.name,
      region: data.region,
      realm: data.realm,
      profile_url: link,
      class: data.class,
      specialization: data.specialization,
      role: data.role,
      equipped_item_level: data.gear?.equipped_item_level ?? null,
      mythic_plus: data.mythic_plus && { season: data.mythic_plus.season, rating: data.mythic_plus.rating },
      raids: data.raids,
      coverage: data.coverage,
      last_crawled_at: data.last_crawled_at,
      snapshot: { id: snapshotID, fetched_at: meta.fetched_at, valid_until: meta.valid_until },
    });
  }

  return {
    schema_version: 1,
    source: 'raiderio',
    compatibility: {
      same_specialization: context.sameSpecialization,
      same_mythic_season: context.sameMythicSeason,
    },
    characters,
  };
}
