package geocoder

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

type fallback struct {
	providers []named
	attempts  int
}

type named struct {
	kind string
	g    Geocoder
}

var defaultPriority = []string{"nominatim", "yandex"}

func NewFallback(providers map[string]Geocoder, attempts int, preferred string) Geocoder {
	if attempts <= 0 {
		attempts = 3
	}
	ordered := make([]named, 0, len(providers))
	if preferred != "" {
		if g, ok := providers[preferred]; ok {
			ordered = append(ordered, named{kind: preferred, g: g})
		}
	}
	priorityRest := make([]string, 0, len(defaultPriority))
	for _, k := range defaultPriority {
		if k == preferred {
			continue
		}
		if _, ok := providers[k]; ok {
			priorityRest = append(priorityRest, k)
		}
	}
	extra := make([]string, 0)
	for k := range providers {
		if k == preferred {
			continue
		}
		found := false
		for _, r := range priorityRest {
			if r == k {
				found = true
				break
			}
		}
		if !found {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	rest := append(priorityRest, extra...)
	for _, k := range rest {
		ordered = append(ordered, named{kind: k, g: providers[k]})
	}
	if len(ordered) == 0 {
		return &fallback{attempts: attempts}
	}
	return &fallback{providers: ordered, attempts: attempts}
}

func (f *fallback) Geocode(ctx context.Context, query string) (*Result, error) {
	if len(f.providers) == 0 {
		return nil, errors.New("геокодер: нет доступных провайдеров")
	}
	var lastErr error
	for i := 0; i < f.attempts; i++ {
		p := f.providers[i%len(f.providers)]
		res, err := p.g.Geocode(ctx, query)
		if err == nil {
			return res, nil
		}
		lastErr = fmt.Errorf("%s: %w", p.kind, err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
	}
	return nil, fmt.Errorf("геокодер: все провайдеры недоступны после %d попыток: %w", f.attempts, lastErr)
}

func (f *fallback) Reverse(ctx context.Context, lat, lon float64) (string, error) {
	if len(f.providers) == 0 {
		return "", errors.New("геокодер: нет доступных провайдеров")
	}
	var lastErr error
	for i := 0; i < f.attempts; i++ {
		p := f.providers[i%len(f.providers)]
		res, err := p.g.Reverse(ctx, lat, lon)
		if err == nil {
			return res, nil
		}
		lastErr = fmt.Errorf("%s: %w", p.kind, err)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
	}
	return "", fmt.Errorf("геокодер: все провайдеры недоступны после %d попыток: %w", f.attempts, lastErr)
}
