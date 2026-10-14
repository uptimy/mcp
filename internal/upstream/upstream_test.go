package upstream

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlanErrorsReadAsPlanErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"FEATURE_NOT_AVAILABLE.400","error":"This feature is not available in your subscription"}`))
	}))
	defer srv.Close()
	err := New(srv.URL, srv.URL, srv.URL, srv.URL).Do(context.Background(), "upt_x", Reports, http.MethodGet, "/v1/api/reports/range", nil, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "paid plan") {
		t.Fatalf("got %v, want a plan error", err)
	}
	if e, ok := err.(*Error); !ok || e.Code != "FEATURE_NOT_AVAILABLE.400" {
		t.Fatalf("code kept: %#v", err)
	}
}
