package repodiscovery

import "context"

// ConfiguredSource lifts the existing config provider into the
// discovery [Source] contract. The wrapper exists so the [Provider]
// can treat all three feeds (configured + tmux + claudeprojects)
// uniformly, even though configured paths arrive pre-canonicalised.
//
// The wrapped [ConfiguredLister] retains authority over slug / id
// metadata — [Provider] re-reads it separately to apply overrides.
type ConfiguredSource struct {
	lister ConfiguredLister
}

// NewConfiguredSource wraps a [ConfiguredLister]. A nil lister yields
// an empty source; the [Provider] tolerates that for tests.
func NewConfiguredSource(lister ConfiguredLister) *ConfiguredSource {
	return &ConfiguredSource{lister: lister}
}

// Roots returns the absolute paths of every configured repo.
// Phantom entries (paths that no longer exist on disk) are NOT filtered
// here — that belongs to the [Provider] so it can log once. Returning
// every configured path keeps the source's contract simple and lets
// the [Provider] reconcile against the configured slug map.
func (s *ConfiguredSource) Roots(ctx context.Context) ([]string, error) {
	if s == nil || s.lister == nil {
		return nil, nil
	}
	repos, err := s.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		if r.Path == "" {
			continue
		}
		out = append(out, r.Path)
	}
	return out, nil
}
