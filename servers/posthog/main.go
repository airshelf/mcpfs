// mcpfs-posthog: PostHog MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   posthog://dashboards                     - all dashboards
//   posthog://dashboards/{id}                - dashboard details + tiles
//   posthog://feature-flags                  - all feature flags
//   posthog://feature-flags/{key}            - flag definition
//   posthog://experiments                    - all experiments
//   posthog://experiments/{id}               - experiment details
//   posthog://insights                       - saved insights
//   posthog://insights/{id}                  - insight details + query
//   posthog://errors                         - active error groups
//   posthog://events                         - event definitions
//   posthog://surveys                        - all surveys
//   posthog://cohorts                        - all cohorts
//
// Auth: POSTHOG_API_KEY and POSTHOG_PROJECT_ID env vars.
//       Optionally POSTHOG_HOST (default: https://us.i.posthog.com).
package main

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
	"github.com/airshelf/mcpfs/pkg/mcptool"
)

//go:embed tools.json
var toolSchemas []byte

var (
	apiKey    string
	projectID string
	host      string
)

func phGet(path string) (json.RawMessage, error) {
	url := host + "/api/projects/" + projectID + path
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("posthog %d: %s", resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

func mcpURL() string {
	if u := os.Getenv("POSTHOG_MCP_URL"); u != "" {
		return u
	}
	return "https://mcp.posthog.com/mcp"
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func extractResults(raw json.RawMessage) (json.RawMessage, error) {
	var paged struct {
		Results json.RawMessage `json:"results"`
	}
	if err := json.Unmarshal(raw, &paged); err == nil && paged.Results != nil {
		return paged.Results, nil
	}
	return raw, nil
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "posthog://dashboards":
		return readList("/dashboards/", true)
	case uri == "posthog://feature-flags":
		return readList("/feature_flags/", true)
	case uri == "posthog://experiments":
		return readList("/experiments/", true)
	case uri == "posthog://insights":
		return readList("/insights/?saved=true&limit=50", true)
	case uri == "posthog://errors":
		return readList("/error_tracking/issues/?status=active&order_by=-occurrences&limit=50", true)
	case uri == "posthog://events":
		return readList("/event_definitions/?limit=100", true)
	case uri == "posthog://surveys":
		return readList("/surveys/", true)
	case uri == "posthog://cohorts":
		return readList("/cohorts/", true)

	case strings.HasPrefix(uri, "posthog://dashboards/"):
		id := strings.TrimPrefix(uri, "posthog://dashboards/")
		return readSingle("/dashboards/" + id + "/")
	case strings.HasPrefix(uri, "posthog://feature-flags/"):
		key := strings.TrimPrefix(uri, "posthog://feature-flags/")
		return readFlagByKey(key)
	case strings.HasPrefix(uri, "posthog://experiments/"):
		id := strings.TrimPrefix(uri, "posthog://experiments/")
		return readSingle("/experiments/" + id + "/")
	case strings.HasPrefix(uri, "posthog://insights/"):
		id := strings.TrimPrefix(uri, "posthog://insights/")
		return readSingle("/insights/" + id + "/")

	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func readList(path string, paged bool) (mcpserve.ReadResult, error) {
	raw, err := phGet(path)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	if paged {
		results, err := extractResults(raw)
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		raw = results
	}
	var v interface{}
	json.Unmarshal(raw, &v)
	out, _ := json.MarshalIndent(v, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readSingle(path string) (mcpserve.ReadResult, error) {
	raw, err := phGet(path)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	var v interface{}
	json.Unmarshal(raw, &v)
	out, _ := json.MarshalIndent(v, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readFlagByKey(key string) (mcpserve.ReadResult, error) {
	// List all flags and find by key.
	raw, err := phGet("/feature_flags/")
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	results, _ := extractResults(raw)

	var flags []struct {
		Key string `json:"key"`
	}
	json.Unmarshal(results, &flags)

	// Re-parse to find the matching flag object.
	var allFlags []json.RawMessage
	json.Unmarshal(results, &allFlags)
	for i, f := range flags {
		if f.Key == key {
			var v interface{}
			json.Unmarshal(allFlags[i], &v)
			out, _ := json.MarshalIndent(v, "", "  ")
			return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
		}
	}
	return mcpserve.ReadResult{}, fmt.Errorf("flag not found: %s", key)
}

func main() {
	apiKey = os.Getenv("POSTHOG_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-posthog: POSTHOG_API_KEY env var required")
		os.Exit(1)
	}

	// CLI tool dispatch mode: mcpfs-posthog <tool-name> [--flags]
	if len(os.Args) > 1 {
		var tools []mcptool.ToolDef
		json.Unmarshal(toolSchemas, &tools)
		caller := &mcptool.HTTPCaller{
			URL:        mcpURL(),
			AuthHeader: "Bearer " + apiKey,
		}
		os.Exit(mcptool.Run("mcpfs-posthog", tools, caller, os.Args[1:]))
	}

	projectID = os.Getenv("POSTHOG_PROJECT_ID")
	if projectID == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-posthog: POSTHOG_PROJECT_ID env var required")
		os.Exit(1)
	}
	host = os.Getenv("POSTHOG_HOST")
	if host == "" {
		host = "https://us.i.posthog.com"
	}

	srv := mcpserve.New("mcpfs-posthog", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "posthog://dashboards", Name: "dashboards",
		Description: "All dashboards", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://feature-flags", Name: "feature-flags",
		Description: "All feature flags", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://experiments", Name: "experiments",
		Description: "All experiments", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://insights", Name: "insights",
		Description: "Saved insights", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://errors", Name: "errors",
		Description: "Active error groups", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://events", Name: "events",
		Description: "Event definitions", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://surveys", Name: "surveys",
		Description: "All surveys", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "posthog://cohorts", Name: "cohorts",
		Description: "All cohorts", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "posthog://dashboards/{id}", Name: "dashboard",
		Description: "Dashboard details with tiles", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "posthog://feature-flags/{key}", Name: "feature-flag",
		Description: "Feature flag definition", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "posthog://experiments/{id}", Name: "experiment",
		Description: "Experiment details", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "posthog://insights/{id}", Name: "insight",
		Description: "Insight details and query", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-posthog: %v\n", err)
		os.Exit(1)
	}
}
