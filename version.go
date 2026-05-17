package main

import "runtime/debug"

// version is set by ldflags at build time: -X main.version=x.y.z
var version = ""

func init() {
	if version == "" || version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		} else {
			version = "dev"
		}
	}
}
