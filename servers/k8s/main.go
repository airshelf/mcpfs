// mcpfs-k8s: Kubernetes MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
// Shells out to kubectl for data (handles all auth methods).
//
// Resources:
//   k8s://namespaces                          - all namespaces
//   k8s://namespaces/{ns}/pods                - pods in namespace
//   k8s://namespaces/{ns}/pods/{name}         - pod details
//   k8s://namespaces/{ns}/pods/{name}/logs    - pod logs (text/plain)
//   k8s://namespaces/{ns}/services            - services
//   k8s://namespaces/{ns}/deployments         - deployments
//   k8s://namespaces/{ns}/deployments/{name}  - deployment details
//   k8s://nodes                               - cluster nodes
//
// Auth: kubectl must be configured (~/.kube/config or KUBECONFIG env).
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
	"github.com/airshelf/mcpfs/pkg/mcptool"
)

func kubectl(args ...string) (json.RawMessage, error) {
	cmd := exec.Command("kubectl", append([]string{"-o", "json"}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("kubectl: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return json.RawMessage(out), nil
}

func kubectlText(args ...string) (string, error) {
	cmd := exec.Command("kubectl", args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("kubectl: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return "", err
	}
	return string(out), nil
}

// slimList extracts .items from kubectl JSON output and keeps only selected fields.
func slimList(data json.RawMessage, fields []string) (json.RawMessage, error) {
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}

	var results []map[string]interface{}
	for _, item := range list.Items {
		var obj map[string]interface{}
		json.Unmarshal(item, &obj)
		slim := make(map[string]interface{})
		for _, f := range fields {
			slim[f] = resolve(obj, f)
		}
		results = append(results, slim)
	}
	if results == nil {
		results = []map[string]interface{}{}
	}
	return json.MarshalIndent(results, "", "  ")
}

// resolve walks dotted path like "metadata.name" into a nested map.
func resolve(obj map[string]interface{}, path string) interface{} {
	parts := strings.Split(path, ".")
	var current interface{} = obj
	for _, p := range parts {
		m, ok := current.(map[string]interface{})
		if !ok {
			return nil
		}
		current = m[p]
	}
	return current
}

var k8sTools = []mcptool.ToolDef{
	{
		Name:        "scale",
		Description: "Scale a deployment's replica count",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "deployment", Type: "string", Desc: "Deployment name", Required: true},
			{Name: "replicas", Type: "integer", Desc: "Number of replicas", Required: true},
			{Name: "namespace", Type: "string", Desc: "Namespace (default: default)"},
		}),
	},
	{
		Name:        "restart",
		Description: "Rolling restart a deployment",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "deployment", Type: "string", Desc: "Deployment name", Required: true},
			{Name: "namespace", Type: "string", Desc: "Namespace (default: default)"},
		}),
	},
	{
		Name:        "delete-pod",
		Description: "Delete a pod (will be recreated by controller)",
		InputSchema: mcptool.BuildSchema([]mcptool.ParamDef{
			{Name: "pod", Type: "string", Desc: "Pod name", Required: true},
			{Name: "namespace", Type: "string", Desc: "Namespace (default: default)"},
		}),
	},
}

type k8sCaller struct{}

func (c *k8sCaller) Call(toolName string, args map[string]interface{}) (json.RawMessage, error) {
	s := func(key string) string { v, _ := args[key].(string); return v }
	ns := s("namespace")
	if ns == "" {
		ns = "default"
	}

	switch toolName {
	case "scale":
		replicas := fmt.Sprintf("%v", args["replicas"])
		out, err := kubectlText("scale", fmt.Sprintf("deployment/%s", s("deployment")), "--replicas="+replicas, "-n", ns)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"status": strings.TrimSpace(out)})
	case "restart":
		out, err := kubectlText("rollout", "restart", fmt.Sprintf("deployment/%s", s("deployment")), "-n", ns)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"status": strings.TrimSpace(out)})
	case "delete-pod":
		out, err := kubectlText("delete", "pod", s("pod"), "-n", ns)
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]string{"status": strings.TrimSpace(out)})
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "k8s://namespaces":
		return readNamespaces()
	case uri == "k8s://nodes":
		return readNodes()
	case strings.HasPrefix(uri, "k8s://namespaces/"):
		return readNamespacedResource(strings.TrimPrefix(uri, "k8s://namespaces/"))
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func readNamespaces() (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "namespaces")
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, err := slimList(data, []string{"metadata.name", "status.phase"})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readNodes() (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "nodes")
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, err := slimList(data, []string{
		"metadata.name", "status.conditions", "status.nodeInfo",
		"status.capacity", "status.allocatable",
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readNamespacedResource(path string) (mcpserve.ReadResult, error) {
	// path formats:
	//   {ns}/pods
	//   {ns}/pods/{name}
	//   {ns}/pods/{name}/logs
	//   {ns}/services
	//   {ns}/deployments
	//   {ns}/deployments/{name}
	parts := strings.SplitN(path, "/", 4)
	if len(parts) < 2 {
		return mcpserve.ReadResult{}, fmt.Errorf("invalid path: %s", path)
	}

	ns := parts[0]
	kind := parts[1]

	switch kind {
	case "pods":
		if len(parts) == 2 {
			return readPodList(ns)
		}
		name := parts[2]
		if len(parts) == 4 && parts[3] == "logs" {
			return readPodLogs(ns, name)
		}
		return readPodDetails(ns, name)

	case "services":
		if len(parts) == 2 {
			return readServiceList(ns)
		}
		return mcpserve.ReadResult{}, fmt.Errorf("unknown service resource: %s", path)

	case "deployments":
		if len(parts) == 2 {
			return readDeploymentList(ns)
		}
		return readDeploymentDetails(ns, parts[2])

	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource kind: %s", kind)
	}
}

func readPodList(ns string) (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "pods", "-n", ns)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, err := slimList(data, []string{
		"metadata.name", "metadata.namespace", "status.phase",
		"status.startTime", "spec.nodeName",
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readPodDetails(ns, name string) (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "pod", name, "-n", ns)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readPodLogs(ns, name string) (mcpserve.ReadResult, error) {
	text, err := kubectlText("logs", name, "-n", ns, "--tail=500")
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: text, MimeType: "text/plain"}, nil
}

func readServiceList(ns string) (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "services", "-n", ns)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, err := slimList(data, []string{
		"metadata.name", "spec.type", "spec.clusterIP",
		"spec.ports", "spec.selector",
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readDeploymentList(ns string) (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "deployments", "-n", ns)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, err := slimList(data, []string{
		"metadata.name", "spec.replicas", "status.readyReplicas",
		"status.availableReplicas", "status.updatedReplicas",
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readDeploymentDetails(ns, name string) (mcpserve.ReadResult, error) {
	data, err := kubectl("get", "deployment", name, "-n", ns)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func main() {
	// Verify kubectl is available
	if _, err := exec.LookPath("kubectl"); err != nil {
		fmt.Fprintln(os.Stderr, "mcpfs-k8s: kubectl not found in PATH")
		os.Exit(1)
	}

	// CLI tool dispatch mode: mcpfs-k8s <tool-name> [--flags]
	if len(os.Args) > 1 {
		os.Exit(mcptool.Run("mcpfs-k8s", k8sTools, &k8sCaller{}, os.Args[1:]))
	}

	srv := mcpserve.New("mcpfs-k8s", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "k8s://namespaces", Name: "namespaces",
		Description: "All namespaces", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "k8s://nodes", Name: "nodes",
		Description: "Cluster nodes", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/pods", Name: "pods",
		Description: "Pods in namespace", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/pods/{name}", Name: "pod",
		Description: "Pod details", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/pods/{name}/logs", Name: "pod-logs",
		Description: "Pod logs (last 500 lines)", MimeType: "text/plain",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/services", Name: "services",
		Description: "Services in namespace", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/deployments", Name: "deployments",
		Description: "Deployments in namespace", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "k8s://namespaces/{ns}/deployments/{name}", Name: "deployment",
		Description: "Deployment details", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-k8s: %v\n", err)
		os.Exit(1)
	}
}
