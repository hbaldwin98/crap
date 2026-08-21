package reportcontract

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

type ToolIdentity struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Revision string `json:"revision,omitempty"`
	Modified bool   `json:"modified"`
}

type Coordinates struct {
	LineBase       int    `json:"lineBase"`
	ColumnBase     int    `json:"columnBase"`
	ColumnEncoding string `json:"columnEncoding"`
	End            string `json:"end"`
}

type FileFingerprint struct {
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256"`
}

type GitFingerprint struct {
	BaseCommit string `json:"baseCommit,omitempty"`
	HeadCommit string `json:"headCommit,omitempty"`
	MergeBase  string `json:"mergeBase,omitempty"`
}

type Fingerprints struct {
	Sources      []FileFingerprint `json:"sources"`
	Coverage     *FileFingerprint  `json:"coverage"`
	NativeReport *FileFingerprint  `json:"nativeReport"`
	Git          *GitFingerprint   `json:"git"`
	ConfigSHA256 string            `json:"configSha256"`
}

func DefaultCoordinates() Coordinates {
	return Coordinates{LineBase: 1, ColumnBase: 1, ColumnEncoding: "utf-8-bytes", End: "exclusive"}
}

func NativeCoordinates() Coordinates {
	return Coordinates{LineBase: 1, ColumnBase: 1, ColumnEncoding: "engine-native", End: "exclusive"}
}

func SHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Fingerprint length-prefixes fields so different field boundaries cannot collide.
func Fingerprint(fields ...string) string {
	hash := sha256.New()
	var size [8]byte
	for _, field := range fields {
		binary.BigEndian.PutUint64(size[:], uint64(len(field)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(field))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func JSONFingerprint(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("fingerprint JSON: %v", err))
	}
	return SHA256(data)
}

func SortFiles(files []FileFingerprint) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		return files[i].SHA256 < files[j].SHA256
	})
}
