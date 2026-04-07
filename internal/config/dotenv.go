package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

// LoadDotEnv は既存の環境変数を優先しつつ .env を読み込みます。
func LoadDotEnv(path string) error {
	values, err := ReadDotEnv(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	for key, value := range values {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}

	return nil
}

// ReadDotEnv は単純な KEY=VALUE 形式の .env を解析します。
func ReadDotEnv(path string) (map[string]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	values := make(map[string]string)
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			return nil, errors.New("invalid .env line " + strconv.Itoa(i+1))
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)
		if key == "" {
			return nil, errors.New("empty key in .env line " + strconv.Itoa(i+1))
		}

		values[key] = value
	}

	return values, nil
}
