package memory

import (
	"context"
	"testing"

	"travelmcp/internal/model"
)

// issue #8/#14: одобренный терминал не должен снова попадать в модерацию
// после повторного импорта того же конфликта. Цепочка: open-конфликт →
// resolve → повторная детекция того же fingerprint (или пустого) → запись
// остаётся закрытой и не видна в списках ревью.
func TestReviewQueueStickyResolved(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 42, Reason: "conflicts_with_confirmed", Fingerprint: "osm:123"}); err != nil {
		t.Fatal(err)
	}
	rows, err := m.ListReviewQueue(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("after save: len=%d, want 1", len(rows))
	}

	if err := m.ResolveReviewQueue(ctx, "terminal", 42, "conflicts_with_confirmed", "resolved"); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.ListReviewQueue(ctx, 0)
	if len(rows) != 0 {
		t.Fatalf("after resolve: open rows=%d, want 0", len(rows))
	}

	// повторный импорт: тот же fingerprint — не пере-открываем
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 42, Reason: "conflicts_with_confirmed", Fingerprint: "osm:123"}); err != nil {
		t.Fatal(err)
	}
	// и пустой fingerprint (UpsertTerminal без кодов) — тоже sticky
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 42, Reason: "conflicts_with_confirmed", Fingerprint: ""}); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.ListReviewQueue(ctx, 0)
	if len(rows) != 0 {
		t.Fatalf("after re-detect same conflict: open rows=%d, want 0 (sticky resolved)", len(rows))
	}

	// изменившийся fingerprint (реально новые данные) — пере-открываем
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 42, Reason: "conflicts_with_confirmed", Fingerprint: "osm:999"}); err != nil {
		t.Fatal(err)
	}
	rows, _ = m.ListReviewQueue(ctx, 0)
	if len(rows) != 1 {
		t.Fatalf("after new fingerprint: open rows=%d, want 1", len(rows))
	}
}

// dismiss → rejected: sticky против пере-открытия, но пере-открытие при
// новых данных работает одинаково для обеих sticky-сторон.
func TestReviewQueueStickyRejected(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()

	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 7, Reason: "skeleton_unverified"}); err != nil {
		t.Fatal(err)
	}
	if err := m.ResolveReviewQueue(ctx, "terminal", 7, "skeleton_unverified", "rejected"); err != nil {
		t.Fatal(err)
	}
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "terminal", EntityID: 7, Reason: "skeleton_unverified"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.ListReviewQueue(ctx, 0)
	if len(rows) != 0 {
		t.Fatalf("rejected must stay sticky on same/empty fingerprint, got %d open", len(rows))
	}

	entries, err := m.ListTerminalReviewEntries(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("terminal card must show only open entries, got %d", len(entries))
	}
}

// валидация state в ResolveReviewQueue
func TestResolveReviewQueueInvalidState(t *testing.T) {
	m := NewMemoryStore()
	if err := m.ResolveReviewQueue(context.Background(), "terminal", 1, "", "bogus"); err == nil {
		t.Fatal("invalid state must error")
	}
}

// PostgreSQL-строка ревью из memory-стора должна транслировать state
// корректно (зеркало ReviewQueueRow).
func TestReviewQueueRowStateMirror(t *testing.T) {
	m := NewMemoryStore()
	ctx := context.Background()
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "trip", EntityID: 100, Reason: "incomplete_trip", Fingerprint: "yandex:t1"}); err != nil {
		t.Fatal(err)
	}
	if err := m.ResolveReviewQueue(ctx, "trip", 100, "incomplete_trip", "resolved"); err != nil {
		t.Fatal(err)
	}
	// пере-открытие с новым fingerprint возвращает state=open
	if err := m.SaveReviewQueue(ctx, model.ReviewQueueEntry{EntityType: "trip", EntityID: 100, Reason: "incomplete_trip", Fingerprint: "yandex:t2"}); err != nil {
		t.Fatal(err)
	}
	rows, _ := m.ListReviewQueue(ctx, 0)
	if len(rows) != 1 || rows[0].State != "open" {
		t.Fatalf("reopened row must be open, got %+v", rows)
	}
}
