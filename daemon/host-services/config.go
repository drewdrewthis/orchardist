package hostservices

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// DefaultServices is the watchlist used when `services` is missing from
// ~/.orchard/config.json or the file is absent. Platform-specific values
// live in default_services_darwin.go / default_services_linux.go.
var DefaultServices = defaultServicesPerOS

// configFile is a narrow view of ~/.orchard/config.json. Only `services`
// is decoded; other keys are tolerated by json.Unmarshal. We never write
// this file — `orchard config` is the only writer.
type configFile struct {
	Services []string `json:"services"`
}

// LoadServicesFromConfig reads the watchlist from path. Defaults apply when:
//   - the file is absent (cold boot before `orchard config init`);
//   - the file exists but `services` is missing or null;
//   - the file exists but `services` is an empty or all-blank array.
//
// Returns an error only on malformed JSON or non-ErrNotExist read failures.
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

	out := dedupServices(cfg.Services)
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
