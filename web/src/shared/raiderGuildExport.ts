import { raiderSourceLink } from './raider.ts';
import type { RaiderGuildProfile } from './raider.ts';
import { summarizeRaiderRoster } from './raiderRoster.ts';
import { exportable } from './social.ts';
import type { Envelope } from './types.ts';

export function makeRaiderGuildExport(profile: Envelope<RaiderGuildProfile | null> | null, expectedSnapshotID: string | null, now = Date.now()) {
  if (!profile?.data || !expectedSnapshotID) return null;
  const { data, meta } = profile;
  if (meta.source !== 'raiderio' || meta.status !== 'fresh' || meta.job_id !== null ||
      meta.snapshot_id !== expectedSnapshotID || !exportable(meta, now) ||
      !meta.fetched_at || !Number.isFinite(Date.parse(meta.fetched_at))) return null;

  const link = raiderSourceLink(data.profile_url, 'guilds');
  if (!link) return null;

  return {
    schema_version: 1,
    source: meta.source,
    guild: {
      name: data.name,
      region: data.region,
      realm: data.realm,
      profile_url: link,
      faction: data.faction,
      last_crawled_at: data.last_crawled_at,
    },
    snapshot: { id: expectedSnapshotID, fetched_at: meta.fetched_at, valid_until: meta.valid_until },
    coverage: data.coverage,
    roster: summarizeRaiderRoster(data.members),
    raids: data.raids,
    rankings: data.rankings,
  };
}
