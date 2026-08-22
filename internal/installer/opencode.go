package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tailscale/hujson"
)

type openCodeServer struct {
	Type    string   `json:"type"`
	Command []string `json:"command"`
	Enabled bool     `json:"enabled"`
}

func openCodePaths(system System, home string) ([]string, error) {
	if path := system.Getenv("OPENCODE_CONFIG"); path != "" {
		absolute, err := filepath.Abs(path)
		return []string{absolute}, err
	}
	directory := system.Getenv("OPENCODE_CONFIG_DIR")
	if directory == "" {
		if xdg := system.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			directory = filepath.Join(xdg, "opencode")
		} else {
			directory = filepath.Join(home, ".config", "opencode")
		}
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve OpenCode config directory: %w", err)
	}
	jsonPath := filepath.Join(directory, "opencode.json")
	jsoncPath := filepath.Join(directory, "opencode.jsonc")
	var paths []string
	for _, path := range []string{jsonPath, jsoncPath} {
		if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		paths = append(paths, jsonPath)
	}
	return paths, nil
}

func prepareOpenCode(system System, home string, paths map[string]string) ([]preparedFile, error) {
	targets, err := openCodePaths(system, home)
	if err != nil {
		return nil, err
	}
	servers := make(map[string]any, len(serverNames))
	for name, server := range openCodeServers(paths) {
		servers[name] = server
	}
	files := make([]preparedFile, 0, len(targets))
	for _, path := range targets {
		file, err := prepareConfig(path, "mcp", servers)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		files = append(files, file)
	}
	return files, nil
}

func openCodeServers(paths map[string]string) map[string]openCodeServer {
	servers := make(map[string]openCodeServer, len(serverNames))
	for _, name := range serverNames {
		servers[name] = openCodeServer{Type: "local", Command: []string{paths[name], "mcp"}, Enabled: true}
	}
	return servers
}

func prepareConfig(path, serverKey string, servers map[string]any) (preparedFile, error) {
	if err := validateDestination(path); err != nil {
		return preparedFile{}, err
	}
	contents, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		contents, err = json.MarshalIndent(map[string]any{serverKey: servers}, "", "  ")
		if err != nil {
			return preparedFile{}, err
		}
		return preparedFile{path: path, contents: append(contents, '\n')}, nil
	}
	if err != nil {
		return preparedFile{}, fmt.Errorf("read config: %w", err)
	}
	original := append([]byte(nil), contents...)
	if len(bytes.TrimSpace(contents)) == 0 {
		contents = []byte("{}")
	}
	updated, err := patchConfig(contents, serverKey, servers)
	if err != nil {
		return preparedFile{}, err
	}
	return preparedFile{path: path, contents: updated, existed: true, original: original}, nil
}

func patchConfig(contents []byte, serverKey string, servers map[string]any) ([]byte, error) {
	value, err := hujson.Parse(contents)
	if err != nil {
		return nil, err
	}
	if err := rejectDuplicateMembers(value.Value, ""); err != nil {
		return nil, err
	}
	standard := value.Clone()
	standard.Standardize()
	var root map[string]json.RawMessage
	if err := json.Unmarshal(standard.Pack(), &root); err != nil || root == nil {
		return nil, fmt.Errorf("top level must be an object")
	}
	operations := []map[string]any{{"op": "add", "path": "/" + serverKey, "value": servers}}
	if raw, ok := root[serverKey]; ok {
		var existing map[string]json.RawMessage
		if err := json.Unmarshal(raw, &existing); err != nil || existing == nil {
			return nil, fmt.Errorf("top-level %s must be an object", serverKey)
		}
		operations = operations[:0]
		for _, name := range serverNames {
			operations = append(operations, map[string]any{"op": "add", "path": "/" + serverKey + "/" + name, "value": servers[name]})
		}
	}
	patch, err := json.Marshal(operations)
	if err != nil {
		return nil, err
	}
	if err := value.Patch(patch); err != nil {
		return nil, err
	}
	value.Format()
	return value.Pack(), nil
}

func rejectDuplicateMembers(value hujson.ValueTrimmed, path string) error {
	switch value := value.(type) {
	case *hujson.Object:
		seen := make(map[string]bool, len(value.Members))
		for _, member := range value.Members {
			name := member.Name.Value.(hujson.Literal).String()
			if seen[name] {
				location := path
				if location == "" {
					location = "/"
				}
				return fmt.Errorf("duplicate object member %q at %s", name, location)
			}
			seen[name] = true
			if err := rejectDuplicateMembers(member.Value.Value, path+"/"+escapeJSONPointer(name)); err != nil {
				return err
			}
		}
	case *hujson.Array:
		for index, element := range value.Elements {
			if err := rejectDuplicateMembers(element.Value, fmt.Sprintf("%s/%d", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

func escapeJSONPointer(name string) string {
	name = strings.ReplaceAll(name, "~", "~0")
	return strings.ReplaceAll(name, "/", "~1")
}
