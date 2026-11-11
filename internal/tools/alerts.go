package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/mcp/internal/upstream"
)

// Alerting: who hears about incidents. Workflows owns it; with a key, agents
// see every workflow but only add template alerts (upti.my-workflows
// utils/alert_template.ts). Connecting Slack and the like stays in the app.

const integrationsURL = "https://app.upti.my/workspaces/settings/?tab=integrations"

var alertProviders = map[string]string{"slack": "Slack", "discord": "Discord", "teams": "Teams", "telegram": "Telegram", "pagerduty": "PagerDuty"}

var eventNames = map[string]string{"incident.created": "opened", "incident.resolved": "resolved", "incident.updated": "updated"}

type integration struct {
	UUID     string         `json:"uuid"`
	Provider string         `json:"provider"`
	Name     string         `json:"name"`
	Config   map[string]any `json:"config"`
}

// label names an integration the way people know it: "Slack #ops (Acme)".
func (i integration) label() string {
	p := alertProviders[i.Provider]
	if p == "" {
		p = i.Provider
	}
	if ch, _ := i.Config["channelName"].(string); ch != "" {
		return fmt.Sprintf("%s #%s (%s)", p, strings.TrimPrefix(ch, "#"), i.Name)
	}
	return fmt.Sprintf("%s (%s)", p, i.Name)
}

type workflow struct {
	UUID         string   `json:"uuid"`
	Name         string   `json:"name"`
	Enabled      bool     `json:"enabled"`
	TriggerTypes []string `json:"triggerTypes"`
	Destinations []struct {
		Type            string `json:"type"`
		IntegrationUUID string `json:"integrationUuid"`
	} `json:"destinations"`
	Alert *struct {
		Destination struct {
			Type            string   `json:"type"`
			IntegrationUUID string   `json:"integrationUuid"`
			Recipients      []string `json:"recipients"`
			URL             string   `json:"url"`
		} `json:"destination"`
		TagIDs      []int    `json:"tagIds"`
		Severities  []string `json:"severities"`
		SourceUUIDs []string `json:"sourceUuids"`
	} `json:"alert"`
}

type alertOut struct {
	UUID    string   `json:"uuid"`
	Name    string   `json:"name"`
	Paused  bool     `json:"paused"`
	Events  []string `json:"events"`
	SendsTo []string `json:"sendsTo"`
	// Scope: an incident must match one of each that's set; none = every incident.
	Tags       []string `json:"tags,omitempty"`
	Severities []string `json:"severities,omitempty"`
	Monitors   []string `json:"monitors,omitempty"`
	// Made or edited in the app's workflow editor: change it there.
	MadeInEditor bool `json:"madeInEditor"`
}

// alertNames resolves the ids in alerts to names, loaded once per call.
type alertNames struct {
	integrations map[string]integration
	tags         map[int]string
	monitors     map[string]string
}

func (t *toolset) loadAlertNames(ctx context.Context, req *mcp.CallToolRequest) (alertNames, []integration, error) {
	n := alertNames{integrations: map[string]integration{}, tags: map[int]string{}, monitors: map[string]string{}}
	var ints upstream.Results[integration]
	if err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/integrations/", nil, nil, &ints); err != nil {
		return n, nil, err
	}
	for _, i := range ints.Results {
		n.integrations[i.UUID] = i
	}
	tags, err := t.listTags(ctx, req)
	if err != nil {
		return n, nil, err
	}
	for _, tag := range tags {
		n.tags[tag.ID] = tag.Name
	}
	hcs, err := t.listHealthchecks(ctx, req)
	if err != nil {
		return n, nil, err
	}
	for _, h := range hcs {
		n.monitors[h.Application.UUID] = h.Application.Name
		n.monitors[h.ApplicationUUID] = h.Application.Name
	}
	hbs, err := t.listHeartbeats(ctx, req)
	if err != nil {
		return n, nil, err
	}
	for _, h := range hbs {
		n.monitors[h.UUID] = h.Name
	}
	return n, ints.Results, nil
}

func (n alertNames) describe(w workflow) alertOut {
	out := alertOut{UUID: w.UUID, Name: w.Name, Paused: !w.Enabled, Events: []string{}, SendsTo: []string{}, MadeInEditor: w.Alert == nil}
	for _, e := range w.TriggerTypes {
		if name, ok := eventNames[e]; ok {
			out.Events = append(out.Events, name)
		}
	}
	if a := w.Alert; a != nil {
		switch a.Destination.Type {
		case "email":
			out.SendsTo = append(out.SendsTo, "email "+strings.Join(a.Destination.Recipients, ", "))
		case "webhook":
			out.SendsTo = append(out.SendsTo, "webhook "+a.Destination.URL)
		default:
			out.SendsTo = append(out.SendsTo, n.integrationLabel(a.Destination.IntegrationUUID, ""))
		}
		for _, id := range a.TagIDs {
			out.Tags = append(out.Tags, n.tags[id])
		}
		out.Severities = a.Severities
		for _, u := range a.SourceUUIDs {
			name := n.monitors[u]
			if name == "" {
				name = u + " (deleted)"
			}
			out.Monitors = append(out.Monitors, name)
		}
		return out
	}
	for _, d := range w.Destinations {
		out.SendsTo = append(out.SendsTo, n.integrationLabel(d.IntegrationUUID, d.Type))
	}
	return out
}

func (n alertNames) integrationLabel(uuid, nodeType string) string {
	if i, ok := n.integrations[uuid]; ok {
		return i.label()
	}
	if nodeType != "" {
		return nodeType
	}
	return "an integration that was removed"
}

// --- list_alerts ---

type integrationOut struct {
	UUID  string `json:"uuid"`
	Label string `json:"label"`
	// Whether add_alert can post to it (Slack, Discord, Teams, Telegram, PagerDuty).
	ForAlerts bool `json:"forAlerts"`
}

type listAlertsOut struct {
	Alerts       []alertOut       `json:"alerts"`
	Integrations []integrationOut `json:"integrations"`
	ConnectMore  string           `json:"connectMore"`
}

func (t *toolset) listAlerts(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, listAlertsOut, error) {
	var wfs upstream.Results[workflow]
	if err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/workflows/", nil, nil, &wfs); err != nil {
		return nil, listAlertsOut{}, err
	}
	names, ints, err := t.loadAlertNames(ctx, req)
	if err != nil {
		return nil, listAlertsOut{}, err
	}
	out := listAlertsOut{Alerts: []alertOut{}, Integrations: []integrationOut{}, ConnectMore: integrationsURL}
	for _, w := range wfs.Results {
		out.Alerts = append(out.Alerts, names.describe(w))
	}
	for _, i := range ints {
		_, ok := alertProviders[i.Provider]
		out.Integrations = append(out.Integrations, integrationOut{UUID: i.UUID, Label: i.label(), ForAlerts: ok})
	}
	return nil, out, nil
}

// --- add_alert ---

type addAlertIn struct {
	Name         string   `json:"name" jsonschema:"e.g. 'Production to #ops'"`
	Integration  string   `json:"integration,omitempty" jsonschema:"send to a connected Slack, Discord, Teams, Telegram or PagerDuty integration: its UUID or label from list_alerts, or just 'slack' when there's one"`
	Emails       []string `json:"emails,omitempty" jsonschema:"or send emails to these addresses"`
	WebhookURL   string   `json:"webhookUrl,omitempty" jsonschema:"or POST each incident as JSON to this https URL (not a Slack, Discord or Teams webhook: connect those in the app as integrations)"`
	Events       []string `json:"events,omitempty" jsonschema:"opened and/or resolved (default both)"`
	Tags         []string `json:"tags,omitempty" jsonschema:"only incidents of monitors with one of these existing tags"`
	Severities   []string `json:"severities,omitempty" jsonschema:"only these severities: critical, high, medium, low"`
	MonitorUUIDs []string `json:"monitorUuids,omitempty" jsonschema:"only incidents of these monitors or cron monitors"`
}

type addAlertOut struct {
	alertOut
	// True when an alert with this name already existed; nothing was added.
	AlreadyExisted bool   `json:"alreadyExisted"`
	Note           string `json:"note"`
}

func (t *toolset) addAlert(ctx context.Context, req *mcp.CallToolRequest, in addAlertIn) (*mcp.CallToolResult, addAlertOut, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, addAlertOut{}, errors.New("name is required")
	}
	given := 0
	for _, set := range []bool{in.Integration != "", len(in.Emails) > 0, in.WebhookURL != ""} {
		if set {
			given++
		}
	}
	if given != 1 {
		return nil, addAlertOut{}, errors.New("give exactly one destination: integration, emails or webhookUrl")
	}

	var wfs upstream.Results[workflow]
	if err := t.call(ctx, req, upstream.Workflows, http.MethodGet, "/v1/api/workflows/", nil, nil, &wfs); err != nil {
		return nil, addAlertOut{}, err
	}
	names, ints, err := t.loadAlertNames(ctx, req)
	if err != nil {
		return nil, addAlertOut{}, err
	}
	for _, w := range wfs.Results {
		if strings.EqualFold(w.Name, name) {
			return nil, addAlertOut{alertOut: names.describe(w), AlreadyExisted: true, Note: "An alert with this name already exists; nothing was added."}, nil
		}
	}

	var destination map[string]any
	switch {
	case in.Integration != "":
		i, err := pickIntegration(ints, in.Integration)
		if err != nil {
			return nil, addAlertOut{}, err
		}
		destination = map[string]any{"type": "integration", "integrationUuid": i.UUID}
	case len(in.Emails) > 0:
		destination = map[string]any{"type": "email", "recipients": in.Emails}
	default:
		destination = map[string]any{"type": "webhook", "url": in.WebhookURL}
	}

	body := map[string]any{"name": name, "destination": destination}
	if len(in.Events) > 0 {
		events := make([]string, 0, len(in.Events))
		for _, e := range in.Events {
			e = strings.ToLower(strings.TrimSpace(e))
			if e != "opened" && e != "resolved" {
				return nil, addAlertOut{}, fmt.Errorf("unknown event %q: use opened or resolved", e)
			}
			events = append(events, e)
		}
		body["events"] = events
	}
	if len(in.Tags) > 0 {
		ids, err := tagIDs(names.tags, in.Tags)
		if err != nil {
			return nil, addAlertOut{}, err
		}
		body["tagIds"] = ids
	}
	if len(in.Severities) > 0 {
		sev := make([]string, 0, len(in.Severities))
		for _, s := range in.Severities {
			s = strings.ToLower(strings.TrimSpace(s))
			switch s {
			case "critical", "high", "medium", "low":
				sev = append(sev, s)
			default:
				return nil, addAlertOut{}, fmt.Errorf("unknown severity %q: use critical, high, medium or low", s)
			}
		}
		body["severities"] = sev
	}
	if len(in.MonitorUUIDs) > 0 {
		// Incidents name their source by application UUID (website/API
		// checks) or heartbeat UUID, as maintenance windows do.
		services, err := t.affectedServices(ctx, req, in.MonitorUUIDs)
		if err != nil {
			return nil, addAlertOut{}, err
		}
		sources := make([]string, 0, len(services))
		for _, s := range services {
			sources = append(sources, s.UUID)
		}
		body["sourceUuids"] = sources
	}

	var created workflow
	if err := t.call(ctx, req, upstream.Workflows, http.MethodPost, "/v1/api/alerts/", nil, body, &created); err != nil {
		return nil, addAlertOut{}, err
	}
	return nil, addAlertOut{alertOut: names.describe(created),
		Note: "Live now: the next matching incident is sent. The workflow editor's Test button sends a real message."}, nil
}

// pickIntegration finds the integration an alert should post to.
func pickIntegration(ints []integration, want string) (integration, error) {
	want = strings.TrimSpace(want)
	var byProvider []integration
	for _, i := range ints {
		if i.UUID == want || strings.EqualFold(i.label(), want) || strings.EqualFold(i.Name, want) {
			if _, ok := alertProviders[i.Provider]; !ok {
				return i, fmt.Errorf("%s can't receive alerts; use a Slack, Discord, Teams, Telegram or PagerDuty integration", i.label())
			}
			return i, nil
		}
		if strings.EqualFold(i.Provider, want) || strings.EqualFold(alertProviders[i.Provider], want) {
			byProvider = append(byProvider, i)
		}
	}
	switch len(byProvider) {
	case 1:
		return byProvider[0], nil
	case 0:
		connected := []string{}
		for _, i := range ints {
			if _, ok := alertProviders[i.Provider]; ok {
				connected = append(connected, i.label())
			}
		}
		msg := fmt.Sprintf("no integration matches %q", want)
		if len(connected) > 0 {
			msg += "; connected: " + strings.Join(connected, ", ")
		}
		return integration{}, errors.New(msg + ". Connect it in the Uptimy app (" + integrationsURL + "), then try again.")
	default:
		labels := make([]string, 0, len(byProvider))
		for _, i := range byProvider {
			labels = append(labels, i.label())
		}
		return integration{}, fmt.Errorf("more than one %s integration is connected; pick one: %s", want, strings.Join(labels, ", "))
	}
}

func tagIDs(tags map[int]string, names []string) ([]int, error) {
	byName := map[string]int{}
	known := make([]string, 0, len(tags))
	for id, name := range tags {
		byName[strings.ToLower(name)] = id
		known = append(known, name)
	}
	ids := []int{}
	for _, n := range names {
		id, ok := byName[strings.ToLower(strings.TrimSpace(n))]
		if !ok {
			return nil, fmt.Errorf("no tag named %q (tags: %s); tag monitors with add_monitors first", n, strings.Join(known, ", "))
		}
		ids = append(ids, id)
	}
	return ids, nil
}

type tag struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func (t *toolset) listTags(ctx context.Context, req *mcp.CallToolRequest) ([]tag, error) {
	var res upstream.Results[tag]
	err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/tags/", nil, nil, &res)
	return res.Results, err
}

// --- set_alert_paused ---

type alertPausedIn struct {
	UUID   string `json:"uuid" jsonschema:"alert (workflow) UUID from list_alerts"`
	Paused bool   `json:"paused" jsonschema:"true to stop sending, false to send again"`
}

func (t *toolset) setAlertPaused(ctx context.Context, req *mcp.CallToolRequest, in alertPausedIn) (*mcp.CallToolResult, actionOut, error) {
	if in.UUID == "" {
		return nil, actionOut{}, errors.New("uuid is required")
	}
	var w workflow
	err := t.call(ctx, req, upstream.Workflows, http.MethodPatch, "/v1/api/workflows/"+url.PathEscape(in.UUID), nil, map[string]any{"enabled": !in.Paused}, &w)
	if upstream.IsNotFound(err) {
		return nil, actionOut{}, errors.New("no alert with UUID " + in.UUID + "; list_alerts lists them")
	}
	if err != nil {
		return nil, actionOut{}, err
	}
	if !w.Enabled {
		return nil, actionOut{OK: true, Message: w.Name + " is paused: it sends nothing until it's resumed."}, nil
	}
	return nil, actionOut{OK: true, Message: w.Name + " is sending again."}, nil
}
