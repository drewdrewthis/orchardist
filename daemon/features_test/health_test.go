package features_test

import (
	"testing"
)

// @scenario Health query returns status ok when daemon is serving
func TestHealth_ReturnsStatusOk(t *testing.T) {
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ health { status uptimeS } }`)
	assertNoErrors(t, r)

	t.Run("when health query returns", func(t *testing.T) {
		health, ok := r.Data["health"]
		if !ok || health == nil {
			t.Fatal("health field missing from response")
		}
		hm := asMap(t, health, "health")

		status, ok := hm["status"].(string)
		if !ok {
			t.Fatalf("health.status: expected string, got %T", hm["status"])
		}
		if status != "ok" {
			t.Errorf("health.status: expected 'ok', got %q", status)
		}

		mustNonNegativeInt(t, hm["uptimeS"], "health.uptimeS")
	})
}

// @scenario Health query returns uptimeS that grows over time
func TestHealth_UptimeSIsNonNegativeInteger(t *testing.T) {
	ts := startMinimalServer(t)

	r := postGQL(t, ts.URL, `{ health { status uptimeS } }`)
	assertNoErrors(t, r)

	health := asMap(t, r.Data["health"], "health")
	mustNonNegativeInt(t, health["uptimeS"], "health.uptimeS")
}
