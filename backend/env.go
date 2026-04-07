package backend

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// LoadEnvFile reads a .env file and returns key=value pairs as a map.
// It searches the executable's directory first, then falls back to the current working directory.
func LoadEnvFile() (map[string]string, error) {
	envPath := ""

	// Try executable directory first
	if exePath, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exePath), ".env")
		if _, err := os.Stat(candidate); err == nil {
			envPath = candidate
		}
	}

	// Fall back to current working directory
	if envPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			candidate := filepath.Join(cwd, ".env")
			if _, err := os.Stat(candidate); err == nil {
				envPath = candidate
			}
		}
	}

	if envPath == "" {
		return nil, os.ErrNotExist
	}

	return parseEnvFile(envPath)
}

func parseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	result := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eqIdx := strings.Index(line, "=")
		if eqIdx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eqIdx])
		value := strings.TrimSpace(line[eqIdx+1:])
		// Strip surrounding quotes
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		result[key] = value
	}
	return result, scanner.Err()
}
