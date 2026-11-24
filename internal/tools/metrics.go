package tools

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/mcp/internal/upstream"
)

const (
	maxSeriesPoints   = 48
	maxRecentFailures = 10
)

type metricsIn struct {
	UUID string `json:"uuid" jsonschema:"website/API monitor UUID from get_status_overview"`
	From string `json:"from,omitempty" jsonschema:"range start, RFC3339 (default: 24 hours before to)"`
	To   string `json:"to,omitempty" jsonschema:"range end, RFC3339 (default: now)"`
}

type stats struct {
	Checks   int `json:"checks"`
	Failures int `json:"failures"`
	// Omitted when the range has no checks, rather than reported as 0%.
	UptimePercent *float64 `json:"uptimePercent,omitempty"`
	// The latency figures are omitted when no check had a response time.
	AvgLatencyMs *float64 `json:"avgLatencyMs,omitempty"`
	P50LatencyMs *int     `json:"p50LatencyMs,omitempty"`
	P95LatencyMs *int     `json:"p95LatencyMs,omitempty"`
	P99LatencyMs *int     `json:"p99LatencyMs,omitempty"`
	MaxLatencyMs *int     `json:"maxLatencyMs,omitempty"`
}

type regionStats struct {
	Region string `json:"region"`
	stats
}

type seriesPoint struct {
	Start string `json:"start"`
	stats
}

type failure struct {
	At     string `json:"at"`
	Region string `json:"region"`
	// Trigger is scheduled, or confirmation (a recheck after a failure), or
	// manual.
	Trigger        string   `json:"trigger"`
	ResponseTimeMs *float64 `json:"responseTimeMs,omitempty"`
	StatusCode     *int     `json:"statusCode,omitempty"`
	Error          string   `json:"error"`
}

// cause is one kind of failure in the range: an HTTP status or an error
// class, how often, where and when last.
type cause struct {
	Cause      string `json:"cause"`
	ErrorClass string `json:"errorClass,omitempty"`
	StatusCode *int   `json:"statusCode,omitempty"`
	Failures   int    `json:"failures"`
	Regions    int    `json:"regions"`
	LastAt     string `json:"lastAt"`
	Example    string `json:"example,omitempty"`
}

type metricsOut struct {
	UUID           string        `json:"uuid"`
	Name           string        `json:"name"`
	From           string        `json:"from"`
	To             string        `json:"to"`
	Resolution     string        `json:"resolution"`
	Note           string        `json:"note"`
	Overall        stats         `json:"overall"`
	Regions        []regionStats `json:"regions"`
	Series         []seriesPoint `json:"series"`
	RecentFailures []failure     `json:"recentFailures"`
	FailureCauses  []cause       `json:"failureCauses"`
}

// figures are upti.my-analytics' figures of a range, a region or a series
// bucket: scheduled checks only, with true percentiles across regions.
type figures struct {
	Checks       int      `json:"checks"`
	Failures     int      `json:"failures"`
	Uptime       float64  `json:"uptime"`
	LatencyAvgMs *float64 `json:"latencyAvgMs"`
	LatencyMaxMs *int     `json:"latencyMaxMs"`
	LatencyP50Ms *int     `json:"latencyP50Ms"`
	LatencyP95Ms *int     `json:"latencyP95Ms"`
	LatencyP99Ms *int     `json:"latencyP99Ms"`
}

func (f figures) stats() stats {
	s := stats{Checks: f.Checks, Failures: f.Failures,
		P50LatencyMs: f.LatencyP50Ms, P95LatencyMs: f.LatencyP95Ms, P99LatencyMs: f.LatencyP99Ms, MaxLatencyMs: f.LatencyMaxMs}
	if f.Checks > 0 {
		uptime := round(f.Uptime)
		s.UptimePercent = &uptime
	}
	if f.LatencyAvgMs != nil {
		avg := round(*f.LatencyAvgMs)
		s.AvgLatencyMs = &avg
	}
	return s
}

// summary is upti.my-analytics' summary of the range (…/summary with
// by=scheduler): the range after the plan's history window clamped it, its
// figures overall and per region, and its failures by status code and error
// class.
type summary struct {
	From string `json:"from"`
	figures
	Regions []struct {
		Scheduler string `json:"schedulerUuid"`
		figures
	} `json:"regions"`
	FailureCauses []struct {
		ErrorClass   *string `json:"errorClass"`
		StatusCode   *int    `json:"statusCode"`
		Failures     int     `json:"failures"`
		Regions      int     `json:"regions"`
		LastAt       string  `json:"lastAt"`
		ErrorMessage *string `json:"errorMessage"`
	} `json:"failureCauses"`
}

// series is upti.my-analytics' time series (…/series): the buckets that
// had checks, oldest first.
type series struct {
	Buckets []struct {
		At string `json:"at"`
		figures
	} `json:"buckets"`
}

// result is one check run from upti.my-analytics (…/results).
type result struct {
	CheckedAt      string   `json:"checkedAt"`
	Scheduler      string   `json:"schedulerUuid"`
	Trigger        string   `json:"trigger"`
	ResponseTimeMs *float64 `json:"responseTimeMs"`
	StatusCode     *int     `json:"statusCode"`
	ErrorClass     *string  `json:"errorClass"`
	ErrorMessage   *string  `json:"errorMessage"`
}

// seriesStep picks the finest series step that covers span in at most
// maxSeriesPoints buckets (one more when the range starts mid-bucket):
// minutes up to 48 minutes, hours up to 48 hours, days up to 48 days, else
// months. Percentiles don't add up, so buckets are never merged.
func seriesStep(span time.Duration) (step, resolution string) {
	switch {
	case span <= maxSeriesPoints*time.Minute:
		return "1m", "minute"
	case span <= maxSeriesPoints*time.Hour:
		return "1h", "hour"
	case span <= maxSeriesPoints*24*time.Hour:
		return "1d", "day"
	default:
		return "1mo", "month"
	}
}

func parseRange(from, to string, now time.Time) (time.Time, time.Time, error) {
	end := now
	if to != "" {
		t, err := time.Parse(time.RFC3339, to)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("to must be RFC3339, e.g. 2026-10-06T12:00:00Z")
		}
		end = t
	}
	start := end.Add(-24 * time.Hour)
	if from != "" {
		t, err := time.Parse(time.RFC3339, from)
		if err != nil {
			return time.Time{}, time.Time{}, errors.New("from must be RFC3339, e.g. 2026-10-05T12:00:00Z")
		}
		start = t
	}
	if !start.Before(end) {
		return time.Time{}, time.Time{}, errors.New("from must be before to")
	}
	return start.UTC(), end.UTC(), nil
}

func (t *toolset) monitorMetrics(ctx context.Context, req *mcp.CallToolRequest, in metricsIn) (*mcp.CallToolResult, metricsOut, error) {
	from, to, err := parseRange(in.From, in.To, time.Now())
	if err != nil {
		return nil, metricsOut{}, err
	}
	m, err := t.findMonitor(ctx, req, in.UUID)
	if err != nil {
		return nil, metricsOut{}, err
	}
	if m.healthcheck == nil {
		return nil, metricsOut{}, errors.New("metrics are for website/API checks; use get_monitor for a heartbeat's last check-ins")
	}
	hc := m.healthcheck
	base := "/v1/api/healthchecks/" + url.PathEscape(hc.UUID)
	window := func() url.Values {
		return url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}}
	}

	// Totals, regions and failure causes in one summary.
	var sum summary
	q := window()
	q.Set("by", "scheduler")
	if err := t.call(ctx, req, upstream.Analytics, http.MethodGet, base+"/summary", q, nil, &sum); err != nil {
		return nil, metricsOut{}, err
	}
	// The plan's history window can start later than the range: the series
	// and failures cover what the summary did, and the step fits that.
	if start, err := time.Parse(time.RFC3339Nano, sum.From); err == nil && start.After(from) {
		from = start
	}

	step, res := seriesStep(to.Sub(from))
	var ser series
	q = window()
	q.Set("step", step)
	if err := t.call(ctx, req, upstream.Analytics, http.MethodGet, base+"/series", q, nil, &ser); err != nil {
		return nil, metricsOut{}, err
	}

	// The newest failed runs of every kind, over the whole range.
	var failed struct {
		Results []result `json:"results"`
	}
	q = window()
	q.Set("outcome", "failure")
	q.Set("limit", strconv.Itoa(maxRecentFailures))
	if err := t.call(ctx, req, upstream.Analytics, http.MethodGet, base+"/results", q, nil, &failed); err != nil {
		return nil, metricsOut{}, err
	}

	regionNames := map[string]string{}
	for _, s := range hc.Schedulers {
		regionNames[s.UUID] = s.Name
	}
	out := summarize(sum, ser, regionNames)
	out.RecentFailures, out.FailureCauses = failures(failed.Results, sum, regionNames)
	out.UUID, out.Name, out.Resolution = hc.UUID, hc.Application.Name, res
	out.From, out.To = from.Format(time.RFC3339), to.Format(time.RFC3339)
	out.Note = "Checks, uptime and latency count scheduled checks only (not confirmation rechecks or manual runs); p50, p95 and p99 are true percentiles over every check, across regions. " +
		"overall and regions count whole 15-minute quarters (a quarter counts when it starts in the range), so they can differ a little from the series at the range's edges. " +
		"series has one point per " + res + " (UTC) that had checks. recentFailures (the newest 10, every kind of run) and failureCauses (scheduled checks) cover the whole range. " +
		"from is where the plan's history starts when that is later than the from asked for."
	return nil, out, nil
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

// summarize shapes the summary's totals and regions (sorted by name) and the
// series.
func summarize(sum summary, ser series, regionNames map[string]string) metricsOut {
	out := metricsOut{Overall: sum.stats(), Regions: make([]regionStats, 0, len(sum.Regions)), Series: make([]seriesPoint, 0, len(ser.Buckets))}
	for _, r := range sum.Regions {
		out.Regions = append(out.Regions, regionStats{Region: regionName(regionNames, r.Scheduler), stats: r.stats()})
	}
	sort.Slice(out.Regions, func(i, j int) bool { return out.Regions[i].Region < out.Regions[j].Region })
	for _, b := range ser.Buckets {
		out.Series = append(out.Series, seriesPoint{Start: b.At, stats: b.stats()})
	}
	return out
}

// regionName is a scheduler's region name, or its UUID when the monitor no
// longer runs there.
func regionName(names map[string]string, scheduler string) string {
	if n := names[scheduler]; n != "" {
		return n
	}
	return scheduler
}

// failures shapes analytics' failed runs (newest first) and failure causes.
func failures(results []result, sum summary, regionNames map[string]string) ([]failure, []cause) {
	recent := make([]failure, 0, len(results))
	for _, r := range results {
		msg := deref(r.ErrorMessage)
		if msg == "" {
			msg = deref(r.ErrorClass)
		}
		recent = append(recent, failure{At: r.CheckedAt, Region: regionName(regionNames, r.Scheduler), Trigger: r.Trigger,
			ResponseTimeMs: r.ResponseTimeMs, StatusCode: r.StatusCode, Error: msg})
	}
	causes := make([]cause, 0, len(sum.FailureCauses))
	for _, c := range sum.FailureCauses {
		name := deref(c.ErrorClass)
		if c.StatusCode != nil {
			name = "HTTP " + strconv.Itoa(*c.StatusCode)
		}
		if name == "" {
			name = "failed"
		}
		causes = append(causes, cause{Cause: name, ErrorClass: deref(c.ErrorClass), StatusCode: c.StatusCode, Failures: c.Failures,
			Regions: c.Regions, LastAt: c.LastAt, Example: deref(c.ErrorMessage)})
	}
	return recent, causes
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
