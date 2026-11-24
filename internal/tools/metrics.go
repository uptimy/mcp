package tools

import (
	"context"
	"encoding/json"
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
	AvgLatencyMs  float64  `json:"avgLatencyMs"`
	P95LatencyMs  float64  `json:"p95LatencyMs"`
	MaxLatencyMs  float64  `json:"maxLatencyMs"`
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

// num decodes MySQL BIGINT/DECIMAL columns, which arrive as JSON strings.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		f, err := strconv.ParseFloat(s, 64)
		*n = num(f)
		return err
	}
	var f float64
	err := json.Unmarshal(b, &f)
	*n = num(f)
	return err
}

type rollup struct {
	MinuteBucket string `json:"minuteBucket"`
	HourBucket   string `json:"hourBucket"`
	DayBucket    string `json:"dayBucket"`
	Scheduler    string `json:"schedulerUuid"`
	Total        num    `json:"totalCount"`
	Failures     num    `json:"failureCount"`
	LatencySumUs num    `json:"latencySumUs"`
	LatencyMaxUs num    `json:"latencyMaxUs"`
	LatencyP95Us num    `json:"latencyP95Us"`
}

func (r rollup) bucket() string {
	switch {
	case r.MinuteBucket != "":
		return r.MinuteBucket
	case r.HourBucket != "":
		return r.HourBucket
	default:
		return r.DayBucket
	}
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

// failureSummary is upti.my-analytics' summary of a range (…/summary): its
// failures by status code and error class, scheduled checks only.
type failureSummary struct {
	FailureCauses []struct {
		ErrorClass   *string `json:"errorClass"`
		StatusCode   *int    `json:"statusCode"`
		Failures     int     `json:"failures"`
		Regions      int     `json:"regions"`
		LastAt       string  `json:"lastAt"`
		ErrorMessage *string `json:"errorMessage"`
	} `json:"failureCauses"`
}

// resolution picks the coarsest rollup that still gives a useful series.
func resolution(span time.Duration) (endpoint, name string) {
	switch {
	case span <= 6*time.Hour:
		return "minute-region", "minute"
	case span <= 7*24*time.Hour:
		return "hour-region", "hour"
	default:
		return "daily-region", "day"
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
	base := "/v1/api/healthchecks/" + url.PathEscape(hc.UUID) + "/analytics/"

	endpoint, res := resolution(to.Sub(from))
	var rollups upstream.Results[rollup]
	q := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	if err := t.call(ctx, req, upstream.API, http.MethodGet, base+endpoint, q, nil, &rollups); err != nil {
		return nil, metricsOut{}, err
	}
	// Hourly and daily rollups are built by a job after each hour ends, so
	// they lack the latest hour or so. Fill that tail from minute rollups,
	// starting right after the newest rollup returned, so a recent outage
	// isn't missing from the totals and nothing is counted twice.
	if endpoint != "minute-region" {
		tailFrom := tailStart(rollups.Results, endpoint == "daily-region", from, to)
		if tailFrom.Before(to) {
			var tail upstream.Results[rollup]
			q := url.Values{"from": {tailFrom.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
			if err := t.call(ctx, req, upstream.API, http.MethodGet, base+"minute-region", q, nil, &tail); err != nil {
				return nil, metricsOut{}, err
			}
			rollups.Results = append(rollups.Results, tail.Results...)
		}
	}

	// Failures from upti.my-analytics, over the whole range (as far back as
	// the plan's history window): the newest runs that failed, and the kinds
	// of failure.
	q = url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	history := "/v1/api/healthchecks/" + url.PathEscape(hc.UUID)
	var failed struct {
		Results []result `json:"results"`
	}
	fq := url.Values{"from": q["from"], "to": q["to"], "outcome": {"failure"}, "limit": {strconv.Itoa(maxRecentFailures)}}
	if err := t.call(ctx, req, upstream.Analytics, http.MethodGet, history+"/results", fq, nil, &failed); err != nil {
		return nil, metricsOut{}, err
	}
	var sum failureSummary
	if err := t.call(ctx, req, upstream.Analytics, http.MethodGet, history+"/summary", q, nil, &sum); err != nil {
		return nil, metricsOut{}, err
	}

	regionNames := map[string]string{}
	for _, s := range hc.Schedulers {
		regionNames[s.UUID] = s.Name
	}
	out := summarize(rollups.Results, regionNames)
	out.RecentFailures, out.FailureCauses = failures(failed.Results, sum, regionNames)
	out.UUID, out.Name, out.Resolution = hc.UUID, hc.Application.Name, res
	out.From, out.To = from.Format(time.RFC3339), to.Format(time.RFC3339)
	out.Note = "overall, regions and series p95 values are check-weighted means of per-region p95s, not exact percentiles. recentFailures (the newest 10, every kind of run) and failureCauses (scheduled checks) cover the whole range, as far back as the plan keeps history."
	return nil, out, nil
}

// maxTail bounds the minute-rollup tail: the hourly job is minutes behind,
// never hours, so a longer gap means there's simply no data.
const maxTail = 3 * time.Hour

// tailStart is where minute rollups take over from hourly or daily ones.
// Hourly: right after the newest hour bucket returned. Daily: today's bucket
// is derived from hourly rollups, so from the start of the current hour.
func tailStart(rows []rollup, daily bool, from, to time.Time) time.Time {
	start := to.Add(-maxTail)
	if daily {
		start = to.Truncate(time.Hour)
	} else {
		for _, r := range rows {
			if t, err := time.Parse(time.RFC3339, r.HourBucket); err == nil && t.Add(time.Hour).After(start) {
				start = t.Add(time.Hour)
			}
		}
	}
	if start.Before(from) {
		start = from
	}
	return start
}

// acc sums rollup rows into stats.
type acc struct {
	total, failures, latSumUs, maxUs, p95Weighted float64
}

func (a *acc) add(r rollup) {
	a.total += float64(r.Total)
	a.failures += float64(r.Failures)
	a.latSumUs += float64(r.LatencySumUs)
	a.p95Weighted += float64(r.LatencyP95Us) * float64(r.Total)
	a.maxUs = math.Max(a.maxUs, float64(r.LatencyMaxUs))
}

func (a acc) stats() stats {
	s := stats{Checks: int(a.total), Failures: int(a.failures), MaxLatencyMs: round(a.maxUs / 1000)}
	if a.total > 0 {
		uptime := round(100 * (a.total - a.failures) / a.total)
		s.UptimePercent = &uptime
		s.AvgLatencyMs = round(a.latSumUs / a.total / 1000)
		s.P95LatencyMs = round(a.p95Weighted / a.total / 1000)
	}
	return s
}

func round(f float64) float64 { return math.Round(f*100) / 100 }

func summarize(rows []rollup, regionNames map[string]string) metricsOut {
	region := func(uuid string) string {
		if n := regionNames[uuid]; n != "" {
			return n
		}
		return uuid
	}

	var overall acc
	byRegion := map[string]*acc{}
	byBucket := map[string]*acc{}
	for _, r := range rows {
		overall.add(r)
		if byRegion[r.Scheduler] == nil {
			byRegion[r.Scheduler] = &acc{}
		}
		byRegion[r.Scheduler].add(r)
		if byBucket[r.bucket()] == nil {
			byBucket[r.bucket()] = &acc{}
		}
		byBucket[r.bucket()].add(r)
	}

	out := metricsOut{Overall: overall.stats(), Regions: []regionStats{}, Series: []seriesPoint{}, RecentFailures: []failure{}, FailureCauses: []cause{}}
	for uuid, a := range byRegion {
		out.Regions = append(out.Regions, regionStats{Region: region(uuid), stats: a.stats()})
	}
	sort.Slice(out.Regions, func(i, j int) bool { return out.Regions[i].Region < out.Regions[j].Region })

	// Merge consecutive buckets so the series stays short whatever the range.
	buckets := make([]string, 0, len(byBucket))
	for b := range byBucket {
		buckets = append(buckets, b)
	}
	sort.Strings(buckets)
	per := (len(buckets) + maxSeriesPoints - 1) / maxSeriesPoints
	for i := 0; i < len(buckets); i += per {
		var a acc
		for _, b := range buckets[i:min(i+per, len(buckets))] {
			x := byBucket[b]
			a.total += x.total
			a.failures += x.failures
			a.latSumUs += x.latSumUs
			a.p95Weighted += x.p95Weighted
			a.maxUs = math.Max(a.maxUs, x.maxUs)
		}
		out.Series = append(out.Series, seriesPoint{Start: buckets[i], stats: a.stats()})
	}

	return out
}

// failures shapes analytics' failed runs (newest first) and failure causes.
func failures(results []result, sum failureSummary, regionNames map[string]string) ([]failure, []cause) {
	recent := make([]failure, 0, len(results))
	for _, r := range results {
		region := regionNames[r.Scheduler]
		if region == "" {
			region = r.Scheduler
		}
		msg := deref(r.ErrorMessage)
		if msg == "" {
			msg = deref(r.ErrorClass)
		}
		recent = append(recent, failure{At: r.CheckedAt, Region: region, Trigger: r.Trigger, ResponseTimeMs: r.ResponseTimeMs, StatusCode: r.StatusCode, Error: msg})
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
