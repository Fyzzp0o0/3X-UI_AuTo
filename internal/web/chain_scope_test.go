package web

import (
	"net/http"
	"testing"

	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service/panel"
)

// TestChainRoutesAreAdminOnly pins the deny-by-default scope behaviour: chain
// routes are not in the monitor/node-sync allowlists, so a restricted token
// must be refused before any handler runs.
func TestChainRoutesAreAdminOnly(t *testing.T) {
	n := startContractNode(t)

	monitor, err := (&panel.ApiTokenService{}).Create("chain-monitor", model.ApiScopeMonitor, 0)
	if err != nil {
		t.Fatalf("create monitor token: %v", err)
	}
	for _, path := range []string{
		"/panel/api/chain/meta",
		"/panel/api/chain/pools",
		"/panel/api/chain/nodes",
		"/panel/api/chain/jobs",
	} {
		req, err := http.NewRequest(http.MethodGet, n.srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+monitor.Token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("GET %s with a monitor token = %d, want 403", path, resp.StatusCode)
		}
	}

	admin, err := (&panel.ApiTokenService{}).Create("chain-admin", model.ApiScopeAdmin, 0)
	if err != nil {
		t.Fatalf("create admin token: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, n.srv.URL+"/panel/api/chain/meta", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+admin.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("admin GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin GET meta = %d, want 200", resp.StatusCode)
	}
}
