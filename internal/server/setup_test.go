package server_test

import (
	"strings"
	"testing"
)

func TestSuggestMonitorsMarksWhatsMonitored(t *testing.T) {
	cs, _, _ := setup(t)
	out := call(t, cs, "suggest_monitors", map[string]any{"domain": "acme.test"})

	if out["canAdd"] != float64(8) {
		t.Errorf("canAdd = %v, want 8", out["canAdd"])
	}
	s := out["suggestions"].([]any)
	if len(s) != 3 {
		t.Fatalf("got %d suggestions, want 3", len(s))
	}
	apex, api, ssl := s[0].(map[string]any), s[1].(map[string]any), s[2].(map[string]any)
	if apex["alreadyMonitored"] != true {
		t.Errorf("acme.test is checked by hc-ok: %v", apex)
	}
	// A 404 answer isn't taken as "up": the suggestion expects 200.
	if api["expectedStatusCode"] != float64(200) || api["alreadyMonitored"] != false || !strings.Contains(api["note"].(string), "404") {
		t.Errorf("api suggestion = %v", api)
	}
	if ssl["type"] != "ssl" || ssl["target"] != "acme.test" || ssl["alreadyMonitored"] != false {
		t.Errorf("ssl suggestion = %v", ssl)
	}
}

func TestAddMonitorsUsesSetupDefaults(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_monitors", map[string]any{
		"monitors": []any{
			map[string]any{"target": "api.acme.test"},
			map[string]any{"type": "ssl", "target": "https://acme.test/login"},
			map[string]any{"target": "https://acme.test/"}, // hc-ok already checks it
		},
		"tags": []any{"prod"},
	})

	if skipped := out["alreadyMonitored"].([]any); len(skipped) != 1 || skipped[0] != "https://acme.test/" {
		t.Errorf("alreadyMonitored = %v", skipped)
	}
	if added := out["added"].([]any); len(added) != 2 || added[0].(map[string]any)["uuid"] != "hc-new1" {
		t.Errorf("added = %v", added)
	}

	items := fake.write("POST /v1/api/healthchecks/bulk")["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("bulk items = %d, want 2", len(items))
	}
	http := items[0].(map[string]any)["healthcheck"].(map[string]any)
	if http["intervalSeconds"] != float64(60) || http["healthcheckTypeId"] != float64(1) || len(http["schedulers"].([]any)) != 2 {
		t.Errorf("http healthcheck = %v", http)
	}
	if cfg := http["config"].(map[string]any); cfg["uri"] != "https://api.acme.test" || cfg["expectedStatusCode"] != float64(200) || cfg["method"] != "GET" {
		t.Errorf("http config = %v", cfg)
	}
	ssl := items[1].(map[string]any)
	if ssl["applicationName"] != "acme.test SSL" {
		t.Errorf("ssl name = %v", ssl["applicationName"])
	}
	sslHC := ssl["healthcheck"].(map[string]any)
	if cfg := sslHC["config"].(map[string]any); sslHC["intervalSeconds"] != float64(3600) || cfg["host"] != "acme.test" || cfg["port"] != float64(443) {
		t.Errorf("ssl healthcheck = %v", sslHC)
	}
	if tags := fake.write("PUT /v1/api/applications/app-new2/healthchecks/tags/")["tagIds"].([]any); len(tags) != 1 || tags[0] != float64(7) {
		t.Errorf("tagIds = %v", tags)
	}
}

func TestAddMonitorsRaisesIntervalToPlanFloor(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "add_monitors", map[string]any{"monitors": []any{map[string]any{"type": "ping", "target": "db.acme.test"}}, "intervalSeconds": 10})
	hc := fake.write("POST /v1/api/healthchecks/bulk")["items"].([]any)[0].(map[string]any)["healthcheck"].(map[string]any)
	if hc["intervalSeconds"] != float64(30) {
		t.Errorf("interval = %v, want the plan's 30", hc["intervalSeconds"])
	}
}

func TestAddMonitorsChecksTargets(t *testing.T) {
	cs, _, _ := setup(t)
	if msg := callErr(t, cs, "add_monitors", map[string]any{"monitors": []any{map[string]any{"type": "tcp", "target": "db.acme.test"}}}); !strings.Contains(msg, "needs a port") {
		t.Errorf("tcp without port: %s", msg)
	}
	if msg := callErr(t, cs, "add_monitors", map[string]any{"monitors": []any{map[string]any{"type": "dns", "target": "acme.test", "recordType": "SOA"}}}); !strings.Contains(msg, "isn't supported") {
		t.Errorf("dns SOA: %s", msg)
	}
}

func TestAddCronMonitor(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_cron_monitor", map[string]any{"name": "Queue worker", "everySeconds": 300})
	body := fake.write("POST /v1/api/heartbeat-monitors/")
	if body["intervalSeconds"] != float64(300) || body["graceSeconds"] != float64(300) || body["cron"] != nil {
		t.Errorf("create body = %v", body)
	}
	if out["pingUrl"] != "https://ping.upti.my/pubnew" || !strings.Contains(out["howToPing"].(string), "curl") || out["alreadyExisted"] != false {
		t.Errorf("out = %v", out)
	}

	if msg := callErr(t, cs, "add_cron_monitor", map[string]any{"name": "Report", "everySeconds": 60, "cron": "0 9 * * *"}); !strings.Contains(msg, "either") {
		t.Errorf("both schedules: %s", msg)
	}
}

func TestAddCronMonitorReturnsExistingByName(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_cron_monitor", map[string]any{"name": "nightly BACKUP", "cron": "0 2 * * *"})
	if out["uuid"] != "hb-1" || out["alreadyExisted"] != true {
		t.Errorf("out = %v", out)
	}
	if body := fake.write("POST /v1/api/heartbeat-monitors/"); body != nil {
		t.Errorf("created a duplicate: %v", body)
	}
}

func TestCreateStatusPageIsPrivateByDefault(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "create_status_page", map[string]any{"name": "Acme Public", "monitorUuids": []any{"hc-down", "hb-1"}})

	if page := fake.write("POST /v1/api/status-pages/"); page["public"] != false || page["name"] != "Acme Public" {
		t.Errorf("page = %v", page)
	}
	entries := fake.write("POST /v1/api/status-pages/sp-new/groups/")["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	// A check is shown as its application; a heartbeat as itself.
	first, second := entries[0].(map[string]any), entries[1].(map[string]any)
	if first["kind"] != "service" || first["applicationUuid"] != "app-down" || first["order"] != float64(0) {
		t.Errorf("first entry = %v", first)
	}
	if second["kind"] != "heartbeat" || second["heartbeatMonitorUuid"] != "hb-1" || second["order"] != float64(1) {
		t.Errorf("second entry = %v", second)
	}
	if out["url"] != "https://acme-public.upti.my" || out["public"] != false {
		t.Errorf("out = %v", out)
	}
}

func TestAddToStatusPageKeepsRowsAndSkipsShownOnes(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_to_status_page", map[string]any{"statusPageUuid": "sp-1", "monitorUuids": []any{"hc-ok", "hb-1"}})

	if shown := out["alreadyShown"].([]any); len(shown) != 1 || shown[0] != "Website" {
		t.Errorf("alreadyShown = %v", shown)
	}
	entries := fake.write("PUT /v1/api/status-pages/sp-1/groups/g-1")["entries"].([]any)
	if len(entries) != 2 {
		t.Fatalf("entries = %v", entries)
	}
	if kept := entries[0].(map[string]any); kept["applicationUuid"] != "app-ok" || kept["customLabel"] != "Website" {
		t.Errorf("existing row changed: %v", kept)
	}
	if added := entries[1].(map[string]any); added["heartbeatMonitorUuid"] != "hb-1" || added["order"] != float64(1) {
		t.Errorf("added row = %v", added)
	}
}
