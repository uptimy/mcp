package tools

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/mcp/internal/upstream"
)

type maintenanceIn struct {
	Title           string   `json:"title" jsonschema:"e.g. 'Database upgrade'"`
	Description     string   `json:"description" jsonschema:"what happens and what users may notice"`
	StartAt         string   `json:"startAt" jsonschema:"window start, RFC3339"`
	FinishAt        string   `json:"finishAt" jsonschema:"window end, RFC3339"`
	MonitorUUIDs    []string `json:"monitorUuids" jsonschema:"monitors and heartbeats (UUIDs from get_status_overview) whose alerts are suppressed during the window"`
	StatusPageUUIDs []string `json:"statusPageUuids,omitempty" jsonschema:"status pages to announce the maintenance on; omit to keep it internal"`
}

type maintenanceOut struct {
	UUID     string   `json:"uuid"`
	Title    string   `json:"title"`
	Status   string   `json:"status"`
	StartAt  string   `json:"startAt"`
	FinishAt string   `json:"finishAt"`
	Monitors []string `json:"monitors"`
}

type affectedService struct {
	UUID          string `json:"uuid"`
	Name          string `json:"name"`
	Functionality string `json:"functionality"`
}

func (t *toolset) scheduleMaintenance(ctx context.Context, req *mcp.CallToolRequest, in maintenanceIn) (*mcp.CallToolResult, maintenanceOut, error) {
	if len(strings.TrimSpace(in.Title)) < 2 || len(strings.TrimSpace(in.Description)) < 2 {
		return nil, maintenanceOut{}, errors.New("title and description are required")
	}
	start, err := time.Parse(time.RFC3339, in.StartAt)
	if err != nil {
		return nil, maintenanceOut{}, errors.New("startAt must be RFC3339, e.g. 2026-10-06T22:00:00Z")
	}
	finish, err := time.Parse(time.RFC3339, in.FinishAt)
	if err != nil {
		return nil, maintenanceOut{}, errors.New("finishAt must be RFC3339, e.g. 2026-10-06T23:00:00Z")
	}
	if !finish.After(start) {
		return nil, maintenanceOut{}, errors.New("finishAt must be after startAt")
	}

	services, err := t.affectedServices(ctx, req, in.MonitorUUIDs)
	if err != nil {
		return nil, maintenanceOut{}, err
	}
	body := map[string]any{
		"title": in.Title, "description": in.Description,
		"startAt": start.UTC().Format(time.RFC3339), "finishAt": finish.UTC().Format(time.RFC3339),
		"affectedServices": services,
	}
	if in.StatusPageUUIDs != nil {
		body["statusPageUuids"] = in.StatusPageUUIDs
	}

	var res struct {
		UUID     string `json:"uuid"`
		Title    string `json:"title"`
		Status   string `json:"status"`
		StartAt  string `json:"startAt"`
		FinishAt string `json:"finishAt"`
	}
	if err := t.call(ctx, req, upstream.Workflows, http.MethodPost, "/v1/api/maintenances/", nil, body, &res); err != nil {
		return nil, maintenanceOut{}, err
	}
	out := maintenanceOut{UUID: res.UUID, Title: res.Title, Status: res.Status, StartAt: res.StartAt, FinishAt: res.FinishAt, Monitors: []string{}}
	for _, s := range services {
		out.Monitors = append(out.Monitors, s.Name)
	}
	return nil, out, nil
}

// affectedServices turns monitor UUIDs into the maintenance's affectedServices.
// The incident engine matches these against an incident's sourceUuid, which
// for website/API checks is the application UUID, not the healthcheck UUID,
// so checks are translated here (as the web app does). Application UUIDs are
// accepted as well, since get_incident reports them as sourceUuid.
func (t *toolset) affectedServices(ctx context.Context, req *mcp.CallToolRequest, uuids []string) ([]affectedService, error) {
	hcs, err := t.listHealthchecks(ctx, req)
	if err != nil {
		return nil, err
	}
	hbs, err := t.listHeartbeats(ctx, req)
	if err != nil {
		return nil, err
	}
	known := map[string]affectedService{}
	for _, h := range hcs {
		s := affectedService{UUID: h.Application.UUID, Name: h.Application.Name}
		if s.UUID == "" {
			s.UUID = h.ApplicationUUID
		}
		known[h.UUID] = s
		known[s.UUID] = s
	}
	for _, h := range hbs {
		known[h.UUID] = affectedService{UUID: h.UUID, Name: h.Name}
	}

	out := make([]affectedService, 0, len(uuids))
	seen := map[string]bool{}
	for _, u := range uuids {
		s, ok := known[u]
		if !ok {
			return nil, errors.New("unknown monitor UUID " + u + "; get_status_overview lists them")
		}
		if !seen[s.UUID] {
			seen[s.UUID] = true
			out = append(out, s)
		}
	}
	return out, nil
}
