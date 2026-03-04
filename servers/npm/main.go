// mcpfs-npm: NPM registry MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   npm://packages/{name}              - package info (slim)
//   npm://packages/{name}/versions     - all versions with dates
//   npm://packages/{name}/downloads    - download stats (last month)
//   npm://packages/{name}/dependencies - latest version dependencies
//   npm://search/{query}               - search results
//
// Auth: none for public packages. NPM_TOKEN env var for private (optional).
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
)

var token string

func init() {
	token = os.Getenv("NPM_TOKEN")
}

func npmRegistry(path string) (json.RawMessage, error) {
	u := "https://registry.npmjs.org" + path
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("npm registry %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	return json.RawMessage(body), nil
}

func npmDownloads(pkg string) (json.RawMessage, error) {
	u := "https://api.npmjs.org/downloads/point/last-month/" + url.PathEscape(pkg)
	resp, err := http.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("npm downloads %d: %s", resp.StatusCode, string(body[:min(len(body), 200)]))
	}
	return json.RawMessage(body), nil
}

// encodePkg handles scoped packages: @scope/name → %40scope%2Fname
func encodePkg(name string) string {
	if strings.HasPrefix(name, "@") {
		return url.PathEscape(name)
	}
	return name
}

// parsePkgURI extracts package name and suffix from npm://packages/{name}[/suffix]
// Handles scoped packages like npm://packages/@scope/name/versions
func parsePkgURI(uri string) (pkg, suffix string) {
	path := strings.TrimPrefix(uri, "npm://packages/")
	if path == uri {
		return "", ""
	}

	if strings.HasPrefix(path, "@") {
		// Scoped: @scope/name[/suffix]
		parts := strings.SplitN(path, "/", 3)
		if len(parts) < 2 {
			return "", ""
		}
		pkg = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			suffix = parts[2]
		}
	} else {
		// Unscoped: name[/suffix]
		parts := strings.SplitN(path, "/", 2)
		pkg = parts[0]
		if len(parts) == 2 {
			suffix = parts[1]
		}
	}
	return pkg, suffix
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	// Search
	if strings.HasPrefix(uri, "npm://search/") {
		query := strings.TrimPrefix(uri, "npm://search/")
		return readSearch(query)
	}

	// Package resources
	if strings.HasPrefix(uri, "npm://packages/") {
		pkg, suffix := parsePkgURI(uri)
		if pkg == "" {
			return mcpserve.ReadResult{}, fmt.Errorf("invalid URI: %s", uri)
		}

		switch suffix {
		case "":
			return readPackageInfo(pkg)
		case "versions":
			return readVersions(pkg)
		case "downloads":
			return readDownloads(pkg)
		case "dependencies":
			return readDependencies(pkg)
		default:
			return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
		}
	}

	return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
}

func readPackageInfo(pkg string) (mcpserve.ReadResult, error) {
	data, err := npmRegistry("/" + encodePkg(pkg))
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var full map[string]interface{}
	if err := json.Unmarshal(data, &full); err != nil {
		return mcpserve.ReadResult{}, err
	}

	// Extract slim info
	slim := map[string]interface{}{
		"name":        full["name"],
		"description": full["description"],
		"license":     full["license"],
		"homepage":    full["homepage"],
		"repository":  full["repository"],
	}

	// Latest version from dist-tags
	if distTags, ok := full["dist-tags"].(map[string]interface{}); ok {
		slim["latest_version"] = distTags["latest"]
	}

	// Maintainers
	if maintainers, ok := full["maintainers"]; ok {
		slim["maintainers"] = maintainers
	}

	// Keywords
	if keywords, ok := full["keywords"]; ok {
		slim["keywords"] = keywords
	}

	out, _ := json.MarshalIndent(slim, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

type versionEntry struct {
	Version string `json:"version"`
	Date    string `json:"date"`
}

func readVersions(pkg string) (mcpserve.ReadResult, error) {
	data, err := npmRegistry("/" + encodePkg(pkg))
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var full struct {
		Time map[string]string `json:"time"`
	}
	if err := json.Unmarshal(data, &full); err != nil {
		return mcpserve.ReadResult{}, err
	}

	var versions []versionEntry
	for ver, date := range full.Time {
		if ver == "created" || ver == "modified" {
			continue
		}
		versions = append(versions, versionEntry{Version: ver, Date: date})
	}

	// Sort by date descending (newest first)
	sort.Slice(versions, func(i, j int) bool {
		return versions[i].Date > versions[j].Date
	})

	// Cap at 50 versions
	if len(versions) > 50 {
		versions = versions[:50]
	}

	out, _ := json.MarshalIndent(versions, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readDownloads(pkg string) (mcpserve.ReadResult, error) {
	data, err := npmDownloads(pkg)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	out, _ := json.MarshalIndent(json.RawMessage(data), "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readDependencies(pkg string) (mcpserve.ReadResult, error) {
	data, err := npmRegistry("/" + encodePkg(pkg) + "/latest")
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var full map[string]interface{}
	if err := json.Unmarshal(data, &full); err != nil {
		return mcpserve.ReadResult{}, err
	}

	slim := map[string]interface{}{
		"name":                full["name"],
		"version":             full["version"],
		"dependencies":        full["dependencies"],
		"devDependencies":     full["devDependencies"],
		"peerDependencies":    full["peerDependencies"],
	}

	out, _ := json.MarshalIndent(slim, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readSearch(query string) (mcpserve.ReadResult, error) {
	u := "/-/v1/search?text=" + url.QueryEscape(query) + "&size=20"
	data, err := npmRegistry(u)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Objects []struct {
			Package json.RawMessage `json:"package"`
		} `json:"objects"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return mcpserve.ReadResult{}, err
	}

	// Slim each package to essentials
	type slimPkg struct {
		Name        string `json:"name"`
		Version     string `json:"version"`
		Description string `json:"description"`
	}
	var packages []slimPkg
	for _, obj := range result.Objects {
		var p slimPkg
		json.Unmarshal(obj.Package, &p)
		packages = append(packages, p)
	}

	out, _ := json.MarshalIndent(packages, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func main() {
	srv := mcpserve.New("mcpfs-npm", "0.1.0", readResource)

	// Unscoped packages: npm://packages/react
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{name}", Name: "package",
		Description: "Package info (name, version, description, license, maintainers)", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{name}/versions", Name: "versions",
		Description: "All versions with publish dates", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{name}/downloads", Name: "downloads",
		Description: "Download stats (last month)", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{name}/dependencies", Name: "dependencies",
		Description: "Dependencies of latest version", MimeType: "application/json",
	})

	// Scoped packages: npm://packages/@scope/name
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{scope}/{name}", Name: "scoped-package",
		Description: "Scoped package info (@scope/name)", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{scope}/{name}/versions", Name: "scoped-versions",
		Description: "Scoped package versions", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{scope}/{name}/downloads", Name: "scoped-downloads",
		Description: "Scoped package download stats", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://packages/{scope}/{name}/dependencies", Name: "scoped-dependencies",
		Description: "Scoped package dependencies", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "npm://search/{query}", Name: "search",
		Description: "Search NPM packages", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-npm: %v\n", err)
		os.Exit(1)
	}
}
