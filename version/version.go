package version

import (
	"fmt"
	"runtime"
)

// These variables are set at build time using ldflags
var (
	Version   = "dev"      // Version number
	GitCommit = "unknown"  // Git commit hash
	BuildDate = "unknown"  // Build date
	GoVersion = runtime.Version()
)

// Info contains version information
type Info struct {
	Version   string `json:"version"`
	GitCommit string `json:"git_commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	Arch      string `json:"arch"`
	OS        string `json:"os"`
}

// Get returns version information
func Get() Info {
	return Info{
		Version:   Version,
		GitCommit: GitCommit,
		BuildDate: BuildDate,
		GoVersion: GoVersion,
		Arch:      runtime.GOARCH,
		OS:        runtime.GOOS,
	}
}

// String returns a formatted version string
func (i Info) String() string {
	result := fmt.Sprintf("devx version %s", i.Version)
	
	if i.GitCommit != "unknown" && i.GitCommit != "" {
		if len(i.GitCommit) > 7 {
			result += fmt.Sprintf(" (%s)", i.GitCommit[:7])
		} else {
			result += fmt.Sprintf(" (%s)", i.GitCommit)
		}
	}
	
	if i.BuildDate != "unknown" && i.BuildDate != "" {
		result += fmt.Sprintf(" built %s", i.BuildDate)
	}
	
	return result
}

// Detailed returns detailed version information
func (i Info) Detailed() string {
	return fmt.Sprintf(`devx version information:
  Version:    %s
  Git commit: %s
  Build date: %s
  Go version: %s
  OS/Arch:    %s/%s`,
		i.Version,
		i.GitCommit,
		i.BuildDate,
		i.GoVersion,
		i.OS,
		i.Arch,
	)
}