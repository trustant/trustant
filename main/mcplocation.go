// Copyright 2025-2026 Nuvolaris Inc
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

// mcplocation.go tracks which Trustant page the user is looking at (the app
// list, an app in the workbench, ...) so an MCP agent can ask "where is the
// user?" and get every URL needed to show the same app, its ACP, or both
// stacked in split.html (spec/22-mcpserver.md).

package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// uiLocationActiveWindow: web/js/location.js reports every 15 s while the tab
// is visible, so a report older than this means no Trustant tab is open.
var uiLocationActiveWindow = 45 * time.Second

// uiNow is a test seam.
var uiNow = time.Now

var uiLocationPages = map[string]bool{
	"applist": true, "app": true, "configure": true, "index": true, "other": true,
}

type uiLocation struct {
	Page      string
	App       string
	Route     string
	Query     string
	UpdatedAt time.Time
}

var uiLocationState struct {
	sync.Mutex
	loc uiLocation
}

func currentUILocation() uiLocation {
	uiLocationState.Lock()
	defer uiLocationState.Unlock()
	return uiLocationState.loc
}

// normalizeRoute keeps what app.html would put after vite.<domain>: a path
// starting with "/", without a fragment, bounded in length.
func normalizeRoute(route string) string {
	route = strings.TrimSpace(route)
	if i := strings.IndexByte(route, '#'); i >= 0 {
		route = route[:i]
	}
	if route == "" || !strings.HasPrefix(route, "/") || strings.HasPrefix(route, "//") || len(route) > 512 {
		return "/"
	}
	return route
}

func normalizeQuery(query string) string {
	query = strings.TrimPrefix(strings.TrimSpace(query), "?")
	if i := strings.IndexByte(query, '#'); i >= 0 {
		query = query[:i]
	}
	if len(query) > 1024 {
		return ""
	}
	return query
}

// handleUILocation serves /api/v1/location: POST from web/js/location.js,
// GET for anyone curious. It is a normal UI API (behind the session when
// local login is enabled); the MCP tool reads the state directly.
func handleUILocation(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONStatus(w, http.StatusOK, uiLocationView(requestBaseURL(r)))
	case http.MethodPost:
		var req struct {
			Page  string `json:"page"`
			App   string `json:"app"`
			Route string `json:"route"`
			Query string `json:"query"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON"})
			return
		}
		if !uiLocationPages[req.Page] {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "unknown page"})
			return
		}
		if req.App != "" && !namePattern.MatchString(req.App) {
			writeJSONStatus(w, http.StatusBadRequest, map[string]any{"error": "invalid app name"})
			return
		}
		loc := uiLocation{Page: req.Page, App: req.App, UpdatedAt: uiNow()}
		if req.Page == "app" {
			loc.Route, loc.Query = normalizeRoute(req.Route), normalizeQuery(req.Query)
		}
		uiLocationState.Lock()
		uiLocationState.loc = loc
		uiLocationState.Unlock()
		writeJSONStatus(w, http.StatusOK, map[string]any{"ok": true})
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// uiLocationView is the location plus the URLs to show what the user sees.
// With no app open in the UI the URLs point at the launched (current) app.
func uiLocationView(base string) map[string]any {
	loc := currentUILocation()
	view := map[string]any{"active": false, "page": "", "app": "", "route": ""}
	if !loc.UpdatedAt.IsZero() {
		age := uiNow().Sub(loc.UpdatedAt)
		view["active"] = age <= uiLocationActiveWindow
		view["seen_seconds_ago"] = int(age.Seconds())
		view["page"] = loc.Page
		view["app"] = loc.App
		view["route"] = loc.Route
	}
	app := loc.App
	if app == "" {
		app, _ = readCurrentApp()
	}
	if current, _ := readCurrentApp(); current != "" {
		view["launched"] = current
	}
	route, query := routeForApp(app)
	view["urls"] = appURLs(base, route, query)
	return view
}

// routeForApp is the route the user last had open for app in the workbench,
// or "/" when the UI is elsewhere or on another app.
func routeForApp(app string) (string, string) {
	loc := currentUILocation()
	if app != "" && loc.App == app && loc.Route != "" {
		return loc.Route, loc.Query
	}
	return "/", ""
}

// appURLs derives every user-facing URL from the trustant.<domain> base:
//
//	app        the running app exactly as app.html shows it: vite.<domain><route>?<query>#<route>
//	dev        the dev server root
//	acp        the TruACP panel (truacp.<domain>), app.html's left pane
//	split      split.html: app on top, ACP below, no Trustant chrome
//	split_app  split.html with only the app; split_acp with only the ACP
//	workbench  app.html; applist  applist.html
func appURLs(base, route, query string) map[string]string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return map[string]string{}
	}
	host := u.Host
	rest := host
	if i := strings.Index(host, "."); i >= 0 {
		rest = host[i+1:]
	}
	route = normalizeRoute(route)
	query = normalizeQuery(query)
	app := u.Scheme + "://vite." + rest + route
	if query != "" {
		app += "?" + query
	}
	app += "#" + route

	splitParams := url.Values{}
	if route != "/" {
		splitParams.Set("route", route)
	}
	if query != "" {
		splitParams.Set("query", query)
	}
	split := u.Scheme + "://" + host + "/split.html"
	withView := func(view string) string {
		p := url.Values{}
		for k, v := range splitParams {
			p[k] = v
		}
		if view != "" {
			p.Set("view", view)
		}
		if enc := p.Encode(); enc != "" {
			return split + "?" + enc
		}
		return split
	}
	return map[string]string{
		"app":       app,
		"dev":       u.Scheme + "://vite." + rest + "/",
		"acp":       u.Scheme + "://truacp." + rest + "/",
		"split":     withView(""),
		"split_app": withView("app"),
		"split_acp": withView("acp"),
		"workbench": u.Scheme + "://" + host + "/app.html",
		"applist":   u.Scheme + "://" + host + "/applist.html",
	}
}

// currentAppURLs is appURLs for app with the route the UI has open for it.
func currentAppURLs(app string) map[string]string {
	route, query := routeForApp(app)
	return appURLs(currentMCPBase(), route, query)
}

func registerMCPLocationTool(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "ui_location", Description: "Where the user is in Trustant right now (the app list, an app open in the workbench with its route, ...), whether a Trustant tab is open, and every URL to show that app: urls.app (the app), urls.acp (the ACP chat), urls.split (app on top, ACP below, no chrome), urls.split_app / urls.split_acp. Open them in your browser pane."},
		func(context.Context, *mcp.CallToolRequest, mcpNoArgs) (*mcp.CallToolResult, any, error) {
			return mcpText(uiLocationView(currentMCPBase())), nil, nil
		})
}
