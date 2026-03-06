package fuse

import (
	"testing"

	"github.com/airshelf/mcpfs/pkg/mcpclient"
)

func TestBuildTreeStaticResources(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://repos", Name: "repos", MimeType: "application/json"},
		{URI: "test://notifications", Name: "notifications", MimeType: "application/json"},
	}

	tree := BuildTree("test", resources, nil)

	if !tree.isDir {
		t.Fatal("root should be a dir")
	}
	if _, ok := tree.children["repos.json"]; !ok {
		t.Error("missing repos.json")
	}
	if _, ok := tree.children["notifications.json"]; !ok {
		t.Error("missing notifications.json")
	}
}

func TestBuildTreeTextResource(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://readme", Name: "readme", MimeType: "text/plain"},
	}

	tree := BuildTree("test", resources, nil)

	// text/plain should NOT get .json extension
	if _, ok := tree.children["readme"]; !ok {
		t.Error("missing readme (text/plain should not have .json suffix)")
	}
	if _, ok := tree.children["readme.json"]; ok {
		t.Error("text/plain resource should not have .json extension")
	}
}

func TestBuildTreeNestedStaticResource(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "test://projects/env", Name: "env", MimeType: "application/json"},
	}

	tree := BuildTree("test", resources, nil)

	proj, ok := tree.children["projects"]
	if !ok {
		t.Fatal("missing projects dir")
	}
	if !proj.isDir {
		t.Error("projects should be a dir")
	}
	if _, ok := proj.children["env.json"]; !ok {
		t.Error("missing env.json under projects/")
	}
}

func TestBuildTreeTemplateOneParam(t *testing.T) {
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "test://items/{id}", Name: "item", MimeType: "application/json"},
	}

	tree := BuildTree("test", nil, templates)

	items, ok := tree.children["items"]
	if !ok {
		t.Fatal("missing items dir")
	}
	if !items.isDir {
		t.Error("items should be a dir")
	}
	if items.template == "" {
		t.Error("items should have a template")
	}
	if items.param != "id" {
		t.Errorf("items.param = %q, want id", items.param)
	}
}

func TestBuildTreeTemplateTwoParams(t *testing.T) {
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "github://repos/{owner}/{repo}", Name: "repo", MimeType: "application/json"},
		{URITemplate: "github://repos/{owner}/{repo}/issues", Name: "issues", MimeType: "application/json"},
		{URITemplate: "github://repos/{owner}/{repo}/readme", Name: "readme", MimeType: "text/plain"},
	}

	tree := BuildTree("github", nil, templates)

	repos, ok := tree.children["repos"]
	if !ok {
		t.Fatal("missing repos dir")
	}
	if repos.param != "owner" {
		t.Errorf("repos.param = %q, want owner", repos.param)
	}
	if repos.nestedParam != "repo" {
		t.Errorf("repos.nestedParam = %q, want repo", repos.nestedParam)
	}
}

func TestBuildTreeMixedResourcesAndTemplates(t *testing.T) {
	resources := []mcpclient.Resource{
		{URI: "vercel://deployments", Name: "deployments", MimeType: "application/json"},
		{URI: "vercel://domains", Name: "domains", MimeType: "application/json"},
	}
	templates := []mcpclient.ResourceTemplate{
		{URITemplate: "vercel://deployments/{url}", Name: "deployment"},
		{URITemplate: "vercel://deployments/{url}/logs/build", Name: "build-logs", MimeType: "text/plain"},
		{URITemplate: "vercel://deployments/{url}/logs/runtime", Name: "runtime-logs", MimeType: "text/plain"},
	}

	tree := BuildTree("vercel", resources, templates)

	if _, ok := tree.children["deployments"]; !ok {
		t.Fatal("missing deployments")
	}
	if _, ok := tree.children["domains.json"]; !ok {
		t.Fatal("missing domains.json")
	}

	deploys := tree.children["deployments"]
	if !deploys.isDir {
		t.Error("deployments should be a dir")
	}
	// Should have both the static .json file and template capabilities
}

func TestResolveURI(t *testing.T) {
	cases := []struct {
		uri    string
		params map[string]string
		want   string
	}{
		{
			"github://repos/{owner}/{repo}",
			map[string]string{"owner": "airshelf", "repo": "mcpfs"},
			"github://repos/airshelf/mcpfs",
		},
		{
			"npm://packages/{name}",
			map[string]string{"name": "react"},
			"npm://packages/react",
		},
		{
			"test://no-params",
			map[string]string{},
			"test://no-params",
		},
		{
			"vercel://deployments/{url}/logs/build",
			map[string]string{"url": "my-app-abc.vercel.app"},
			"vercel://deployments/my-app-abc.vercel.app/logs/build",
		},
	}

	for _, tc := range cases {
		got := resolveURI(tc.uri, tc.params)
		if got != tc.want {
			t.Errorf("resolveURI(%q, %v) = %q, want %q", tc.uri, tc.params, got, tc.want)
		}
	}
}

func TestCopyParams(t *testing.T) {
	orig := map[string]string{"a": "1", "b": "2"}
	cp := copyParams(orig)

	// Modify copy, original should be unchanged
	cp["c"] = "3"
	if _, ok := orig["c"]; ok {
		t.Error("copyParams did not create independent copy")
	}
	if cp["a"] != "1" || cp["b"] != "2" {
		t.Error("copy missing original values")
	}
}

func TestSingularize(t *testing.T) {
	cases := []struct{ input, want string }{
		{"repos", "repo"},
		{"issues", "issue"},
		{"pulls", "pull"},
		{"item", "item"},
		{"s", ""},
		{"deployments", "deployment"},
	}
	for _, tc := range cases {
		got := singularize(tc.input)
		if got != tc.want {
			t.Errorf("singularize(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

