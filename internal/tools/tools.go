// Package tools defines the Uptimy MCP tools. Each one wraps the same
// /v1/api/* routes uptimyctl calls, then trims the answer down to what a
// model needs: raw API payloads (e.g. a healthcheck list carries every
// monitor's daily rollups) would burn the caller's context for nothing.
package tools

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

// TokenInfo.Extra keys, set by the server's key verifier.
const (
	KeyExtra       = "key"
	WorkspaceExtra = "workspace"
)

// Register adds every tool to srv.
// pingBaseURL is the public base of heartbeat ping URLs; the upstream
// client may talk to the heartbeat service over an in-cluster address.
func Register(srv *mcp.Server, up *upstream.Client, pingBaseURL string) {
	t := &toolset{up: up, pingBaseURL: pingBaseURL}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_status_overview",
		Title:       "Status overview",
		Description: "What is down right now: every website/API monitor and cron heartbeat with its current status, open incidents, status pages, and the third-party services the workspace follows (e.g. GitHub, Stripe) with their status. Problems are listed first. Use the UUIDs here with the other tools.",
		Annotations: readOnly(),
	}, t.statusOverview)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_monitor",
		Title:       "Get monitor",
		Description: "Details of one monitor (website/API check or cron heartbeat) by UUID: target, schedule, regions, current status and last check-in.",
		Annotations: readOnly(),
	}, t.getMonitor)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_monitor_metrics",
		Title:       "Monitor metrics",
		Description: "Uptime and latency of a website/API monitor over a time range: totals, a per-region breakdown, a time series, and the most recent failed checks with their errors. Defaults to the last 24 hours.",
		Annotations: readOnly(),
	}, t.monitorMetrics)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_incidents",
		Title:       "List incidents",
		Description: "Recent incidents, newest first. Filter by open/resolved and severity.",
		Annotations: readOnly(),
	}, t.listIncidents)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_incident",
		Title:       "Get incident",
		Description: "One incident with its timeline: updates (public and internal), status changes, and the status pages it is shown on.",
		Annotations: readOnly(),
	}, t.getIncident)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_reports",
		Title:       "List uptime reports",
		Description: "The workspace's uptime reports: what each covers (every monitor, or chosen ones, e.g. one client's), its SLA target and its email schedule. Pass a report's name to get_uptime_report.",
		Annotations: readOnly(),
	}, t.listReports)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_uptime_report",
		Title:       "Uptime report",
		Description: "Availability against the SLA target for a period: the last 7, 30 (default) or 90 days, the last sent week, this week, a calendar month or quarter, or any range of days. Returns downtime, incidents and time to resolve, each monitor's availability worst first, the longest incidents with their causes, and planned maintenance (not counted as downtime). Uptime is time-based: downtime is the time an automatic incident was open. Below the Growth plan only last_week (the weekly report) is available.",
		Annotations: readOnly(),
	}, t.getUptimeReport)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_dependencies",
		Title:       "Third-party dependencies",
		Description: "Third-party services Uptimy measures independently (GitHub, Cloudflare, Stripe, ...) and which ones this workspace follows. Without slug: every service with its status and 30-day uptime, followed ones first. With slug: that service's probes (e.g. website, API, git operations), what each one means, its status and uptime, and whether it's followed. Use it to tell whether a problem is yours or a provider's.",
		Annotations: readOnly(),
	}, t.getDependencies)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "follow_dependency",
		Title:       "Follow or unfollow a dependency",
		Description: "Follow a third-party service so its outages open incidents in this workspace and run its alert workflows, or stop following it (which resolves its open incident here). Optionally follow only some probes.",
		Annotations: write(true, true, false),
	}, t.followDependency)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_incident",
		Title:       "Create incident",
		Description: "Open an incident manually. Pass statusPageUuids to show it publicly on those status pages.",
		Annotations: write(false, false, true),
	}, t.createIncident)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "post_incident_update",
		Title:       "Post incident update",
		Description: "Add a timeline update to an incident, move it to a new status (Acknowledged, Investigating, Identified, Monitoring, Resolved), and/or show it on more status pages. Public updates appear on every status page the incident is on; pass statusPageUuids to publish an incident that is on none yet (incidents opened automatically by a monitor start on none).",
		Annotations: write(false, false, true),
	}, t.postIncidentUpdate)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_monitor_paused",
		Title:       "Pause or resume monitor",
		Description: "Pause (stop checking and alerting) or resume a website/API monitor or cron heartbeat.",
		Annotations: write(true, true, false),
	}, t.setMonitorPaused)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "trigger_check",
		Title:       "Run check now",
		Description: "Run a website/API monitor's check immediately from all its regions, e.g. to confirm a fix. Read the result with get_monitor_metrics a few seconds later.",
		Annotations: write(false, false, false),
	}, t.triggerCheck)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "schedule_maintenance",
		Title:       "Schedule maintenance",
		Description: "Schedule a maintenance window. Alerts and incidents for the listed monitors are suppressed while it is active. Pass statusPageUuids to announce it on status pages.",
		Annotations: write(false, false, true),
	}, t.scheduleMaintenance)

	// Setting the workspace up (setup.go): these only add things.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "suggest_monitors",
		Title:       "Suggest monitors for a domain",
		Description: "Scan a domain the way Uptimy's Setup does: its usual subdomains (www, api, app, ...) and their certificates. Returns what to monitor, which of it is already monitored, and how many more checks the plan allows. Adds nothing: show the list to the user, then call add_monitors with what they pick.",
		Annotations: readOnlyOpenWorld(),
	}, t.suggestMonitors)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_monitors",
		Title:       "Add monitors",
		Description: "Add website/API checks (http), certificate expiry (ssl), open ports (tcp), ping and DNS checks, up to 20 at a time. They check from every region the plan allows, every 60s by default (ssl hourly), and alert like the workspace's other monitors. A target already checked by a monitor of the same type is skipped and listed, so calling twice is safe.",
		Annotations: write(false, true, false),
	}, t.addMonitors)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_cron_monitor",
		Title:       "Add cron job monitor",
		Description: "Watch a scheduled job (cron, backup, queue worker) with a heartbeat: give how often it runs or its cron schedule. Returns the ping URL and the line to add to the job; a run that's late or doesn't ping alerts. If a cron monitor with this name exists, returns it instead of adding another.",
		Annotations: write(false, true, false),
	}, t.addCronMonitor)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "create_status_page",
		Title:       "Create status page",
		Description: "Create a status page showing these monitors and cron monitors. Private (workspace members only) unless public is true; publish only when the user asks.",
		Annotations: write(false, false, true),
	}, t.createStatusPage)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_to_status_page",
		Title:       "Add to status page",
		Description: "Show more monitors or cron monitors on an existing status page (after the ones it already shows). Monitors already on the page are skipped. Changes what a public page's visitors see.",
		Annotations: write(false, true, true),
	}, t.addToStatusPage)

	// Alerting (alerts.go): who hears about incidents.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_alerts",
		Title:       "List alerts",
		Description: "Who hears about incidents: every alert (workflow) with the events it sends, where it sends them, its scope and whether it's paused, plus the connected integrations (Slack, Teams, ...) an alert can post to. Alerts made in the workflow editor are listed but can only be changed there.",
		Annotations: readOnly(),
	}, t.listAlerts)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_alert",
		Title:       "Add alert",
		Description: "Send incidents (opened and/or resolved) to a connected Slack, Discord, Teams, Telegram or PagerDuty integration, to email addresses, or to a webhook, optionally only for some tags, severities or monitors. Live at once. Integrations are connected in the Uptimy app; list_alerts shows which are. If an alert with this name exists, returns it instead of adding another.",
		Annotations: write(false, true, false),
	}, t.addAlert)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_alert_paused",
		Title:       "Pause or resume alert",
		Description: "Stop an alert from sending (e.g. during planned work) or let it send again. Works on any alert, including ones made in the workflow editor.",
		Annotations: write(true, true, false),
	}, t.setAlertPaused)

	// Reports (report_setup.go): what each report covers and who gets it.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "add_report",
		Title:       "Add report",
		Description: "Add an uptime report, e.g. one per client: availability against an SLA target for every monitor, chosen monitors or tagged ones, optionally branded with a company name and color. Given emails, it's emailed monthly (or weekly or quarterly); without, it's read on demand with get_uptime_report. Needs Growth and above. If a report with this name exists, returns it instead of adding another.",
		Annotations: write(false, true, false),
	}, t.addReport)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_report",
		Title:       "Change report",
		Description: "Change a report's monitors, SLA target, email schedule, recipients or branding; what isn't given stays as it is. Works on the default report too, which always covers every monitor. Below Growth only the default report changes, and it stays weekly.",
		Annotations: write(true, true, false),
	}, t.updateReport)
}

type toolset struct {
	up          *upstream.Client
	pingBaseURL string
}

// Every tool sets all three hints explicitly: directories (Claude, ChatGPT)
// review them, and ChatGPT requires explicit booleans.
func readOnly() *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &f, OpenWorldHint: &f}
}

// readOnlyOpenWorld describes a tool that changes nothing in Uptimy but
// reaches outside it (suggest_monitors probes the user's domain).
func readOnlyOpenWorld() *mcp.ToolAnnotations {
	f, t := false, true
	return &mcp.ToolAnnotations{ReadOnlyHint: true, DestructiveHint: &f, OpenWorldHint: &t}
}

// write describes a tool that changes the workspace. publishes marks tools
// that can post to public status pages (and their subscribers): the
// open-world hint, which clients use to ask before acting outside the app.
func write(destructive, idempotent, publishes bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &publishes}
}

// call runs one upstream request with the key the request authenticated with.
func (t *toolset) call(ctx context.Context, req *mcp.CallToolRequest, svc upstream.Service, method, path string, query url.Values, body, out any) error {
	return t.up.Do(ctx, key(req), svc, method, path, query, body, out)
}

func key(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ""
	}
	k, _ := req.Extra.TokenInfo.Extra[KeyExtra].(string)
	return k
}

func workspace(req *mcp.CallToolRequest) string {
	if req == nil || req.Extra == nil || req.Extra.TokenInfo == nil {
		return ""
	}
	w, _ := req.Extra.TokenInfo.Extra[WorkspaceExtra].(string)
	return w
}

// healthcheck is the subset of the API's healthcheck payload the tools use.
type healthcheck struct {
	UUID            string         `json:"uuid"`
	IntervalSeconds int            `json:"intervalSeconds"`
	TimeoutSeconds  int            `json:"timeoutSeconds"`
	Config          map[string]any `json:"config"`
	Active          bool           `json:"active"`
	TypeID          any            `json:"healthcheckTypeId"`
	ApplicationUUID string         `json:"applicationUuid"`
	Type            struct {
		ID   any    `json:"id"`
		Name string `json:"name"`
	} `json:"healthcheckType"`
	Application struct {
		UUID             string `json:"uuid"`
		Name             string `json:"name"`
		Description      string `json:"description"`
		Status           string `json:"status"`
		LastStatusUpdate string `json:"lastStatusUpdate"`
	} `json:"application"`
	Schedulers []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"schedulers"`
	Tags []struct {
		ID   any    `json:"id"`
		Name string `json:"name"`
	} `json:"tags"`
}

func (h healthcheck) status() string {
	if !h.Active {
		return "Paused"
	}
	if h.Application.Status == "" {
		return "Unknown"
	}
	return h.Application.Status
}

func (h healthcheck) regions() []string {
	out := make([]string, 0, len(h.Schedulers))
	for _, s := range h.Schedulers {
		out = append(out, s.Name)
	}
	return out
}

// heartbeat is the subset of the heartbeat service's monitor payload the tools use.
type heartbeat struct {
	UUID              string         `json:"uuid"`
	Name              string         `json:"name"`
	Description       string         `json:"description"`
	IntervalSeconds   int            `json:"intervalSeconds"`
	Cron              string         `json:"cron"`
	Timezone          string         `json:"timezone"`
	NoSchedule        bool           `json:"noSchedule"`
	MaxRunSeconds     int            `json:"maxRunSeconds"`
	GraceSeconds      int            `json:"graceSeconds"`
	FailureThreshold  any            `json:"failureThreshold"`
	RecoveryThreshold any            `json:"recoveryThreshold"`
	EnforceOrder      any            `json:"enforceOrder"`
	Steps             any            `json:"steps"`
	PausedAt          *string        `json:"pausedAt"`
	Credential        map[string]any `json:"credential"`
	StatusSnapshot    *struct {
		Status            string  `json:"status"`
		LastPingAt        *string `json:"lastPingAt"`
		LastSuccessAt     *string `json:"lastSuccessAt"`
		LastFailureAt     *string `json:"lastFailureAt"`
		LastTransitionAt  *string `json:"lastTransitionAt"`
		NextDeadlineAt    *string `json:"nextDeadlineAt"`
		ConsecutiveMisses int     `json:"consecutiveMisses"`
	} `json:"statusSnapshot"`
}

func (h heartbeat) status() string {
	if h.PausedAt != nil {
		return "Paused"
	}
	if h.StatusSnapshot == nil || h.StatusSnapshot.Status == "" {
		return "Unknown"
	}
	return h.StatusSnapshot.Status
}

func (t *toolset) listHealthchecks(ctx context.Context, req *mcp.CallToolRequest) ([]healthcheck, error) {
	var res upstream.Results[healthcheck]
	err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/healthchecks/", nil, nil, &res)
	return res.Results, err
}

func (t *toolset) listHeartbeats(ctx context.Context, req *mcp.CallToolRequest) ([]heartbeat, error) {
	var res upstream.Results[heartbeat]
	err := t.call(ctx, req, upstream.Heartbeats, http.MethodGet, "/v1/api/heartbeat-monitors/", nil, nil, &res)
	return res.Results, err
}

// monitor is a UUID resolved to whichever kind of monitor it names.
type monitor struct {
	healthcheck *healthcheck
	heartbeat   *heartbeat
}

// findMonitor looks a UUID up as a healthcheck first, then as a heartbeat.
func (t *toolset) findMonitor(ctx context.Context, req *mcp.CallToolRequest, uuid string) (monitor, error) {
	if uuid == "" {
		return monitor{}, errors.New("uuid is required")
	}
	var hc healthcheck
	err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/healthchecks/"+url.PathEscape(uuid), nil, nil, &hc)
	if err == nil {
		return monitor{healthcheck: &hc}, nil
	}
	if !upstream.IsNotFound(err) {
		return monitor{}, err
	}
	var hb heartbeat
	err = t.call(ctx, req, upstream.Heartbeats, http.MethodGet, "/v1/api/heartbeat-monitors/"+url.PathEscape(uuid), nil, nil, &hb)
	if upstream.IsNotFound(err) {
		return monitor{}, errors.New("no monitor or heartbeat with UUID " + uuid + "; get_status_overview lists them")
	}
	if err != nil {
		return monitor{}, err
	}
	return monitor{heartbeat: &hb}, nil
}

// sensitive config keys never reach the model: request headers and bodies
// can carry credentials, and browser-check scripts are long and can too.
var sensitive = []string{"header", "auth", "password", "secret", "token", "cookie", "key", "body", "script", "credential"}

func redactConfig(cfg map[string]any) map[string]any {
	out := make(map[string]any, len(cfg))
	for k, v := range cfg {
		lk := strings.ToLower(k)
		hidden := false
		for _, s := range sensitive {
			if strings.Contains(lk, s) {
				hidden = true
				break
			}
		}
		if hidden {
			out[k] = "[redacted]"
			continue
		}
		out[k] = v
	}
	return out
}
