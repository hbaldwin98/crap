package buildinfo

import (
	"runtime/debug"
	"strconv"

	"github.com/hbaldwin98/crap/internal/reportcontract"
)

var (
	Version  = "0.2.0"
	Revision string
	Modified string
)

func Tool(name string) reportcontract.ToolIdentity {
	version, revision, modified := CurrentVersion(), Revision, Modified == "true"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				if revision == "" {
					revision = setting.Value
				}
			case "vcs.modified":
				if Modified == "" {
					modified, _ = strconv.ParseBool(setting.Value)
				}
			}
		}
	}
	return reportcontract.ToolIdentity{Name: name, Version: version, Revision: revision, Modified: modified}
}

func CurrentVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok && Version == "0.2.0" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return Version
}
