package sync

// LogicVersion — семантическая версия обработчиков конвейера синхронизации
// (план §4.3/§6): входит в plan_id ВМЕСТО sha(binary). Правило: бамп при
// семантическом изменении обработчика (логика стадий, формат записи,
// ключи матчинга), НЕ при каждом деплое — иначе каждый билд = full-resync
// чанков. Совпадение plan_id с plan_id_done чанка = чанк свежий и
// пропускается независимо от новых live-ответов geocode_cache.
const LogicVersion = "2"

// LogicVersionID — строка для ComputePlanID (единый вход всех раннеров,
// вместо разрозненных appVersion-констант).
func LogicVersionID() string {
	return "logic/" + LogicVersion
}
