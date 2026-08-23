package analysis

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/hbaldwin98/crap/internal/rootauth"
)

const discoveryExampleLimit = 5

type discoveryResult struct {
	files      []string
	exclusions map[string]int
	examples   map[string][]string
}

type sourceCollector struct {
	ctx              context.Context
	root             string
	includeTests     bool
	includeGenerated bool
	seen             map[string]bool
	exclusionSeen    map[string]map[string]bool
	result           discoveryResult
	authorization    *rootauth.Root
	gitRoot          string
	gitPatterns      []gitignore.Pattern
	loadedGitIgnore  map[string]bool
	gitignore        gitignore.Matcher
	crapignore       gitignore.Matcher
	excludes         gitignore.Matcher
}

func findSourceFiles(root string, paths, excludes []string, includeTests, includeGenerated bool, authorization *rootauth.Root) (discoveryResult, error) {
	return findSourceFilesContext(context.Background(), root, paths, excludes, includeTests, includeGenerated, authorization)
}

func findSourceFilesContext(ctx context.Context, root string, paths, excludes []string, includeTests, includeGenerated bool, authorization *rootauth.Root) (discoveryResult, error) {
	if err := ctx.Err(); err != nil {
		return discoveryResult{}, err
	}
	root, err := canonicalAnalysisRoot(root, authorization)
	if err != nil {
		return discoveryResult{}, err
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	crapPatterns, err := readIgnoreFileContext(ctx, filepath.Join(root, ".crapignore"), nil, authorization)
	if err != nil {
		return discoveryResult{}, err
	}
	excludePatterns := make([]gitignore.Pattern, 0, len(excludes))
	for _, pattern := range excludes {
		if strings.TrimSpace(pattern) == "" || strings.HasPrefix(pattern, "!") {
			return discoveryResult{}, fmt.Errorf("exclude pattern %q must select a path and cannot be negated", pattern)
		}
		excludePatterns = append(excludePatterns, gitignore.ParsePattern(portableIgnoreEscapes(pattern), nil))
	}
	gitRoot := ignoreRoot(root, authorization)
	collector := sourceCollector{
		ctx: ctx, root: root, includeTests: includeTests, includeGenerated: includeGenerated,
		seen: make(map[string]bool), exclusionSeen: make(map[string]map[string]bool), authorization: authorization,
		gitRoot: gitRoot, loadedGitIgnore: make(map[string]bool), crapignore: gitignore.NewMatcher(crapPatterns), excludes: gitignore.NewMatcher(excludePatterns),
		result: discoveryResult{files: make([]string, 0), exclusions: make(map[string]int), examples: make(map[string][]string)},
	}
	if err := collector.loadGitIgnoreChain(root); err != nil {
		return discoveryResult{}, err
	}
	for _, requested := range paths {
		if err := ctx.Err(); err != nil {
			return discoveryResult{}, err
		}
		if err := collector.add(requested); err != nil {
			return discoveryResult{}, err
		}
	}
	sort.Strings(collector.result.files)
	return collector.result, nil
}

func canonicalAnalysisRoot(root string, authorization *rootauth.Root) (string, error) {
	if authorization != nil {
		return authorization.Path(), nil
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve root links: %w", err)
	}
	return canonical, nil
}

func readIgnoreFile(path string, domain []string, authorization *rootauth.Root) ([]gitignore.Pattern, error) {
	return readIgnoreFileContext(context.Background(), path, domain, authorization)
}

func readIgnoreFileContext(ctx context.Context, path string, domain []string, authorization *rootauth.Root) ([]gitignore.Pattern, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	// Git does not follow ignore-file symlinks. Applying the same rule also
	// prevents custom ignore files from reading outside an authorized root.
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, nil
	}
	if authorization != nil {
		path, err = authorization.Existing(path)
		if err != nil {
			return nil, fmt.Errorf("authorize %s: %w", filepath.Base(path), err)
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", filepath.Base(path), err)
	}
	defer file.Close()
	return scanIgnorePatterns(ctx, file, path, domain)
}

func scanIgnorePatterns(ctx context.Context, file *os.File, path string, domain []string) ([]gitignore.Pattern, error) {
	patterns := make([]gitignore.Pattern, 0)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := scanner.Text()
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(portableIgnoreEscapes(line), domain))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	return patterns, nil
}

func portableIgnoreEscapes(pattern string) string {
	if strings.HasPrefix(pattern, `\#`) {
		return "[#]" + pattern[2:]
	}
	if strings.HasPrefix(pattern, `\!`) {
		return "[!]" + pattern[2:]
	}
	if strings.HasSuffix(pattern, `\ `) {
		return strings.TrimSuffix(pattern, `\ `) + "[ ]"
	}
	return pattern
}

func (collector *sourceCollector) add(requested string) error {
	if err := collector.ctx.Err(); err != nil {
		return err
	}
	path := requested
	if !filepath.IsAbs(path) {
		path = filepath.Join(collector.root, path)
	}
	canonical, err := collector.canonical(path, requested)
	if err != nil {
		return err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", requested, err)
	}
	if !info.IsDir() && !supportedSource(canonical) {
		return fmt.Errorf("explicit source %s has an unsupported extension", requested)
	}
	if collector.excluded(canonical, info.IsDir(), true) {
		return nil
	}
	if info.IsDir() {
		if err := collector.loadGitIgnoreChain(canonical); err != nil {
			return err
		}
		if err := filepath.WalkDir(canonical, collector.visit); err != nil {
			return fmt.Errorf("walk %s: %w", requested, err)
		}
		return nil
	}
	return collector.collect(canonical, true)
}

func (collector *sourceCollector) visit(path string, entry fs.DirEntry, walkErr error) error {
	if err := collector.ctx.Err(); err != nil {
		return err
	}
	if walkErr != nil {
		return walkErr
	}
	if path == collector.root {
		return nil
	}
	if collector.excluded(path, entry.IsDir(), false) {
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	}
	if entry.IsDir() {
		return collector.loadGitIgnore(path)
	}
	if !entry.IsDir() {
		return collector.collect(path, false)
	}
	return nil
}

func (collector *sourceCollector) collect(path string, explicit bool) error {
	if err := collector.ctx.Err(); err != nil {
		return err
	}
	if !supportedSource(path) {
		return nil
	}
	canonical, err := collector.canonical(path, path)
	if err != nil {
		return err
	}
	if !explicit && !collector.includeTests && isTestSource(canonical) {
		collector.record("test", canonical)
		return nil
	}
	if !explicit && !collector.includeGenerated && isGeneratedSource(canonical) {
		collector.record("generated", canonical)
		return nil
	}
	if !collector.seen[canonical] {
		collector.seen[canonical] = true
		collector.result.files = append(collector.result.files, canonical)
	}
	return nil
}

func (collector *sourceCollector) canonical(path, displayed string) (string, error) {
	if collector.authorization != nil {
		canonical, err := collector.authorization.Existing(path)
		if err != nil {
			return "", fmt.Errorf("authorize source %s: %w", displayed, err)
		}
		return canonical, nil
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve source %s: %w", displayed, err)
	}
	if !pathWithinRoot(collector.root, canonical) {
		return "", fmt.Errorf("source %s is outside analysis root", displayed)
	}
	return canonical, nil
}

func (collector *sourceCollector) excluded(path string, isDir, explicit bool) bool {
	relative, err := filepath.Rel(collector.root, path)
	if err != nil || relative == "." {
		return false
	}
	portable := normalizePath(relative)
	rootComponents := strings.Split(portable, "/")
	if collector.excludes.Match(rootComponents, isDir) {
		collector.record("explicit", path)
		return true
	}
	if explicit {
		return false
	}
	if builtInIgnoredDirectory(portable, isDir) {
		collector.record("built-in", path)
		return true
	}
	if collector.crapignore.Match(rootComponents, isDir) {
		collector.record("crapignore", path)
		return true
	}
	gitRelative, gitErr := filepath.Rel(collector.gitRoot, path)
	if gitErr == nil && collector.gitignore != nil && collector.gitignore.Match(strings.Split(normalizePath(gitRelative), "/"), isDir) {
		collector.record("gitignore", path)
		return true
	}
	return false
}

func (collector *sourceCollector) record(reason, path string) {
	relative, err := filepath.Rel(collector.root, path)
	if err != nil {
		return
	}
	portable := normalizePath(relative)
	if collector.exclusionSeen[reason] == nil {
		collector.exclusionSeen[reason] = make(map[string]bool)
	}
	if collector.exclusionSeen[reason][portable] {
		return
	}
	collector.exclusionSeen[reason][portable] = true
	collector.result.exclusions[reason]++
	examples := append(collector.result.examples[reason], portable)
	sort.Strings(examples)
	if len(examples) > discoveryExampleLimit {
		examples = examples[:discoveryExampleLimit]
	}
	collector.result.examples[reason] = examples
}

func ignoreRoot(root string, authorization *rootauth.Root) string {
	// An MCP request root is an authorization boundary. Do not inspect parent
	// directories to discover repository-level ignore files outside it.
	if authorization != nil {
		return root
	}
	for candidate := root; ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return root
		}
	}
}

func (collector *sourceCollector) loadGitIgnoreChain(directory string) error {
	relative, err := filepath.Rel(collector.gitRoot, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil
	}
	current := collector.gitRoot
	if err := collector.loadGitIgnore(current); err != nil {
		return err
	}
	if relative == "." {
		return nil
	}
	for _, component := range strings.Split(relative, string(filepath.Separator)) {
		if err := collector.ctx.Err(); err != nil {
			return err
		}
		current = filepath.Join(current, component)
		if err := collector.loadGitIgnore(current); err != nil {
			return err
		}
	}
	return nil
}

func (collector *sourceCollector) loadGitIgnore(directory string) error {
	if collector.loadedGitIgnore[directory] {
		return nil
	}
	collector.loadedGitIgnore[directory] = true
	relative, err := filepath.Rel(collector.gitRoot, directory)
	if err != nil {
		return err
	}
	domain := []string(nil)
	if relative != "." {
		domain = strings.Split(normalizePath(relative), "/")
	}
	patterns, err := readIgnoreFileContext(collector.ctx, filepath.Join(directory, ".gitignore"), domain, collector.authorization)
	if err != nil {
		return err
	}
	if len(patterns) > 0 {
		collector.gitPatterns = append(collector.gitPatterns, patterns...)
		collector.gitignore = gitignore.NewMatcher(collector.gitPatterns)
	}
	return nil
}

func (result discoveryResult) metadata() DiscoveryMetadata {
	reasons := make([]string, 0, len(result.exclusions))
	for reason := range result.exclusions {
		reasons = append(reasons, reason)
	}
	sort.Strings(reasons)
	metadata := DiscoveryMetadata{Selected: len(result.files), Exclusions: make([]DiscoveryExclusion, 0, len(reasons))}
	for _, reason := range reasons {
		examples := append([]string(nil), result.examples[reason]...)
		sort.Strings(examples)
		metadata.Exclusions = append(metadata.Exclusions, DiscoveryExclusion{Reason: reason, Count: result.exclusions[reason], Examples: examples})
	}
	return metadata
}

func builtInIgnoredDirectory(relative string, isDir bool) bool {
	if !isDir {
		return false
	}
	base := strings.ToLower(filepath.Base(filepath.FromSlash(relative)))
	if base == ".git" || base == "node_modules" || base == ".next" || base == "bin" || base == "obj" || base == "testdata" {
		return true
	}
	return !strings.Contains(relative, "/") && (base == "vendor" || base == "dist" || base == "build" || base == "coverage")
}

func supportedSource(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".cs" || extension == ".go" || extension == ".ts" || extension == ".tsx"
}

func isTestSource(path string) bool {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".go" {
		return strings.HasSuffix(strings.ToLower(path), "_test.go")
	}
	return (extension == ".ts" || extension == ".tsx") && isTypeScriptTest(path)
}

func isGeneratedSource(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, suffix := range []string{".d.ts", ".g.cs", ".g.i.cs", ".generated.cs", ".designer.cs", ".assemblyinfo.cs", ".assemblyattributes.cs", ".generated.ts", ".generated.tsx", ".gen.ts", ".gen.tsx"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return strings.HasPrefix(base, "temporarygeneratedfile_")
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func isTypeScriptTest(path string) bool {
	base := strings.ToLower(filepath.Base(path))
	return strings.HasSuffix(base, ".spec.ts") || strings.HasSuffix(base, ".test.ts") ||
		strings.HasSuffix(base, ".spec.tsx") || strings.HasSuffix(base, ".test.tsx")
}
