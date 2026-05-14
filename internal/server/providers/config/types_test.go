package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestPeerRow_RoundTrip_WithPurpose serialises a File containing a peer
// with a purpose string, deserialises, and re-serialises to verify the
// purpose value is preserved byte-for-byte and no other fields mutate.
func TestPeerRow_RoundTrip_WithPurpose(t *testing.T) {
	original := File{
		Version: 1,
		Peers: []PeerRow{
			{
				Name:    "boxd",
				Address: "graphql.orchard.boxd.sh:443",
				TLS:     true,
				Purpose: "boxd_orchardist on graphql.orchard.boxd.sh",
			},
		},
	}

	// First serialise.
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("first marshal: %v", err)
	}

	// Deserialise.
	var decoded File
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Re-serialise.
	data2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}

	// Check purpose value is preserved.
	if got := decoded.Peers[0].Purpose; got != "boxd_orchardist on graphql.orchard.boxd.sh" {
		t.Errorf("purpose not preserved after round-trip: got %q", got)
	}

	// Both serialisations must be identical (no field mutation).
	if string(data) != string(data2) {
		t.Errorf("round-trip mutated JSON:\n  first:  %s\n  second: %s", data, data2)
	}
}

// TestPeerRow_RoundTrip_WithoutPurpose verifies that a peer without a
// purpose field does not gain `"purpose": ""` after a JSON round-trip.
// This is the omitempty guarantee.
func TestPeerRow_RoundTrip_WithoutPurpose(t *testing.T) {
	original := File{
		Version: 1,
		Peers: []PeerRow{
			{
				Name:    "boxd",
				Address: "graphql.orchard.boxd.sh:443",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if strings.Contains(string(data), `"purpose"`) {
		t.Errorf(`omitempty failed: JSON contains "purpose" key when purpose is empty: %s`, data)
	}

	// Deserialise and re-serialise; still must not contain the key.
	var decoded File
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	data2, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("second marshal: %v", err)
	}

	if strings.Contains(string(data2), `"purpose"`) {
		t.Errorf(`omitempty failed after round-trip: JSON contains "purpose" key: %s`, data2)
	}
}

// TestPeerRow_AllFieldsPopulated deserialises a peer row with all four
// fields set and asserts every field is populated without error.
func TestPeerRow_AllFieldsPopulated(t *testing.T) {
	raw := `{"name":"boxd","address":"graphql.orchard.boxd.sh:443","tls":true,"purpose":"p"}`

	var row PeerRow
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if row.Name != "boxd" {
		t.Errorf("Name: got %q, want %q", row.Name, "boxd")
	}
	if row.Address != "graphql.orchard.boxd.sh:443" {
		t.Errorf("Address: got %q, want %q", row.Address, "graphql.orchard.boxd.sh:443")
	}
	if !row.TLS {
		t.Errorf("TLS: got false, want true")
	}
	if row.Purpose != "p" {
		t.Errorf("Purpose: got %q, want %q", row.Purpose, "p")
	}
}
