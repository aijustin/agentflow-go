package core

import (
	"runtime/debug"
	"sync"
)

var (
	buildInfoOnce sync.Once
	fwVersion     string
	fwCommit      string
)

func loadBuildInfo() {
	buildInfoOnce.Do(func() {
		info, ok := debug.ReadBuildInfo()
		if !ok || info == nil {
			return
		}
		fwVersion = info.Main.Version
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" && setting.Value != "" {
				fwCommit = setting.Value
				if len(fwCommit) > 12 {
					fwCommit = fwCommit[:12]
				}
				break
			}
		}
		if fwVersion == "" || fwVersion == "(devel)" {
			if fwCommit != "" {
				fwVersion = "devel+" + fwCommit
			} else {
				fwVersion = "devel"
			}
		}
	})
}

// FrameworkVersion returns the module version from build info (ldflags / VCS).
func FrameworkVersion() string {
	loadBuildInfo()
	return fwVersion
}

// FrameworkCommit returns the short VCS revision when available.
func FrameworkCommit() string {
	loadBuildInfo()
	return fwCommit
}

// FrameworkBuildFields returns version/commit keys for RunStarted payloads.
func FrameworkBuildFields() map[string]string {
	loadBuildInfo()
	out := map[string]string{}
	if fwVersion != "" {
		out["framework_version"] = fwVersion
	}
	if fwCommit != "" {
		out["framework_commit"] = fwCommit
	}
	return out
}
