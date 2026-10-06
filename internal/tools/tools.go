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
// publicHeartbeatsURL is the public base of heartbeat ping URLs; the upstream
// client may talk to the heartbeat service over an in-cluster address.
func Register(srv *mcp.Server, up *upstream.Client, publicHeartbeatsURL string) {
	t := &toolset{up: up, publicHeartbeatsURL: publicHeartbeatsURL}

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_status_overview",
		Title:       "Status overview",
		Description: "What is down right now: every website/API monitor and cron heartbeat with its current status, open incidents, and status pages. Problems are listed first. Use the UUIDs here with the other tools.",
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
		Name:        "create_incident",
		Title:       "Create incident",
		Description: "Open an incident manually. Pass statusPageUuids to show it publicly on those status pages (subscribers are notified).",
		Annotations: write(false, false),
	}, t.createIncident)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "post_incident_update",
		Title:       "Post incident update",
		Description: "Add a timeline update to an incident and/or move it to a new status (Acknowledged, Investigating, Identified, Monitoring, Resolved). Public updates appear on every status page the incident is shown on.",
		Annotations: write(false, false),
	}, t.postIncidentUpdate)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "set_monitor_paused",
		Title:       "Pause or resume monitor",
		Description: "Pause (stop checking and alerting) or resume a website/API monitor or cron heartbeat.",
		Annotations: write(true, true),
	}, t.setMonitorPaused)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "trigger_check",
		Title:       "Run check now",
		Description: "Run a website/API monitor's check immediately from all its regions, e.g. to confirm a fix. Read the result with get_monitor_metrics a few seconds later.",
		Annotations: write(false, false),
	}, t.triggerCheck)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "schedule_maintenance",
		Title:       "Schedule maintenance",
		Description: "Schedule a maintenance window. Alerts and incidents for the listed monitors are suppressed while it is active. Pass statusPageUuids to announce it on status pages.",
		Annotations: write(false, false),
	}, t.scheduleMaintenance)
}

type toolset struct {
	up                  *upstream.Client
	publicHeartbeatsURL string
}

func readOnly() *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f}
}

func write(destructive, idempotent bool) *mcp.ToolAnnotations {
	f := false
	return &mcp.ToolAnnotations{DestructiveHint: &destructive, IdempotentHint: idempotent, OpenWorldHint: &f}
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
