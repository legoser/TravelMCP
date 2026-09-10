package sync

// LogicVersion — семантическая версия обработчиков конвейера синхронизации
// (план §4.3/§6): входит в plan_id ВМЕСТО sha(binary). Правило: бамп при
// семантическом изменении обработчика (логика стадий, формат записи,
// ключи матчинга), НЕ при каждом деплое — иначе каждый билд = full-resync
// чанков. Совпадение plan_id с plan_id_done чанка = чанк свежий и
// пропускается независимо от новых live-ответов geocode_cache.
//
// "3": вход trips-sync — flat-формат (flat_trips.json от standalone-коннектора
// tools/registry-parser) вместо среза реестра; RouteNK приходит готовым из
// коннектора (trust-nk переехал туда), коды стопов — в FlatStop.Codes;
// source — обязательный параметр (fail-loud, дефолтов нет).
const LogicVersion = "3"

// LogicVersionID — строка для ComputePlanID (единый вход всех раннеров,
// вместо разрозненных appVersion-констант).
func LogicVersionID() string {
	return "logic/" + LogicVersion
}
