package hostservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// DefaultServices is the watchlist used when `services` is missing from
// `~/.config/orchard/config.json` (or the file itself is absent).
//
// Aligned with the briefing: the orchardist cares about the daemon
// supervising itself (`orchard`), the launch surface for remote claude
// sessions (`claude-remote`), and dotfile sync (`chezmoi`).
var DefaultServices = []string{"claude-remote", "orchard", "chezmoi"}

// configFile is a *narrow* view of `~/.config/orchard/config.json`.
//
// Only `services` is decoded here; the file's other top-level keys
// (e.g. `version`, `projects` from ws-b-config) are tolerated by
// json.Unmarshal's default behaviour. We never write this file —
// `orchard config` is the only writer, so any user-added field round-
// trips intact.
type configFile struct {
	Services []string `json:"services"`
}

// LoadServicesFromConfig reads the watchlist from path. Defaults apply
// when:
//
//   - the file is absent (cold boot before `orchard config init`);
//   - the file exists but `services` is missing or null;
//   - the file exists but `services` is an empty array.
//
// Returns an error only on malformed JSON or read failures other than
// "not exist" — partial config should still boot the daemon with the
// defaults, but a corrupt file is operator-visible noise.
func LoadServicesFromConfig(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cloneDefaults(), nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	var cfg configFile
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}

	if len(cfg.Services) == 0 {
		return cloneDefaults(), nil
	}

	out := make([]string, 0, len(cfg.Services))
	seen := make(map[string]struct{}, len(cfg.Services))
	for _, s := range cfg.Services {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return cloneDefaults(), nil
	}
	return out, nil
}

func cloneDefaults() []string {
	out := make([]string, len(DefaultServices))
	copy(out, DefaultServices)
	return out
}
