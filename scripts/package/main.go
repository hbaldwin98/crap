package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/hbaldwin98/crap/internal/buildinfo"
)

var archiveTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

func main() {
	version := flag.String("version", "", "release version without a v prefix")
	revision := flag.String("revision", "", "full source revision")
	output := flag.String("output", "dist", "output directory")
	flag.Parse()

	if err := run(*version, *revision, *output); err != nil {
		fmt.Fprintln(os.Stderr, "package:", err)
		os.Exit(1)
	}
}

func run(version, revision, output string) error {
	if version == "" || revision == "" {
		return fmt.Errorf("version and revision are required")
	}
	if version != buildinfo.Version {
		return fmt.Errorf("release version %q does not match buildinfo.Version %q", version, buildinfo.Version)
	}
	if len(revision) != 40 {
		return fmt.Errorf("revision must be a full 40-character commit SHA")
	}
	if _, err := hex.DecodeString(revision); err != nil {
		return fmt.Errorf("revision must be a hexadecimal commit SHA")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		return fmt.Errorf("unsupported native architecture %s", runtime.GOARCH)
	}

	if err := os.MkdirAll(output, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	work, err := os.MkdirTemp("", "crap-release-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(work)

	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	files := []string{"crap" + extension, "crap-mutate" + extension}
	packages := []string{"./cmd/crap", "./cmd/crap-mutate"}
	ldflags := strings.Join([]string{
		"-s", "-w",
		"-X", "github.com/hbaldwin98/crap/internal/buildinfo.Version=" + version,
		"-X", "github.com/hbaldwin98/crap/internal/buildinfo.Revision=" + revision,
		"-X", "github.com/hbaldwin98/crap/internal/buildinfo.Modified=false",
	}, " ")
	for i, name := range files {
		destination := filepath.Join(work, name)
		command := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", destination, packages[i])
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		command.Env = append(os.Environ(), "CGO_ENABLED=1")
		if err := command.Run(); err != nil {
			return fmt.Errorf("build %s: %w", name, err)
		}
		versionCommand := exec.Command(destination, "--version")
		actual, err := versionCommand.Output()
		if err != nil {
			return fmt.Errorf("check %s version: %w", name, err)
		}
		if strings.TrimSpace(string(actual)) != version {
			return fmt.Errorf("%s reports version %q", name, strings.TrimSpace(string(actual)))
		}
	}
	licenseFiles, err := stageLicenses(work, packages)
	if err != nil {
		return err
	}
	files = append(files, licenseFiles...)

	base := fmt.Sprintf("crap_v%s_%s_%s", version, runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(output, base+".tar.gz")
	if runtime.GOOS == "windows" {
		archive = filepath.Join(output, base+".zip")
	}
	if err := writeArchive(archive, work, files); err != nil {
		return err
	}
	fmt.Println(archive)
	return nil
}

type listedPackage struct {
	Module *listedModule
}

type listedModule struct {
	Path    string
	Dir     string
	Main    bool
	Replace *listedModule
}

func stageLicenses(work string, packages []string) ([]string, error) {
	if err := copyPath(filepath.Join(work, "LICENSE"), "LICENSE"); err != nil {
		return nil, fmt.Errorf("stage project license: %w", err)
	}
	names := []string{"LICENSE"}

	command := exec.Command("go", append([]string{"list", "-deps", "-json"}, packages...)...)
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("list dependency licenses: %w", err)
	}
	command.Stderr = os.Stderr
	if err := command.Start(); err != nil {
		return nil, fmt.Errorf("list dependency licenses: %w", err)
	}

	modules := make(map[string]listedModule)
	decoder := json.NewDecoder(output)
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			command.Process.Kill()
			command.Wait()
			return nil, fmt.Errorf("decode dependency list: %w", err)
		}
		if pkg.Module != nil && !pkg.Module.Main {
			modules[pkg.Module.Path] = *pkg.Module
		}
	}
	if err := command.Wait(); err != nil {
		return nil, fmt.Errorf("list dependency licenses: %w", err)
	}

	modulePaths := make([]string, 0, len(modules))
	for modulePath := range modules {
		modulePaths = append(modulePaths, modulePath)
	}
	sort.Strings(modulePaths)
	for _, modulePath := range modulePaths {
		module := modules[modulePath]
		source := module
		if module.Replace != nil {
			source = *module.Replace
		}
		legalFiles, err := moduleLegalFiles(source.Dir)
		if err != nil {
			return nil, fmt.Errorf("find licenses for %s: %w", modulePath, err)
		}
		if len(legalFiles) == 0 {
			return nil, fmt.Errorf("dependency %s has no root license or notice file", modulePath)
		}
		for _, sourcePath := range legalFiles {
			name := filepath.ToSlash(filepath.Join("licenses", modulePath, filepath.Base(sourcePath)))
			if err := copyPath(filepath.Join(work, filepath.FromSlash(name)), sourcePath); err != nil {
				return nil, fmt.Errorf("stage license for %s: %w", modulePath, err)
			}
			names = append(names, name)
		}
	}
	return names, nil
}

func moduleLegalFiles(directory string) ([]string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, entry := range entries {
		name := strings.ToUpper(entry.Name())
		if entry.Type().IsRegular() && (strings.HasPrefix(name, "LICENSE") || strings.HasPrefix(name, "COPYING") || strings.HasPrefix(name, "NOTICE")) {
			files = append(files, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func copyPath(destination, source string) error {
	contents, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	return os.WriteFile(destination, contents, 0o644)
}

func writeArchive(destination, root string, names []string) error {
	sort.Strings(names)
	file, err := os.Create(destination)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			file.Close()
		}
	}()

	if strings.HasSuffix(destination, ".zip") {
		err = writeZip(file, root, names)
	} else {
		err = writeTarGzip(file, root, names)
	}
	if err != nil {
		return fmt.Errorf("write archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	closed = true
	return nil
}

func writeZip(output io.Writer, root string, names []string) error {
	archive := zip.NewWriter(output)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(archiveTime)
		header.SetMode(archiveMode(name))
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		if err := copyFile(writer, filepath.Join(root, name)); err != nil {
			return err
		}
	}
	return archive.Close()
}

func writeTarGzip(output io.Writer, root string, names []string) error {
	gz, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return err
	}
	gz.Header.ModTime = archiveTime
	gz.Header.OS = 255
	archive := tar.NewWriter(gz)
	for _, name := range names {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			return err
		}
		header := &tar.Header{Name: name, Mode: int64(archiveMode(name)), Size: info.Size(), ModTime: archiveTime, Typeflag: tar.TypeReg}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if err := copyFile(archive, filepath.Join(root, name)); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return gz.Close()
}

func archiveMode(name string) os.FileMode {
	base := filepath.Base(name)
	if base == "crap" || base == "crap.exe" || base == "crap-mutate" || base == "crap-mutate.exe" {
		return 0o755
	}
	return 0o644
}

func copyFile(destination io.Writer, source string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = io.Copy(destination, file)
	return err
}
