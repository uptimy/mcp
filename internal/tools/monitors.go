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

type uuidIn struct {
	UUID string `json:"uuid" jsonschema:"monitor or heartbeat UUID from get_status_overview"`
}

type monitorOut struct {
	UUID        string `json:"uuid"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Description string `json:"description,omitempty"`
	Status      string `json:"status"`
	StatusSince string `json:"statusSince,omitempty"`
	// A heartbeat on a cron schedule has Cron and Timezone instead, and one
	// that expects no run has NoSchedule.
	IntervalSeconds int    `json:"intervalSeconds,omitempty"`
	Cron            string `json:"cron,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	NoSchedule      bool   `json:"noSchedule,omitempty"`
	// MaxRunSeconds: with no schedule, how long a run may take. 0: no limit.
	MaxRunSeconds int `json:"maxRunSeconds,omitempty"`
	// Checks only.
	Type           string         `json:"type,omitempty"`
	TimeoutSeconds int            `json:"timeoutSeconds,omitempty"`
	Target         map[string]any `json:"target,omitempty"`
	Regions        []string       `json:"regions,omitempty"`
	Tags           []string       `json:"tags,omitempty"`
	// Heartbeats only.
	GraceSeconds      int     `json:"graceSeconds,omitempty"`
	PingURL           string  `json:"pingUrl,omitempty"`
	LastCheckIn       *string `json:"lastCheckIn,omitempty"`
	LastSuccess       *string `json:"lastSuccess,omitempty"`
	LastFailure       *string `json:"lastFailure,omitempty"`
	NextDeadline      *string `json:"nextDeadline,omitempty"`
	ConsecutiveMisses int     `json:"consecutiveMisses,omitempty"`
}

func (t *toolset) getMonitor(ctx context.Context, req *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, monitorOut, error) {
	m, err := t.findMonitor(ctx, req, in.UUID)
	if err != nil {
		return nil, monitorOut{}, err
	}
	if h := m.healthcheck; h != nil {
		out := monitorOut{
			UUID: h.UUID, Name: h.Application.Name, Kind: "check", Description: h.Application.Description,
			Status: h.status(), StatusSince: h.Application.LastStatusUpdate, IntervalSeconds: h.IntervalSeconds,
			Type: h.Type.Name, TimeoutSeconds: h.TimeoutSeconds, Target: redactConfig(h.Config), Regions: h.regions(),
		}
		for _, tag := range h.Tags {
			out.Tags = append(out.Tags, tag.Name)
		}
		return nil, out, nil
	}
	h := m.heartbeat
	out := monitorOut{
		UUID: h.UUID, Name: h.Name, Kind: "heartbeat", Description: h.Description, Status: h.status(),
		IntervalSeconds: h.IntervalSeconds, Cron: h.Cron, Timezone: h.Timezone, NoSchedule: h.NoSchedule, MaxRunSeconds: h.MaxRunSeconds, GraceSeconds: h.GraceSeconds, PingURL: t.pingURL(*h),
	}
	if s := h.StatusSnapshot; s != nil {
		if s.LastTransitionAt != nil {
			out.StatusSince = *s.LastTransitionAt
		}
		out.LastCheckIn, out.LastSuccess, out.LastFailure, out.NextDeadline = s.LastPingAt, s.LastSuccessAt, s.LastFailureAt, s.NextDeadlineAt
		out.ConsecutiveMisses = s.ConsecutiveMisses
	}
	return nil, out, nil
}

// pingURL is where the monitored job sends its check-ins.
func (t *toolset) pingURL(h heartbeat) string {
	id, _ := h.Credential["publicId"].(string)
	if id == "" {
		return ""
	}
	return strings.TrimRight(t.publicHeartbeatsURL, "/") + "/v1/monitors/" + url.PathEscape(id)
}

type pausedIn struct {
	UUID   string `json:"uuid" jsonschema:"monitor or heartbeat UUID from get_status_overview"`
	Paused bool   `json:"paused" jsonschema:"true to pause, false to resume"`
}

type actionOut struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

func (t *toolset) setMonitorPaused(ctx context.Context, req *mcp.CallToolRequest, in pausedIn) (*mcp.CallToolResult, actionOut, error) {
	m, err := t.findMonitor(ctx, req, in.UUID)
	if err != nil {
		return nil, actionOut{}, err
	}
	verb := "Resumed"
	if in.Paused {
		verb = "Paused"
	}

	if h := m.healthcheck; h != nil {
		// The update is a full replace, so every field is carried over (as
		// uptimyctl's `healthchecks pause` does) and only active changes.
		typeID := h.TypeID
		if typeID == nil {
			typeID = h.Type.ID
		}
		tags := make([]map[string]any, 0, len(h.Tags))
		for _, tag := range h.Tags {
			tags = append(tags, map[string]any{"id": tag.ID})
		}
		body := map[string]any{
			"intervalSeconds":   h.IntervalSeconds,
			"timeoutSeconds":    h.TimeoutSeconds,
			"config":            h.Config,
			"active":            !in.Paused,
			"healthcheckTypeId": typeID,
			"schedulers":        h.Schedulers,
			"tags":              tags,
		}
		if err := t.call(ctx, req, upstream.API, http.MethodPut, "/v1/api/healthchecks/"+url.PathEscape(h.UUID), nil, body, nil); err != nil {
			return nil, actionOut{}, err
		}
		return nil, actionOut{OK: true, Message: verb + " check " + h.Application.Name}, nil
	}

	// Heartbeat updates also replace the whole configuration, steps included.
	h := m.heartbeat
	body := map[string]any{
		"paused": in.Paused, "name": h.Name, "description": h.Description,
		"intervalSeconds": h.IntervalSeconds, "graceSeconds": h.GraceSeconds,
	}
	if h.Cron != "" {
		body["cron"], body["timezone"] = h.Cron, h.Timezone
	}
	for k, v := range map[string]any{"failureThreshold": h.FailureThreshold, "recoveryThreshold": h.RecoveryThreshold, "enforceOrder": h.EnforceOrder, "steps": h.Steps} {
		if v != nil {
			body[k] = v
		}
	}
	if err := t.call(ctx, req, upstream.Heartbeats, http.MethodPut, "/v1/api/heartbeat-monitors/"+url.PathEscape(h.UUID), nil, body, nil); err != nil {
		return nil, actionOut{}, err
	}
	return nil, actionOut{OK: true, Message: verb + " heartbeat " + h.Name}, nil
}

func (t *toolset) triggerCheck(ctx context.Context, req *mcp.CallToolRequest, in uuidIn) (*mcp.CallToolResult, actionOut, error) {
	m, err := t.findMonitor(ctx, req, in.UUID)
	if err != nil {
		return nil, actionOut{}, err
	}
	if m.healthcheck == nil {
		return nil, actionOut{}, errors.New("heartbeats wait for check-ins from your job and can't be run on demand; only website/API checks can")
	}
	if err := t.call(ctx, req, upstream.API, http.MethodPatch, "/v1/api/healthchecks/"+url.PathEscape(in.UUID)+"/trigger", nil, nil, nil); err != nil {
		return nil, actionOut{}, err
	}
	return nil, actionOut{OK: true, Message: "Check queued for " + m.healthcheck.Application.Name + " in " + strings.Join(m.healthcheck.regions(), ", ")}, nil
}
