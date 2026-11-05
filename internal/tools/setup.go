package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/uptimy/upti.my-mcp/internal/upstream"
)

// Setting a workspace up from a chat: find what to monitor, add it, add cron
// jobs, show them on a status page. These tools only add things, with the
// same defaults as the app's Setup (CONFIGURE_WITH_AGENTS_PLAN.md).

// Healthcheck type ids (upti.my-api healthcheck_type).
const (
	typeHTTP = 1
	typePing = 4
	typeTCP  = 5
	typeSSL  = 6
	typeDNS  = 7
)

// The bulk route takes at most this many monitors per request.
const maxMonitorsPerCall = 20

var monitorTypes = map[string]int{"http": typeHTTP, "ssl": typeSSL, "tcp": typeTCP, "ping": typePing, "dns": typeDNS}

type monitorSpec struct {
	Type               string   `json:"type,omitempty" jsonschema:"http (default), ssl (certificate expiry), tcp (port open), ping or dns"`
	Target             string   `json:"target" jsonschema:"http: a URL; ssl, tcp and ping: a host; dns: a domain"`
	Name               string   `json:"name,omitempty" jsonschema:"defaults to the target"`
	Port               int      `json:"port,omitempty" jsonschema:"tcp: required; ssl: default 443"`
	ExpectedStatusCode int      `json:"expectedStatusCode,omitempty" jsonschema:"http: the status that means it's up (default 200)"`
	RecordType         string   `json:"recordType,omitempty" jsonschema:"dns: A (default), AAAA, CNAME, MX, TXT or NS"`
	ExpectedValues     []string `json:"expectedValues,omitempty" jsonschema:"dns: values that must appear in the records (part of a record is enough, e.g. v=spf1)"`
}

// --- suggest_monitors ---

type suggestIn struct {
	Domain string `json:"domain" jsonschema:"a domain such as example.com (its usual subdomains are scanned), or one URL to check as given"`
}

type suggestedMonitor struct {
	monitorSpec
	Note             string `json:"note,omitempty"`
	AlreadyMonitored bool   `json:"alreadyMonitored"`
}

type suggestOut struct {
	Domain      string             `json:"domain"`
	Suggestions []suggestedMonitor `json:"suggestions"`
	// How many more checks the plan allows.
	CanAdd int    `json:"canAdd"`
	Next   string `json:"next"`
}

func (t *toolset) suggestMonitors(ctx context.Context, req *mcp.CallToolRequest, in suggestIn) (*mcp.CallToolResult, suggestOut, error) {
	if strings.TrimSpace(in.Domain) == "" {
		return nil, suggestOut{}, errors.New("domain is required, e.g. example.com")
	}
	var res struct {
		Domain      string `json:"domain"`
		Suggestions []struct {
			Host string `json:"host"`
			URL  string `json:"url"`
			Kind string `json:"kind"`
			Meta struct {
				StatusCode    int    `json:"statusCode"`
				Healthy       *bool  `json:"healthy"`
				CertExpiresAt string `json:"certExpiresAt"`
			} `json:"meta"`
		} `json:"suggestions"`
		Quota struct {
			Remaining int `json:"remaining"`
		} `json:"quota"`
	}
	if err := t.call(ctx, req, upstream.API, http.MethodPost, "/v1/api/discovery/", nil, map[string]any{"domain": in.Domain}, &res); err != nil {
		return nil, suggestOut{}, err
	}
	existing, err := t.monitoredTargets(ctx, req)
	if err != nil {
		return nil, suggestOut{}, err
	}

	out := suggestOut{Domain: res.Domain, Suggestions: []suggestedMonitor{}, CanAdd: res.Quota.Remaining,
		Next: "Show these to the user; add the ones they pick with add_monitors (pass type, target and expectedStatusCode as given)."}
	for _, s := range res.Suggestions {
		m := suggestedMonitor{}
		if s.Kind == "ssl" {
			m.monitorSpec = monitorSpec{Type: "ssl", Target: s.Host, Name: s.Host + " SSL"}
			if s.Meta.CertExpiresAt != "" {
				m.Note = "certificate expires " + s.Meta.CertExpiresAt
			}
		} else {
			// As Setup: a healthy answer is what "up" looks like (a 401 behind a
			// login stays up); an unhealthy one (404 root, sleeping instance)
			// expects 200, so a transient error isn't defined as up.
			expected := 200
			if (s.Meta.Healthy == nil || *s.Meta.Healthy) && s.Meta.StatusCode != 0 {
				expected = s.Meta.StatusCode
			}
			m.monitorSpec = monitorSpec{Type: "http", Target: s.URL, Name: s.Host, ExpectedStatusCode: expected}
			if s.Meta.Healthy != nil && !*s.Meta.Healthy {
				m.Note = fmt.Sprintf("answered %d now; expecting 200", s.Meta.StatusCode)
			}
		}
		if spec, err := normalizeSpec(m.monitorSpec); err == nil {
			if _, ok := existing[spec.key()]; ok {
				m.AlreadyMonitored = true
			} else if at, ok := existing[httpHost(spec)]; ok {
				m.AlreadyMonitored, m.Note = true, "already checked at "+at
			}
		}
		out.Suggestions = append(out.Suggestions, m)
	}
	return nil, out, nil
}

// --- add_monitors ---

type addMonitorsIn struct {
	Monitors        []monitorSpec `json:"monitors" jsonschema:"what to check, up to 20 per call"`
	Tags            []string      `json:"tags,omitempty" jsonschema:"tag names for every new monitor (e.g. a client or environment); missing tags are created"`
	IntervalSeconds int           `json:"intervalSeconds,omitempty" jsonschema:"how often to check; default 60 (ssl 3600), raised to the plan's minimum"`
}

type addedMonitor struct {
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Type            string   `json:"type"`
	Target          string   `json:"target"`
	IntervalSeconds int      `json:"intervalSeconds"`
	Regions         []string `json:"regions"`
}

type addMonitorsOut struct {
	Added []addedMonitor `json:"added"`
	// Targets skipped because a monitor of the same type already checks them.
	AlreadyMonitored []string `json:"alreadyMonitored"`
}

func (t *toolset) addMonitors(ctx context.Context, req *mcp.CallToolRequest, in addMonitorsIn) (*mcp.CallToolResult, addMonitorsOut, error) {
	if len(in.Monitors) == 0 {
		return nil, addMonitorsOut{}, errors.New("monitors is required: at least one target")
	}
	existing, err := t.monitoredTargets(ctx, req)
	if err != nil {
		return nil, addMonitorsOut{}, err
	}

	out := addMonitorsOut{Added: []addedMonitor{}, AlreadyMonitored: []string{}}
	var specs []monitorSpec
	seen := map[string]bool{}
	for _, raw := range in.Monitors {
		spec, err := normalizeSpec(raw)
		if err != nil {
			return nil, addMonitorsOut{}, err
		}
		if _, ok := existing[spec.key()]; ok || seen[spec.key()] {
			out.AlreadyMonitored = append(out.AlreadyMonitored, spec.Target)
			continue
		}
		seen[spec.key()] = true
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return nil, out, nil
	}
	if len(specs) > maxMonitorsPerCall {
		return nil, addMonitorsOut{}, fmt.Errorf("at most %d new monitors per call; split the list", maxMonitorsPerCall)
	}

	var quota struct {
		HealthcheckFrequency int `json:"healthcheckFrequency"`
	}
	if err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/quota", nil, nil, &quota); err != nil {
		return nil, addMonitorsOut{}, err
	}
	// Every region the plan allows, as Setup does. They're sent back as the
	// API lists them: the healthcheck keeps them as its region list.
	var schedulers upstream.Results[map[string]any]
	if err := t.call(ctx, req, upstream.API, http.MethodGet, "/v1/api/schedulers/", nil, nil, &schedulers); err != nil {
		return nil, addMonitorsOut{}, err
	}
	if len(schedulers.Results) == 0 {
		return nil, addMonitorsOut{}, errors.New("this workspace has no regions to check from; pick a plan with regions in the Uptimy app")
	}
	regions := make([]string, 0, len(schedulers.Results))
	for _, s := range schedulers.Results {
		name, _ := s["name"].(string)
		regions = append(regions, name)
	}

	tagIDs, err := t.ensureTags(ctx, req, in.Tags)
	if err != nil {
		return nil, addMonitorsOut{}, err
	}

	items := make([]map[string]any, 0, len(specs))
	for _, spec := range specs {
		interval := in.IntervalSeconds
		if interval == 0 {
			interval = 60
			if spec.Type == "ssl" {
				interval = 3600
			}
		}
		interval = max(interval, quota.HealthcheckFrequency)
		items = append(items, map[string]any{
			"applicationName": spec.Name,
			"healthcheck": map[string]any{
				"intervalSeconds":   interval,
				"timeoutSeconds":    30,
				"active":            true,
				"healthcheckTypeId": monitorTypes[spec.Type],
				"config":            spec.config(),
				"schedulers":        schedulers.Results,
			},
		})
	}
	var created upstream.Results[struct {
		UUID        string       `json:"uuid"`
		Name        string       `json:"name"`
		Healthcheck *healthcheck `json:"healthcheck"`
	}]
	if err := t.call(ctx, req, upstream.API, http.MethodPost, "/v1/api/healthchecks/bulk", nil, map[string]any{"items": items}, &created); err != nil {
		return nil, addMonitorsOut{}, err
	}

	for i, app := range created.Results {
		if len(tagIDs) > 0 {
			path := "/v1/api/applications/" + url.PathEscape(app.UUID) + "/healthchecks/tags/"
			if err := t.call(ctx, req, upstream.API, http.MethodPut, path, nil, map[string]any{"tagIds": tagIDs}, nil); err != nil {
				return nil, addMonitorsOut{}, fmt.Errorf("monitors were added, but tagging %s failed: %w", app.Name, err)
			}
		}
		m := addedMonitor{Name: app.Name, Regions: regions}
		if app.Healthcheck != nil {
			m.UUID, m.IntervalSeconds = app.Healthcheck.UUID, app.Healthcheck.IntervalSeconds
		}
		if i < len(specs) {
			m.Type, m.Target = specs[i].Type, specs[i].Target
		}
		out.Added = append(out.Added, m)
	}
	return nil, out, nil
}

// normalizeSpec checks a monitor and fills in its defaults.
func normalizeSpec(s monitorSpec) (monitorSpec, error) {
	s.Type = strings.ToLower(strings.TrimSpace(s.Type))
	if s.Type == "" {
		s.Type = "http"
	}
	if _, ok := monitorTypes[s.Type]; !ok {
		return s, fmt.Errorf("unknown type %q: use http, ssl, tcp, ping or dns", s.Type)
	}
	target := strings.TrimSpace(s.Target)
	if target == "" {
		return s, errors.New("every monitor needs a target")
	}

	if s.Type == "http" {
		if !strings.Contains(target, "://") {
			target = "https://" + target
		}
		u, err := url.Parse(target)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return s, fmt.Errorf("%q isn't an http(s) URL", s.Target)
		}
		s.Target = u.String()
		if s.ExpectedStatusCode == 0 {
			s.ExpectedStatusCode = 200
		}
		if s.Name == "" {
			s.Name = strings.TrimSuffix(u.Host+u.Path, "/")
		}
		return s, nil
	}

	// The other types take a host (or a domain): accept a URL or host:port too.
	if !strings.Contains(target, "://") {
		target = "//" + target
	}
	u, err := url.Parse(target)
	if err != nil || u.Hostname() == "" {
		return s, fmt.Errorf("%q isn't a host", s.Target)
	}
	s.Target = strings.ToLower(u.Hostname())
	if p, err := strconv.Atoi(u.Port()); err == nil && s.Port == 0 {
		s.Port = p
	}
	switch s.Type {
	case "ssl":
		if s.Port == 0 {
			s.Port = 443
		}
		if s.Name == "" {
			s.Name = s.Target + " SSL"
		}
	case "tcp":
		if s.Port < 1 || s.Port > 65535 {
			return s, fmt.Errorf("tcp monitor for %s needs a port", s.Target)
		}
		if s.Name == "" {
			s.Name = s.Target + ":" + strconv.Itoa(s.Port)
		}
	case "ping":
		if s.Name == "" {
			s.Name = s.Target + " ping"
		}
	case "dns":
		s.RecordType = strings.ToUpper(strings.TrimSpace(s.RecordType))
		if s.RecordType == "" {
			s.RecordType = "A"
		}
		switch s.RecordType {
		case "A", "AAAA", "CNAME", "MX", "TXT", "NS":
		default:
			return s, fmt.Errorf("record type %s isn't supported: use A, AAAA, CNAME, MX, TXT or NS", s.RecordType)
		}
		if s.Name == "" {
			s.Name = s.Target + " " + s.RecordType
		}
	}
	return s, nil
}

// config is the healthcheck config the app sends for this type.
func (s monitorSpec) config() map[string]any {
	switch s.Type {
	case "ssl":
		return map[string]any{"host": s.Target, "port": s.Port, "expiryWarningDays": 30}
	case "tcp":
		return map[string]any{"host": s.Target, "port": s.Port}
	case "ping":
		return map[string]any{"host": s.Target}
	case "dns":
		cfg := map[string]any{"domain": s.Target, "recordType": s.RecordType}
		var values []string
		for _, v := range s.ExpectedValues {
			if v = strings.TrimSpace(v); v != "" {
				values = append(values, v)
			}
		}
		if len(values) > 0 {
			cfg["expectedRecords"] = values
		}
		return cfg
	default:
		return map[string]any{"method": "GET", "uri": s.Target, "expectedStatusCode": s.ExpectedStatusCode,
			"body": map[string]any{}, "headers": map[string]any{}, "expectedResponseBody": map[string]any{}, "proxyUrl": ""}
	}
}

// key identifies what a monitor checks, to tell a new monitor from a duplicate.
func (s monitorSpec) key() string {
	switch s.Type {
	case "http":
		return "http|" + strings.TrimRight(strings.ToLower(s.Target), "/")
	case "ssl", "tcp":
		return s.Type + "|" + s.Target + ":" + strconv.Itoa(s.Port)
	case "dns":
		return "dns|" + s.Target + "|" + s.RecordType
	default:
		return s.Type + "|" + s.Target
	}
}

// monitoredTargets maps the key of every target the workspace already checks
// to that target, plus each website/API host to a URL it's checked at (a
// health endpoint, say), so a scan doesn't suggest the host again.
func (t *toolset) monitoredTargets(ctx context.Context, req *mcp.CallToolRequest) (map[string]string, error) {
	hcs, err := t.listHealthchecks(ctx, req)
	if err != nil {
		return nil, err
	}
	keys := map[string]string{}
	for _, h := range hcs {
		var typeName string
		for name, id := range monitorTypes {
			if fmt.Sprint(h.TypeID) == strconv.Itoa(id) {
				typeName = name
			}
		}
		if typeName == "" {
			continue
		}
		spec := monitorSpec{Type: typeName}
		switch typeName {
		case "http":
			spec.Target, _ = h.Config["uri"].(string)
		case "dns":
			spec.Target, _ = h.Config["domain"].(string)
			spec.RecordType, _ = h.Config["recordType"].(string)
		default:
			spec.Target, _ = h.Config["host"].(string)
			if p, ok := h.Config["port"].(float64); ok {
				spec.Port = int(p)
			}
		}
		if spec.Target == "" {
			continue
		}
		if spec, err := normalizeSpec(spec); err == nil {
			keys[spec.key()] = spec.Target
			if host := httpHost(spec); host != "" {
				keys[host] = spec.Target
			}
		}
	}
	return keys, nil
}

// httpHost keys a website/API check by its host alone.
func httpHost(s monitorSpec) string {
	if s.Type != "http" {
		return ""
	}
	u, err := url.Parse(s.Target)
	if err != nil || u.Hostname() == "" {
		return ""
	}
	return "host|" + strings.ToLower(u.Hostname())
}

// ensureTags returns the ids of the named tags, creating the missing ones
// (the api returns an existing tag for a name it already has).
func (t *toolset) ensureTags(ctx context.Context, req *mcp.CallToolRequest, names []string) ([]int, error) {
	ids := []int{}
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		var tag struct {
			ID int `json:"id"`
		}
		if err := t.call(ctx, req, upstream.API, http.MethodPost, "/v1/api/tags/", nil, map[string]any{"name": name}, &tag); err != nil {
			return nil, fmt.Errorf("tag %q: %w", name, err)
		}
		ids = append(ids, tag.ID)
	}
	return ids, nil
}

// --- add_cron_monitor ---

type cronMonitorIn struct {
	Name         string   `json:"name" jsonschema:"what the job does, e.g. 'Nightly database backup'"`
	EverySeconds int      `json:"everySeconds,omitempty" jsonschema:"how often the job runs, in seconds (3600 = hourly); or use cron"`
	Cron         string   `json:"cron,omitempty" jsonschema:"the job's cron schedule, e.g. '0 2 * * *'"`
	Timezone     string   `json:"timezone,omitempty" jsonschema:"IANA time zone of the cron schedule, default UTC"`
	GraceSeconds int      `json:"graceSeconds,omitempty" jsonschema:"how late a run may be before it counts as missed (default 300)"`
	Tags         []string `json:"tags,omitempty" jsonschema:"tag names; missing tags are created"`
}

type cronMonitorOut struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
	// True when a cron monitor with this name already existed; nothing was created.
	AlreadyExisted bool   `json:"alreadyExisted"`
	Schedule       string `json:"schedule"`
	PingURL        string `json:"pingUrl"`
	HowToPing      string `json:"howToPing"`
}

func (t *toolset) addCronMonitor(ctx context.Context, req *mcp.CallToolRequest, in cronMonitorIn) (*mcp.CallToolResult, cronMonitorOut, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, cronMonitorOut{}, errors.New("name is required")
	}
	if (in.EverySeconds > 0) == (strings.TrimSpace(in.Cron) != "") {
		return nil, cronMonitorOut{}, errors.New("give either everySeconds or cron")
	}
	hbs, err := t.listHeartbeats(ctx, req)
	if err != nil {
		return nil, cronMonitorOut{}, err
	}
	for _, h := range hbs {
		if strings.EqualFold(h.Name, name) {
			return nil, t.cronOut(h, true), nil
		}
	}

	tagIDs, err := t.ensureTags(ctx, req, in.Tags)
	if err != nil {
		return nil, cronMonitorOut{}, err
	}
	grace := in.GraceSeconds
	if grace == 0 {
		grace = 300
	}
	// The app's defaults for a new heartbeat.
	body := map[string]any{"name": name, "intervalSeconds": in.EverySeconds, "graceSeconds": grace,
		"failureThreshold": 1, "recoveryThreshold": 1, "enforceOrder": true, "paused": false, "tagIds": tagIDs}
	if cron := strings.TrimSpace(in.Cron); cron != "" {
		body["cron"] = cron
		tz := strings.TrimSpace(in.Timezone)
		if tz == "" {
			tz = "UTC"
		}
		body["timezone"] = tz
	}
	var created heartbeat
	if err := t.call(ctx, req, upstream.Heartbeats, http.MethodPost, "/v1/api/heartbeat-monitors/", nil, body, &created); err != nil {
		return nil, cronMonitorOut{}, err
	}
	return nil, t.cronOut(created, false), nil
}

func (t *toolset) cronOut(h heartbeat, existed bool) cronMonitorOut {
	out := cronMonitorOut{UUID: h.UUID, Name: h.Name, AlreadyExisted: existed, PingURL: t.pingURL(h)}
	switch {
	case h.Cron != "":
		out.Schedule = "cron " + h.Cron + " (" + h.Timezone + ")"
	case h.NoSchedule:
		out.Schedule = "no schedule"
	default:
		out.Schedule = fmt.Sprintf("every %ds", h.IntervalSeconds)
	}
	if out.PingURL != "" {
		out.HowToPing = "Add `&& curl -fsS -m 10 --retry 3 " + out.PingURL + "` after the job's command, so it pings only when the job succeeds: a failed or missing run alerts."
	}
	return out
}

// --- status pages ---

type statusPageIn struct {
	Name         string   `json:"name" jsonschema:"e.g. 'Acme Status'"`
	MonitorUUIDs []string `json:"monitorUuids" jsonschema:"monitors and cron monitors to show, by UUID (get_status_overview, add_monitors)"`
	Public       bool     `json:"public,omitempty" jsonschema:"true publishes it now; default private (workspace members only) so the user can review it first"`
}

type statusPageOut struct {
	UUID     string   `json:"uuid"`
	Name     string   `json:"name"`
	URL      string   `json:"url"`
	Public   bool     `json:"public"`
	Monitors []string `json:"monitors"`
	// Monitors that were already on the page (add_to_status_page).
	AlreadyShown []string `json:"alreadyShown,omitempty"`
}

// pageEntry is one row of a status page group, as the api takes it.
type pageEntry struct {
	Kind                 string  `json:"kind"`
	ApplicationUUID      *string `json:"applicationUuid,omitempty"`
	HeartbeatMonitorUUID *string `json:"heartbeatMonitorUuid,omitempty"`
	Order                int     `json:"order"`
	ShowDailies          bool    `json:"showDailies"`
	IsVisible            bool    `json:"isVisible"`
	CustomLabel          *string `json:"customLabel,omitempty"`

	name string
}

func (e pageEntry) id() string {
	if e.ApplicationUUID != nil {
		return *e.ApplicationUUID
	}
	if e.HeartbeatMonitorUUID != nil {
		return *e.HeartbeatMonitorUUID
	}
	return ""
}

// pageEntries turns monitor UUIDs into status page rows: a website/API check
// is shown as its application (healthcheck or application UUID), a cron
// monitor as itself.
func (t *toolset) pageEntries(ctx context.Context, req *mcp.CallToolRequest, uuids []string) ([]pageEntry, error) {
	hcs, err := t.listHealthchecks(ctx, req)
	if err != nil {
		return nil, err
	}
	hbs, err := t.listHeartbeats(ctx, req)
	if err != nil {
		return nil, err
	}
	known := map[string]pageEntry{}
	for _, h := range hcs {
		app := h.Application.UUID
		if app == "" {
			app = h.ApplicationUUID
		}
		e := pageEntry{Kind: "service", ApplicationUUID: &app, name: h.Application.Name}
		known[h.UUID], known[app] = e, e
	}
	for _, h := range hbs {
		id := h.UUID
		known[id] = pageEntry{Kind: "heartbeat", HeartbeatMonitorUUID: &id, name: h.Name}
	}

	out := make([]pageEntry, 0, len(uuids))
	seen := map[string]bool{}
	for _, u := range uuids {
		e, ok := known[u]
		if !ok {
			return nil, errors.New("unknown monitor UUID " + u + "; get_status_overview lists them")
		}
		if !seen[e.id()] {
			seen[e.id()] = true
			out = append(out, e)
		}
	}
	return out, nil
}

func (t *toolset) createStatusPage(ctx context.Context, req *mcp.CallToolRequest, in statusPageIn) (*mcp.CallToolResult, statusPageOut, error) {
	if strings.TrimSpace(in.Name) == "" {
		return nil, statusPageOut{}, errors.New("name is required")
	}
	entries, err := t.pageEntries(ctx, req, in.MonitorUUIDs)
	if err != nil {
		return nil, statusPageOut{}, err
	}
	// The app's defaults for a new status page.
	body := map[string]any{
		"name": strings.TrimSpace(in.Name), "public": in.Public, "whiteLabel": false, "theme": map[string]any{},
		"branding": map[string]any{"logoUrl": "", "logoDarkUrl": "", "websiteUrl": "", "faviconUrl": ""},
		"componentsVisibility": map[string]any{"showServices": true, "showIncidents": true, "showMaintenance": true,
			"showResponseTime": true, "showRegions": true, "hideEmptyIncidents": true, "hideEmptyMaintenance": true, "showSubscriptions": false},
		"enableLiveUpdates": false,
	}
	var page statusPage
	if err := t.call(ctx, req, upstream.API, http.MethodPost, "/v1/api/status-pages/", nil, body, &page); err != nil {
		return nil, statusPageOut{}, err
	}
	out := statusPageOut{UUID: page.UUID, Name: page.Name, URL: page.url(), Public: page.Public, Monitors: []string{}}
	if len(entries) == 0 {
		return nil, out, nil
	}
	if err := t.saveGroup(ctx, req, page.UUID, nil, entries); err != nil {
		return nil, statusPageOut{}, fmt.Errorf("the status page was created, but adding its monitors failed: %w", err)
	}
	for _, e := range entries {
		out.Monitors = append(out.Monitors, e.name)
	}
	return nil, out, nil
}

type addToPageIn struct {
	StatusPageUUID string   `json:"statusPageUuid" jsonschema:"from get_status_overview"`
	MonitorUUIDs   []string `json:"monitorUuids" jsonschema:"monitors and cron monitors to add, by UUID"`
}

type pageGroup struct {
	UUID    string      `json:"uuid"`
	Label   string      `json:"label"`
	Hidden  bool        `json:"hidden"`
	Order   int         `json:"order"`
	Entries []pageEntry `json:"entries"`
}

func (t *toolset) addToStatusPage(ctx context.Context, req *mcp.CallToolRequest, in addToPageIn) (*mcp.CallToolResult, statusPageOut, error) {
	if in.StatusPageUUID == "" || len(in.MonitorUUIDs) == 0 {
		return nil, statusPageOut{}, errors.New("statusPageUuid and monitorUuids are required")
	}
	base := "/v1/api/status-pages/" + url.PathEscape(in.StatusPageUUID)
	var page statusPage
	if err := t.call(ctx, req, upstream.API, http.MethodGet, base, nil, nil, &page); err != nil {
		if upstream.IsNotFound(err) {
			return nil, statusPageOut{}, errors.New("no status page with UUID " + in.StatusPageUUID + "; get_status_overview lists them")
		}
		return nil, statusPageOut{}, err
	}
	entries, err := t.pageEntries(ctx, req, in.MonitorUUIDs)
	if err != nil {
		return nil, statusPageOut{}, err
	}
	var groups upstream.Results[pageGroup]
	if err := t.call(ctx, req, upstream.API, http.MethodGet, base+"/groups/", nil, nil, &groups); err != nil {
		return nil, statusPageOut{}, err
	}

	out := statusPageOut{UUID: page.UUID, Name: page.Name, URL: page.url(), Public: page.Public, Monitors: []string{}, AlreadyShown: []string{}}
	shown := map[string]bool{}
	for _, g := range groups.Results {
		for _, e := range g.Entries {
			shown[e.id()] = true
		}
	}
	var added []pageEntry
	for _, e := range entries {
		if shown[e.id()] {
			out.AlreadyShown = append(out.AlreadyShown, e.name)
			continue
		}
		added = append(added, e)
		out.Monitors = append(out.Monitors, e.name)
	}
	if len(added) == 0 {
		return nil, out, nil
	}
	// New rows go at the end of the first group, or a new one if there's none.
	var first *pageGroup
	if len(groups.Results) > 0 {
		sort.SliceStable(groups.Results, func(i, j int) bool { return groups.Results[i].Order < groups.Results[j].Order })
		first = &groups.Results[0]
	}
	if err := t.saveGroup(ctx, req, page.UUID, first, added); err != nil {
		return nil, statusPageOut{}, err
	}
	return nil, out, nil
}

// saveGroup appends entries to group (a new "Services" group when nil). The
// api replaces a group's rows on update, so the existing ones are sent too.
func (t *toolset) saveGroup(ctx context.Context, req *mcp.CallToolRequest, pageUUID string, group *pageGroup, add []pageEntry) error {
	g := pageGroup{Label: "Services", Entries: []pageEntry{}}
	if group != nil {
		g = *group
	}
	rows := append([]pageEntry{}, g.Entries...)
	for _, e := range add {
		e.Order, e.ShowDailies, e.IsVisible = len(rows), true, true
		rows = append(rows, e)
	}
	body := map[string]any{"label": g.Label, "hidden": g.Hidden, "order": g.Order, "entries": rows}
	path := "/v1/api/status-pages/" + url.PathEscape(pageUUID) + "/groups/"
	if group == nil {
		return t.call(ctx, req, upstream.API, http.MethodPost, path, nil, body, nil)
	}
	return t.call(ctx, req, upstream.API, http.MethodPut, path+url.PathEscape(g.UUID), nil, body, nil)
}
