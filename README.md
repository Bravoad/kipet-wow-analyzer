# Kipet WoW Analyzer

Локальный анализатор World of Warcraft Retail для регионов EU и US. Интерфейс доступен на русском и английском.

- **Персонажи Raider.IO** — экипировка, код талантов, рейтинг и лучшие прохождения Mythic+, рейдовый прогресс.
- **Гильдии Raider.IO** — состав с фильтрами, сводка по классам, специализациям и ролям, прогресс и места по рейдам. Сводку можно скачать в JSON. Из состава можно выбрать до пяти персонажей для сравнения.
- **Сравнение Raider.IO** — экипировка, специализации и рейтинги выбранных персонажей. При общем сезоне Mythic+ можно сопоставить лучшие наблюдаемые ключи по подземельям. Сводка доступна в JSON.
- **Боевые отчёты Warcraft Logs** — характеристики участника в выбранном бою, способности, таймлайн и расчёт DPS/HPS при наличии полного потока событий.
- **Демоданные** — примеры профиля, анализа экипировки, гильдии, сравнения и боевого отчёта для знакомства с интерфейсом без ключей.

## Быстрый запуск на Windows

Запустите Docker Desktop в режиме Linux-контейнеров. Откройте PowerShell в папке проекта, где находится `compose.yaml`.

```powershell
docker compose up --build --wait
```

Откройте сайт после завершения команды. При первом запуске с настройками по умолчанию доступны демоданные. [Kipet WoW Analyzer](http://127.0.0.1:5173/)

Для реальных профилей включите `RAIDERIO_ENABLED=true` в локальном `.env`. Для Warcraft Logs настройте отдельную БД отчётов и OAuth-клиент API V2. [Пошаговая инструкция по запуску и настройке](docs/operations/local-development.md)

Для остановки выполните `docker compose stop`. Данные PostgreSQL сохранятся.

## Как устроен проект

| Компонент | Назначение |
| --- | --- |
| `web/` | React, TypeScript и Vite. Интерфейс обслуживается nginx, который проксирует запросы к API. |
| `cmd/api/` | Go HTTP API на Chi. Возвращает снимки данных и состояние фоновых задач. |
| `cmd/worker/` | Фоновое получение данных и обработка отчётов. |
| `migrations/` | SQL-миграции PostgreSQL. Compose применяет их перед запуском API и worker. |
| `compose.yaml` | Локальное окружение с PostgreSQL и Redis. |

## Документация

- [Утверждённая архитектура v1.0](docs/architecture/v1.0.md) · [Решение о принятии архитектуры](docs/decisions/0001-architecture-baseline.md) · [Спецификация MVP](docs/mvp/specification.md)
- [REST-контракт](docs/mvp/api-contract.md) · [OpenAPI](api/openapi/mvp-v1.yaml) · [Модель данных](docs/mvp/data-model.md) · [Проект SQL-схемы](docs/mvp/schema.sql) · [Контракты адаптеров](docs/mvp/adapter-contracts.md)
- [Наборы правил](rules/README.md) · [Правила анализа](docs/mvp/analysis-rules.md) · [Приёмочные сценарии](docs/mvp/acceptance-tests.md) · [Источники](docs/mvp/sources.md) · [Проверка спецификаций](docs/mvp/validation.md)
- [Проверка реализации](docs/operations/implementation-validation.md) · [Анализ](docs/operations/analysis-validation.md) · [Mythic+ и рейды](docs/operations/progress-validation.md) · [Боевые отчёты](docs/operations/combat-validation.md) · [Гильдии и сравнение](docs/operations/social-validation.md)
- [Границы релиза](docs/operations/release-validation.md) · [Prometheus и Grafana](docs/operations/monitoring.md) · [Нагрузочные проверки](docs/operations/load-testing.md) · [Резервное копирование и восстановление](docs/operations/recovery.md)
