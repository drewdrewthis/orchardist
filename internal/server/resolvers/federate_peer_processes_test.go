package resolvers

import (
	"encoding/json"
	"testing"

	graphql1 "github.com/drewdrewthis/git-orchard-rs/internal/server/graphql"
	"github.com/drewdrewthis/git-orchard-rs/internal/server/providers/peerproxy"
)

func TestPeerNameFromHost_PrefersMachineID(t *testing.T) {
	got := peerNameFromHost(&graphql1.Host{ID: "Host:other", MachineID: "boxd-1", Hostname: "fallback"})
	if got != "boxd-1" {
		t.Fatalf("want boxd-1, got %q", got)
	}
}

func TestPeerNameFromHost_FallsBackToHostname(t *testing.T) {
	got := peerNameFromHost(&graphql1.Host{ID: "Host:other", Hostname: "via-hostname"})
	if got != "via-hostname" {
		t.Fatalf("want via-hostname, got %q", got)
	}
}

func TestPeerNameFromHost_FallsBackToIDSuffix(t *testing.T) {
	got := peerNameFromHost(&graphql1.Host{ID: "Host:via-id"})
	if got != "via-id" {
		t.Fatalf("want via-id, got %q", got)
	}
}

func TestPeerNameFromHost_NilOrEmpty(t *testing.T) {
	if got := peerNameFromHost(nil); got != "" {
		t.Fatalf("nil host should return empty, got %q", got)
	}
	if got := peerNameFromHost(&graphql1.Host{}); got != "" {
		t.Fatalf("empty host should return empty, got %q", got)
	}
}

func TestBuildProcessFilterVars_Nil(t *testing.T) {
	if got := buildProcessFilterVars(nil); got != nil {
		t.Fatalf("nil filter should yield nil vars, got %v", got)
	}
}

func TestBuildProcessFilterVars_Empty(t *testing.T) {
	if got := buildProcessFilterVars(&graphql1.ProcessFilter{}); got != nil {
		t.Fatalf("empty filter should yield nil vars (avoid sending {} to peer), got %v", got)
	}
}

func TestBuildProcessFilterVars_All(t *testing.T) {
	prefix := "/home/boxd/"
	vars := buildProcessFilterVars(&graphql1.ProcessFilter{
		PidIn:     []int64{1, 2, 3},
		CommandIn: []string{"claude"},
		CwdPrefix: &prefix,
	})
	if vars == nil {
		t.Fatalf("non-empty filter should produce vars")
	}
	if pids, ok := vars["pidIn"].([]int64); !ok || len(pids) != 3 {
		t.Fatalf("pidIn missing or wrong: %v", vars["pidIn"])
	}
	if cmds, ok := vars["commandIn"].([]string); !ok || len(cmds) != 1 {
		t.Fatalf("commandIn missing or wrong: %v", vars["commandIn"])
	}
	if cwd, ok := vars["cwdPrefix"].(string); !ok || cwd != prefix {
		t.Fatalf("cwdPrefix missing or wrong: %v", vars["cwdPrefix"])
	}
}

func TestDecodePeerProcesses_Shape(t *testing.T) {
	raw := json.RawMessage(`{
        "host": {
            "processes": [
                {
                    "id": "boxd-vm:1234",
                    "pid": 1234,
                    "ppid": 1,
                    "command": "claude",
                    "startedAt": "2026-05-07T15:00:00Z",
                    "cpuPercent": 1.5,
                    "memBytes": 10485760,
                    "tty": "ttys000"
                },
                {
                    "id": "boxd-vm:5678",
                    "pid": 5678,
                    "ppid": 1234,
                    "command": "node",
                    "startedAt": "2026-05-07T15:01:00Z",
                    "cpuPercent": 0.2,
                    "memBytes": 5242880,
                    "tty": null
                }
            ]
        }
    }`)
	got, err := decodePeerProcesses(peerproxy.QueryResult{Data: raw}, "boxd-vm")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 processes, got %d", len(got))
	}
	for _, p := range got {
		if p.Host == nil {
			t.Fatalf("process %d missing host pointer", p.Pid)
		}
		if p.Host.ID != "Host:boxd-vm" || p.Host.MachineID != "boxd-vm" {
			t.Fatalf("process %d host wrong: %+v", p.Pid, p.Host)
		}
	}
	if got[0].Tty == nil || *got[0].Tty != "ttys000" {
		t.Fatalf("first process tty should be ttys000, got %v", got[0].Tty)
	}
	if got[1].Tty != nil {
		t.Fatalf("second process tty should be nil (json null), got %v", *got[1].Tty)
	}
}

func TestDecodePeerProcesses_EmptyList(t *testing.T) {
	raw := json.RawMessage(`{"host":{"processes":[]}}`)
	got, err := decodePeerProcesses(peerproxy.QueryResult{Data: raw}, "boxd-vm")
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %d", len(got))
	}
}

func TestDecodePeerProcesses_BadJSON(t *testing.T) {
	raw := json.RawMessage(`{"this":"is not the right shape"`)
	if _, err := decodePeerProcesses(peerproxy.QueryResult{Data: raw}, "x"); err == nil {
		t.Fatal("malformed json should surface a decode error")
	}
}
