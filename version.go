package main

import (
	"runtime/debug"
	"strings"
)

// version is set by ldflags at build time: -X main.version=x.y.z
var version = ""

func init() {
	if version == "" || version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = strings.TrimPrefix(info.Main.Version, "v")
		} else {
			version = "dev"
		}
	}
}
