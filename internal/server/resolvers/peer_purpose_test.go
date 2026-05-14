// Tests for the purposeForLocalHost helper (AC #2 — peer-sourced Host.purpose).
package resolvers

import (
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

func TestPurposeForLocalHost(t *testing.T) {
	cases := []struct {
		name    string
		local   *graphql1.Host
		cfgs    []peerproxy.PeerConfig
		want    string
	}{
		{
			name: "matches by hostname",
			local: &graphql1.Host{
				MachineID: "some-machine",
				Hostname:  "graphql.orchard.boxd.sh",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "orchard", Address: "graphql.orchard.boxd.sh", Purpose: "boxd_orchardist"},
			},
			want: "boxd_orchardist",
		},
		{
			name: "matches via SSH-user prefix stripping on Address",
			local: &graphql1.Host{
				MachineID: "some-machine",
				Hostname:  "graphql.orchard.boxd.sh",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "orchard", Address: "boxd@graphql.orchard.boxd.sh", Purpose: "boxd_orchardist"},
			},
			want: "boxd_orchardist",
		},
		{
			name: "returns empty when no peer matches",
			local: &graphql1.Host{
				MachineID: "local-machine",
				Hostname:  "local.example.com",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "other", Address: "other.example.com", Purpose: "some_purpose"},
			},
			want: "",
		},
		{
			name: "returns empty when matched peer has no purpose",
			local: &graphql1.Host{
				MachineID: "some-machine",
				Hostname:  "graphql.orchard.boxd.sh",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "orchard", Address: "graphql.orchard.boxd.sh", Purpose: ""},
			},
			want: "",
		},
		{
			name: "matches by cfg.Name == local.MachineID",
			local: &graphql1.Host{
				MachineID: "orchard",
				Hostname:  "some-hostname.example.com",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "orchard", Address: "1.2.3.4:7777", Purpose: "my_purpose"},
			},
			want: "my_purpose",
		},
		{
			name: "matches by cfg.Name == local.Hostname",
			local: &graphql1.Host{
				MachineID: "machine-id-x",
				Hostname:  "orchard",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "orchard", Address: "1.2.3.4:7777", Purpose: "named_purpose"},
			},
			want: "named_purpose",
		},
		{
			name: "matches by cfg.Address == local.MachineID",
			local: &graphql1.Host{
				MachineID: "10.0.0.1:7777",
				Hostname:  "some-hostname",
			},
			cfgs: []peerproxy.PeerConfig{
				{Name: "peer", Address: "10.0.0.1:7777", Purpose: "addr_purpose"},
			},
			want: "addr_purpose",
		},
		{
			name: "nil local host returns empty",
			local: nil,
			cfgs: []peerproxy.PeerConfig{
				{Name: "peer", Address: "10.0.0.1:7777", Purpose: "some_purpose"},
			},
			want: "",
		},
		{
			name:  "empty cfgs returns empty",
			local: &graphql1.Host{MachineID: "m", Hostname: "h"},
			cfgs:  []peerproxy.PeerConfig{},
			want:  "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := purposeForLocalHost(tc.local, tc.cfgs)
			if got != tc.want {
				t.Errorf("purposeForLocalHost() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStripSSHUser(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"boxd@graphql.orchard.boxd.sh", "graphql.orchard.boxd.sh"},
		{"user@host.example.com", "host.example.com"},
		{"graphql.orchard.boxd.sh", "graphql.orchard.boxd.sh"},
		{"localhost", "localhost"},
		{"10.0.0.1:7777", "10.0.0.1:7777"},
		{"", ""},
	}
	for _, tc := range cases {
		got := stripSSHUser(tc.in)
		if got != tc.want {
			t.Errorf("stripSSHUser(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
