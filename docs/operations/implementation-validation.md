# Проверка реализации MVP

30 октября 2026 года. Локальная версия этапа 1. Приложение запущено через Docker Compose; публикации в интернете нет.

Это отчёт о первом этапе. Дополнения Rule Engine, пятой миграции и вкладки «Анализ» описаны в [отчёте этапа 2](analysis-validation.md).

## Работает

Go API и отдельный worker, четыре SQL-миграции, PostgreSQL snapshots/sections и очередь, Redis cache/limiter, серверный Blizzard OAuth-адаптер, отдельный fixture provider, React RU/EN интерфейс и production nginx. Поиск, фоновые задания, профиль, экипировка, характеристики и таланты связаны в один рабочий сценарий.

Источник явно обозначается в UI, runtime endpoint и `meta.source`. База привязывается к режиму `fixture` или `blizzard`; смена режима не публикует прежние демоданные как реальные. API не вызывает Blizzard из HTTP handlers. Подтверждённое отсутствие вызывает purge, неоднозначный отказ доступа — карантин с возможностью перепроверки после cooldown. Истёкшие данные скрываются до их физической очистки.

## Выполненные проверки

| Проверка | Результат |
| --- | --- |
| `go test ./... -count=1` с `TEST_DATABASE_URL` | PASS, включая настоящий PostgreSQL |
| `go vet ./...` | PASS |
| Linux amd64 `go test -race ./... -count=1` в Docker | PASS, включая все четыре миграции и привязку базы к источнику |
| Миграции | Up, повторный Up, Down/Up и checksum ledger на изолированных schemas |
| PostgreSQL storage | Конкурентный enqueue/claim, истечение lease, late publish, неизвестные поля/частичные секции, purge, замена идентичности, quarantine/recovery, retention, cooldown и source-mode guard |
| Blizzard HTTP adapter | OAuth coalescing, один retry 401, 429/Retry-After, URL/redirect/body limits, Unicode lookup, nullable talents, точные int64 IDs |
| API/application | Unicode/NFC/casefold, один URI decode, запрет force/oversize/trailing JSON, freshness/pending/expired/quarantine, скрытые health, отсутствие обхода limiter через поддельный IP-header |
| `npm run build --prefix web` | PASS, TypeScript и production bundle |
| `npm test --prefix web` | PASS, 3 теста URI/polling/cancellation |
| Контрактный HTTP-прогон через nginx | PASS: ответы EU/US, разделы, задания, безопасный 404 и ошибки валидации соответствуют OpenAPI |
| Docker deployment images | Собраны API/worker/migrate и web; все пять постоянных services запускаются, migrate завершается с 0 |
| Внутренние endpoints | Readiness возвращает ok, `/metrics` выдаёт HTTP/queue/age gauges без идентификаторов персонажей |
| Browser QA | Поиск EU и US, профиль, экипировка/характеристики/таланты, RU/EN и mobile 360 px; горизонтального переполнения не обнаружено |

При проверке Docker Hub возвращал TLS timeout. Образы официальной библиотеки получены через Amazon ECR Public mirror, сборка выполнена с соответствующими build arguments; защитные настройки компьютера не менялись.

## Практические ограничения

Реальные Blizzard credentials не использовались. Текущие поля EU/US Retail, полнота талантов и квоты внешнего API требуют live-проверки с обезличенными fixtures. Изменения формата возвращают partial/unavailable; подмена на демоданные при сбое запрещена. Контроль исходящих запросов консервативный и общий для реплик; полноценная настройка circuit breaker и калибровка лимитов относятся к подготовке публичного релиза.

56 сценариев в исходной приёмочной матрице — план приёмки, а не заявление, что все 56 прошли отдельный автоматический E2E-прогон. Проверенный браузером сценарий не заменяет долгосрочную E2E-регрессию Playwright. GitHub Actions workflow подготовлен, но удалённый запуск не выполнялся. Нагрузочный/security-аудит, SLO, backup/restore и публичный production-релиз ещё не выполнены.

Rule Engine, эталонные билды, боевые логи, Mythic+, рейды, гильдии и сравнения — следующие этапы. Текущая версия показывает факты и полноту данных без оценки качества игрока. SQL выполняется через pgx; sqlc generation пока не подключена. Условия источников перед публичным использованием требуют актуальной проверки.

См. [инструкцию запуска](local-development.md) и [исходную спецификацию](../mvp/specification.md). Демонстрационные персонажи и все тестовые игровые значения синтетические.
