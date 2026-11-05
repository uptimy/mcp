package server_test

import (
	"strings"
	"testing"
)

func TestListAlertsNamesWhatTheyDo(t *testing.T) {
	cs, _, _ := setup(t)
	out := call(t, cs, "list_alerts", nil)

	alerts := out["alerts"].([]any)
	if len(alerts) != 2 {
		t.Fatalf("got %d alerts, want 2", len(alerts))
	}
	def, ops := alerts[0].(map[string]any), alerts[1].(map[string]any)
	if def["madeInEditor"] != true || def["sendsTo"].([]any)[0] != "email" {
		t.Errorf("default workflow = %v", def)
	}
	if ops["paused"] != true || ops["madeInEditor"] != false || ops["sendsTo"].([]any)[0] != "Slack #ops (Acme)" {
		t.Errorf("template alert = %v", ops)
	}
	if ops["tags"].([]any)[0] != "prod" || ops["monitors"].([]any)[0] != "Checkout API" {
		t.Errorf("scope = tags %v, monitors %v", ops["tags"], ops["monitors"])
	}
	ints := out["integrations"].([]any)
	if len(ints) != 2 || ints[1].(map[string]any)["forAlerts"] != false {
		t.Errorf("integrations = %v", ints)
	}
}

func TestAddAlertResolvesNames(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_alert", map[string]any{
		"name": "Critical to #ops", "integration": "slack", "tags": []any{"PROD"},
		"severities": []any{"Critical"}, "monitorUuids": []any{"hc-down"},
	})

	body := fake.write("POST /v1/api/alerts/")
	if dest := body["destination"].(map[string]any); dest["type"] != "integration" || dest["integrationUuid"] != "int-slack" {
		t.Errorf("destination = %v", dest)
	}
	// A check is scoped by its application UUID: incidents name their source by it.
	if body["tagIds"].([]any)[0] != float64(7) || body["severities"].([]any)[0] != "critical" || body["sourceUuids"].([]any)[0] != "app-down" {
		t.Errorf("scope = %v", body)
	}
	if body["events"] != nil {
		t.Errorf("events should default on the server, got %v", body["events"])
	}
	if out["uuid"] != "wf-new" || out["alreadyExisted"] != false || !strings.Contains(out["note"].(string), "real message") {
		t.Errorf("out = %v", out)
	}
}

func TestAddAlertExplainsWhatsWrong(t *testing.T) {
	cs, _, _ := setup(t)
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"name": "x", "integration": "slack", "emails": []any{"a@acme.test"}}, "exactly one destination"},
		{map[string]any{"name": "x", "emails": []any{"a@acme.test"}, "tags": []any{"nope"}}, "no tag named"},
		{map[string]any{"name": "x", "integration": "teams"}, "Connect it in the Uptimy app"},
		{map[string]any{"name": "x", "integration": "acme/app"}, "can't receive alerts"},
		{map[string]any{"name": "x", "emails": []any{"a@acme.test"}, "events": []any{"updated"}}, "use opened or resolved"},
	}
	for _, c := range cases {
		if msg := callErr(t, cs, "add_alert", c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%v: %q doesn't say %q", c.args, msg, c.want)
		}
	}
}

func TestAddAlertReturnsExistingByName(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_alert", map[string]any{"name": "prod to #OPS", "integration": "slack"})
	if out["uuid"] != "wf-ops" || out["alreadyExisted"] != true {
		t.Errorf("out = %v", out)
	}
	if body := fake.write("POST /v1/api/alerts/"); body != nil {
		t.Errorf("created a duplicate: %v", body)
	}
}

func TestSetAlertPaused(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "set_alert_paused", map[string]any{"uuid": "wf-ops", "paused": false})
	if body := fake.write("PATCH /v1/api/workflows/wf-ops"); body["enabled"] != true {
		t.Errorf("patch body = %v", body)
	}
	if !strings.Contains(out["message"].(string), "sending again") {
		t.Errorf("out = %v", out)
	}
}
