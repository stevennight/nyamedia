package version

import (
	"fmt"
	"strings"
)

var (
	Version   = "0.1.0-dev"
	Commit    = ""
	BuildDate = ""
)

type BuildInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildDate string `json:"build_date,omitempty"`
}

func Info() BuildInfo {
	return BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildDate: BuildDate,
	}
}

func String() string {
	parts := []string{Version}
	if strings.TrimSpace(Commit) != "" {
		parts = append(parts, "commit="+Commit)
	}
	if strings.TrimSpace(BuildDate) != "" {
		parts = append(parts, "built="+BuildDate)
	}
	return strings.Join(parts, " ")
}

func Print(name string) string {
	return fmt.Sprintf("%s %s", name, String())
}

func IsVersionCommand(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-version" || args[0] == "version")
}
