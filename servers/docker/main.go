// mcpfs-docker: Docker Engine MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   docker://containers                  - running containers (slim)
//   docker://containers/{id}             - container inspect
//   docker://containers/{id}/logs        - container logs (last 100 lines)
//   docker://containers/{id}/stats       - one-shot CPU/memory/network stats
//   docker://images                      - local images (slim)
//   docker://volumes                     - volumes
//   docker://networks                    - networks
//
// Auth: Docker socket access (user in docker group or root).
// DOCKER_HOST env var for remote Docker (default: /var/run/docker.sock).
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
	"github.com/airshelf/mcpfs/pkg/mcptool"
)

var client *http.Client

func init() {
	sock := os.Getenv("DOCKER_HOST")
	if sock == "" {
		sock = "/var/run/docker.sock"
	} else {
		// DOCKER_HOST can be "unix:///var/run/docker.sock"
		sock = strings.TrimPrefix(sock, "unix://")
	}

	client = &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sock)
			},
		},
	}
}

func dockerAPI(path string) (json.RawMessage, error) {
	resp, err := client.Get("http://localhost/v1.47" + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Docker API %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	return json.RawMessage(body), nil
}

// dockerLogs reads multiplexed Docker log stream (8-byte header per frame).
func dockerLogs(path string) (string, error) {
	resp, err := client.Get("http://localhost/v1.47" + path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Docker API %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}

	var b strings.Builder
	header := make([]byte, 8)
	for {
		_, err := io.ReadFull(resp.Body, header)
		if err != nil {
			break
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		frame := make([]byte, size)
		_, err = io.ReadFull(resp.Body, frame)
		if err != nil {
			break
		}
		b.Write(frame)
	}
	return b.String(), nil
}

func dockerPost(path string) (json.RawMessage, error) {
	resp, err := client.Post("http://localhost/v1.47"+path, "application/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Docker API %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	if len(body) == 0 {
		return json.RawMessage(`{"status":"ok"}`), nil
	}
	return json.RawMessage(body), nil
}

var dockerTools = []mcptool.ToolDef{
	{
		Name:        "start",
		Description: "Start a stopped container",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "id", Type: "string", Desc: "Container ID or name", Required: true},
		}),
	},
	{
		Name:        "stop",
		Description: "Stop a running container",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "id", Type: "string", Desc: "Container ID or name", Required: true},
		}),
	},
	{
		Name:        "restart",
		Description: "Restart a container",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "id", Type: "string", Desc: "Container ID or name", Required: true},
		}),
	},
}

type dockerCaller struct{}

func (c *dockerCaller) Call(toolName string, args map[string]interface{}) (json.RawMessage, error) {
	id, _ := args["id"].(string)
	switch toolName {
	case "start":
		return dockerPost(fmt.Sprintf("/containers/%s/start", id))
	case "stop":
		return dockerPost(fmt.Sprintf("/containers/%s/stop", id))
	case "restart":
		return dockerPost(fmt.Sprintf("/containers/%s/restart", id))
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
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

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "docker://containers":
		data, err := dockerAPI("/containers/json")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(data, []string{"Id", "Names", "Image", "State", "Status"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case uri == "docker://images":
		data, err := dockerAPI("/images/json")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(data, []string{"Id", "RepoTags", "Size", "Created"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case uri == "docker://volumes":
		data, err := dockerAPI("/volumes")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		// Docker wraps volumes in {"Volumes": [...]}
		var wrapper struct {
			Volumes json.RawMessage `json:"Volumes"`
		}
		if err := json.Unmarshal(data, &wrapper); err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, _ := json.MarshalIndent(wrapper.Volumes, "", "  ")
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case uri == "docker://networks":
		data, err := dockerAPI("/networks")
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, err := slimObjects(data, []string{"Id", "Name", "Driver", "Scope"})
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	default:
		return readContainerResource(uri)
	}
}

func readContainerResource(uri string) (mcpserve.ReadResult, error) {
	// docker://containers/{id}[/logs|/stats]
	path := strings.TrimPrefix(uri, "docker://containers/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return mcpserve.ReadResult{}, fmt.Errorf("invalid URI: %s", uri)
	}
	id := parts[0]
	suffix := ""
	if len(parts) == 2 {
		suffix = parts[1]
	}

	switch suffix {
	case "":
		// Container inspect
		data, err := dockerAPI(fmt.Sprintf("/containers/%s/json", id))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	case "logs":
		text, err := dockerLogs(fmt.Sprintf("/containers/%s/logs?stdout=1&stderr=1&tail=100", id))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		return mcpserve.ReadResult{Text: text, MimeType: "text/plain"}, nil

	case "stats":
		data, err := dockerAPI(fmt.Sprintf("/containers/%s/stats?stream=false", id))
		if err != nil {
			return mcpserve.ReadResult{}, err
		}
		out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
		return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil

	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func main() {
	// CLI tool dispatch mode: mcpfs-docker <tool-name> [--flags]
	if len(os.Args) > 1 {
		os.Exit(mcptool.Run("mcpfs-docker", dockerTools, &dockerCaller{}, os.Args[1:]))
	}

	srv := mcpserve.New("mcpfs-docker", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "docker://containers", Name: "containers",
		Description: "Running containers", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "docker://images", Name: "images",
		Description: "Local images", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "docker://volumes", Name: "volumes",
		Description: "Volumes", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "docker://networks", Name: "networks",
		Description: "Networks", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "docker://containers/{id}", Name: "container",
		Description: "Container details", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "docker://containers/{id}/logs", Name: "logs",
		Description: "Container logs (last 100 lines)", MimeType: "text/plain",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "docker://containers/{id}/stats", Name: "stats",
		Description: "Container CPU/memory/network stats", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-docker: %v\n", err)
		os.Exit(1)
	}
}
