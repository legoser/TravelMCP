# Админка TravelMCP — как пользоваться

`GET /admin` — веб-интерфейс, `Authorization: Bearer <ADMIN_TOKEN|tm_...>` для API.

## Вход
* **Первый админ**: задайте `ADMIN_TOKEN=secret` в `.env` → `make run` → вставьте `secret` в поле токена наверху → Сохранить. Создаётся синтетический `user 0 admin`.
* **Обычные пользователи**: вкладка вход — `email`/`пароль` → `Войти → получить токен` (`POST /api/v1/login` → `tm_...`). До `active` нужна модерация `POST /api/v1/users/{id}/moderate {"status":"active"}` admin'ом.
* Все `/admin/*` и `/api/v1/admin/*` требуют `role=admin` или `ADMIN_TOKEN`.

## Вкладки /admin

### Пользователи / Ключи / Конфиг / Дашборд
Базовые: модерация `pending→active→blocked`, выдача `mcp:read/admin` ключей (`POST /api/v1/keys`), hot-reload `PUT /api/v1/config {"providers":{"enabled":["synth","intercity"]},"planner":{"engine":"csa"}}`, дашборд `GET /api/v1/dashboard` + `GET /api/v1/providers`/`/metrics`.

### Импорты (Фаза 5 B+)
`GET /api/v1/admin/imports` — последние `imports(provider, at, records, status, checksum)` (источник истины `Postgres`, порядок `at DESC`).  
`GET /api/v1/admin/logs` — `import_logs(job_id, entity_type, stage, action, confidence)` — per-stop trace `normalize→enrich→dedup→verify→canonical`.  
`GET /api/v1/jobs` / `POST /api/v1/jobs {"type":"sync_mintrans","payload":"{}"}` — очередь `jobs(state pending→running→retry→done/dead, attempts, next_run)`; состояние видно в таблице.  
Кнопки: **Обновить автобусы** → `POST /api/v1/import/mintrans` → `jobs type=sync_mintrans`, **Обновить ж/д** → `sync_rail`, **Upload gtfs.zip** → `import_gtfs`. Воркер `internal/jobs/worker.go` забирает `ClaimNextJob FOR UPDATE SKIP LOCKED`, `429` → `MarkJobRetry(next_run=now+attempt*2m cap 30m)` + ротация `B=mintrans→yandex→nominatim` (`api_quotas`).

### Терминалы — ручная правка
`PUT /api/v1/admin/terminals/{id} {"name":"Кемерово АВ","lat":55.355,"lon":86.088}`  
Эффект: `terminals.is_locked=true`, `last_verified_at=now`, `terminal_names ru`, `terminal_identifiers`, `provenance(entity_type=terminal, source=manual, confidence=1.0, actor_id=user.ID)` + `review_queue(conflicts_with_confirmed)` + `audit_log(action=update_terminal)`. Последующие авто-импорты не перезапишут `is_locked` (hysteresis, §3.8).

### Ревью
`GET /api/v1/review?region=42&reason=low_confidence` — `v_review_stops` (джойн `review_queue` + `terminals` + `import_logs` по `entity_type+entity_id`), `GET /api/v1/review/export.csv` — CSV для ручного анализа. Проблемные причины: `low_confidence, missing_coords, duplicate_ambiguous, speed_implausible, conflicts_with_confirmed`.

### Внешний API
`POST /api/v1/admin/external-call {"provider":"yandex","query":"Кемерово автовокзал","lat":55.35,"lon":86.08}`  
Шаги: `TryConsumeQuota(provider, quota_limit) WHERE used<quota_limit RETURNING` (атомарно, 429 если 0 строк), `RecordApiCall`, валидация (`query≥2`, `lat/lon` в границах РФ), ответ `validated:true`. При успехе: `audit_log(action=external_call)` + `provenance(source=provider, actor_id)` — `source ≠ actor identity` (провайдер vs оператор). Ошибки мапятся в `400`/`429`.

### Квоты / Аудит
`GET /api/v1/quotas` — `api_quotas(provider,day,used,quota_limit,reset_at)` (`PK provider,day`, `reset_at=00:00 MSK` следующего дня). Инкремент только `INSERT ... ON CONFLICT DO UPDATE SET used=used+1 WHERE used<quota_limit`. Строка на день — источник истины, `ListQuotas` сортирует `day DESC`.  
`GET /api/v1/admin/audit?limit=50` — `audit_log(user_id, action, entity_type, entity_id, at, details jsonb)` — все действия админки (`update_terminal`, `external_call`).

## Типовой сценарий оператора (пилот 42/54/70)
1. `ADMIN_TOKEN` → Импорты → Обновить автобусы → наблюдать `jobs`/`logs`.
2. Ревью → фильтр `low_confidence` → открыть `terminals/{id}` → править → Сохранить (is_locked).
3. Внешний API → `yandex` `Кемерово автовокзал` → Валидация → Сохранить (проверяется quota).
4. Квоты → проверить `used/quota_limit`, Аудит → кто правил.

GTFS per-region (`GET /api/v1/gtfs?region=42` → `gtfs_42_all.zip`, `feed_id=f-ru-42-all`) компилируется воркером в одной `REPEATABLE READ` пачке.
