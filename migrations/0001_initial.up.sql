-- Kipet WoW Analyzer: проект схемы MVP 0.1, 2026-10-30.
-- Спецификация, не production-миграция. Применять только в пустой тестовой БД.
-- UUID создает приложение; расширения PostgreSQL не требуются.
-- Все внешние timestamps передаются как UTC; сессии БД используют UTC.

CREATE TABLE realms (
    id uuid PRIMARY KEY,
    region text NOT NULL CHECK (region IN ('eu', 'us')),
    provider_realm_id bigint NOT NULL CHECK (provider_realm_id > 0),
    slug text NOT NULL CHECK (char_length(slug) BETWEEN 1 AND 100),
    names jsonb NOT NULL CHECK (jsonb_typeof(names) = 'object'),
    connected_realm_id bigint CHECK (connected_realm_id > 0),
    fetched_at timestamptz NOT NULL,
    UNIQUE (region, provider_realm_id),
    UNIQUE (region, slug),
    UNIQUE (region, id)
);

CREATE TABLE characters (
    id uuid PRIMARY KEY,
    region text NOT NULL CHECK (region IN ('eu', 'us')),
    realm_id uuid NOT NULL,
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 64),
    name_normalized text NOT NULL CHECK (char_length(name_normalized) BETWEEN 1 AND 64),
    provider_character_id bigint CHECK (provider_character_id > 0),
    current_snapshot_id uuid,
    publication_blocked_at timestamptz,
    publication_block_reason text CHECK (publication_block_reason IN ('provider_access_denied')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (region, realm_id) REFERENCES realms (region, id),
    UNIQUE (region, realm_id, name_normalized),
    CHECK ((publication_blocked_at IS NULL AND publication_block_reason IS NULL)
        OR (publication_blocked_at IS NOT NULL AND publication_block_reason IS NOT NULL))
);

-- Это очередь только обновлений персонажей. Каталоги обновляет отдельный
-- планировщик; его задания и будущие виды анализа не маскируются под эти строки.
CREATE TABLE refresh_jobs (
    id uuid PRIMARY KEY,
    character_id uuid NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    status text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'retry_wait', 'succeeded', 'failed', 'cancelled')),
    priority smallint NOT NULL DEFAULT 0 CHECK (priority BETWEEN 0 AND 100),
    reason text NOT NULL
        CHECK (reason IN ('cache_miss', 'stale_read', 'user_refresh', 'revalidation')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL DEFAULT 5 CHECK (max_attempts BETWEEN 1 AND 20),
    run_after timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    lease_owner text CHECK (char_length(lease_owner) BETWEEN 1 AND 128),
    leased_at timestamptz,
    lease_until timestamptz,
    last_error_code text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    CHECK (attempts <= max_attempts),
    CHECK (
        (status = 'running' AND lease_token IS NOT NULL AND lease_owner IS NOT NULL
            AND leased_at IS NOT NULL AND lease_until IS NOT NULL AND lease_until > leased_at)
        OR
        (status <> 'running' AND lease_token IS NULL AND lease_owner IS NULL
            AND leased_at IS NULL AND lease_until IS NULL)
    ),
    CHECK (
        (status IN ('succeeded', 'failed', 'cancelled') AND finished_at IS NOT NULL)
        OR (status IN ('queued', 'running', 'retry_wait') AND finished_at IS NULL)
    )
);

CREATE UNIQUE INDEX refresh_jobs_one_active_per_character
    ON refresh_jobs (character_id)
    WHERE status IN ('queued', 'running', 'retry_wait');
CREATE INDEX refresh_jobs_claim
    ON refresh_jobs (priority DESC, run_after, created_at, id)
    WHERE status IN ('queued', 'retry_wait');
CREATE INDEX refresh_jobs_expired_lease
    ON refresh_jobs (lease_until)
    WHERE status = 'running';
CREATE INDEX refresh_jobs_terminal_cleanup
    ON refresh_jobs (finished_at)
    WHERE status IN ('succeeded', 'failed', 'cancelled');

CREATE TABLE character_snapshots (
    id uuid PRIMARY KEY,
    character_id uuid NOT NULL REFERENCES characters (id) ON DELETE CASCADE,
    refresh_job_id uuid UNIQUE REFERENCES refresh_jobs (id) ON DELETE SET NULL,
    source text NOT NULL DEFAULT 'blizzard' CHECK (source = 'blizzard'),
    fetched_at timestamptz NOT NULL,
    source_updated_at timestamptz,
    profile_verified_at timestamptz NOT NULL,
    valid_until timestamptz NOT NULL,
    patch text CHECK (char_length(patch) BETWEEN 1 AND 64),
    game_build text CHECK (char_length(game_build) BETWEEN 1 AND 64),
    season_id bigint CHECK (season_id > 0),
    normalization_version integer NOT NULL DEFAULT 1 CHECK (normalization_version > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (character_id, id),
    CHECK (valid_until > fetched_at),
    CHECK (valid_until <= fetched_at + interval '720 hours'),
    CHECK (valid_until <= profile_verified_at + interval '720 hours')
);

-- Составной FK не позволяет назначить снимок другого персонажа текущим.
-- DEFERRABLE разрешает каскадное удаление персонажа и его снимков в транзакции.
ALTER TABLE characters ADD CONSTRAINT characters_current_snapshot_fk
    FOREIGN KEY (id, current_snapshot_id)
    REFERENCES character_snapshots (character_id, id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE INDEX character_snapshots_history
    ON character_snapshots (character_id, fetched_at DESC, id);
CREATE INDEX character_snapshots_retention
    ON character_snapshots (valid_until);

CREATE TABLE snapshot_sections (
    snapshot_id uuid NOT NULL REFERENCES character_snapshots (id) ON DELETE CASCADE,
    section text NOT NULL CHECK (section IN ('profile', 'equipment', 'statistics', 'talents')),
    state text NOT NULL CHECK (state IN ('complete', 'partial', 'unavailable')),
    source text NOT NULL DEFAULT 'blizzard' CHECK (source = 'blizzard'),
    fetched_at timestamptz,
    source_updated_at timestamptz,
    checked_at timestamptz NOT NULL,
    fresh_until timestamptz,
    valid_until timestamptz,
    error_code text,
    payload jsonb,
    PRIMARY KEY (snapshot_id, section),
    CHECK (payload IS NULL OR jsonb_typeof(payload) = 'object'),
    CHECK (
        (state IN ('complete', 'partial') AND fetched_at IS NOT NULL
            AND fresh_until IS NOT NULL AND valid_until IS NOT NULL
            AND fresh_until > fetched_at AND fresh_until <= valid_until
            AND valid_until > fetched_at
            AND valid_until <= fetched_at + interval '720 hours'
            AND payload IS NOT NULL)
        OR
        (state = 'unavailable' AND fetched_at IS NULL AND source_updated_at IS NULL
            AND fresh_until IS NULL AND valid_until IS NULL AND payload IS NULL)
    )
);
CREATE INDEX snapshot_sections_retention
    ON snapshot_sections (valid_until) WHERE valid_until IS NOT NULL;

-- Каталог не содержит связи с персонажем. Названия локализуются отдельно,
-- строка каталога не считается доказательством применимости зачарования.
CREATE TABLE item_catalog (
    provider_item_id bigint PRIMARY KEY CHECK (provider_item_id > 0),
    names jsonb NOT NULL CHECK (jsonb_typeof(names) = 'object'),
    item_class_id bigint CHECK (item_class_id >= 0),
    item_subclass_id bigint CHECK (item_subclass_id >= 0),
    inventory_type text,
    fetched_at timestamptz NOT NULL
);

CREATE TABLE equipment_items (
    snapshot_id uuid NOT NULL,
    section text NOT NULL DEFAULT 'equipment' CHECK (section = 'equipment'),
    slot text NOT NULL CHECK (char_length(slot) BETWEEN 1 AND 64),
    provider_item_id bigint NOT NULL CHECK (provider_item_id > 0),
    item_level integer CHECK (item_level >= 0),
    quality text,
    names jsonb NOT NULL CHECK (jsonb_typeof(names) = 'object'),
    inventory_type text,
    enchantments_state text NOT NULL
        CHECK (enchantments_state IN ('known', 'unknown')),
    enchantments jsonb CHECK (jsonb_typeof(enchantments) = 'array'),
    sockets_state text NOT NULL CHECK (sockets_state IN ('known', 'unknown')),
    sockets jsonb CHECK (jsonb_typeof(sockets) = 'array'),
    stats jsonb CHECK (jsonb_typeof(stats) = 'array'),
    set_info jsonb CHECK (jsonb_typeof(set_info) = 'object'),
    PRIMARY KEY (snapshot_id, slot),
    FOREIGN KEY (snapshot_id, section)
        REFERENCES snapshot_sections (snapshot_id, section) ON DELETE CASCADE,
    CHECK ((enchantments_state = 'known' AND enchantments IS NOT NULL)
        OR (enchantments_state = 'unknown' AND enchantments IS NULL)),
    CHECK ((sockets_state = 'known' AND sockets IS NOT NULL)
        OR (sockets_state = 'unknown' AND sockets IS NULL))
);
CREATE INDEX equipment_items_catalog_lookup ON equipment_items (provider_item_id);

CREATE TABLE talent_loadouts (
    id uuid PRIMARY KEY,
    snapshot_id uuid NOT NULL,
    section text NOT NULL DEFAULT 'talents' CHECK (section = 'talents'),
    provider_loadout_id bigint CHECK (provider_loadout_id >= 0),
    specialization_id bigint NOT NULL CHECK (specialization_id > 0),
    name text CHECK (char_length(name) <= 128),
    is_active boolean,
    export_code text CHECK (octet_length(export_code) <= 16384),
    nodes_state text NOT NULL CHECK (nodes_state IN ('known', 'unknown')),
    selected_nodes jsonb CHECK (jsonb_typeof(selected_nodes) = 'array'),
    FOREIGN KEY (snapshot_id, section)
        REFERENCES snapshot_sections (snapshot_id, section) ON DELETE CASCADE,
    CHECK ((nodes_state = 'known' AND selected_nodes IS NOT NULL)
        OR (nodes_state = 'unknown' AND selected_nodes IS NULL))
);
CREATE UNIQUE INDEX talent_loadouts_provider_unique
    ON talent_loadouts (snapshot_id, provider_loadout_id)
    WHERE provider_loadout_id IS NOT NULL;
CREATE UNIQUE INDEX talent_loadouts_one_active
    ON talent_loadouts (snapshot_id) WHERE is_active;
CREATE INDEX talent_loadouts_snapshot ON talent_loadouts (snapshot_id);

-- Координация бюджетов и circuit breaker. OAuth-токены и client secret
-- сюда не записываются. Значения лимитов задаются конфигурацией провайдера.
CREATE TABLE provider_state (
    provider text NOT NULL CHECK (provider = 'blizzard'),
    scope text NOT NULL CHECK (scope IN ('global', 'eu', 'us')),
    next_allowed_at timestamptz,
    circuit_state text NOT NULL DEFAULT 'closed'
        CHECK (circuit_state IN ('closed', 'open', 'half_open')),
    circuit_until timestamptz,
    consecutive_failures integer NOT NULL DEFAULT 0 CHECK (consecutive_failures >= 0),
    budget_remaining numeric CHECK (budget_remaining >= 0),
    budget_refilled_at timestamptz,
    budget_resets_at timestamptz,
    revision bigint NOT NULL DEFAULT 0 CHECK (revision >= 0),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, scope)
);

-- Важные межстрочные инварианты проверяются прикладной транзакцией:
-- * ровно четыре секции снимка; profile complete, checked_at не раньше fetch;
-- * текущий снимок принадлежит успешной проверке summary этого персонажа;
-- * refresh_job.character_id совпадает с snapshot.character_id;
-- * дочерние items/loadouts существуют только при доступной секции;
-- * stale/retention/coverage выводятся по каждой секции, а не по ее копированию;
-- * запись current_snapshot_id и статус succeeded защищены lease_token;
-- * очистка просроченной секции удаляет также ее items/loadouts и JSON payload.

