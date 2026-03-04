// mcpfs-slack: Slack MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   slack://channels                       - list channels
//   slack://channels/{name}/messages       - last 50 messages
//   slack://channels/{name}/pinned         - pinned items
//   slack://channels/{name}/members        - channel members
//   slack://users                          - all users
//   slack://search/{query}                 - search messages
//
// Auth: SLACK_TOKEN env var (Bot User OAuth Token).
// Required scopes: channels:read, channels:history, users:read, pins:read, search:read.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
)

var (
	token      string
	channelMap map[string]string // name → id
	channelMu  sync.Mutex
)

func slackAPI(method string, params url.Values) (json.RawMessage, error) {
	u := "https://slack.com/api/" + method
	if params != nil {
		u += "?" + params.Encode()
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var envelope struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	json.Unmarshal(body, &envelope)
	if !envelope.OK {
		return nil, fmt.Errorf("slack %s: %s", method, envelope.Error)
	}
	return json.RawMessage(body), nil
}

// resolveChannel maps channel name to ID. Caches on first call.
func resolveChannel(name string) (string, error) {
	channelMu.Lock()
	defer channelMu.Unlock()

	if channelMap == nil {
		channelMap = make(map[string]string)
		data, err := slackAPI("conversations.list", url.Values{
			"types": {"public_channel,private_channel"},
			"limit": {"1000"},
		})
		if err != nil {
			return "", err
		}
		var result struct {
			Channels []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"channels"`
		}
		json.Unmarshal(data, &result)
		for _, ch := range result.Channels {
			channelMap[ch.Name] = ch.ID
		}
	}

	if id, ok := channelMap[name]; ok {
		return id, nil
	}
	// Treat as raw channel ID if not found by name
	return name, nil
}

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "slack://channels":
		return readChannels()
	case uri == "slack://users":
		return readUsers()
	case strings.HasPrefix(uri, "slack://search/"):
		return readSearch(strings.TrimPrefix(uri, "slack://search/"))
	case strings.HasPrefix(uri, "slack://channels/"):
		return readChannelResource(strings.TrimPrefix(uri, "slack://channels/"))
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

func readChannels() (mcpserve.ReadResult, error) {
	data, err := slackAPI("conversations.list", url.Values{
		"types": {"public_channel,private_channel"},
		"limit": {"200"},
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Channels []json.RawMessage `json:"channels"`
	}
	json.Unmarshal(data, &result)

	type slimChannel struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		Topic     string `json:"topic,omitempty"`
		Members   int    `json:"num_members"`
		IsPrivate bool   `json:"is_private"`
	}
	var channels []slimChannel
	for _, raw := range result.Channels {
		var ch struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Topic struct {
				Value string `json:"value"`
			} `json:"topic"`
			NumMembers int  `json:"num_members"`
			IsPrivate  bool `json:"is_private"`
		}
		json.Unmarshal(raw, &ch)
		channels = append(channels, slimChannel{
			ID: ch.ID, Name: ch.Name, Topic: ch.Topic.Value,
			Members: ch.NumMembers, IsPrivate: ch.IsPrivate,
		})
	}
	if channels == nil {
		channels = []slimChannel{}
	}

	out, _ := json.MarshalIndent(channels, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readUsers() (mcpserve.ReadResult, error) {
	data, err := slackAPI("users.list", nil)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Members []json.RawMessage `json:"members"`
	}
	json.Unmarshal(data, &result)

	type slimUser struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		RealName string `json:"real_name"`
		Status   string `json:"status,omitempty"`
		IsBot    bool   `json:"is_bot"`
	}
	var users []slimUser
	for _, raw := range result.Members {
		var u struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			RealName string `json:"real_name"`
			IsBot    bool   `json:"is_bot"`
			Profile  struct {
				StatusText string `json:"status_text"`
			} `json:"profile"`
		}
		json.Unmarshal(raw, &u)
		users = append(users, slimUser{
			ID: u.ID, Name: u.Name, RealName: u.RealName,
			Status: u.Profile.StatusText, IsBot: u.IsBot,
		})
	}
	if users == nil {
		users = []slimUser{}
	}

	out, _ := json.MarshalIndent(users, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readChannelResource(path string) (mcpserve.ReadResult, error) {
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return mcpserve.ReadResult{}, fmt.Errorf("invalid channel path: %s", path)
	}

	name := parts[0]
	suffix := parts[1]

	channelID, err := resolveChannel(name)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	switch suffix {
	case "messages":
		return readMessages(channelID)
	case "pinned":
		return readPinned(channelID)
	case "members":
		return readMembers(channelID)
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown channel resource: %s", suffix)
	}
}

func readMessages(channelID string) (mcpserve.ReadResult, error) {
	data, err := slackAPI("conversations.history", url.Values{
		"channel": {channelID},
		"limit":   {"50"},
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Messages []json.RawMessage `json:"messages"`
	}
	json.Unmarshal(data, &result)

	type slimMsg struct {
		User string `json:"user"`
		Text string `json:"text"`
		TS   string `json:"ts"`
	}
	var messages []slimMsg
	for _, raw := range result.Messages {
		var m slimMsg
		json.Unmarshal(raw, &m)
		messages = append(messages, m)
	}
	if messages == nil {
		messages = []slimMsg{}
	}

	out, _ := json.MarshalIndent(messages, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readPinned(channelID string) (mcpserve.ReadResult, error) {
	data, err := slackAPI("pins.list", url.Values{
		"channel": {channelID},
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Items []json.RawMessage `json:"items"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Items, "", "  ")
	if result.Items == nil {
		out = []byte("[]")
	}
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readMembers(channelID string) (mcpserve.ReadResult, error) {
	data, err := slackAPI("conversations.members", url.Values{
		"channel": {channelID},
		"limit":   {"200"},
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Members []string `json:"members"`
	}
	json.Unmarshal(data, &result)

	out, _ := json.MarshalIndent(result.Members, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func readSearch(query string) (mcpserve.ReadResult, error) {
	data, err := slackAPI("search.messages", url.Values{
		"query": {query},
		"count": {"20"},
	})
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var result struct {
		Messages struct {
			Matches []json.RawMessage `json:"matches"`
		} `json:"messages"`
	}
	json.Unmarshal(data, &result)

	type slimMatch struct {
		Text    string `json:"text"`
		User    string `json:"username"`
		Channel string `json:"channel_name"`
		TS      string `json:"ts"`
	}
	var matches []slimMatch
	for _, raw := range result.Messages.Matches {
		var m struct {
			Text    string `json:"text"`
			User    string `json:"username"`
			Channel struct {
				Name string `json:"name"`
			} `json:"channel"`
			TS string `json:"ts"`
		}
		json.Unmarshal(raw, &m)
		matches = append(matches, slimMatch{
			Text: m.Text, User: m.User, Channel: m.Channel.Name, TS: m.TS,
		})
	}
	if matches == nil {
		matches = []slimMatch{}
	}

	out, _ := json.MarshalIndent(matches, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func main() {
	token = os.Getenv("SLACK_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-slack: SLACK_TOKEN env var required")
		os.Exit(1)
	}

	srv := mcpserve.New("mcpfs-slack", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "slack://channels", Name: "channels",
		Description: "List all channels", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "slack://users", Name: "users",
		Description: "All workspace users", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "slack://channels/{name}/messages", Name: "messages",
		Description: "Last 50 messages in channel", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "slack://channels/{name}/pinned", Name: "pinned",
		Description: "Pinned items in channel", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "slack://channels/{name}/members", Name: "members",
		Description: "Channel members", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "slack://search/{query}", Name: "search",
		Description: "Search messages", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-slack: %v\n", err)
		os.Exit(1)
	}
}
