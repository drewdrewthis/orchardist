package manifest

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// Entry is a single host row parsed from the manifest file.
//
// Fields mirror the YAML schema (`name`, `role`, `purpose`,
// `owner_orchardist`, `decommission_signal`, `last_verified`, `address`).
// Unknown YAML keys are tolerated so the manifest format can grow without
// breaking older daemons.
type Entry struct {
	Name               string `yaml:"name"`
	Role               string `yaml:"role"`
	Address            string `yaml:"address"`
	Purpose            string `yaml:"purpose"`
	OwnerOrchardist    string `yaml:"owner_orchardist"`
	DecommissionSignal string `yaml:"decommission_signal"`
	LastVerified       string `yaml:"last_verified"`
}

// rawFile mirrors the top-level YAML structure. The manifest carries
// schema metadata (`schema_version`, `last_update`) which the daemon
// ignores — only the `hosts` slice is loaded.
type rawFile struct {
	SchemaVersion int     `yaml:"schema_version"`
	Hosts         []Entry `yaml:"hosts"`
}

// parseManifest decodes raw YAML bytes into a slice of entries. The
// result is normalised: blank names are dropped, the first occurrence of
// a duplicate name wins, and `last_verified` is coerced to a string so
// the YAML `unknown` bareword and a quoted ISO date both round-trip.
//
// Errors describe what went wrong (line numbers come from yaml.v3) so
// the caller can surface them through the `health` query unchanged.
func parseManifest(data []byte) ([]Entry, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var raw rawFile
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse manifest yaml: %w", err)
	}
	out := make([]Entry, 0, len(raw.Hosts))
	seen := make(map[string]struct{}, len(raw.Hosts))
	for _, e := range raw.Hosts {
		name := strings.TrimSpace(e.Name)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, Entry{
			Name:               name,
			Role:               strings.TrimSpace(e.Role),
			Address:            strings.TrimSpace(e.Address),
			Purpose:            strings.TrimSpace(e.Purpose),
			OwnerOrchardist:    strings.TrimSpace(e.OwnerOrchardist),
			DecommissionSignal: strings.TrimSpace(e.DecommissionSignal),
			LastVerified:       strings.TrimSpace(e.LastVerified),
		})
	}
	return out, nil
}
