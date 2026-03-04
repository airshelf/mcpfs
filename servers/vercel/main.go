// mcpfs-vercel: Vercel MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   vercel://deployments                          - latest deployments (slim)
//   vercel://deployments/{url}                    - single deployment
//   vercel://deployments/{url}/logs/build         - build logs (text)
//   vercel://deployments/{url}/logs/runtime       - runtime logs (text)
//   vercel://projects                             - all projects (slim)
//   vercel://projects/{name}                      - single project
//   vercel://projects/{name}/env                  - environment variables
//   vercel://domains                              - all domains (slim)
//
// Auth: VERCEL_TOKEN env var (required).
// Optional: VERCEL_TEAM_ID to scope requests to a team.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
)

var (
	token  string
	teamID string
)

func init() {
	token = os.Getenv("VERCEL_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-vercel: set VERCEL_TOKEN env var (vercel.com/account/tokens)")
		os.Exit(1)
	}
	teamID = os.Getenv("VERCEL_TEAM_ID")
}

func vercelAPI(path string) (json.RawMessage, error) {
	u := "https://api.vercel.com" + path
	if teamID != "" {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		u += sep + "teamId=" + url.QueryEscape(teamID)
	}

	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Vercel API %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	return json.RawMessage(body), nil
}

// slimObjects extracts only the named fields from an array of JSON objects.
func slimObjects(data json.RawMessage, fields []string) ([]byte, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	var slim []map[string]interface{}
	for _, item := range items {
		var full map[string]interface{}
		if err := json.Unmarshal(item, &full); err != nil {
			continue
		}
		s := make(map[string]interface{}, len(fields))
		for _, f := range fields {
			s[f] = full[f]
		}
		slim = append(slim, s)
	}
	return json.MarshalIndent(slim, "", "  ")
}

// resolveDeploymentID looks up a deployment by URL and returns its ID.
func resolveDeploymentID(deployURL string) (string, error) {
	data, err := vercelAPI("/v13/deployments/get?url=" + url.QueryEscape(deployURL))
	if err != nil {
		return "", err
	}
	var d struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return "", err
	}
	return d.ID, nil
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "vercel://deployments":
		data, err := vercelAPI("/v6/deployments?limit=10")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var wrapper struct {
			Deployments json.RawMessage `json:"deployments"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(wrapper.Deployments, []string{"uid", "url", "name", "state", "created", "target", "meta"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case uri == "vercel://projects":
		data, err := vercelAPI("/v9/projects")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var wrapper struct {
			Projects json.RawMessage `json:"projects"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(wrapper.Projects, []string{"id", "name", "framework", "updatedAt", "latestDeployments"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case uri == "vercel://domains":
		data, err := vercelAPI("/v5/domains")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var wrapper struct {
			Domains json.RawMessage `json:"domains"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(wrapper.Domains, []string{"name", "verified", "configured", "createdAt"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	default:
		return readTemplatedResource(uri)
	}
}

func readTemplatedResource(uri string) (mcpserve.ReadResult, error) {
	// vercel://deployments/{url}[/logs/build|/logs/runtime]
	if strings.HasPrefix(uri, "vercel://deployments/") {
		return readDeploymentResource(uri)
	}
	// vercel://projects/{name}[/env]
	if strings.HasPrefix(uri, "vercel://projects/") {
		return readProjectResource(uri)
	}
	return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
}

func readDeploymentResource(uri string) (mcpserve.ReadResult, error) {
	rest := strings.TrimPrefix(uri, "vercel://deployments/")
	// rest is "{url}" or "{url}/logs/build" or "{url}/logs/runtime"

	var deployURL, suffix string
	if idx := strings.Index(rest, "/logs/"); idx >= 0 {
		deployURL = rest[:idx]
		suffix = rest[idx+1:] // "logs/build" or "logs/runtime"
	} else {
		deployURL = rest
	}

	switch suffix {
	case "":
		// Single deployment details
		data, err := vercelAPI("/v13/deployments/get?url=" + url.QueryEscape(deployURL))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case "logs/build":
		id, err := resolveDeploymentID(deployURL)
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		data, err := vercelAPI(fmt.Sprintf("/v7/deployments/%s/events?builds=1&direction=backward&limit=100", id))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var events []struct {
			Type    string `json:"type"`
			Payload struct {
				Text string `json:"text"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(data, &events); err != nil {
			return mcpserve.ReadResult{Text: "(no build logs)", MimeType: "text/plain"}, nil
		}
		var b strings.Builder
		for _, e := range events {
			if e.Type == "stdout" || e.Type == "stderr" {
				b.WriteString(e.Payload.Text)
			}
		}
		text := b.String()
		if text == "" {
			text = "(no build logs)"
		}
		return mcpserve.ReadResult{Text: text, MimeType: "text/plain"}, nil

	case "logs/runtime":
		id, err := resolveDeploymentID(deployURL)
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		data, err := vercelAPI(fmt.Sprintf("/v3/deployments/%s/events?limit=100&direction=backward", id))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var events []struct {
			Date    string `json:"date"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(data, &events); err != nil {
			return mcpserve.ReadResult{Text: "(no runtime logs)", MimeType: "text/plain"}, nil
		}
		var lines []string
		for _, e := range events {
			lines = append(lines, e.Date+" "+e.Message)
		}
		text := strings.Join(lines, "\n")
		if text == "" {
			text = "(no runtime logs)"
		}
		return mcpserve.ReadResult{Text: text, MimeType: "text/plain"}, nil

	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown deployment resource: %s", uri)
	}
}

func readProjectResource(uri string) (mcpserve.ReadResult, error) {
	rest := strings.TrimPrefix(uri, "vercel://projects/")
	// rest is "{name}" or "{name}/env"

	var name, suffix string
	if idx := strings.Index(rest, "/"); idx >= 0 {
		name = rest[:idx]
		suffix = rest[idx+1:]
	} else {
		name = rest
	}

	switch suffix {
	case "":
		data, err := vercelAPI("/v9/projects/" + url.PathEscape(name))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case "env":
		data, err := vercelAPI("/v10/projects/" + url.PathEscape(name) + "/env")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		var wrapper struct {
			Envs json.RawMessage `json:"envs"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(wrapper.Envs, []string{"key", "value", "target", "type", "updatedAt"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown project resource: %s", uri)
	}
}

func main() {
	srv := mcpserve.New("mcpfs-vercel", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "vercel://deployments", Name: "deployments",
		Description: "Latest deployments", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "vercel://projects", Name: "projects",
		Description: "All projects", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "vercel://domains", Name: "domains",
		Description: "All domains", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "vercel://deployments/{url}", Name: "deployment",
		Description: "Single deployment by URL", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "vercel://deployments/{url}/logs/build", Name: "build-logs",
		Description: "Build logs for a deployment", MimeType: "text/plain",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "vercel://deployments/{url}/logs/runtime", Name: "runtime-logs",
		Description: "Runtime logs for a deployment", MimeType: "text/plain",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "vercel://projects/{name}", Name: "project",
		Description: "Single project by name or ID", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "vercel://projects/{name}/env", Name: "env",
		Description: "Environment variables for a project", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-vercel: %v\n", err)
		os.Exit(1)
	}
}
