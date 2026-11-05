package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/mcp/internal/upstream"
)

// Report settings, as the app's report drawer sets them: what a report
// covers, its SLA target and its email schedule. The reports service keeps
// the plan's rules: a new report needs advancedReports (Growth and above);
// without it only the default report changes, and it stays weekly. Share
// links and logos stay in the app.

const defaultSLATarget = 99.9

// reportSettings are the fields add_report and update_report share. On
// update, what isn't given stays as it is.
type reportSettings struct {
	MonitorUUIDs []string `json:"monitorUuids,omitempty" jsonschema:"cover only these monitors or cron monitors (UUIDs from get_status_overview)"`
	Tags         []string `json:"tags,omitempty" jsonschema:"or cover the monitors with any of these existing tags"`
	SLATarget    *float64 `json:"slaTargetPercent,omitempty" jsonschema:"availability the report is judged against, 50-100 (default 99.9)"`
	SlowAboveMs  *int     `json:"slowAboveMs,omitempty" jsonschema:"count days with p95 latency above this many ms as slow (shown only, never downtime); 0 to stop"`
	Schedule     string   `json:"schedule,omitempty" jsonschema:"when it's emailed: weekly, monthly, quarterly or none (read on demand only)"`
	SendDay      *int     `json:"sendDay,omitempty" jsonschema:"weekly: 0 Sunday to 6 Saturday; monthly and quarterly: day of the month 1-28 (default 1)"`
	SendTime     string   `json:"sendTime,omitempty" jsonschema:"HH:MM, 24-hour (default 09:00)"`
	Timezone     string   `json:"timezone,omitempty" jsonschema:"IANA timezone of the send time, e.g. Europe/Berlin (default: the default report's)"`
	Emails       []string `json:"emails,omitempty" jsonschema:"who gets it by email (replaces the list on update)"`
	SendEmail    *bool    `json:"sendEmail,omitempty" jsonschema:"false to stop emailing it and keep the recipients"`
	BrandName    *string  `json:"brandName,omitempty" jsonschema:"company name the report is branded with, e.g. a client's; empty to remove"`
	BrandColor   *string  `json:"brandColor,omitempty" jsonschema:"accent color #rrggbb; empty to remove"`
}

type reportOut struct {
	reportRow
	Emails []string `json:"emails"`
	// True when a report with this name already existed; nothing was added.
	AlreadyExisted bool   `json:"alreadyExisted,omitempty"`
	Note           string `json:"note,omitempty"`
}

// --- add_report ---

type addReportIn struct {
	Name string `json:"name" jsonschema:"e.g. a client's name: 'Acme'"`
	reportSettings
}

func (t *toolset) addReport(ctx context.Context, req *mcp.CallToolRequest, in addReportIn) (*mcp.CallToolResult, reportOut, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, reportOut{}, errors.New("name is required")
	}
	defs, err := t.reportDefinitions(ctx, req)
	if err != nil {
		return nil, reportOut{}, err
	}
	timezone := "UTC"
	for _, d := range defs {
		if strings.EqualFold(d.Name, name) {
			out, err := t.reportOut(ctx, req, d)
			out.AlreadyExisted, out.Note = true, "A report with this name already exists; nothing was added. update_report changes it."
			return nil, out, err
		}
		if d.IsDefault && d.SendTimezone != "" {
			timezone = d.SendTimezone
		}
	}

	// The app's defaults for a new report; a report nobody is emailed is
	// read on demand.
	schedule := "monthly"
	if len(in.Emails) == 0 {
		schedule = "none"
	}
	body := map[string]any{
		"name": name, "scopeType": "workspace", "monitorUuids": nil, "tagIds": nil,
		"slaTarget": defaultSLATarget, "latencyThresholdMs": nil,
		"cadence": schedule, "sendDay": 1, "sendHour": 9, "sendMinute": 0, "sendTimezone": timezone,
		"emailEnabled": true, "recipients": []string{},
		"brandName": nil, "brandLogoUrl": nil, "brandColor": nil,
	}
	if err := t.applyReportSettings(ctx, req, body, in.reportSettings); err != nil {
		return nil, reportOut{}, err
	}
	var created reportDefinition
	if err := t.call(ctx, req, upstream.Reports, http.MethodPost, "/v1/api/reports/definitions", nil, body, &created); err != nil {
		return nil, reportOut{}, reportPlanError(err)
	}
	out, err := t.reportOut(ctx, req, created)
	out.Note = "get_uptime_report reads it for any period."
	if created.Cadence != "none" && created.EmailEnabled {
		out.Note += " The first email goes out when the current period ends."
	}
	return nil, out, err
}

// --- update_report ---

type updateReportIn struct {
	Report      string `json:"report" jsonschema:"the report's UUID or name from list_reports"`
	Name        string `json:"name,omitempty" jsonschema:"new name (the default report keeps its own)"`
	AllMonitors bool   `json:"allMonitors,omitempty" jsonschema:"cover every monitor again, instead of chosen or tagged ones"`
	reportSettings
}

func (t *toolset) updateReport(ctx context.Context, req *mcp.CallToolRequest, in updateReportIn) (*mcp.CallToolResult, reportOut, error) {
	defs, err := t.reportDefinitions(ctx, req)
	if err != nil {
		return nil, reportOut{}, err
	}
	d, err := findReport(defs, in.Report)
	if err != nil {
		return nil, reportOut{}, err
	}
	if in.AllMonitors && (len(in.MonitorUUIDs) > 0 || len(in.Tags) > 0) {
		return nil, reportOut{}, errors.New("give one of allMonitors, monitorUuids or tags")
	}

	// The update takes the whole report: send it back with the changes.
	var current map[string]any
	path := "/v1/api/reports/definitions/" + url.PathEscape(d.UUID)
	if err := t.call(ctx, req, upstream.Reports, http.MethodGet, path, nil, nil, &current); err != nil {
		return nil, reportOut{}, err
	}
	body := map[string]any{}
	for _, k := range []string{"name", "scopeType", "monitorUuids", "tagIds", "slaTarget", "latencyThresholdMs", "cadence", "sendDay", "sendHour", "sendMinute",
		"sendTimezone", "emailEnabled", "recipients", "brandName", "brandLogoUrl", "brandColor"} {
		body[k] = current[k]
	}
	if name := strings.TrimSpace(in.Name); name != "" {
		body["name"] = name
	}
	if in.AllMonitors {
		body["scopeType"], body["monitorUuids"], body["tagIds"] = "workspace", nil, nil
	}
	if err := t.applyReportSettings(ctx, req, body, in.reportSettings); err != nil {
		return nil, reportOut{}, err
	}
	var updated reportDefinition
	if err := t.call(ctx, req, upstream.Reports, http.MethodPut, path, nil, body, &updated); err != nil {
		return nil, reportOut{}, reportPlanError(err)
	}
	out, err := t.reportOut(ctx, req, updated)
	return nil, out, err
}

// applyReportSettings writes the given settings into a report body.
func (t *toolset) applyReportSettings(ctx context.Context, req *mcp.CallToolRequest, body map[string]any, s reportSettings) error {
	switch {
	case len(s.MonitorUUIDs) > 0 && len(s.Tags) > 0:
		return errors.New("give monitorUuids or tags, not both")
	case len(s.MonitorUUIDs) > 0:
		// Reports name monitors by application UUID (website/API checks)
		// or heartbeat UUID, as incidents do.
		services, err := t.affectedServices(ctx, req, s.MonitorUUIDs)
		if err != nil {
			return err
		}
		uuids := make([]string, 0, len(services))
		for _, svc := range services {
			uuids = append(uuids, svc.UUID)
		}
		body["scopeType"], body["monitorUuids"], body["tagIds"] = "monitors", uuids, nil
	case len(s.Tags) > 0:
		tags, err := t.listTags(ctx, req)
		if err != nil {
			return err
		}
		names := map[int]string{}
		for _, tag := range tags {
			names[tag.ID] = tag.Name
		}
		ids, err := tagIDs(names, s.Tags)
		if err != nil {
			return err
		}
		body["scopeType"], body["monitorUuids"], body["tagIds"] = "tags", nil, ids
	}

	if s.SLATarget != nil {
		body["slaTarget"] = *s.SLATarget
	}
	if s.SlowAboveMs != nil {
		body["latencyThresholdMs"] = nil
		if *s.SlowAboveMs > 0 {
			body["latencyThresholdMs"] = *s.SlowAboveMs
		}
	}
	if s.Schedule != "" {
		cadence := strings.ToLower(strings.TrimSpace(s.Schedule))
		switch cadence {
		case "none", "weekly", "monthly", "quarterly":
		default:
			return fmt.Errorf("unknown schedule %q: use weekly, monthly, quarterly or none", s.Schedule)
		}
		// A weekday and a day of the month don't carry over (as in the app).
		if old, _ := body["cadence"].(string); (old == "weekly") != (cadence == "weekly") {
			body["sendDay"] = 1
		}
		body["cadence"] = cadence
	}
	if s.SendDay != nil {
		body["sendDay"] = *s.SendDay
	}
	if s.SendTime != "" {
		at, err := time.Parse("15:04", strings.TrimSpace(s.SendTime))
		if err != nil {
			return fmt.Errorf("sendTime %q: use HH:MM, e.g. 09:00", s.SendTime)
		}
		body["sendHour"], body["sendMinute"] = at.Hour(), at.Minute()
	}
	if s.Timezone != "" {
		body["sendTimezone"] = strings.TrimSpace(s.Timezone)
	}
	if s.Emails != nil {
		body["recipients"] = s.Emails
	}
	if s.SendEmail != nil {
		body["emailEnabled"] = *s.SendEmail
	}
	if s.BrandName != nil {
		body["brandName"] = nilIfEmpty(*s.BrandName)
	}
	if s.BrandColor != nil {
		body["brandColor"] = nilIfEmpty(*s.BrandColor)
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s = strings.TrimSpace(s); s == "" {
		return nil
	}
	return s
}

func findReport(defs []reportDefinition, ref string) (reportDefinition, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return reportDefinition{}, errors.New("report is required: its UUID or name from list_reports")
	}
	for _, d := range defs {
		if d.UUID == ref || strings.EqualFold(d.Name, ref) {
			return d, nil
		}
	}
	return reportDefinition{}, fmt.Errorf("no report named %q; list_reports shows the workspace's reports", ref)
}

func (t *toolset) reportOut(ctx context.Context, req *mcp.CallToolRequest, d reportDefinition) (reportOut, error) {
	tags, err := t.reportTags(ctx, req, d)
	emails := d.Recipients
	if emails == nil {
		emails = []string{}
	}
	return reportOut{reportRow: d.row(tags), Emails: emails}, err
}

// reportPlanError says which report settings the plan is missing.
func reportPlanError(err error) error {
	var e *upstream.Error
	if errors.As(err, &e) && e.Code == "FEATURE_NOT_AVAILABLE.400" {
		return errors.New("this needs Growth and above: more reports than the default one, monthly or quarterly emails, an SLA target other than 99.9%, slow days and branding. On this plan the default report can change its recipients and send time and stays weekly. Upgrade in the Uptimy app")
	}
	return err
}
