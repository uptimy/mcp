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

	"github.com/uptimy/mcp/internal/upstream"
)

// Dependencies are the public services Uptimy measures (www.upti.my/status)
// that a workspace follows. The catalog is public on the API; the follows
// belong to workflows, which also turns a followed service's outage into an
// incident in the workspace.

type publicService struct {
	Slug        string   `json:"slug"`
	Name        string   `json:"name"`
	WorkspaceID int      `json:"workspaceId"`
	Status      string   `json:"status"`
	ProbeCount  int      `json:"probeCount"`
	Uptime30    *float64 `json:"uptime30"`
}

type publicProbe struct {
	Key             string `json:"key"`
	Label           string `json:"label"`
	Meaning         string `json:"meaning"`
	ApplicationUUID string `json:"applicationUuid"`
	Status          string `json:"status"`
	Uptime          struct {
		D1  *float64 `json:"d1"`
		D7  *float64 `json:"d7"`
		D30 *float64 `json:"d30"`
	} `json:"uptime"`
}

func (t *toolset) catalog(ctx context.Context, req *mcp.CallToolRequest) ([]publicService, error) {
	var c struct {
		Services []publicService `json:"services"`
	}
	err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/public/services", nil, nil, &c)
	return c.Services, err
}

func (t *toolset) service(ctx context.Context, req *mcp.CallToolRequest, slug string) (publicService, []publicProbe, error) {
	var d struct {
		publicService
		Probes []publicProbe `json:"probes"`
	}
	err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/public/services/"+url.PathEscape(slug), nil, nil, &d)
	if upstream.IsNotFound(err) {
		return publicService{}, nil, errors.New("no public service " + strconv.Quote(slug) + "; get_dependencies lists them")
	}
	return d.publicService, d.Probes, err
}

// follows returns the followed probe UUIDs, grouped by service workspace.
func (t *toolset) follows(ctx context.Context, req *mcp.CallToolRequest) (map[int]map[string]bool, error) {
	var res upstream.Results[struct {
		SourceWorkspaceID int    `json:"sourceWorkspaceId"`
		SourceUUID        string `json:"sourceUuid"`
	}]
	if err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/source-follows/", nil, nil, &res); err != nil {
		return nil, err
	}
	out := map[int]map[string]bool{}
	for _, f := range res.Results {
		if out[f.SourceWorkspaceID] == nil {
			out[f.SourceWorkspaceID] = map[string]bool{}
		}
		out[f.SourceWorkspaceID][f.SourceUUID] = true
	}
	return out, nil
}

type dependencyRow struct {
	Slug           string   `json:"slug"`
	Name           string   `json:"name"`
	Status         string   `json:"status"`
	Uptime30       *float64 `json:"uptime30,omitempty"`
	Probes         int      `json:"probes"`
	FollowedProbes int      `json:"followedProbes"`
}

type dependencyProbe struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Meaning  string   `json:"meaning"`
	Status   string   `json:"status"`
	Uptime1  *float64 `json:"uptime24h,omitempty"`
	Uptime7  *float64 `json:"uptime7d,omitempty"`
	Uptime30 *float64 `json:"uptime30d,omitempty"`
	Followed bool     `json:"followed"`
}

type dependenciesIn struct {
	Slug  string `json:"slug,omitempty" jsonschema:"a service's slug (e.g. github) for its probes; omit to list every service you can follow"`
	Query string `json:"query,omitempty" jsonschema:"filter the list by name or slug, e.g. stripe"`
}

type dependenciesOut struct {
	Services []dependencyRow `json:"services,omitempty"`
	// Set when slug is given.
	Service *dependencyRow    `json:"service,omitempty"`
	Probes  []dependencyProbe `json:"probes,omitempty"`
}

func (t *toolset) getDependencies(ctx context.Context, req *mcp.CallToolRequest, in dependenciesIn) (*mcp.CallToolResult, dependenciesOut, error) {
	follows, err := t.follows(ctx, req)
	if err != nil {
		return nil, dependenciesOut{}, err
	}
	if in.Slug != "" {
		s, probes, err := t.service(ctx, req, in.Slug)
		if err != nil {
			return nil, dependenciesOut{}, err
		}
		followed := follows[s.WorkspaceID]
		row := dependencyRow{Slug: s.Slug, Name: s.Name, Status: s.Status, Uptime30: s.Uptime30, Probes: len(probes), FollowedProbes: len(followed)}
		out := dependenciesOut{Service: &row, Probes: make([]dependencyProbe, 0, len(probes))}
		for _, p := range probes {
			out.Probes = append(out.Probes, dependencyProbe{
				Key: p.Key, Label: p.Label, Meaning: p.Meaning, Status: p.Status,
				Uptime1: p.Uptime.D1, Uptime7: p.Uptime.D7, Uptime30: p.Uptime.D30, Followed: followed[p.ApplicationUUID],
			})
		}
		return nil, out, nil
	}

	services, err := t.catalog(ctx, req)
	if err != nil {
		return nil, dependenciesOut{}, err
	}
	q := strings.ToLower(strings.TrimSpace(in.Query))
	out := dependenciesOut{Services: []dependencyRow{}}
	for _, s := range services {
		if q != "" && !strings.Contains(strings.ToLower(s.Name), q) && !strings.Contains(s.Slug, q) {
			continue
		}
		out.Services = append(out.Services, dependencyRow{Slug: s.Slug, Name: s.Name, Status: s.Status, Uptime30: s.Uptime30, Probes: s.ProbeCount, FollowedProbes: len(follows[s.WorkspaceID])})
	}
	// Followed services first, then the rest by name.
	slices.SortStableFunc(out.Services, func(a, b dependencyRow) int {
		if (a.FollowedProbes > 0) != (b.FollowedProbes > 0) {
			if a.FollowedProbes > 0 {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
	return nil, out, nil
}

type followIn struct {
	Slug   string   `json:"slug" jsonschema:"the service's slug, e.g. github (from get_dependencies)"`
	Follow bool     `json:"follow" jsonschema:"true to follow, false to stop following"`
	Probes []string `json:"probes,omitempty" jsonschema:"probe keys to follow (from get_dependencies with slug); omit to follow every probe. Replaces what was followed before for this service."`
}

func (t *toolset) followDependency(ctx context.Context, req *mcp.CallToolRequest, in followIn) (*mcp.CallToolResult, actionOut, error) {
	if strings.TrimSpace(in.Slug) == "" {
		return nil, actionOut{}, errors.New("slug is required")
	}
	s, probes, err := t.service(ctx, req, in.Slug)
	if err != nil {
		return nil, actionOut{}, err
	}

	if !in.Follow {
		if err := t.call(ctx, req, upstream.Workflows, http.MethodDelete, "/v1/api/source-follows/"+strconv.Itoa(s.WorkspaceID), nil, nil, nil); err != nil {
			return nil, actionOut{}, err
		}
		return nil, actionOut{OK: true, Message: "Stopped following " + s.Name + ". Its open incident in this workspace, if any, is resolved."}, nil
	}

	byKey := map[string]string{}
	keys := make([]string, 0, len(probes))
	for _, p := range probes {
		byKey[p.Key] = p.ApplicationUUID
		keys = append(keys, p.Key)
	}
	uuids := []string{}
	if len(in.Probes) == 0 {
		for _, p := range probes {
			uuids = append(uuids, p.ApplicationUUID)
		}
	} else {
		for _, k := range in.Probes {
			uuid, ok := byKey[strings.TrimSpace(k)]
			if !ok {
				return nil, actionOut{}, errors.New(s.Name + " has no probe " + strconv.Quote(k) + "; its probes are " + strings.Join(keys, ", "))
			}
			uuids = append(uuids, uuid)
		}
	}
	body := map[string]any{"sourceWorkspaceId": s.WorkspaceID, "sourceUuids": uuids}
	if err := t.call(ctx, req, upstream.Workflows, http.MethodPut, "/v1/api/source-follows/", nil, body, nil); err != nil {
		return nil, actionOut{}, err
	}
	what := "all " + strconv.Itoa(len(probes)) + " probes"
	if len(uuids) < len(probes) {
		what = strconv.Itoa(len(uuids)) + " of " + strconv.Itoa(len(probes)) + " probes"
	}
	return nil, actionOut{OK: true, Message: "Following " + s.Name + " (" + what + "). Its outages now open incidents in this workspace."}, nil
}
