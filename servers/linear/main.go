// mcpfs-linear: Linear MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   linear://issues                  - assigned issues
//   linear://issues/{id}             - issue details + description
//   linear://issues/{id}/comments    - issue comments
//   linear://projects                - active projects
//   linear://projects/{id}/issues    - project issues
//   linear://cycles                  - current + upcoming cycles
//   linear://teams                   - teams
//
// Auth: LINEAR_API_KEY env var (personal API key from linear.app/settings/api).
package main

import (
	"bytes"
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

var token string

func linearQuery(query string) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest("POST", "https://api.linear.app/graphql", bytes.NewReader(body))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("linear API %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	json.Unmarshal(respBody, &envelope)
	if len(envelope.Errors) > 0 {
		return nil, fmt.Errorf("linear: %s", envelope.Errors[0].Message)
	}
	return envelope.Data, nil
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "linear://issues":
		return readIssues()
	case uri == "linear://projects":
		return readProjects()
	case uri == "linear://cycles":
		return readCycles()
	case uri == "linear://teams":
		return readTeams()
	case strings.HasPrefix(uri, "linear://issues/"):
		return readIssueResource(strings.TrimPrefix(uri, "linear://issues/"))
	case strings.HasPrefix(uri, "linear://projects/"):
		return readProjectIssues(strings.TrimPrefix(uri, "linear://projects/"))
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func readIssues() (mcpserve.ReadResult, error) {
	data, err := linearQuery(`{
		issues(filter: { assignee: { isMe: { eq: true } } }, first: 50) {
			nodes {
				id identifier title
				state { name }
				priority priorityLabel
				assignee { name }
				team { name key }
				createdAt updatedAt
			}
		}
	}`)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Issues struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"issues"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Issues.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readIssueResource(path string) (mcpserve.ReadResult, error) {
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]

	if len(parts) == 2 && parts[1] == "comments" {
		return readIssueComments(id)
	}

	return readIssueDetails(id)
}

func readIssueDetails(id string) (mcpserve.ReadResult, error) {
	data, err := linearQuery(fmt.Sprintf(`{
		issue(id: "%s") {
			id identifier title description
			state { name }
			priority priorityLabel
			assignee { name }
			team { name key }
			labels { nodes { name } }
			createdAt updatedAt
			url
		}
	}`, escapeGQL(id)))
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Issue json.RawMessage `json:"issue"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Issue, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readIssueComments(id string) (mcpserve.ReadResult, error) {
	data, err := linearQuery(fmt.Sprintf(`{
		issue(id: "%s") {
			comments(first: 50) {
				nodes {
					id body
					user { name }
					createdAt
				}
			}
		}
	}`, escapeGQL(id)))
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Issue struct {
			Comments struct {
				Nodes []json.RawMessage `json:"nodes"`
			} `json:"comments"`
		} `json:"issue"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Issue.Comments.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readProjects() (mcpserve.ReadResult, error) {
	data, err := linearQuery(`{
		projects(first: 50, filter: { state: { in: ["started", "planned"] } }) {
			nodes {
				id name state
				progress
				lead { name }
				startDate targetDate
				teams { nodes { name key } }
			}
		}
	}`)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Projects struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"projects"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Projects.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readProjectIssues(path string) (mcpserve.ReadResult, error) {
	// path: {id}/issues
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 || parts[1] != "issues" {
		return mcpserve.ReadResult{}, fmt.Errorf("unknown project resource: %s", path)
	}
	id := parts[0]

	data, err := linearQuery(fmt.Sprintf(`{
		project(id: "%s") {
			issues(first: 100) {
				nodes {
					id identifier title
					state { name }
					priority priorityLabel
					assignee { name }
				}
			}
		}
	}`, escapeGQL(id)))
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Project struct {
			Issues struct {
				Nodes []json.RawMessage `json:"nodes"`
			} `json:"issues"`
		} `json:"project"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Project.Issues.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readCycles() (mcpserve.ReadResult, error) {
	data, err := linearQuery(`{
		cycles(first: 5, filter: { isPast: { eq: false } }) {
			nodes {
				id number name
				startsAt endsAt
				progress
				team { name key }
			}
		}
	}`)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Cycles struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"cycles"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Cycles.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readTeams() (mcpserve.ReadResult, error) {
	data, err := linearQuery(`{
		teams {
			nodes {
				id name key
				members { nodes { name email } }
			}
		}
	}`)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Teams struct {
			Nodes []json.RawMessage `json:"nodes"`
		} `json:"teams"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Teams.Nodes, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

// escapeGQL escapes a string for use in a GraphQL query (prevent injection).
func escapeGQL(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

func main() {
	token = os.Getenv("LINEAR_API_KEY")
	if token == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-linear: LINEAR_API_KEY env var required")
		os.Exit(1)
	}

	// CLI tool dispatch mode: mcpfs-linear <tool-name> [--flags]
	if len(os.Args) > 1 {
		var tools []mcptool.ToolDef
		json.Unmarshal(toolSchemas, &tools)
		caller := &mcptool.StdioCaller{
			Command: "npx",
			Args:    []string{"@mseep/linear-mcp"},
		}
		defer caller.Close()
		os.Exit(mcptool.Run("mcpfs-linear", tools, caller, os.Args[1:]))
	}

	srv := mcpserve.New("mcpfs-linear", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "linear://issues", Name: "issues",
		Description: "Your assigned issues", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "linear://projects", Name: "projects",
		Description: "Active projects", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "linear://cycles", Name: "cycles",
		Description: "Current and upcoming cycles", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "linear://teams", Name: "teams",
		Description: "Teams", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "linear://issues/{id}", Name: "issue",
		Description: "Issue details with description", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "linear://issues/{id}/comments", Name: "issue-comments",
		Description: "Issue comments", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "linear://projects/{id}/issues", Name: "project-issues",
		Description: "Issues in project", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-linear: %v\n", err)
		os.Exit(1)
	}
}
