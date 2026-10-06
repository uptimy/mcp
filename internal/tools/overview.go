package tools

import (
	"context"
	"net/http"
	"net/url"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

type overviewIn struct{}

type overviewMonitor struct {
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Kind            string   `json:"kind"` // "check" or "heartbeat"
	Type            string   `json:"type,omitempty"`
	Status          string   `json:"status"`
	Since           string   `json:"since,omitempty"`
	IntervalSeconds int      `json:"intervalSeconds"`
	Regions         []string `json:"regions,omitempty"`
	LastCheckIn     string   `json:"lastCheckIn,omitempty"`
}

type overviewIncident struct {
	UUID      string `json:"uuid"`
	Title     string `json:"title"`
	Severity  string `json:"severity"`
	Status    string `json:"status"`
	StartedAt string `json:"startedAt"`
	Manual    bool   `json:"manual"`
}

type overviewStatusPage struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type overviewOut struct {
	Workspace     string               `json:"workspace,omitempty"`
	Summary       map[string]int       `json:"summary"`
	Monitors      []overviewMonitor    `json:"monitors"`
	OpenIncidents []overviewIncident   `json:"openIncidents"`
	StatusPages   []overviewStatusPage `json:"statusPages"`
	Warnings      []string             `json:"warnings,omitempty"`
}

// statusRank orders monitors so problems come first.
var statusRank = map[string]int{"Down": 0, "Degraded": 1, "AtRisk": 1, "Unknown": 2, "Paused": 3, "Healthy": 4}

func (t *toolset) statusOverview(ctx context.Context, req *mcp.CallToolRequest, _ overviewIn) (*mcp.CallToolResult, overviewOut, error) {
	var (
		wg                             sync.WaitGroup
		hcs                            []healthcheck
		hbs                            []heartbeat
		incidents                      upstream.Results[incident]
		pages                          upstream.Results[statusPage]
		errHC, errHB, errInc, errPages error
	)
	wg.Add(4)
	go func() { defer wg.Done(); hcs, errHC = t.listHealthchecks(ctx, req) }()
	go func() { defer wg.Done(); hbs, errHB = t.listHeartbeats(ctx, req) }()
	go func() {
		defer wg.Done()
		q := url.Values{"status": {"Ongoing"}, "perPage": {"50"}, "sortBy": {"createdAt"}, "sortDirection": {"DESC"}}
		errInc = t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/incidents/", q, nil, &incidents)
	}()
	go func() {
		defer wg.Done()
		errPages = t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/status-pages/", nil, nil, &pages)
	}()
	wg.Wait()

	// Monitors are the point of this tool; the other sources degrade to a
	// warning so one slow service doesn't hide what is down.
	if errHC != nil {
		return nil, overviewOut{}, errHC
	}
	return nil, buildOverview(workspace(req), hcs, hbs, incidents.Results, pages.Results, errHB, errInc, errPages), nil
}

func buildOverview(ws string, hcs []healthcheck, hbs []heartbeat, incidents []incident, pages []statusPage, errHB, errInc, errPages error) overviewOut {
	out := overviewOut{
		Workspace:     ws,
		Summary:       map[string]int{},
		Monitors:      make([]overviewMonitor, 0, len(hcs)+len(hbs)),
		OpenIncidents: make([]overviewIncident, 0, len(incidents)),
		StatusPages:   make([]overviewStatusPage, 0, len(pages)),
	}
	for _, h := range hcs {
		m := overviewMonitor{
			UUID: h.UUID, Name: h.Application.Name, Kind: "check", Type: h.Type.Name,
			Status: h.status(), IntervalSeconds: h.IntervalSeconds, Regions: h.regions(),
		}
		if m.Status != "Healthy" && m.Status != "Paused" {
			m.Since = h.Application.LastStatusUpdate
		}
		out.Monitors = append(out.Monitors, m)
	}
	for _, h := range hbs {
		m := overviewMonitor{UUID: h.UUID, Name: h.Name, Kind: "heartbeat", Status: h.status(), IntervalSeconds: h.IntervalSeconds}
		if s := h.StatusSnapshot; s != nil {
			if s.LastPingAt != nil {
				m.LastCheckIn = *s.LastPingAt
			}
			if m.Status != "Healthy" && m.Status != "Paused" && s.LastTransitionAt != nil {
				m.Since = *s.LastTransitionAt
			}
		}
		out.Monitors = append(out.Monitors, m)
	}
	sort.SliceStable(out.Monitors, func(i, j int) bool {
		ri, ok := statusRank[out.Monitors[i].Status]
		if !ok {
			ri = 2
		}
		rj, ok := statusRank[out.Monitors[j].Status]
		if !ok {
			rj = 2
		}
		return ri < rj
	})
	for _, m := range out.Monitors {
		out.Summary[m.Status]++
	}
	out.Summary["total"] = len(out.Monitors)

	for _, inc := range incidents {
		out.OpenIncidents = append(out.OpenIncidents, overviewIncident{
			UUID: inc.UUID, Title: inc.Title, Severity: inc.Severity, Status: inc.Status, StartedAt: inc.StartedAt, Manual: inc.Manual,
		})
	}
	out.Summary["openIncidents"] = len(out.OpenIncidents)

	for _, p := range pages {
		out.StatusPages = append(out.StatusPages, overviewStatusPage{UUID: p.UUID, Name: p.Name, URL: p.url()})
	}

	if errHB != nil {
		out.Warnings = append(out.Warnings, "heartbeats unavailable: "+errHB.Error())
	}
	if errInc != nil {
		out.Warnings = append(out.Warnings, "incidents unavailable: "+errInc.Error())
	}
	if errPages != nil {
		out.Warnings = append(out.Warnings, "status pages unavailable: "+errPages.Error())
	}
	return out
}

type statusPage struct {
	UUID         string  `json:"uuid"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	CustomDomain *string `json:"customDomain"`
}

func (p statusPage) url() string {
	if p.CustomDomain != nil && *p.CustomDomain != "" {
		return "https://" + *p.CustomDomain
	}
	return "https://" + p.Slug + ".upti.my"
}
