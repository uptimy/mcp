package tools

import (
	"testing"
	"time"
)

func TestTailStart(t *testing.T) {
	to := time.Date(2026, 10, 8, 11, 20, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	hour := func(h int) rollup {
		return rollup{HourBucket: time.Date(2026, 10, 8, h, 0, 0, 0, time.UTC).Format(time.RFC3339)}
	}

	cases := []struct {
		name  string
		rows  []rollup
		daily bool
		want  time.Time
	}{
		{"after the newest hour rolled up", []rollup{hour(8), hour(10)}, false, time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)},
		{"previous hour not rolled up yet", []rollup{hour(8), hour(9)}, false, time.Date(2026, 10, 8, 10, 0, 0, 0, time.UTC)},
		{"new monitor: at most three hours", nil, false, to.Add(-maxTail)},
		{"daily: the current hour", nil, true, time.Date(2026, 10, 8, 11, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := tailStart(c.rows, c.daily, from, to); !got.Equal(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	if got := tailStart(nil, false, to.Add(-time.Hour), to); !got.Equal(to.Add(-time.Hour)) {
		t.Errorf("never before the range start: got %s", got)
	}
}

func TestNoUptimeWithoutChecks(t *testing.T) {
	out := summarize(nil, nil, nil)
	if out.Overall.UptimePercent != nil {
		t.Errorf("uptime with no checks = %v, want omitted", *out.Overall.UptimePercent)
	}
	out = summarize([]rollup{{MinuteBucket: "2026-10-08T11:00:00Z", Total: 4, Failures: 4}}, nil, nil)
	if out.Overall.UptimePercent == nil || *out.Overall.UptimePercent != 0 {
		t.Errorf("all checks failing should be 0%%, got %v", out.Overall.UptimePercent)
	}
}
