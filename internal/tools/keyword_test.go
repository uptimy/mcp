package tools

import (
	"encoding/json"
	"testing"
)

// An existing keyword monitor is recognised, so adding the same one again is
// skipped as a duplicate while a plain check of the URL stays a separate key.
func TestHTTPConfigKeyword(t *testing.T) {
	var cfg map[string]any
	_ = json.Unmarshal([]byte(`{"steps":[{"name":"request","request":{"method":"GET","uri":"https://acme.test"},
		"assertions":[{"source":"status","op":"eq","value":200},{"source":"body","op":"not_contains","value":"Maintenance"}]}]}`), &cfg)
	kw, mode := httpConfigKeyword(cfg)
	if kw != "Maintenance" || mode != "absent" {
		t.Fatalf("keyword = %q %q", kw, mode)
	}
	plain := monitorSpec{Type: "http", Target: "https://acme.test"}
	withKeyword := monitorSpec{Type: "http", Target: "https://acme.test", Keyword: kw, KeywordMode: mode}
	if plain.key() == withKeyword.key() {
		t.Error("a keyword monitor shares its key with the plain check")
	}
	if kw, _ := httpConfigKeyword(map[string]any{"uri": "https://acme.test"}); kw != "" {
		t.Errorf("flat config keyword = %q", kw)
	}
}
