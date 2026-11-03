import { raiderSourceLink } from './raider.ts';
import type { RaiderProfile, RaiderRun } from './raider.ts';
import { raiderComparisonContext } from './raiderComparison.ts';
import { characterKeyID } from './social.ts';
import type { Envelope } from './types.ts';

export interface RaiderDungeonComparison {
  season: string;
  columns: {
    id: string;
    name: string;
    region: RaiderProfile['region'];
    realm: string;
    coverage: 'available' | 'partial' | 'unavailable' | 'empty';
  }[];
  rows: { dungeon: string; cells: RaiderRun[][] }[];
}

export function raiderDungeonComparison(profiles: readonly (Envelope<RaiderProfile> | null)[]): RaiderDungeonComparison | null {
  const season = profiles[0]?.data.mythic_plus?.season;
  if (!season || raiderComparisonContext(profiles).sameMythicSeason !== true) return null;

  const columns: RaiderDungeonComparison['columns'] = [];
  const dungeons = new Map<string, RaiderRun[][]>();
  for (const [index, snapshot] of profiles.entries()) {
    if (!snapshot) return null;
    const profile = snapshot.data;
    const runs = profile.mythic_plus?.best_runs ?? null;
    let partial = profile.coverage.mythic_plus?.state !== 'complete';
    for (const run of runs ?? []) {
      const dungeon = run.dungeon.trim().normalize('NFC');
      const link = raiderSourceLink(run.url, 'mythic-plus-runs');
      const linkedSeason = link?.match(/^https:\/\/raider\.io\/mythic-plus-runs\/([a-z0-9-]{1,80})\/[a-z0-9-]{1,200}$/)?.[1];
      if (!dungeon || !link || link !== run.url || linkedSeason !== season) {
        partial = true;
        continue;
      }
      let cells = dungeons.get(dungeon);
      if (!cells) {
        cells = Array.from({ length: profiles.length }, (): RaiderRun[] => []);
        dungeons.set(dungeon, cells);
      }
      const cell = cells[index];
      if (!cell.some(entry => entry.url === link)) cell.push({ ...run, url: link });
    }

    const coverage = runs === null ? 'unavailable' : !runs.length ? 'empty' : partial ? 'partial' : 'available';
    const id = characterKeyID({ name: profile.name, region: profile.region, realm: profile.realm.slug });
    columns.push({ id, name: profile.name, region: profile.region, realm: profile.realm.name, coverage });
  }

  const rows = [...dungeons.keys()].sort().map(dungeon => ({ dungeon, cells: dungeons.get(dungeon)! }));
  return { season, columns, rows };
}
