package installer

import (
	"fmt"
	"io"
	"strings"
)

func configureClaude(runner Runner, claude string, paths map[string]string, stdout io.Writer) error {
	for _, name := range serverNames {
		output, err := runner.Output(claude, claudeAddArgs(name, paths[name])...)
		if err == nil {
			fmt.Fprintf(stdout, "Added Claude Code server: %s\n", name)
			continue
		}
		if strings.Contains(strings.ToLower(string(output)), "already exists") {
			fmt.Fprintf(stdout, "Claude Code server already exists; left unchanged: %s\n", name)
			continue
		}
		return fmt.Errorf("configure Claude Code server %s: %w", name, commandError(output, err))
	}
	return nil
}

func claudeAddArgs(name, path string) []string {
	return []string{"mcp", "add", "--scope", "user", "--transport", "stdio", name, "--", path, "mcp"}
}
