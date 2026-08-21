package rootauth

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Policy struct {
	defaultRoot string
	allowed     []string
}

type Root struct {
	path string
}

func New(defaultRoot string, allowRoots ...string) (*Policy, error) {
	if defaultRoot == "" {
		var err error
		defaultRoot, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("determine MCP root: %w", err)
		}
	}
	configured := append([]string{defaultRoot}, allowRoots...)
	allowed := make([]string, 0, len(configured))
	seen := make(map[string]bool)
	for _, path := range configured {
		canonical, err := canonicalExisting(path)
		if err != nil {
			return nil, fmt.Errorf("authorize MCP root %q: %w", path, err)
		}
		info, err := os.Stat(canonical)
		if err != nil {
			return nil, fmt.Errorf("authorize MCP root %q: %w", path, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("authorize MCP root %q: not a directory", path)
		}
		key := filepath.Clean(canonical)
		if !seen[key] {
			seen[key] = true
			allowed = append(allowed, key)
		}
	}
	defaultCanonical, err := canonicalExisting(defaultRoot)
	if err != nil {
		return nil, err
	}
	return &Policy{defaultRoot: defaultCanonical, allowed: allowed}, nil
}

func (policy *Policy) Root(requested string) (*Root, error) {
	if policy == nil {
		return nil, fmt.Errorf("MCP root policy is required")
	}
	if requested == "" {
		requested = policy.defaultRoot
	} else if !filepath.IsAbs(requested) {
		requested = filepath.Join(policy.defaultRoot, requested)
	}
	canonical, err := canonicalExisting(requested)
	if err != nil {
		return nil, fmt.Errorf("authorize requested root %q: %w", requested, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, fmt.Errorf("authorize requested root %q: %w", requested, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("requested root is not a directory: %s", requested)
	}
	for _, allowed := range policy.allowed {
		if contains(allowed, canonical) {
			return &Root{path: canonical}, nil
		}
	}
	return nil, fmt.Errorf("requested root is outside authorized MCP roots: %s", requested)
}

func (root *Root) Path() string { return root.path }

func (root *Root) Existing(path string) (string, error) {
	candidate := root.resolve(path)
	if !contains(root.path, candidate) {
		return "", fmt.Errorf("path is outside authorized root: %s", path)
	}
	canonical, err := canonicalExisting(candidate)
	if err != nil {
		return "", fmt.Errorf("authorize path %q: %w", path, err)
	}
	if !contains(root.path, canonical) {
		return "", fmt.Errorf("path resolves outside authorized root: %s", path)
	}
	return canonical, nil
}

func (root *Root) Future(path string) (string, error) {
	candidate := root.resolve(path)
	if !contains(root.path, candidate) {
		return "", fmt.Errorf("path is outside authorized root: %s", path)
	}
	current := candidate
	missing := make([]string, 0)
	for {
		_, err := os.Lstat(current)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("authorize path %q: %w", path, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("authorize path %q: no existing parent", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
	canonical, err := canonicalExisting(current)
	if err != nil {
		return "", fmt.Errorf("authorize path %q: %w", path, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		canonical = filepath.Join(canonical, missing[index])
	}
	canonical = filepath.Clean(canonical)
	if !contains(root.path, canonical) {
		return "", fmt.Errorf("path resolves outside authorized root: %s", path)
	}
	return canonical, nil
}

func (root *Root) resolve(path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(root.path, path))
}

func canonicalExisting(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Abs(canonical)
}

func contains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
