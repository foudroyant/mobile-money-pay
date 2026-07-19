// Package envfile loads KEY=VALUE pairs from a .env file into the process
// environment, so config doesn't have to be retyped in every shell session.
// Deliberately minimal (no external dependency) — this is not a general
// dotenv implementation, just enough for this project's flat config.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load reads path and calls os.Setenv for each KEY=VALUE line, skipping
// blank lines and lines starting with "#". An existing environment
// variable is never overwritten, so real env vars (e.g. set by a deploy
// platform) always win over the file. It is not an error for path to not
// exist — .env is optional local convenience, not required config.
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}

		if _, alreadySet := os.LookupEnv(key); !alreadySet {
			os.Setenv(key, value)
		}
	}

	return scanner.Err()
}
