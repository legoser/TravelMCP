package logger

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"travelmcp/internal/config"
	"travelmcp/internal/telemetry"
)

type Factory struct {
	global    slog.Level
	overrides map[string]slog.Level
	addSource bool
	format    string
	lokiCfg   *config.LokiLog
}

func NewFactory(cfg config.Log) *Factory {
	f := &Factory{
		global:    parseLevel(cfg.Level),
		overrides: map[string]slog.Level{},
		addSource: cfg.AddSource,
		format:    cfg.Format,
	}
	if cfg.Loki.URL != "" && (cfg.Loki.Enabled == nil || *cfg.Loki.Enabled) {
		lk := cfg.Loki
		f.lokiCfg = &lk
	}
	for k, v := range cfg.Levels {
		key := strings.ToLower(strings.ReplaceAll(k, ".", "_"))
		key = strings.ReplaceAll(key, "-", "_")
		f.overrides[key] = parseLevel(v)
		f.overrides[strings.ToLower(k)] = parseLevel(v)
	}
	return f
}

func (f *Factory) For(module string) *slog.Logger {
	lvl := f.levelFor(module)
	addSource := f.addSource || lvl == slog.LevelDebug
	var h slog.Handler
	if strings.ToLower(f.format) == "text" {
		h = newColorHandler(os.Stdout, lvl, addSource)
	} else {
		opts := &slog.HandlerOptions{
			Level:     lvl,
			AddSource: addSource,
			ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
				if a.Key == slog.TimeKey && len(groups) == 0 {
					if t, ok := a.Value.Any().(time.Time); ok {
						a.Value = slog.StringValue(t.Format(time.RFC3339Nano))
					}
				}
				return a
			},
		}
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	lg := slog.New(h)
	if module != "" {
		lg = lg.With("module", module)
	}
	if f.lokiCfg != nil {
		lg = telemetry.NewLokiHandler(*f.lokiCfg, lg)
	}
	return lg
}

func colorLevel(l slog.Level) string {
	switch {
	case l <= slog.LevelDebug:
		return "\x1b[36mDBG\x1b[0m"
	case l <= slog.LevelInfo:
		return "\x1b[32mINF\x1b[0m"
	case l <= slog.LevelWarn:
		return "\x1b[33mWRN\x1b[0m"
	default:
		return "\x1b[31mERR\x1b[0m"
	}
}

type colorHandler struct {
	mu        sync.Mutex
	w         io.Writer
	level     slog.Level
	addSource bool
	attrs     []slog.Attr
	groups    []string
}

func newColorHandler(w io.Writer, level slog.Level, addSource bool) *colorHandler {
	return &colorHandler{w: w, level: level, addSource: addSource}
}

func (h *colorHandler) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= h.level }

func (h *colorHandler) Handle(_ context.Context, r slog.Record) error {
	tm := r.Time.Format("15:04:05.000")
	lvl := colorLevel(r.Level)
	src := ""
	if h.addSource && r.PC != 0 {
		fs := runtime.CallersFrames([]uintptr{r.PC})
		if fr, ok := <-func() chan runtime.Frame {
			ch := make(chan runtime.Frame, 1)
			go func() {
				f, _ := fs.Next()
				ch <- f
			}()
			return ch
		}(); ok && fr.File != "" {
			src = fmt.Sprintf(" %s:%d", shortFile(fr.File), fr.Line)
		}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "%s %s%s %s", tm, lvl, src, r.Message)
	if len(h.attrs) > 0 {
		for _, a := range h.attrs {
			buf.WriteString(" ")
			buf.WriteString(a.Key)
			buf.WriteString("=")
			buf.WriteString(formatValue(a.Value))
		}
	}
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "" {
			return true
		}
		buf.WriteString(" ")
		if len(h.groups) > 0 {
			buf.WriteString(strings.Join(h.groups, "."))
			buf.WriteString(".")
		}
		buf.WriteString(a.Key)
		buf.WriteString("=")
		buf.WriteString(formatValue(a.Value))
		return true
	})
	buf.WriteString("\n")
	h.mu.Lock()
	_, err := h.w.Write(buf.Bytes())
	h.mu.Unlock()
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	nh := &colorHandler{w: h.w, level: h.level, addSource: h.addSource, attrs: append(append([]slog.Attr(nil), h.attrs...), attrs...), groups: h.groups}
	return nh
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	nh := &colorHandler{w: h.w, level: h.level, addSource: h.addSource, attrs: append([]slog.Attr(nil), h.attrs...), groups: append(append([]string(nil), h.groups...), name)}
	return nh
}

func shortFile(f string) string {
	if idx := strings.LastIndex(f, "/"); idx >= 0 {
		if j := strings.LastIndex(f[:idx], "/"); j >= 0 {
			return f[j+1:]
		}
		return f[idx+1:]
	}
	return f
}

func formatValue(v slog.Value) string {
	switch v.Kind() {
	case slog.KindString:
		s := v.String()
		if strings.ContainsAny(s, " \t\n\"'") {
			return fmt.Sprintf("%q", s)
		}
		return s
	case slog.KindTime:
		return v.Time().Format("15:04:05")
	case slog.KindDuration:
		return v.Duration().String()
	default:
		return fmt.Sprintf("%v", v.Any())
	}
}

func (f *Factory) levelFor(module string) slog.Level {
	key := strings.ToLower(module)
	if lvl, ok := f.overrides[key]; ok {
		return lvl
	}
	norm := strings.ReplaceAll(key, ".", "_")
	if lvl, ok := f.overrides[norm]; ok {
		return lvl
	}
	for k, lvl := range f.overrides {
		if strings.HasPrefix(key, k+".") || strings.HasPrefix(norm, k) {
			return lvl
		}
	}
	return f.global
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
