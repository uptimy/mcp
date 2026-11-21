package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// HTTP checks keep headers and body under steps[].request: they are
// redacted there too, and the rest of the check stays readable.
func TestRedactConfigReachesNestedSteps(t *testing.T) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"proxyUrl":"http://proxy.acme.test","steps":[
		{"name":"login","request":{"method":"POST","uri":"https://api.acme.test/login","headers":{"Authorization":"Bearer secret"},"body":{"password":"hunter2"}},
		 "assertions":[{"source":"header","name":"X-Version","op":"exists"}],
		 "extract":[{"var":"token","source":"json","path":"access_token"}]}]}`), &cfg)

	out, _ := json.Marshal(redactConfig(cfg))
	got := string(out)
	for _, leaked := range []string{"Bearer secret", "hunter2"} {
		if strings.Contains(got, leaked) {
			t.Errorf("%q leaked: %s", leaked, got)
		}
	}
	for _, kept := range []string{`"uri":"https://api.acme.test/login"`, `"headers":"[redacted]"`, `"body":"[redacted]"`, `"X-Version"`, `"op":"exists"`} {
		if !strings.Contains(got, kept) {
			t.Errorf("missing %s in %s", kept, got)
		}
	}
	// The original is left alone: pause and other writes send it back.
	if cfg["steps"].([]any)[0].(map[string]any)["request"].(map[string]any)["headers"] == "[redacted]" {
		t.Error("redactConfig changed its input")
	}
}
