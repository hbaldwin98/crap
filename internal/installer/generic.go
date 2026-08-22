package installer

import "fmt"

type genericServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func prepareGeneric(path string, paths map[string]string) (preparedFile, error) {
	servers := make(map[string]any, len(serverNames))
	for _, name := range serverNames {
		servers[name] = genericServer{Command: paths[name], Args: []string{"mcp"}}
	}
	file, err := prepareConfig(path, "mcpServers", servers)
	if err != nil {
		return preparedFile{}, fmt.Errorf("prepare generic MCP config %s: %w", path, err)
	}
	return file, nil
}
