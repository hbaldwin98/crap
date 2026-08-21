package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreCanceledAnalysisDiscoveryAndCoverage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	root := t.TempDir()
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	if report, err := analyzer.AnalyzeContext(ctx, Options{Root: root}); !errors.Is(err, context.Canceled) || report.SchemaVersion != "" {
		t.Fatalf("analysis = %#v, %v", report, err)
	}
	if _, err := findSourceFilesContext(ctx, root, nil, nil, false, false, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("discovery error = %v", err)
	}
	if _, err := loadCoverageContext(ctx, "ignored", root); !errors.Is(err, context.Canceled) {
		t.Fatalf("coverage error = %v", err)
	}
}

type blockingGitRunner struct{ started chan struct{} }

func (runner blockingGitRunner) Output(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	close(runner.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestGitRunnerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	root := t.TempDir()
	started := make(chan struct{})
	errorsChannel := make(chan error, 1)
	go func() {
		_, err := gitChangedLinesContext(ctx, root, "main", nil, blockingGitRunner{started: started})
		errorsChannel <- err
	}()
	<-started
	cancel()
	err := <-errorsChannel
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestParallelAnalysisIsDeterministicAndSelectsLowestFileError(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"c.go", "a.go", "b.go"} {
		source := "package sample\nfunc " + strings.TrimSuffix(name, ".go") + "() { if true {} }\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	analyzer, err := NewAnalyzer()
	if err != nil {
		t.Fatal(err)
	}
	defer analyzer.Close()
	var baseline []byte
	for iteration := 0; iteration < 10; iteration++ {
		report, err := analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		if iteration > 0 && string(data) != string(baseline) {
			t.Fatal("parallel report changed between runs")
		}
		baseline = data
	}
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package sample\nfunc {"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = analyzer.Analyze(Options{Root: root, Paths: []string{"."}, CRAPThreshold: 30})
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "a.go")) {
		t.Fatalf("error = %v, want lowest sorted file", err)
	}
}

func TestBinaryRangeSearchBoundaries(t *testing.T) {
	ranges := []lineRange{{Start: 2, End: 4}, {Start: 8, End: 10}}
	for _, test := range []struct {
		start, end int
		want       bool
	}{{2, 2, true}, {4, 4, true}, {1, 2, false}, {4, 5, false}, {8, 10, true}, {10, 11, false}, {5, 7, false}} {
		if got := rangesContainSpan(ranges, test.start, test.end); got != test.want {
			t.Errorf("rangesContainSpan(%d, %d) = %v, want %v", test.start, test.end, got, test.want)
		}
	}
	root := t.TempDir()
	file := filepath.Join(root, "work.go")
	changes := changedFiles{RepositoryRoot: root, Files: map[string][]lineRange{"work.go": ranges}}
	for _, test := range []struct {
		start, end int
		want       bool
	}{{1, 1, false}, {1, 2, true}, {4, 8, true}, {5, 7, false}, {10, 10, true}, {11, 12, false}} {
		if got := changes.intersects(file, test.start, test.end); got != test.want {
			t.Errorf("intersects(%d, %d) = %v, want %v", test.start, test.end, got, test.want)
		}
	}
}
