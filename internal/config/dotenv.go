package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DotenvPath ищет .env вверх от рабочего каталога (или берёт путь из DOTENV).
// Возвращает "" если файла нет.
func DotenvPath() string {
	if p := os.Getenv("DOTENV"); p != "" {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	for {
		candidate := filepath.Join(dir, ".env")
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// LoadDotenv парсит .env и выставляет значения os.Environ, только если
// переменная ещё не задана в реальном окружении (env имеет приоритет).
// Отсутствующий файл — не ошибка.
func LoadDotenv() error {
	path := DotenvPath()
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	values, err := parseDotenv(data)
	if err != nil {
		return err
	}
	for k, v := range values {
		if _, exists := os.LookupEnv(k); !exists {
			if err := os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseDotenv разбирает простой формат KEY=VALUE: комментарии (#), строки
// "export ", одинарные/двойные кавычки, empty-значения. Спецсимволов и
// многострочных значений нет.
func parseDotenv(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		eq := strings.IndexByte(line, '=')
		if eq < 1 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		if key == "" {
			continue
		}
		val := strings.TrimSpace(line[eq+1:])
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		out[key] = val
	}
	return out, nil
}
