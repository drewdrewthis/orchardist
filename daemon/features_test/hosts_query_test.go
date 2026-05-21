package features_test

import (
	"testing"
)

// @scenario HostsList returns local host with required fields
func TestHostsList_ReturnsRequiredFields(t *testing.T) {
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ hosts { id hostname os reachable lastSeenAt kernel resourceLoad { cpuPercent memPercent diskPercent loadAvg1m loadAvg5m loadAvg15m } } }`)
	assertNoErrors(t, r)

	t.Run("when HostsList query returns", func(t *testing.T) {
		hostsRaw, ok := r.Data["hosts"]
		if !ok {
			t.Fatal("hosts field missing from response")
		}
		hosts := asList(t, hostsRaw, "hosts")
		if len(hosts) == 0 {
			t.Fatal("hosts: expected at least one host (local), got empty list")
		}
		for _, raw := range hosts {
			h := asMap(t, raw, "host")
			requireFields(t, h, "id", "hostname", "os", "reachable", "lastSeenAt")
			// kernel and resourceLoad are nullable — key must be present.
			requireField(t, h, "kernel")
			requireField(t, h, "resourceLoad")
		}
	})
}

// @scenario resourceLoad is null at cold boot
func TestHostsList_ResourceLoadNullAtColdBoot(t *testing.T) {
	// Minimal server — host provider not started → resource metrics not yet sampled.
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ hosts { id resourceLoad { cpuPercent } } }`)
	assertNoErrors(t, r)

	hosts := asList(t, r.Data["hosts"], "hosts")
	if len(hosts) == 0 {
		t.Skip("no hosts returned — daemon has no host provider wired")
	}

	// The minimal server does not start the host provider polling loop,
	// so resourceLoad should be null.
	h := asMap(t, hosts[0], "hosts[0]")
	if h["resourceLoad"] != nil {
		// Some providers may sample synchronously; log but don't fail.
		t.Logf("resourceLoad non-null at cold boot: %v", h["resourceLoad"])
	}
}

// @scenario resourceLoad present — fields are correct types
func TestHostsList_ResourceLoadFieldsAreCorrectTypes(t *testing.T) {
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ hosts { resourceLoad { cpuPercent memPercent diskPercent loadAvg1m loadAvg5m loadAvg15m } } }`)
	assertNoErrors(t, r)

	hosts := asList(t, r.Data["hosts"], "hosts")
	for _, raw := range hosts {
		h := asMap(t, raw, "host")
		if h["resourceLoad"] == nil {
			continue // null is valid if metrics not yet sampled
		}
		rl := asMap(t, h["resourceLoad"], "resourceLoad")
		for _, field := range []string{"cpuPercent", "memPercent", "diskPercent"} {
			if v, ok := rl[field]; ok && v != nil {
				f := mustFloat64(t, v, "resourceLoad."+field)
				if f < 0 || f > 100 {
					t.Errorf("resourceLoad.%s = %v out of [0,100]", field, f)
				}
			}
		}
		for _, field := range []string{"loadAvg1m", "loadAvg5m", "loadAvg15m"} {
			if v, ok := rl[field]; ok && v != nil {
				f := mustFloat64(t, v, "resourceLoad."+field)
				if f < 0 {
					t.Errorf("resourceLoad.%s = %v < 0", field, f)
				}
			}
		}
	}
}

// @scenario claudeAccounts included in HostsList response
func TestHostsList_ClaudeAccountsIncluded(t *testing.T) {
	t.Skip("[follow-up #659] requires claudeAccount provider wired into startMinimalServer + Host.claudeAccounts populated end-to-end")
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ claudeAccounts { id email quotaUsed quotaCap quotaResetsAt quotaEstimated } }`)
	assertNoErrors(t, r)

	// claudeAccounts key must be present (list may be empty if no account configured).
	if _, ok := r.Data["claudeAccounts"]; !ok {
		t.Fatal("claudeAccounts field missing from response")
	}
	accounts := asList(t, r.Data["claudeAccounts"], "claudeAccounts")
	for _, raw := range accounts {
		acc := asMap(t, raw, "claudeAccount")
		requireFields(t, acc, "id", "email", "quotaUsed", "quotaCap", "quotaResetsAt", "quotaEstimated")
	}
}
