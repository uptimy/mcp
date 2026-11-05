package server_test

import (
	"strings"
	"testing"
)

func TestAddReportUsesAppDefaults(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_report", map[string]any{
		"name": "Globex", "tags": []any{"PROD"}, "slaTargetPercent": 99.5,
		"emails": []any{"cto@globex.test"}, "sendTime": "08:30",
	})

	body := fake.write("POST /v1/api/reports/definitions")
	if body["scopeType"] != "tags" || body["tagIds"].([]any)[0] != float64(7) || body["monitorUuids"] != nil {
		t.Errorf("scope = %v %v %v", body["scopeType"], body["tagIds"], body["monitorUuids"])
	}
	// Emailed: monthly on the 1st, in the default report's timezone.
	if body["cadence"] != "monthly" || body["sendDay"] != float64(1) || body["sendHour"] != float64(8) || body["sendMinute"] != float64(30) || body["sendTimezone"] != "UTC" {
		t.Errorf("schedule = %v", body)
	}
	if body["slaTarget"] != 99.5 || body["emailEnabled"] != true || body["brandLogoUrl"] != nil {
		t.Errorf("body = %v", body)
	}
	if out["covers"] != "monitors tagged prod" || out["emails"].([]any)[0] != "cto@globex.test" || !strings.Contains(out["note"].(string), "first email") {
		t.Errorf("out = %v", out)
	}
}

func TestAddReportWithoutEmailsIsReadOnDemand(t *testing.T) {
	cs, fake, _ := setup(t)
	call(t, cs, "add_report", map[string]any{"name": "Checkout", "monitorUuids": []any{"hc-down", "hb-1"}})

	body := fake.write("POST /v1/api/reports/definitions")
	if body["cadence"] != "none" {
		t.Errorf("cadence = %v, want none", body["cadence"])
	}
	// Healthcheck UUIDs become the application UUIDs reports use.
	if uuids := body["monitorUuids"].([]any); body["scopeType"] != "monitors" || len(uuids) != 2 || uuids[0] != "app-down" || uuids[1] != "hb-1" {
		t.Errorf("scope = %v %v", body["scopeType"], uuids)
	}
}

func TestAddReportReturnsExisting(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "add_report", map[string]any{"name": "ACME", "emails": []any{"x@acme.test"}})

	if out["alreadyExisted"] != true || out["uuid"] != "00000000-0000-0000-0000-00000000acme" {
		t.Errorf("out = %v", out)
	}
	if fake.write("POST /v1/api/reports/definitions") != nil {
		t.Error("an existing report must not be added again")
	}
}

func TestUpdateReportKeepsWhatIsNotGiven(t *testing.T) {
	cs, fake, _ := setup(t)
	out := call(t, cs, "update_report", map[string]any{
		"report": "all monitors", "emails": []any{"ops@acme.test", "cto@acme.test"}, "sendDay": 5, "sendTime": "17:00",
	})

	body := fake.write("PUT /v1/api/reports/definitions/def-all")
	if body["name"] != "All monitors" || body["scopeType"] != "workspace" || body["cadence"] != "weekly" || body["sendTimezone"] != "Europe/Sarajevo" {
		t.Errorf("kept = %v", body)
	}
	// The logo can only be uploaded in the app; an update keeps it.
	if body["brandLogoUrl"] != "https://cdn.acme.test/logo.png" {
		t.Errorf("brandLogoUrl = %v", body["brandLogoUrl"])
	}
	if body["sendDay"] != float64(5) || body["sendHour"] != float64(17) || len(body["recipients"].([]any)) != 2 {
		t.Errorf("changed = %v", body)
	}
	if _, ok := body["lastSentPeriodStart"]; ok {
		t.Error("read-only fields are not sent back")
	}
	if out["schedule"] != "weekly, sent Friday at 17:00 Europe/Sarajevo" {
		t.Errorf("schedule = %v", out["schedule"])
	}
}

func TestReportSettingsErrors(t *testing.T) {
	cs, _, _ := setup(t)
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"add_report", map[string]any{"name": "X", "monitorUuids": []any{"hc-ok"}, "tags": []any{"prod"}}, "not both"},
		{"add_report", map[string]any{"name": "X", "schedule": "daily"}, "unknown schedule"},
		{"add_report", map[string]any{"name": "X", "sendTime": "9am"}, "HH:MM"},
		{"add_report", map[string]any{"name": "X", "tags": []any{"nope"}}, "no tag named"},
		{"update_report", map[string]any{"report": "Initech"}, "no report named"},
		{"update_report", map[string]any{"report": "Acme", "allMonitors": true, "tags": []any{"prod"}}, "one of allMonitors"},
	} {
		if msg := callErr(t, cs, c.tool, c.args); !strings.Contains(msg, c.want) {
			t.Errorf("%s %v: %q, want %q", c.tool, c.args, msg, c.want)
		}
	}
}
