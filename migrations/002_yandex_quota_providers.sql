-- issue #10: у Яндекса два независимых API с отдельными ключами и
-- суточными лимитами — Rasp (расписания, лимит из yandex.rasp_quota_limit,
-- дефолт 500) и Geocoder (геокодинг, лимит из geocoder.max_calls, дефолт
-- 1000). Раньше оба писали в одну строку api_quotas(provider='yandex')
-- и блокировали друг друга. Код 'yandex' остаётся каноническим system
-- для identifier_schemes/provenance (источник данных не меняется);
-- yandex_rasp/yandex_geocode — только ключи квот (docs/14-plan.md §3.7).
INSERT INTO providers(code, name) VALUES
  ('yandex_rasp', 'Яндекс Расписание (Rasp API)'),
  ('yandex_geocode', 'Яндекс Геокодер (Geocode API)')
ON CONFLICT DO NOTHING;
