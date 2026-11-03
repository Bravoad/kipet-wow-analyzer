# Контракты прикладных сервисов и адаптеров

Статус: проект этапа 1, 30 октября 2026 года. Ниже приведены сигнатуры и инварианты будущего Go-кода, а не реализация. REST DTO определены в [OpenAPI](../../api/openapi/mvp-v1.yaml), хранение — в [модели данных](data-model.md).

## 1. Порты и направление зависимостей

Порты определяет потребляющий прикладной слой. Его структуры не содержат `http.Response`, `pgx.Rows`, Redis-команды или provider-specific JSON. Все операции принимают `context.Context`; отмена и deadline доходят до БД и провайдера. Часы, генератор UUID и jitter передаются как зависимости для проверяемых сценариев.

| Порт | Операции | Гарантия |
| --- | --- | --- |
| RealmCatalog | Resolve, List, ReplaceRegionCatalog | Канонический realm в конкретном регионе, доступные локализации, версия каталога |
| CharacterReader | ReadPublishable, ReadLastRefresh | Согласованный текущий снимок, проверка карантина/срока/текущего UUID в БД |
| RefreshRequests | EnsureRefresh | Создание либо присоединение к одной активной задаче, cooldown и capacity в БД |
| JobReader | ReadPublicJob | Только публичное ограниченное представление, без внутреннего payload |
| WorkQueue | Claim, Renew, Defer, FinishFailure | Аренда с fencing token, ограниченные попытки, потеря lease запрещает запись |
| SnapshotWriter | PublishSnapshot, Quarantine, PurgeCharacter | Транзакция публикации/очистки с проверкой действующего lease |
| SnapshotCache | Get, Put, Invalidate | Не создаёт право на публикацию, версия совпадает с разрешённым в БД снимком |
| ProviderBudget | Acquire, ObserveResponse | Общий лимит для всех workers/реплик, поддержка Retry-After и circuit breaker |
| BlizzardCharacters | Profile, Equipment, Statistics, Talents | Нормализованные секции и типизированные ошибки |
| BlizzardRealms | FetchCatalog | Нормализованный каталог региона с провайдерской версией, если доступна |
| AppTokenProvider | Token, Invalidate | Только серверные access tokens, coalescing обновлений, без логирования секретов |

У API нет зависимости от `BlizzardCharacters`: она принадлежит worker. API читает БД/кеш и планирует задачи. Bootstrap каталога выполняется worker/управляемой служебной операцией, не скрытым вызовом Blizzard при первом вводе имени.

## 2. Сигнатуры прикладного уровня

```go
// Псевдосигнатуры: определения типов находятся в спецификации.
type CharacterQueries interface {
    Read(ctx context.Context, key CharacterKey, locale Locale,
        section SectionName) (CharacterReadResult, error)
}

type RefreshRequests interface {
    EnsureRefresh(ctx context.Context, key CharacterKey,
        cause RefreshCause, requestedAt time.Time) (RefreshDecision, error)
}

type WorkQueue interface {
    Claim(ctx context.Context, workerID string, now time.Time) (*Lease, error)
    Renew(ctx context.Context, lease Lease, now time.Time) (Lease, error)
    Defer(ctx context.Context, lease Lease, nextAttempt time.Time,
        failure SafeFailure) error
}

type SnapshotWriter interface {
    PublishSnapshot(ctx context.Context, lease Lease,
        snapshot NormalizedSnapshot) error
    Quarantine(ctx context.Context, lease Lease, reason SafeReason) error
    PurgeCharacter(ctx context.Context, lease Lease,
        proof ConfirmedProfileMissing) error
}
```

`CharacterReadResult` содержит `data`, snapshot ID, секционную полноту, исходные timestamps, необязательный job ID и типизированное состояние. `pending` возможен только после успешной фиксации задания или нахождения уже активного. `RefreshDecision` — `already_fresh`, `created`, `joined`, `cooldown`, `capacity_exceeded` либо типизированная ошибка инфраструктуры. HTTP-статус выбирает transport по [REST-контракту](api-contract.md).

`Lease` содержит job ID, fencing token, поколение/номер попытки и expires-at. Нельзя выполнить `FinishSuccess` отдельно от публикации снимка: иначе падение между этими действиями оставит job успешным без результата. `PublishSnapshot` блокирует строку задачи, проверяет token, `running`, `lease_until > db_now`, совпадение идентичности и доступность персонажа; сохраняет снимок, меняет current pointer и переводит задачу в succeeded одной транзакцией. `PurgeCharacter` синхронизируется теми же блокировками и инвалидирует ранее захваченные задачи.

Для `Renew`/`Defer`/commit отсутствие совпадающей строки или истёкший lease — `LeaseLost`, а не успех. Сетевые обращения выполняются вне SQL-транзакции. Dead worker может быть заменён только после истечения lease; новый token делает его старый результат недействительным.

## 3. Blizzard-адаптер

Вход: проверенные `Region`, `CanonicalRealmSlug`, `NormalizedName`, серверный locale и deadline. API host и namespace выбирает только адаптер. Пользователь не задаёт URL, endpoint, namespace или заголовки. Путь строится с кодированием каждого сегмента; повторное декодирование и редиректы на произвольный host запрещены. Само значение имени не используется для построения SQL.

Планируемая карта источников, подлежащая окончательной проверке по актуальному порталу и redacted fixtures перед реализацией:

| Операция | Семейство Blizzard API | Namespace |
| --- | --- | --- |
| Profile | `/profile/wow/character/{realm}/{name}` | `profile-{region}` |
| Equipment | профиль персонажа, `/equipment` | `profile-{region}` |
| Statistics | профиль персонажа, `/statistics` | `profile-{region}` |
| Talents | профиль персонажа, `/specializations` | `profile-{region}` |
| Realm catalog | `/data/wow/realm/index` и сведения realm | `dynamic-{region}` |

Не предполагаем, что в live Midnight гарантированы все исторические поля талантов, экспортная строка или узлы. При отсутствии соответствующих данных адаптер возвращает partial/unavailable с причиной. Нераспознанные поля допустимы; изменение типа обязательного поля — `InvalidPayload`, не молчаливое приведение к нулю. Live-контракт не подтверждён этим документом, см. [sources.md](sources.md).

На сервере OAuth используется grant `client_credentials`; HTTP Basic/другой способ передачи пары определяется актуальным OAuth-контрактом. Access token хранится в серверной памяти или защищённом кеше с запасом до expiry и единственным конкурентным обновлением. В логах запрещены client secret, token response, Authorization, токен в query. Провайдерские ответы ограничиваются по размеру, JSON/decompression имеют лимиты; в браузер исходные тела не передаются.

Локаль источника по умолчанию одна для региона (`en_GB` для EU, `en_US` для US как проектное предложение до fixture-проверки). Переключение языка UI не требует нового сбора персонажа. Отображаемые игровые строки хранятся отдельно по locale; отсутствующий перевод имеет явный fallback, который не переименовывается в якобы русский. `ru-RU`/`en-US` REST и provider locale сопоставляет адаптер, а не клиент.

## 4. Результат секции и ошибки

У результата секции есть `state`, нормализованный payload, `fetched_at`, nullable `source_updated_at`, `fresh_until`, `valid_until`, safe reason и версия нормализации. Успешный HTTP 200 ещё не означает полную секцию. `source_updated_at` — время источника, `fetched_at` — время фактического получения; их нельзя менять местами. Перенос старой секции в новый снимок сохраняет её возраст и происхождение.

| Категория адаптера | Реакция worker/application | Публичное поведение |
| --- | --- | --- |
| ProfileMissingConfirmed | Каскадная очистка, завершение/удаление задач; короткий negative cache | 404, без старого профиля |
| SectionMissing | Отметить только соответствующую секцию | 200 partial/unavailable при пригодном профиле |
| AccessDenied (summary 403) | Карантин, серверная диагностика, запрет выдачи старых данных | 502 `PROVIDER_ACCESS_DENIED` без утверждения об удалении |
| AccessDenied (optional section) | Скрыть соответствующую секцию; прежняя запрещённая секция не выдаётся | Профиль доступен, секция unavailable |
| Unauthorized | Однократная инвалидация/перезагрузка токена; при повторе terminal/circuit | 502 `PROVIDER_AUTH_FAILURE` при отсутствии пригодных данных |
| RateLimited | Сохранить provider next_allowed_at, учесть Retry-After, отложить retry | 200 stale, либо pending активной задачи; пользовательский 429 только от нашего limiter |
| Timeout / TemporaryUnavailable | Ограниченный retry с jitter, circuit breaker | Пригодные данные stale; иначе 503/ошибка задачи |
| InvalidPayload | Не публиковать некорректную секцию; ограниченный retry/alert | 502, либо явно partial пригодного профиля |
| LeaseLost / ContextCancelled | Остановить работу, запретить коммит | Никакого нового ложного результата |
| StorageUnavailable | Не утверждать, что работа принята | 503 |

`ConfirmedProfileMissing` может создать только обработка обязательного profile summary при проверенных регионе, realm, namespace, корректном endpoint и пригодной авторизации. 404 с неизвестным realm или ошибкой конфигурации не является доказательством исчезновения ранее известного персонажа. Для массовых необычных 404 действует сигнализация и закрытие опасного purge до проверки конфигурации; профиль при обоснованном сомнении скрывается, а не выдаётся как достоверный.

Ошибки содержат safe code, категорию, retryable и nullable retry-at. Внутренний provider request ID допустим только в защищённой диагностике; raw body и технические URL не попадают в REST. Публичный `request_id` генерирует наше приложение.

## 5. Кеш, лимиты и очистка

Ключ кеша включает schema version, region, character UUID, snapshot UUID, section и locale для локализованного представления. Поисковый alias `region/realm/name` не даёт права показать payload без проверки текущей записи БД. TTL Redis ограничен `valid_until` секции и профиля. Cache miss не запускает Blizzard напрямую. При ошибке Put API продолжает читать PostgreSQL; отсутствие кеша не отменяет refresh dedup.

`EnsureRefresh` атомарно проверяет состояние последней попытки/cooldown и частичный уникальный индекс на активный ключ задачи. Redis lock — оптимизация. При гонке unique violation обрабатывается повторным чтением существующей задачи. Терминальный job не мешает будущему разрешённому refresh, но GET не обходит cooldown. Unavailable-секция не инициирует автоматический повтор раньше `checked_at + TTL` секции; `not_provided` не вызывает новый сбор при каждом чтении. Retry-After соблюдается всеми workers через общее состояние provider budget.

При удалении сначала БД прекращает публикацию и аннулирует задачи; затем идемпотентно очищается Redis. Даже если очистка Redis задержана, чтение запрещено. Старая горутина не может создать character заново из своего payload: создание идентичности допускается только в актуальной `EnsureRefresh` после negative cooldown.

## 6. Будущие адаптеры

Warcraft Logs, Raider.IO, симуляции и источники эталонных билдов в MVP не создают исходящих запросов. Для Warcraft Logs впоследствии понадобится отдельный порт публичных отчётов, учёт GraphQL point budget, проверка видимости и пагинация событий. Raider.IO остаётся отключённым. Наличие названия порта в архитектуре не означает подключения аккаунта или согласия с условиями источника.
