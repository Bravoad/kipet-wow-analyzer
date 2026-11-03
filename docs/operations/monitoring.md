# Локальная наблюдаемость

1 ноября 2026. Prometheus и Grafana запускаются дополнительным Compose-файлом. Основное приложение работает независимо от них. Все опубликованные порты остаются на loopback; внутренние listener API `9091` и worker `9092` не публикуются на host и закрыты публичным nginx. Это соответствует [модели безопасности Prometheus](https://prometheus.io/docs/operating/security/).

## Запуск

Из корня проекта, PowerShell 7:

```powershell
# Только при первом запуске; существующий файл с ключами не перезаписывать.
if (!(Test-Path -LiteralPath '.env.monitoring')) {
    $grafanaPassword = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
    $grafanaKey = [Convert]::ToBase64String([Security.Cryptography.RandomNumberGenerator]::GetBytes(32))
    @("GRAFANA_ADMIN_PASSWORD=$grafanaPassword", "GRAFANA_SECRET_KEY=$grafanaKey") |
        Set-Content -LiteralPath '.env.monitoring' -Encoding utf8
}
docker compose --env-file .env.monitoring -f compose.yaml -f deploy/observability/compose.monitoring.yaml up -d --no-build
```

Перед этим соберите приложение по [основной инструкции](local-development.md). Если основной запуск использует собственный `.env`, передайте оба файла: `--env-file .env --env-file .env.monitoring`. Провайдер и HMAC-ключ должны совпадать с уже созданной базой. Не меняйте source marker ради включения мониторинга.

Открыть [Prometheus](http://127.0.0.1:9090/targets) и [Grafana](http://127.0.0.1:3000/d/kipet-operations). Логин Grafana — `kipet-admin`, пароль хранится в локальном `.env.monitoring`; файл игнорируется Git и Docker build context. Анонимный вход и регистрация отключены. Изменение переменной admin password после первого запуска не является автоматической сменой пароля сохранённого пользователя Grafana. Существующий файл и volume следует сохранять вместе; не удаляйте volume ради смены пароля.

Используются официальные образы Prometheus 3.15.1 и Grafana 13.3.0, закреплённые digest. Метрики сохраняются максимум 24 часа или 256 MiB; это локальная настройка, а не production retention. Grafana получает datasource и восемь панелей из репозитория. Проверка обновлений, отчётность и автоматическая предустановка/обновление плагинов выключены. Отдельный случайный `GRAFANA_SECRET_KEY` применяется к локальному хранилищу Grafana.

## Что показывают проверки

API `/health/live` проверяет процесс. Readiness проверяет доступность PostgreSQL и полный migration ledger с checksum; неизвестная, отсутствующая или изменённая миграция снимает готовность. Проверка ledger читает данные и не запускает миграции. Успешный ledger не заменяет проверку фактической структуры всех таблиц. Redis может дать `degraded`, при этом сохраняются PostgreSQL-проверки публикации и ограниченный локальный limiter.

Worker readiness дополнительно требует работающих ожидаемых компонентов: персонажи, гильдии и, если включён, combat. Завершение компонента или shutdown снимает готовность. Это проверка жизненного цикла, а не доказательство успешной обработки каждого задания; зависания видны по возрасту due-задач и истёкшим lease. Ошибка схемы/БД на `/metrics` даёт 503 и `kipet_metrics_scrape_success=0`, сохранившиеся старые значения не выдаются за новый успешный scrape.

Панели показывают доступность обоих scrape targets, HTTP-частоту по status, p95 только 2xx, три очереди, возраст due-задач, активные компоненты, истёкшие lease и использование fallback limiter. HTTP histogram использует шаблон маршрута, ограниченный метод и status; имена, URL-параметры, коды отчётов, IP и request ID в labels не попадают. `429` виден отдельно от успешных ответов.

Четыре стартовых alert rules описывают пропавший scrape, старую due-очередь, остановку worker и долю 5xx. Будущие retry-задачи не входят в due-возраст. Пороги являются локальной начальной настройкой. Alertmanager и внешние получатели не подключены; сообщения никому не отправляются.

## Журналы и диагностика

nginx пишет только ограниченный HTTP-метод, status, число байт и длительность. Его стандартный error log выключен, поскольку он включает raw URI; ошибки gateway остаются видны в status-only access log. API пишет безопасные коды ошибок и шаблоны маршрутов. PostgreSQL настроен без statement logging и параметров в error log, с terse-выводом. Docker local log driver ограничивает журналы каждого контейнера тремя файлами до 10 MiB; это ограничение объёма, не гарантия удаления по календарному возрасту.

```powershell
docker compose ps
docker compose exec -T api wget -qO- http://127.0.0.1:9091/health/ready
docker compose exec -T worker wget -qO- http://127.0.0.1:9092/health/ready
docker compose exec -T worker wget -qO- http://127.0.0.1:9092/metrics
docker compose logs --tail 20 api worker web
```

Для production требуются собственные retention/backup метрик, ограниченный доступ к панели, TLS, согласованные пороги и отдельно выбранные получатели оповещений. Текущие loopback-порты не следует переносить в публичный ingress.
