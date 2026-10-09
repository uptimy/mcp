package tools

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

var (
	severities       = []string{"critical", "high", "medium", "low"}
	incidentStatuses = []string{"Created", "Acknowledged", "Investigating", "Identified", "Monitoring", "Resolved"}
)

// incident is the workflows service's incident payload.
type incident struct {
	UUID         string  `json:"uuid"`
	Title        string  `json:"title"`
	Description  *string `json:"description"`
	Severity     string  `json:"severity"`
	Status       string  `json:"status"`
	Manual       bool    `json:"manual"`
	ErrorMessage *string `json:"errorMessage"`
	Source       *string `json:"source"`
	SourceUUID   *string `json:"sourceUuid"`
	StartedAt    string  `json:"startedAt"`
	ResolvedAt   *string `json:"resolvedAt"`
	Updates      []struct {
		Message   string `json:"message"`
		Public    bool   `json:"public"`
		CreatedAt string `json:"createdAt"`
		User      *struct {
			FirstName string `json:"firstName"`
			LastName  string `json:"lastName"`
		} `json:"user"`
	} `json:"incidentUpdates"`
	StatusHistory []struct {
		FromStatus string `json:"fromStatus"`
		ToStatus   string `json:"toStatus"`
		ChangedAt  string `json:"changedAt"`
	} `json:"statusHistory"`
	StatusPages []struct {
		StatusPageUUID string `json:"statusPageUuid"`
	} `json:"incidentStatusPages"`
}

type incidentRow struct {
	UUID       string  `json:"uuid"`
	Title      string  `json:"title"`
	Severity   string  `json:"severity"`
	Status     string  `json:"status"`
	Manual     bool    `json:"manual"`
	Source     *string `json:"source,omitempty"`
	StartedAt  string  `json:"startedAt"`
	ResolvedAt *string `json:"resolvedAt,omitempty"`
	OnPages    int     `json:"onStatusPages"`
}

func (i incident) row() incidentRow {
	return incidentRow{
		UUID: i.UUID, Title: i.Title, Severity: i.Severity, Status: i.Status, Manual: i.Manual,
		Source: i.Source, StartedAt: i.StartedAt, ResolvedAt: i.ResolvedAt, OnPages: len(i.StatusPages),
	}
}

type listIncidentsIn struct {
	Status   string `json:"status,omitempty" jsonschema:"open, resolved or all (default all)"`
	Severity string `json:"severity,omitempty" jsonschema:"critical, high, medium or low"`
	Limit    int    `json:"limit,omitempty" jsonschema:"max incidents to return, 1-100 (default 20)"`
}

type listIncidentsOut struct {
	Total     int           `json:"total"`
	Incidents []incidentRow `json:"incidents"`
}

func (t *toolset) listIncidents(ctx context.Context, req *mcp.CallToolRequest, in listIncidentsIn) (*mcp.CallToolResult, listIncidentsOut, error) {
	q := url.Values{"sortBy": {"createdAt"}, "sortDirection": {"DESC"}}
	switch strings.ToLower(in.Status) {
	case "", "all":
	case "open":
		q.Set("status", "Ongoing")
	case "resolved":
		q.Set("status", "Resolved")
	default:
		return nil, listIncidentsOut{}, errors.New("status must be open, resolved or all")
	}
	if in.Severity != "" {
		if !slices.Contains(severities, strings.ToLower(in.Severity)) {
			return nil, listIncidentsOut{}, errors.New("severity must be critical, high, medium or low")
		}
		q.Set("severity", strings.ToLower(in.Severity))
	}
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	q.Set("perPage", strconv.Itoa(min(limit, 100)))

	var res struct {
		Results   []incident `json:"results"`
		Paginator struct {
			Total int `json:"total"`
		} `json:"paginator"`
	}
	if err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/incidents/", q, nil, &res); err != nil {
		return nil, listIncidentsOut{}, err
	}
	out := listIncidentsOut{Total: res.Paginator.Total, Incidents: make([]incidentRow, 0, len(res.Results))}
	for _, i := range res.Results {
		out.Incidents = append(out.Incidents, i.row())
	}
	return nil, out, nil
}

type incidentIn struct {
	UUID string `json:"uuid" jsonschema:"incident UUID"`
}

type timelineEntry struct {
	At      string `json:"at"`
	Kind    string `json:"kind"` // "update" or "status"
	Message string `json:"message,omitempty"`
	Public  *bool  `json:"public,omitempty"`
	By      string `json:"by,omitempty"`
	From    string `json:"from,omitempty"`
	To      string `json:"to,omitempty"`
}

type incidentOut struct {
	incidentRow
	Description     *string         `json:"description,omitempty"`
	ErrorMessage    *string         `json:"errorMessage,omitempty"`
	SourceUUID      *string         `json:"sourceUuid,omitempty"`
	StatusPageUUIDs []string        `json:"statusPageUuids"`
	Timeline        []timelineEntry `json:"timeline"`
}

func (i incident) detail() incidentOut {
	out := incidentOut{
		incidentRow: i.row(), Description: i.Description, ErrorMessage: i.ErrorMessage, SourceUUID: i.SourceUUID,
		StatusPageUUIDs: make([]string, 0, len(i.StatusPages)), Timeline: []timelineEntry{},
	}
	for _, p := range i.StatusPages {
		out.StatusPageUUIDs = append(out.StatusPageUUIDs, p.StatusPageUUID)
	}
	for _, u := range i.Updates {
		e := timelineEntry{At: u.CreatedAt, Kind: "update", Message: u.Message, Public: &u.Public}
		if u.User != nil {
			e.By = strings.TrimSpace(u.User.FirstName + " " + u.User.LastName)
		}
		out.Timeline = append(out.Timeline, e)
	}
	for _, h := range i.StatusHistory {
		out.Timeline = append(out.Timeline, timelineEntry{At: h.ChangedAt, Kind: "status", From: h.FromStatus, To: h.ToStatus})
	}
	slices.SortStableFunc(out.Timeline, func(a, b timelineEntry) int { return strings.Compare(a.At, b.At) })
	return out
}

func (t *toolset) fetchIncident(ctx context.Context, req *mcp.CallToolRequest, uuid string) (incident, error) {
	if uuid == "" {
		return incident{}, errors.New("uuid is required")
	}
	var inc incident
	err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/incidents/"+url.PathEscape(uuid), nil, nil, &inc)
	return inc, err
}

func (t *toolset) getIncident(ctx context.Context, req *mcp.CallToolRequest, in incidentIn) (*mcp.CallToolResult, incidentOut, error) {
	inc, err := t.fetchIncident(ctx, req, in.UUID)
	if err != nil {
		return nil, incidentOut{}, err
	}
	return nil, inc.detail(), nil
}

type createIncidentIn struct {
	Title           string   `json:"title" jsonschema:"short public title, e.g. 'Checkout API errors'"`
	Severity        string   `json:"severity" jsonschema:"critical, high, medium or low"`
	Description     string   `json:"description,omitempty" jsonschema:"what is affected"`
	StatusPageUUIDs []string `json:"statusPageUuids,omitempty" jsonschema:"status pages to show the incident on; omit to keep it internal"`
}

func (t *toolset) createIncident(ctx context.Context, req *mcp.CallToolRequest, in createIncidentIn) (*mcp.CallToolResult, incidentOut, error) {
	if strings.TrimSpace(in.Title) == "" {
		return nil, incidentOut{}, errors.New("title is required")
	}
	if !slices.Contains(severities, strings.ToLower(in.Severity)) {
		return nil, incidentOut{}, errors.New("severity must be critical, high, medium or low")
	}
	body := map[string]any{"title": in.Title, "severity": strings.ToLower(in.Severity)}
	if in.Description != "" {
		body["description"] = in.Description
	}
	if in.StatusPageUUIDs != nil {
		body["statusPageUuids"] = in.StatusPageUUIDs
	}
	var inc incident
	if err := t.call(ctx, req, upstream.Workflows, http.MethodPost, "/v1/api/incidents/", nil, body, &inc); err != nil {
		return nil, incidentOut{}, err
	}
	return nil, inc.detail(), nil
}

type incidentUpdateIn struct {
	UUID            string   `json:"uuid" jsonschema:"incident UUID"`
	Message         string   `json:"message,omitempty" jsonschema:"update text; required unless only changing status or status pages"`
	Public          bool     `json:"public" jsonschema:"true shows the message on the status pages the incident is on; false keeps it internal"`
	Status          string   `json:"status,omitempty" jsonschema:"new status: Acknowledged, Investigating, Identified, Monitoring or Resolved"`
	StatusPageUUIDs []string `json:"statusPageUuids,omitempty" jsonschema:"status pages to also show the incident on, added to the ones it is already on (get_status_overview lists them). Incidents a monitor opened automatically are on no status page until you add one."`
}

func (t *toolset) postIncidentUpdate(ctx context.Context, req *mcp.CallToolRequest, in incidentUpdateIn) (*mcp.CallToolResult, incidentOut, error) {
	if strings.TrimSpace(in.Message) == "" && in.Status == "" && len(in.StatusPageUUIDs) == 0 {
		return nil, incidentOut{}, errors.New("pass a message, a status, status pages, or a combination")
	}
	if in.Status != "" && !slices.Contains(incidentStatuses, in.Status) {
		return nil, incidentOut{}, errors.New("status must be one of " + strings.Join(incidentStatuses, ", "))
	}
	if in.UUID == "" {
		return nil, incidentOut{}, errors.New("uuid is required")
	}
	path := "/v1/api/incidents/" + url.PathEscape(in.UUID)

	// Pages first, so a public message posted below shows on them at once.
	if in.Status != "" || len(in.StatusPageUUIDs) > 0 {
		// The update replaces title/description/severity/status (and the status
		// page set, when sent) together, so it is built from the current
		// incident, as uptimyctl does.
		inc, err := t.fetchIncident(ctx, req, in.UUID)
		if err != nil {
			return nil, incidentOut{}, err
		}
		status := inc.Status
		if in.Status != "" {
			status = in.Status
		}
		body := map[string]any{"title": inc.Title, "severity": inc.Severity, "status": status, "description": inc.Description}
		if len(in.StatusPageUUIDs) > 0 {
			// Workflows replaces the whole set; add to the pages already linked.
			pages := []string{}
			for _, p := range inc.StatusPages {
				pages = append(pages, p.StatusPageUUID)
			}
			for _, p := range in.StatusPageUUIDs {
				if !slices.Contains(pages, p) {
					pages = append(pages, p)
				}
			}
			body["statusPageUuids"] = pages
		}
		if err := t.call(ctx, req, upstream.Workflows, http.MethodPut, path, nil, body, nil); err != nil {
			return nil, incidentOut{}, err
		}
	}
	if strings.TrimSpace(in.Message) != "" {
		if err := t.call(ctx, req, upstream.Workflows, http.MethodPost, path+"/updates/", nil, map[string]any{"message": in.Message, "public": in.Public}, nil); err != nil {
			return nil, incidentOut{}, err
		}
	}
	inc, err := t.fetchIncident(ctx, req, in.UUID)
	if err != nil {
		return nil, incidentOut{}, err
	}
	return nil, inc.detail(), nil
}
