package tools

import (
	"testing"
	"time"
)

func TestSeriesStepKeepsTheSeriesShort(t *testing.T) {
	cases := []struct {
		span       time.Duration
		step, name string
	}{
		{30 * time.Minute, "1m", "minute"},
		{48 * time.Minute, "1m", "minute"},
		{time.Hour, "1h", "hour"},
		{24 * time.Hour, "1h", "hour"},
		{48 * time.Hour, "1h", "hour"},
		{7 * 24 * time.Hour, "1d", "day"},
		{30 * 24 * time.Hour, "1d", "day"},
		{90 * 24 * time.Hour, "1mo", "month"},
	}
	for _, c := range cases {
		if step, name := seriesStep(c.span); step != c.step || name != c.name {
			t.Errorf("%s: got %s (%s), want %s (%s)", c.span, step, name, c.step, c.name)
		}
	}
}

func TestNoUptimeOrLatencyWithoutChecks(t *testing.T) {
	s := figures{}.stats()
	if s.UptimePercent != nil || s.AvgLatencyMs != nil || s.P95LatencyMs != nil {
		t.Errorf("no checks = %+v, want uptime and latency omitted", s)
	}
	s = figures{Checks: 4, Failures: 4}.stats()
	if s.UptimePercent == nil || *s.UptimePercent != 0 {
		t.Errorf("all checks failing should be 0%%, got %v", s.UptimePercent)
	}
	if s.AvgLatencyMs != nil {
		t.Errorf("no response times: avg = %v, want omitted", *s.AvgLatencyMs)
	}
}
