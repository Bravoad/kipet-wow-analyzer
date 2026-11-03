import type { RaiderGuildProfile } from './raider.ts';

export interface RaiderRosterGroup {
  name: string;
  count: number;
}

export interface RaiderSpecializationGroup extends RaiderRosterGroup {
  className: string;
}

export interface RaiderRosterSummary {
  members: number;
  realms: number;
  classes: RaiderRosterGroup[];
  specializations: RaiderSpecializationGroup[];
  roles: RaiderRosterGroup[];
  unknown: { class: number; specialization: number; role: number };
}

function groupRows(counts: Map<string, number>): RaiderRosterGroup[] {
  return Array.from(counts, ([name, count]) => ({ name, count }))
    .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name, 'en'));
}

export function summarizeRaiderRoster(members: RaiderGuildProfile['members']): RaiderRosterSummary | null {
  if (members === null) return null;

  const realms = new Set<string>();
  const classes = new Map<string, number>();
  const specializations = new Map<string, RaiderSpecializationGroup>();
  const roles = new Map<string, number>();
  const unknown = { class: 0, specialization: 0, role: 0 };

  for (const member of members) {
    const { key, class: className, specialization, role } = member;
    realms.add(`${key.region}/${key.realm.trim().normalize('NFC').toLowerCase()}`);

    if (className) classes.set(className, (classes.get(className) ?? 0) + 1);
    else unknown.class++;

    if (className && specialization) {
      const id = JSON.stringify([className, specialization]);
      const group = specializations.get(id);
      if (group) group.count++;
      else specializations.set(id, { className, name: specialization, count: 1 });
    } else unknown.specialization++;

    if (role) roles.set(role, (roles.get(role) ?? 0) + 1);
    else unknown.role++;
  }

  return {
    members: members.length,
    realms: realms.size,
    classes: groupRows(classes),
    specializations: [...specializations.values()]
      .sort((a, b) => b.count - a.count || a.className.localeCompare(b.className, 'en') || a.name.localeCompare(b.name, 'en')),
    roles: groupRows(roles),
    unknown,
  };
}
