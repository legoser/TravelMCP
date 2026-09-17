package sync

import "fmt"

// ErrOfflineCacheEmpty — offline-сбор (только кэш): в дисковом кэше нет
// расписаний станции ни на одну дату. Ретраить бессмысленно — кэш сам
// не появится; воркер переводит такое задание сразу в dead.
// Сообщение намеренно без слов-маркеров квоты/рейт-лимита, чтобы
// substring-классификатор воркера не уводил его в ретрай-ветку.
type ErrOfflineCacheEmpty struct {
	TerminalID int64
	Code       string
	Region     string
	Date       string
}

func (e *ErrOfflineCacheEmpty) Error() string {
	code := e.Code
	if code == "" {
		code = "?"
	}
	return fmt.Sprintf("collect trips: расписаний станции %s нет в кэше ни на одну дату (offline, terminal %d, регион %q, дата %s) — снимите «только кэш» для сбора через API", code, e.TerminalID, e.Region, e.Date)
}

// ErrQuotaBlocked — сбор упёрся в суточную квоту провайдера. Повтор
// ставится на время сброса квоты (reset_at из api_quotas), а не на
// минутный бэкофф.
type ErrQuotaBlocked struct {
	Provider string
}

func (e *ErrQuotaBlocked) Error() string {
	return fmt.Sprintf("collect trips: суточный лимит %s исчерпан, повтор после сброса квоты", e.Provider)
}
