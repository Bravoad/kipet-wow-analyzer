# Blizzard adapter

`blizzard.New(Config)` requires server-side credentials. All HTTP destinations are fixed to `oauth.battle.net` and the EU/US Blizzard API hosts. Injected `Permit` and `PauseUntil` callbacks coordinate every outgoing request, including OAuth, through the application's shared provider budget. Credentials and raw responses are never included in returned errors.

`Fetch` requires a valid canonical realm and normalized character key. The mandatory summary establishes character identity. Optional equipment/statistics/specializations failures preserve a usable profile and explicitly identify unavailable data. A summary `404` is distinct from an optional section `404`. A summary `403` returns a typed access-denied fault; the application decides quarantine/purge actions.

Missing source values remain null. Statistics retain separate melee/ranged/spell values. Talent nodes are populated only from explicit node IDs/ranks, and missing loadout activity is null. Unknown patch/season values remain unset. Request retries after `401` are limited to a single token refresh; `Retry-After` on `429` is preserved, with no hidden sleep inside the adapter.

The tests use **synthetic HTTP responses**, never captured live player data. They verify OAuth coalescing, token placement, one-time `401` recovery, redirects/body limits, shared rate-limit deferral, exact integer IDs, identity guards and partial responses. Production credentials have not been used; the current Retail schema must still be verified with redacted contract fixtures before a public launch.

Primary references consulted on 30 October 2026:

- [Blizzard's OAuth Client Credentials example](https://github.com/Blizzard/node-signature-generator).
- [Blizzard staff API patch notes](https://us.forums.blizzard.com/en/blizzard/t/wow-game-data-and-profile-api-patch-notes-2019-12-10/2392), confirming the equipment and specializations endpoint families. These historical notes do not establish today's talent fields.
- [Current Profile API portal](https://community.developer.battle.net/documentation/world-of-warcraft/profile-apis) and [OAuth guide](https://community.developer.battle.net/documentation/guides/using-oauth): their complete dynamically rendered documentation was unavailable to the browsing tool during implementation.

Fixture mode is a separate provider selected explicitly at startup. It contains only `eu / гордунни / Кипет` and `us / stormrage / Arcanis`; unknown names return a missing-character fault. It is never an automatic fallback for Blizzard outages.

Stage 3 adds optional character `mythic-keystone-profile`, its selected `season/{id}` detail, and `encounters/raids`. The regional dynamic season index is cached for one hour with request coalescing; only an explicit current-season field establishes current status. The largest listed season ID is never selected by inference. Best-run scope distinguishes season and current-period records. A cached season must not lend its label to an unbound current-index rating or period. Raid observations retain expansions, instances, difficulties and nullable boss counts, including historical data. Missing collections remain null; explicit empty collections remain empty arrays. Each progress section has its own 15-minute freshness deadline and 30-day retention ceiling. The core rule engine does not consume progress.

Progress contracts are covered by synthetic HTTP fixtures, including season transitions, optional 404, identity mismatch, false/zero/null, precision and concurrent catalog requests. Attempts to read the official Profile and Game Data portals on 1 November 2026 returned an inaccessible redirect or JavaScript shell. Credentials were unavailable; current live progress fields and endpoint availability have not been accepted against Blizzard.
