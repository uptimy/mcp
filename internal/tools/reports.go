package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

// Uptime reports come from the reports service (/v1/api/reports/...). A
// report definition is what a report covers (every monitor, or chosen ones),
// its SLA target and its email schedule; any period can be read for one.

type reportDefinition struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	IsDefault    bool     `json:"isDefault"`
	ScopeType    string   `json:"scopeType"`
	MonitorUUIDs []string `json:"monitorUuids"`
	TagIDs       []int    `json:"tagIds"`
	SLATarget    float64  `json:"slaTarget"`
	Cadence      string   `json:"cadence"`
	SendDay      int      `json:"sendDay"`
	SendHour     int      `json:"sendHour"`
	SendMinute   int      `json:"sendMinute"`
	SendTimezone string   `json:"sendTimezone"`
	EmailEnabled bool     `json:"emailEnabled"`
	Recipients   []string `json:"recipients"`
}

type reportRow struct {
	UUID       string  `json:"uuid"`
	Name       string  `json:"name"`
	Default    bool    `json:"default"`
	Covers     string  `json:"covers"`
	SLATarget  float64 `json:"slaTargetPercent"`
	Schedule   string  `json:"schedule"`
	Recipients int     `json:"recipients"`
}

var weekdays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

// row describes a definition; tags names the workspace's tags by id.
func (d reportDefinition) row(tags map[int]string) reportRow {
	covers := "every monitor"
	switch d.ScopeType {
	case "monitors":
		covers = fmt.Sprintf("%d chosen monitors", len(d.MonitorUUIDs))
	case "tags":
		names := make([]string, 0, len(d.TagIDs))
		for _, id := range d.TagIDs {
			if n, ok := tags[id]; ok {
				names = append(names, n)
			}
		}
		covers = "monitors tagged " + strings.Join(names, " or ")
	}
	schedule := "not scheduled (read on demand)"
	if d.Cadence != "none" {
		when := fmt.Sprintf("day %d", d.SendDay)
		if d.Cadence == "weekly" && d.SendDay >= 0 && d.SendDay < 7 {
			when = weekdays[d.SendDay]
		}
		schedule = fmt.Sprintf("%s, sent %s at %02d:%02d %s", d.Cadence, when, d.SendHour, d.SendMinute, d.SendTimezone)
		if !d.EmailEnabled {
			schedule += " (email off)"
		}
	}
	return reportRow{UUID: d.UUID, Name: d.Name, Default: d.IsDefault, Covers: covers, SLATarget: d.SLATarget, Schedule: schedule, Recipients: len(d.Recipients)}
}

type listReportsIn struct{}

type listReportsOut struct {
	Reports []reportRow `json:"reports"`
}

func (t *toolset) listReports(ctx context.Context, req *mcp.CallToolRequest, _ listReportsIn) (*mcp.CallToolResult, listReportsOut, error) {
	defs, err := t.reportDefinitions(ctx, req)
	if err != nil {
		return nil, listReportsOut{}, err
	}
	tags, err := t.reportTags(ctx, req, defs...)
	if err != nil {
		return nil, listReportsOut{}, err
	}
	out := listReportsOut{Reports: make([]reportRow, 0, len(defs))}
	for _, d := range defs {
		out.Reports = append(out.Reports, d.row(tags))
	}
	return nil, out, nil
}

// reportTags names the workspace's tags, loaded only when a report is scoped by them.
func (t *toolset) reportTags(ctx context.Context, req *mcp.CallToolRequest, defs ...reportDefinition) (map[int]string, error) {
	names := map[int]string{}
	for _, d := range defs {
		if d.ScopeType == "tags" {
			tags, err := t.listTags(ctx, req)
			for _, tag := range tags {
				names[tag.ID] = tag.Name
			}
			return names, err
		}
	}
	return names, nil
}

func (t *toolset) reportDefinitions(ctx context.Context, req *mcp.CallToolRequest) ([]reportDefinition, error) {
	var res upstream.Results[reportDefinition]
	if err := t.call(ctx, req, upstream.Reports, http.MethodGet, "/v1/api/reports/definitions", nil, nil, &res); err != nil {
		return nil, err
	}
	return res.Results, nil
}

// The report as the reports service returns it (only what the tool keeps).
type reportData struct {
	Report struct {
		Period struct {
			Type     string `json:"type"`
			Start    string `json:"start"`
			End      string `json:"end"`
			Timezone string `json:"timezone"`
		} `json:"period"`
		SLATarget float64 `json:"slaTarget"`
		Overall   struct {
			Availability    *float64 `json:"availability"`
			DowntimeSeconds int64    `json:"downtimeSeconds"`
			SLAMet          *bool    `json:"slaMet"`
			Trend           *float64 `json:"trend"`
		} `json:"overall"`
		Heartbeats struct {
			Count          int      `json:"count"`
			MissedCheckins int      `json:"missedCheckins"`
			CheckinRate    *float64 `json:"checkinRate"`
		} `json:"heartbeats"`
		Monitors []struct {
			UUID            string   `json:"uuid"`
			Kind            string   `json:"kind"`
			Name            string   `json:"name"`
			Availability    float64  `json:"availability"`
			DowntimeSeconds int64    `json:"downtimeSeconds"`
			P95LatencyMs    *float64 `json:"p95LatencyMs"`
			SlowDays        *int     `json:"slowDays"`
			MissedCheckins  int      `json:"missedCheckins"`
			Deleted         bool     `json:"deleted"`
		} `json:"monitors"`
		Incidents struct {
			Count       int      `json:"count"`
			Resolved    int      `json:"resolved"`
			MTTRSeconds *float64 `json:"mttrSeconds"`
			List        []struct {
				Title       string   `json:"title"`
				MonitorUUID *string  `json:"monitorUuid"`
				StartedAt   string   `json:"startedAt"`
				ResolvedAt  *string  `json:"resolvedAt"`
				ErrorClass  *string  `json:"errorClass"`
				Regions     []string `json:"regions"`
			} `json:"list"`
		} `json:"incidents"`
		Maintenance []struct {
			Title           string   `json:"title"`
			Start           string   `json:"start"`
			End             *string  `json:"end"`
			Seconds         int64    `json:"seconds"`
			Monitors        []string `json:"monitors"`
			AddedAfterStart bool     `json:"addedAfterStart"`
		} `json:"maintenance"`
	} `json:"report"`
	Live        bool   `json:"live"`
	Methodology string `json:"methodology"`
}

type getReportIn struct {
	Report string `json:"report,omitempty" jsonschema:"report name or UUID from list_reports (default: the workspace's report covering every monitor)"`
	Period string `json:"period,omitempty" jsonschema:"last_7_days, last_30_days (default), last_90_days, last_week (the latest sent weekly report, every plan), this_week, month, quarter or range"`
	Start  string `json:"start,omitempty" jsonschema:"month or quarter: its first day, YYYY-MM-DD (default: the current one)"`
	From   string `json:"from,omitempty" jsonschema:"range: first day, YYYY-MM-DD"`
	To     string `json:"to,omitempty" jsonschema:"range: last day (included), YYYY-MM-DD"`
}

type reportMonitorRow struct {
	Name            string   `json:"name"`
	AvailabilityPct float64  `json:"availabilityPercent"`
	DowntimeMinutes float64  `json:"downtimeMinutes"`
	P95Ms           *float64 `json:"p95Ms,omitempty"`
	SlowDays        *int     `json:"slowDays,omitempty"`
	Deleted         bool     `json:"deleted,omitempty"`
}

type reportIncidentRow struct {
	Title           string   `json:"title"`
	Monitor         string   `json:"monitor,omitempty"`
	StartedAt       string   `json:"startedAt"`
	DurationMinutes *float64 `json:"durationMinutes,omitempty"`
	Ongoing         bool     `json:"ongoing,omitempty"`
	Cause           string   `json:"cause,omitempty"`
	Regions         []string `json:"regions,omitempty"`
}

type reportMaintenanceRow struct {
	Title           string   `json:"title"`
	Start           string   `json:"start"`
	End             *string  `json:"end,omitempty"`
	Minutes         float64  `json:"minutesInPeriod"`
	Monitors        []string `json:"monitors"`
	AddedAfterStart bool     `json:"addedAfterStart,omitempty"`
}

type getReportOut struct {
	Report          string                 `json:"report"`
	Period          string                 `json:"period"`
	Timezone        string                 `json:"timezone"`
	InProgress      bool                   `json:"inProgress"`
	SLATargetPct    float64                `json:"slaTargetPercent"`
	AvailabilityPct *float64               `json:"availabilityPercent"`
	SLAMet          *bool                  `json:"slaMet"`
	DowntimeMinutes float64                `json:"downtimeMinutes"`
	TrendPts        *float64               `json:"trendVsPreviousPeriodPts,omitempty"`
	Incidents       int                    `json:"incidents"`
	MTTRMinutes     *float64               `json:"mttrMinutes,omitempty"`
	HeartbeatRate   *float64               `json:"heartbeatCheckinRatePercent,omitempty"`
	MissedCheckins  int                    `json:"missedCheckins,omitempty"`
	Monitors        []reportMonitorRow     `json:"monitors"`
	MoreMonitors    int                    `json:"moreMonitors,omitempty"`
	Longest         []reportIncidentRow    `json:"longestIncidents"`
	Maintenance     []reportMaintenanceRow `json:"plannedMaintenance,omitempty"`
	Methodology     string                 `json:"methodology"`
}

// Rows the tool returns; the rest is summarized, the full report is in the app.
const (
	reportMonitorRows  = 15
	reportIncidentRows = 5
)

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F-]{36}$`)

func minutes(seconds float64) float64 { return float64(int64(seconds/6+0.5)) / 10 }

func (t *toolset) getUptimeReport(ctx context.Context, req *mcp.CallToolRequest, in getReportIn) (*mcp.CallToolResult, getReportOut, error) {
	// The report: by UUID, by name, or the default.
	q := url.Values{}
	name := "every monitor"
	if in.Report != "" {
		if uuidPattern.MatchString(in.Report) {
			q.Set("definition", in.Report)
			name = in.Report
		} else {
			defs, err := t.reportDefinitions(ctx, req)
			if err != nil {
				return nil, getReportOut{}, err
			}
			found := false
			for _, d := range defs {
				if strings.EqualFold(d.Name, strings.TrimSpace(in.Report)) {
					if !d.IsDefault {
						q.Set("definition", d.UUID)
					}
					name, found = d.Name, true
					break
				}
			}
			if !found {
				return nil, getReportOut{}, fmt.Errorf("no report named %q; list_reports shows the workspace's reports", in.Report)
			}
		}
	}

	path, err := t.reportPath(ctx, req, q, in)
	if err != nil {
		return nil, getReportOut{}, err
	}
	var res reportData
	if err := t.call(ctx, req, upstream.Reports, http.MethodGet, path, q, nil, &res); err != nil {
		return nil, getReportOut{}, err
	}
	return nil, summariseReport(name, res), nil
}

// reportPath picks the endpoint for the period, adding its query parameters.
func (t *toolset) reportPath(ctx context.Context, req *mcp.CallToolRequest, q url.Values, in getReportIn) (string, error) {
	period := strings.ToLower(strings.TrimSpace(in.Period))
	if period == "" {
		period = "last_30_days"
	}
	switch period {
	case "this_week":
		return "/v1/api/reports/live", nil
	case "last_week":
		var periods struct {
			Weeks []struct {
				PeriodStart string `json:"periodStart"`
			} `json:"weeks"`
		}
		pq := url.Values{}
		if d := q.Get("definition"); d != "" {
			pq.Set("definition", d)
		}
		if err := t.call(ctx, req, upstream.Reports, http.MethodGet, "/v1/api/reports/periods", pq, nil, &periods); err != nil {
			return "", err
		}
		if len(periods.Weeks) == 0 {
			return "", errors.New("this report hasn't sent a weekly report yet; try last_7_days")
		}
		return "/v1/api/reports/weekly/" + periods.Weeks[0].PeriodStart, nil
	case "month", "quarter":
		start := in.Start
		if start == "" {
			today, err := t.reportToday(ctx, req, q)
			if err != nil {
				return "", err
			}
			month := int(today.Month())
			if period == "quarter" {
				month = (month-1)/3*3 + 1
			}
			start = time.Date(today.Year(), time.Month(month), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02")
		}
		if !datePattern.MatchString(start) {
			return "", errors.New("start must be a date (YYYY-MM-DD)")
		}
		kind := "monthly"
		if period == "quarter" {
			kind = "quarterly"
		}
		return "/v1/api/reports/" + kind + "/" + start, nil
	case "range":
		if !datePattern.MatchString(in.From) || !datePattern.MatchString(in.To) {
			return "", errors.New("range needs from and to as dates (YYYY-MM-DD)")
		}
		q.Set("from", in.From)
		q.Set("to", in.To)
		return "/v1/api/reports/range", nil
	case "last_7_days", "last_30_days", "last_90_days":
		days := map[string]int{"last_7_days": 7, "last_30_days": 30, "last_90_days": 90}[period]
		today, err := t.reportToday(ctx, req, q)
		if err != nil {
			return "", err
		}
		q.Set("from", today.AddDate(0, 0, -(days-1)).Format("2006-01-02"))
		q.Set("to", today.Format("2006-01-02"))
		return "/v1/api/reports/range", nil
	default:
		return "", errors.New("period must be last_7_days, last_30_days, last_90_days, last_week, this_week, month, quarter or range")
	}
}

// reportToday is today's date in the workspace's report timezone.
func (t *toolset) reportToday(ctx context.Context, req *mcp.CallToolRequest, q url.Values) (time.Time, error) {
	var periods struct {
		Timezone string `json:"timezone"`
	}
	pq := url.Values{}
	if d := q.Get("definition"); d != "" {
		pq.Set("definition", d)
	}
	if err := t.call(ctx, req, upstream.Reports, http.MethodGet, "/v1/api/reports/periods", pq, nil, &periods); err != nil {
		return time.Time{}, err
	}
	loc, err := time.LoadLocation(periods.Timezone)
	if err != nil {
		loc = time.UTC
	}
	now := time.Now().In(loc)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), nil
}

func summariseReport(name string, res reportData) getReportOut {
	r := res.Report
	lastDay, _ := time.Parse("2006-01-02", r.Period.End)
	out := getReportOut{
		Report:          name,
		Period:          fmt.Sprintf("%s to %s (%s)", r.Period.Start, lastDay.AddDate(0, 0, -1).Format("2006-01-02"), r.Period.Type),
		Timezone:        r.Period.Timezone,
		InProgress:      res.Live,
		SLATargetPct:    r.SLATarget,
		AvailabilityPct: r.Overall.Availability,
		SLAMet:          r.Overall.SLAMet,
		DowntimeMinutes: minutes(float64(r.Overall.DowntimeSeconds)),
		TrendPts:        r.Overall.Trend,
		Incidents:       r.Incidents.Count,
		Methodology:     res.Methodology,
	}
	if r.Incidents.MTTRSeconds != nil {
		m := minutes(*r.Incidents.MTTRSeconds)
		out.MTTRMinutes = &m
	}
	if r.Heartbeats.Count > 0 {
		out.HeartbeatRate = r.Heartbeats.CheckinRate
		out.MissedCheckins = r.Heartbeats.MissedCheckins
	}

	names := map[string]string{}
	monitors := make([]reportMonitorRow, 0, len(r.Monitors))
	for _, m := range r.Monitors {
		names[m.UUID] = m.Name
		if m.Kind == "heartbeat" {
			continue
		}
		monitors = append(monitors, reportMonitorRow{Name: m.Name, AvailabilityPct: m.Availability, DowntimeMinutes: minutes(float64(m.DowntimeSeconds)), P95Ms: m.P95LatencyMs, SlowDays: m.SlowDays, Deleted: m.Deleted})
	}
	// Worst first.
	sort.SliceStable(monitors, func(i, j int) bool {
		if monitors[i].AvailabilityPct != monitors[j].AvailabilityPct {
			return monitors[i].AvailabilityPct < monitors[j].AvailabilityPct
		}
		return monitors[i].DowntimeMinutes > monitors[j].DowntimeMinutes
	})
	if len(monitors) > reportMonitorRows {
		out.MoreMonitors = len(monitors) - reportMonitorRows
		monitors = monitors[:reportMonitorRows]
	}
	out.Monitors = monitors

	// The reports service lists incidents longest first.
	out.Longest = make([]reportIncidentRow, 0, reportIncidentRows)
	for _, i := range r.Incidents.List {
		if len(out.Longest) == reportIncidentRows {
			break
		}
		row := reportIncidentRow{Title: i.Title, StartedAt: i.StartedAt, Regions: i.Regions}
		if i.MonitorUUID != nil {
			row.Monitor = names[*i.MonitorUUID]
		}
		if i.ErrorClass != nil {
			row.Cause = *i.ErrorClass
		}
		if i.ResolvedAt == nil {
			row.Ongoing = true
		} else if start, err := time.Parse(time.RFC3339, i.StartedAt); err == nil {
			if end, err := time.Parse(time.RFC3339, *i.ResolvedAt); err == nil {
				d := minutes(end.Sub(start).Seconds())
				row.DurationMinutes = &d
			}
		}
		out.Longest = append(out.Longest, row)
	}

	for _, m := range r.Maintenance {
		out.Maintenance = append(out.Maintenance, reportMaintenanceRow{Title: m.Title, Start: m.Start, End: m.End, Minutes: minutes(float64(m.Seconds)), Monitors: m.Monitors, AddedAfterStart: m.AddedAfterStart})
	}
	return out
}
