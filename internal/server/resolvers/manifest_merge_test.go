package resolvers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/manifest"
)

// loadManifest returns a started provider rooted at a tempdir containing
// the given YAML body. t.Cleanup stops the provider so the refresh
// goroutine exits with the test.
func loadManifest(t *testing.T, body string) *manifest.Provider {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fleet-manifest.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	p := manifest.New(path)
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("manifest.Start: %v", err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	return p
}

const fleetYAML = `hosts:
  - name: drudrukungfu
    role: local_orchardist
    address: local
    purpose: "Drew's Mac."
    owner_orchardist: local_orchardist
    decommission_signal: never
    last_verified: "2026-05-13"
  - name: lw-fed-c
    role: federation_worker
    address: "boxd@lw-fed-c.boxd.sh"
    purpose: "LangWatch federation worker."
    owner_orchardist: shared
    decommission_signal: "shared pool"
    last_verified: "2026-05-13"
  - name: issue3201
    role: fork_per_issue
    address: "boxd@issue3201.boxd.sh"
    purpose: "Dedicated VM for lw#3201."
    owner_orchardist: boxd_orchardist
    decommission_signal: "lw#3201 closed AND PR merged"
    last_verified: unknown
`

// findHost is a tiny helper to look up a Host by machineId in a slice.
func findHost(hosts []*graphql1.Host, key string) *graphql1.Host {
	for _, h := range hosts {
		if h.MachineID == key || h.Hostname == key {
			return h
		}
	}
	return nil
}

func TestMergeManifestHosts_AppendsOfflineEntries(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	live := []*graphql1.Host{
		{ID: "Host:drudrukungfu", MachineID: "drudrukungfu", Hostname: "drudrukungfu", Reachable: true, Peers: []*graphql1.Host{}},
	}
	merged := mergeManifestHosts(live, p)
	if got, want := len(merged), 3; got != want {
		t.Fatalf("merged length = %d, want %d (1 live + 2 manifest-only)", got, want)
	}
	for _, name := range []string{"drudrukungfu", "lw-fed-c", "issue3201"} {
		h := findHost(merged, name)
		if h == nil {
			t.Fatalf("expected %q to appear in merged hosts", name)
		}
		if !h.InManifest {
			t.Fatalf("%q should have inManifest=true", name)
		}
	}
}

func TestMergeManifestHosts_MarksDrift(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	live := []*graphql1.Host{
		// Live host not in the manifest — pure drift.
		{ID: "Host:wandering", MachineID: "wandering", Hostname: "wandering", Reachable: true, Peers: []*graphql1.Host{}},
	}
	merged := mergeManifestHosts(live, p)
	wandering := findHost(merged, "wandering")
	if wandering == nil {
		t.Fatal("drift host must remain in the merged output")
	}
	if wandering.InManifest {
		t.Fatal("drift host must report inManifest=false")
	}
	if wandering.Role != nil {
		t.Fatal("drift host should not carry a manifest role")
	}
}

func TestMergeManifestHosts_EnrichesLiveHost(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	live := []*graphql1.Host{
		{ID: "Host:drudrukungfu", MachineID: "drudrukungfu", Hostname: "drudrukungfu", Reachable: true, Peers: []*graphql1.Host{}},
	}
	merged := mergeManifestHosts(live, p)
	h := findHost(merged, "drudrukungfu")
	if h == nil || !h.InManifest {
		t.Fatalf("expected drudrukungfu enriched, got %+v", h)
	}
	if h.Role == nil || *h.Role != graphql1.HostRoleLocalOrchardist {
		t.Fatalf("role should be local_orchardist, got %+v", h.Role)
	}
	if h.Purpose == nil || *h.Purpose != "Drew's Mac." {
		t.Fatalf("purpose mismatch, got %+v", h.Purpose)
	}
	if h.OwnerOrchardist == nil || *h.OwnerOrchardist != "local_orchardist" {
		t.Fatalf("owner mismatch, got %+v", h.OwnerOrchardist)
	}
	if h.LastVerified == nil || *h.LastVerified != "2026-05-13" {
		t.Fatalf("lastVerified mismatch, got %+v", h.LastVerified)
	}
}

func TestMergeManifestHosts_OfflineEntryShape(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	merged := mergeManifestHosts(nil, p)
	h := findHost(merged, "issue3201")
	if h == nil {
		t.Fatal("expected offline entry for issue3201")
	}
	if h.Reachable {
		t.Fatal("offline entry must report reachable=false")
	}
	if h.LastSeenAt != "" {
		t.Fatalf("offline entry must have empty lastSeenAt, got %v", h.LastSeenAt)
	}
	if !h.InManifest {
		t.Fatal("offline entry must have inManifest=true")
	}
	if h.Address == nil || *h.Address != "boxd@issue3201.boxd.sh" {
		t.Fatalf("offline entry should carry the manifest address, got %+v", h.Address)
	}
	if h.LastVerified == nil || *h.LastVerified != "unknown" {
		t.Fatalf("lastVerified should preserve `unknown`, got %+v", h.LastVerified)
	}
}

func TestMergeManifestHosts_LookupByAddress(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	// Live host whose MachineID does NOT match — but address does.
	addr := "boxd@lw-fed-c.boxd.sh"
	live := []*graphql1.Host{
		{ID: "Host:peer-fed-c", MachineID: "peer-fed-c", Hostname: "peer-fed-c", Reachable: true,
			Address: &addr, Peers: []*graphql1.Host{}},
	}
	merged := mergeManifestHosts(live, p)
	if got := len(merged); got != 3 {
		t.Fatalf("expected 3 merged (1 live matched by address + 2 manifest-only), got %d", got)
	}
	live0 := merged[0]
	if !live0.InManifest {
		t.Fatal("live host whose address matches manifest should be marked inManifest")
	}
	if live0.Role == nil || *live0.Role != graphql1.HostRoleFederationWorker {
		t.Fatalf("role should resolve through address, got %+v", live0.Role)
	}
}

func TestMergeManifestHosts_NilProvider(t *testing.T) {
	live := []*graphql1.Host{
		{ID: "Host:solo", MachineID: "solo", Reachable: true, Peers: []*graphql1.Host{}},
	}
	merged := mergeManifestHosts(live, nil)
	if got := len(merged); got != 1 {
		t.Fatalf("nil provider should leave the live list alone, got %d entries", got)
	}
	if merged[0].InManifest {
		t.Fatal("with no manifest, inManifest must be false")
	}
}

func TestMapHostRole_UnknownReturnsFalse(t *testing.T) {
	if _, ok := mapHostRole("not_a_real_role"); ok {
		t.Fatal("unknown role must return ok=false")
	}
	if role, ok := mapHostRole("federation_worker"); !ok || role != graphql1.HostRoleFederationWorker {
		t.Fatalf("federation_worker should map, got (%v, %v)", role, ok)
	}
}

func TestBuildManifestStatus_NilProvider(t *testing.T) {
	st := buildManifestStatus(nil)
	if st == nil {
		t.Fatal("buildManifestStatus must return a non-nil status even when no provider is wired")
	}
	if st.Loaded {
		t.Fatal("status with no provider must report loaded=false")
	}
	if st.HostCount != 0 {
		t.Fatalf("status with no provider must report 0 hosts, got %d", st.HostCount)
	}
}

func TestBuildManifestStatus_PopulatesFromProvider(t *testing.T) {
	p := loadManifest(t, fleetYAML)
	st := buildManifestStatus(p)
	if !st.Loaded {
		t.Fatal("provider with a good manifest must report loaded=true")
	}
	if st.HostCount != 3 {
		t.Fatalf("hostCount = %d, want 3", st.HostCount)
	}
	if st.LastLoadedAt == nil || *st.LastLoadedAt == "" {
		t.Fatal("LastLoadedAt should be set after a successful load")
	}
	if st.Error != nil {
		t.Fatalf("Error should be nil on success, got %v", *st.Error)
	}
}
