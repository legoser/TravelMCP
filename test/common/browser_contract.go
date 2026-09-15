package common

import (
	"context"
	"testing"

	"travelmcp/internal/model"
	store "travelmcp/internal/store"
)

// BrowserSeeder — минимальный write-поверх для сида краевых фикстур.
// Реализуют оба бэкенда (memory/postgres): один контракт гоняется везде.
type BrowserSeeder interface {
	store.BrowserStore
	UpsertTerminal(ctx context.Context, r store.TerminalRow, names map[string]string, identifiers []model.AdaptedIdentifier) (int64, error)
	ListTerminals(ctx context.Context, limit, offset int, sort string) ([]map[string]any, int, error)
	ListTerminalsFiltered(ctx context.Context, limit, offset int, sort, order, q string) ([]map[string]any, int, error)
}

const edgePrefix = "ZZEDGE9Q"

func seedBrowserEdge(t *testing.T, s BrowserSeeder) {
	t.Helper()
	ctx := context.Background()
	names := []string{
		edgePrefix + " Дубль, вокзал",
		edgePrefix + " Дубль, вокзал",
		edgePrefix + " 100% хлопок, остановка",
		edgePrefix + " Стоп_Х, павильон",
		edgePrefix + " Обычный, причал",
	}
	for i, n := range names {
		if _, err := s.UpsertTerminal(ctx, store.TerminalRow{Lat: 55.0 + float64(i)*0.01, Lon: 86.0}, map[string]string{"ru": n}, nil); err != nil {
			t.Fatalf("seed %q: %v", n, err)
		}
	}
}

// RunBrowserEdgeContract — краевые случаи листинга, общие для memory/postgres:
// пагинация, сортировка, injection-строки, LIKE-метасимволы. Сид изолирован
// префиксом edgePrefix: чужие данные в БД на проверки не влияют.
func RunBrowserEdgeContract(t *testing.T, s BrowserSeeder) {
	t.Helper()
	ctx := context.Background()
	seedBrowserEdge(t, s)

	ids := func(items []map[string]any) []int64 {
		out := make([]int64, 0, len(items))
		for _, it := range items {
			id, ok := it["id"].(int64)
			if !ok {
				t.Fatalf("id is %T, want int64", it["id"])
			}
			out = append(out, id)
		}
		return out
	}
	list := func(limit, offset int, sort, order, q string) ([]map[string]any, int) {
		t.Helper()
		items, total, err := s.ListTerminalsFiltered(ctx, limit, offset, sort, order, q)
		if err != nil {
			t.Fatalf("ListTerminalsFiltered(%d,%d,%q,%q,%q): %v", limit, offset, sort, order, q, err)
		}
		return items, total
	}

	t.Run("zero limit", func(t *testing.T) {
		items, total := list(0, 0, "", "asc", edgePrefix)
		if len(items) != 0 || total != 5 {
			t.Fatalf("want 0 items total 5, got %d total %d", len(items), total)
		}
	})
	t.Run("negative page", func(t *testing.T) {
		items, _ := list(-5, -3, "", "asc", edgePrefix)
		if len(items) != 0 {
			t.Fatalf("negative page must be empty, got %d", len(items))
		}
	})
	t.Run("offset beyond total", func(t *testing.T) {
		items, total := list(20, 1000, "", "asc", edgePrefix)
		if len(items) != 0 || total != 5 {
			t.Fatalf("want empty total 5, got %d total %d", len(items), total)
		}
	})
	t.Run("page slice", func(t *testing.T) {
		all, _ := list(100, 0, "", "asc", edgePrefix)
		page, _ := list(2, 1, "", "asc", edgePrefix)
		want := ids(all)[1:3]
		got := ids(page)
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Fatalf("page mismatch: want %v got %v", want, got)
		}
	})
	t.Run("injection sort/order", func(t *testing.T) {
		items, total := list(100, 0, "'; DROP TABLE terminals;--", "desc\x00", edgePrefix)
		if total != 5 || len(items) != 5 {
			t.Fatalf("injection must fall back to default order: %d total %d", len(items), total)
		}
		got := ids(items)
		for i := 1; i < len(got); i++ {
			if got[i-1] > got[i] {
				t.Fatalf("default order must be id asc: %v", got)
			}
		}
	})
	t.Run("injection q", func(t *testing.T) {
		items, total := list(100, 0, "", "asc", "' OR '1'='1")
		if total != 0 || len(items) != 0 {
			t.Fatalf("injection q must match nothing: %d total %d", len(items), total)
		}
	})
	t.Run("percent literal", func(t *testing.T) {
		items, total := list(100, 0, "", "asc", "100%")
		if total != 1 || len(items) != 1 {
			t.Fatalf("%% must be literal (want 1): %d total %d", len(items), total)
		}
	})
	t.Run("underscore literal", func(t *testing.T) {
		items, total := list(100, 0, "", "asc", edgePrefix+" Стоп_Х")
		if total != 1 || len(items) != 1 {
			t.Fatalf("_ must be literal (want 1): %d total %d", len(items), total)
		}
	})
	t.Run("name sort deterministic", func(t *testing.T) {
		first, _ := list(100, 0, "name", "asc", edgePrefix)
		want := ids(first)
		for i := 0; i < 30; i++ {
			items, _ := list(100, 0, "name", "asc", edgePrefix)
			got := ids(items)
			for j := range want {
				if want[j] != got[j] {
					t.Fatalf("unstable name order run %d: %v vs %v", i, want, got)
				}
			}
		}
	})
	t.Run("wrapper negative", func(t *testing.T) {
		items, _, err := s.ListTerminals(ctx, -1, -1, "")
		if err != nil {
			t.Fatalf("ListTerminals negative: %v", err)
		}
		if len(items) != 0 {
			t.Fatalf("negative wrapper must be empty, got %d", len(items))
		}
	})
}
