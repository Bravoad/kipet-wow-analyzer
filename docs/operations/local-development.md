# Запуск Kipet WoW Analyzer на Windows

## 1. Подготовьте Docker

Установите и запустите Docker Desktop с поддержкой Linux-контейнеров. Откройте PowerShell в корне проекта, где лежит `compose.yaml`. Для запуска через Docker достаточно Docker Desktop и доступа к интернету. [Установка Docker Desktop на Windows](https://docs.docker.com/desktop/setup/install/windows-install/)

```powershell
docker version --format '{{.Server.Os}}'
docker compose version
```

Первая команда должна вывести `linux`, вторая — версию Compose.

## 2. Запустите сайт

```powershell
docker compose up --build --wait
```

Compose соберёт образы, запустит PostgreSQL и Redis, применит миграции и дождётся готовности сервисов. При первой сборке потребуется скачать образы. После успешного запуска откройте сайт. [Kipet WoW Analyzer](http://127.0.0.1:5173/) · [Команда docker compose up](https://docs.docker.com/reference/cli/docker/compose/up/)

При настройках по умолчанию доступны примеры **Кипет · EU · Гордунни**, **Arcanis · US · Stormrage**, демогильдия и демоотчёт `DEMOCombat000001`. Если в папке уже есть `.env`, Compose использует его настройки.

## 3. Включите Raider.IO

Создайте `.env`, если его ещё нет. Существующая конфигурация останется на месте. [Шаблон настроек](../../.env.example)

```powershell
if (-not (Test-Path -LiteralPath '.env')) {
    Copy-Item -LiteralPath '.env.example' -Destination '.env'
}
notepad .env
```

Установите `RAIDERIO_ENABLED=true`. В новом `.env` оставьте `PROVIDER_MODE=fixture`. Raider.IO загружает публичные профили независимо от этой настройки и работает без ключа API. [Документация Raider.IO](https://raider.io/api)

Сохраните файл и выполните команду.

```powershell
docker compose up --wait
```

На вкладке **Raider.IO** укажите регион `EU`, сервер `gordunni` и имя `Кипет`. Первый поиск может занять несколько секунд, пока worker получит профиль. Из профиля можно открыть гильдию и выбрать до пяти участников для сравнения. [Готовая ссылка на профиль Кипета](http://127.0.0.1:5173/?view=raiderio&region=eu&realm=gordunni&name=%D0%9A%D0%B8%D0%BF%D0%B5%D1%82)

В **Сводке состава** показано распределение наблюдаемых участников по классам, специализациям и ролям. **Скачать сводку JSON** повторно проверяет актуальность снимка и сохраняет сводные числа, рейдовые данные и ссылку на источник. При смене снимка перечитайте гильдию и повторите выгрузку.

В **Сравнении Raider.IO** совпадение специализации учитывает класс, а рейтинг Mythic+ показан с сезоном каждого профиля. **Скачать сравнение JSON** повторно проверяет снимки всех выбранных персонажей. В файл попадают наблюдения, рейдовый прогресс, время загрузки и ссылка на источник для каждого профиля.

Для двух и более персонажей одного подтверждённого сезона раскройте **Ключи Mythic+ по подземельям**. Таблица показывает уровень ключа, время, статус таймера и очки из списка лучших прохождений. Запрашиваемый список Raider.IO ограничен десятью результатами по очкам. В ячейках без прохождения указан статус данных. [Схема Raider.IO API](https://raider.io/swagger.json)

## 4. Подключите Warcraft Logs при необходимости

Создайте клиент API V2 в аккаунте Warcraft Logs и возьмите его OAuth Client ID и Client Secret. [Настройки API-клиентов](https://www.warcraftlogs.com/api/clients) · [Документация OAuth](https://www.warcraftlogs.com/api/docs)

Сначала подготовьте отдельную БД отчётов. Команда создаёт её только при отсутствии.

```powershell
docker compose up --wait postgres
$kipetCombatDb = docker compose exec -T postgres psql -U kipet -d postgres -Atc "SELECT 1 FROM pg_database WHERE datname = 'kipet_warcraftlogs'"
if ($LASTEXITCODE -ne 0) { throw 'Не удалось проверить базу отчётов' }
if ($kipetCombatDb -ne '1') {
    docker compose exec -T postgres createdb -U kipet kipet_warcraftlogs
    if ($LASTEXITCODE -ne 0) { throw 'Не удалось создать базу отчётов' }
}
```

Откройте локальный `.env` и заполните следующие поля своими значениями. Адрес `postgres` используется внутри сети Compose.

```dotenv
COMBAT_PROVIDER_MODE=warcraftlogs
COMBAT_DATABASE_URL=postgres://kipet:kipet@postgres:5432/kipet_warcraftlogs?sslmode=disable
WCL_CLIENT_ID=
WCL_CLIENT_SECRET=
```

Для Warcraft Logs нужен приватный случайный `APP_HMAC_SECRET` длиной не менее 32 байт. Если в `.env` ещё стоит значение из шаблона, сгенерируйте новое и скопируйте его в `APP_HMAC_SECRET`. Храните секреты в локальном `.env`, который исключён из Git и Docker build context.

```powershell
$kipetBytes = New-Object byte[] 32
$kipetRng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
try { $kipetRng.GetBytes($kipetBytes) } finally { $kipetRng.Dispose() }
[Convert]::ToBase64String($kipetBytes)
```

Запустите `docker compose up --wait`. Затем откройте **Боевые отчёты** и введите ссылку или код публичного отчёта. Если интерфейс предлагает настроить источник, проверьте, что заполнены оба поля OAuth. Состояние `combat_mode=warcraftlogs` подтверждает выбор режима. Успешная загрузка публичного отчёта подтверждает работу OAuth и доступа к API.

Выберите бой и участника. Раздел **Характеристики участника боя** показывает первичные и вторичные значения из события `CombatantInfo` с отметкой времени от начала боя. Значения привязаны к этому событию. Знак «—» обозначает неизвестное значение, а наблюдаемый ноль отображается числом. [Типы событий Warcraft Logs](https://www.warcraftlogs.com/v2-api-docs/warcraft/eventdatatype.doc.html)

Для возврата к демоотчётам укажите `COMBAT_PROVIDER_MODE=fixture` и очистите `COMBAT_DATABASE_URL`, если основная БД была создана в деморежиме и использовалась для демоотчётов либо ещё не привязана к источнику отчётов. После сохранения `.env` выполните `docker compose up --wait`.

## Проверка и диагностика

```powershell
docker compose ps -a
Invoke-RestMethod 'http://127.0.0.1:5173/api/v1/runtime' |
    Select-Object provider_mode, raiderio_enabled, combat_mode, combat_setup_required
```

В рабочем окружении `api`, `worker`, `postgres` и `redis` имеют статус `healthy`, а `web` работает. `migrate` завершается с кодом `0` после применения миграций. Запрос `runtime` проверяет, что сайт проксирует запросы к API.

| Симптом | Что проверить |
| --- | --- |
| Docker не отвечает | Запустите Docker Desktop и дождитесь готовности Linux-движка. |
| Сайт не открывается | Проверьте `docker compose ps -a` и доступность портов 5173, 8080, 5432 и 6379. Все четыре порта привязаны к `127.0.0.1`. |
| Миграция завершилась с ошибкой | Выполните `docker compose logs --tail 50 migrate`. При ошибке подключения к базе отчётов проверьте её наличие и `COMBAT_DATABASE_URL`. |
| Raider.IO или отчёты показывают ошибку | Проверьте режимы через `runtime` и выполните `docker compose logs --tail 50 api worker`. |
| Правки `.env` не применились | Запустите `docker compose up --wait`, чтобы Compose перечитал конфигурацию и пересоздал нужные сервисы. |

Проверка готовности API из контейнера.

```powershell
docker compose exec -T api wget -qO- http://127.0.0.1:9091/health/ready
```

Если Docker Hub недоступен, для сборки можно использовать зеркала Docker Official Images в Amazon ECR Public. Если локальных образов PostgreSQL и Redis ещё нет, скачайте и пометьте их ожидаемыми тегами.

```powershell
docker pull public.ecr.aws/docker/library/postgres:18-alpine
docker tag public.ecr.aws/docker/library/postgres:18-alpine postgres:18-alpine
docker pull public.ecr.aws/docker/library/redis:8-alpine
docker tag public.ecr.aws/docker/library/redis:8-alpine redis:8-alpine
$env:GO_IMAGE = 'public.ecr.aws/docker/library/golang:1.27-alpine'
$env:ALPINE_IMAGE = 'public.ecr.aws/docker/library/alpine:3.23'
$env:NODE_IMAGE = 'public.ecr.aws/docker/library/node:24-alpine'
$env:NGINX_IMAGE = 'public.ecr.aws/docker/library/nginx:stable-alpine'
docker compose up --build --wait
```

## Остановка и повторный запуск

```powershell
docker compose stop
```

Для повторного запуска выполните `docker compose up --wait`, после правок кода — `docker compose up --build --wait`. Обычный `docker compose down` удаляет контейнеры и сеть, а данные PostgreSQL остаются в именованном томе. Флаг `--volumes` удаляет этот том вместе с данными.

## Нативная разработка

Проверенные версии для разработки — Go 1.27 и Node.js 24. Остановите контейнеры `api`, `worker` и `web`, чтобы освободить порты приложения. Оставьте PostgreSQL и Redis в Docker.

```powershell
docker compose stop api worker web
docker compose up --wait postgres redis
```

В терминалах для Go задайте переменные окружения. `.env` автоматически читает Compose. Go-команды получают настройки из окружения своего терминала.

```powershell
$env:PROVIDER_MODE = 'fixture'
$env:COMBAT_PROVIDER_MODE = 'fixture'
$env:APP_HMAC_SECRET = 'local-development-only-replace-this-before-real-use'
```

1. В первом терминале выполните `go run ./cmd/migrate`, затем `go run ./cmd/api`.
2. Во втором терминале выполните `go run ./cmd/worker` с теми же переменными окружения.
3. В третьем выполните `npm.cmd ci --prefix web`, затем `npm.cmd run dev --prefix web`. Vite проксирует `/api` на порт 8080. [Открыть сайт](http://127.0.0.1:5173/)

Для адаптера Blizzard используются `PROVIDER_MODE=blizzard`, `BLIZZARD_CLIENT_ID`, `BLIZZARD_CLIENT_SECRET` и приватный `APP_HMAC_SECRET`. Основная БД закрепляет источник при первом запуске. Для Blizzard используйте отдельную чистую БД или окружение, чтобы сохранить демоданные.

## Тесты

```powershell
$env:TEST_DATABASE_URL = 'postgres://kipet:kipet@127.0.0.1:5432/kipet?sslmode=disable'
go test ./... -count=1
go vet ./...
npm.cmd ci --prefix web
npm.cmd run build --prefix web
npm.cmd test --prefix web
```

Интеграционные Go-тесты создают изолированные схемы PostgreSQL. Контрактный HTTP-тест запускайте на работающем демоокружении с `provider_mode=fixture` и `combat_mode=fixture`.

```powershell
npm.cmd ci --prefix tests/contract
$env:API_BASE_URL = 'http://127.0.0.1:5173'
npm.cmd test --prefix tests/contract
```

- [Команды CI](../../.github/workflows/ci.yml)
- [Prometheus, Grafana и диагностика](monitoring.md)
- [Нагрузочная проверка и HTTP smoke](load-testing.md)
- [Резервное копирование и восстановление](recovery.md)
