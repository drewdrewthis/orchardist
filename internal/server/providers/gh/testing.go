package gh

import (
	"context"
	"net/http"
)

// SetHTTPClientForTest swaps the underlying *http.Client a Provider
// uses to talk to the GitHub API. **Test-only.** Production code never
// calls this — it goes through Provider.New / NewWith with the env-
// driven base URL.
//
// Why expose this? End-to-end tests stand up an httptest.NewTLSServer,
// which terminates TLS with a self-signed cert that http.DefaultClient
// will (correctly) reject. The test server's *http.Client trusts the
// generated CA; injecting it lets the gh.Client follow that trust path
// without weakening the production cert verification.
//
// The injection happens after the Provider has built its lazy Client.
// We trigger that build via a no-op call (e.g. AuthError) before
// injecting, so the swap targets the real client instance.
func SetHTTPClientForTest(p *Provider, c *http.Client) {
	if p == nil || c == nil {
		return
	}
	// Force-build the client. resolveAuth caches under sync.Once, so
	// repeat calls are free.
	ctx := context.Background()
	_, _ = p.httpClient(ctx)
	if p.client != nil {
		p.client.HTTP = c
	}
}
