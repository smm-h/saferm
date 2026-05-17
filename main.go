package main

import "github.com/smm-h/strictcli/go/strictcli"

func main() {
	app := strictcli.NewApp("saferm", version, "AI-first safe rm replacement",
		strictcli.WithEnvPrefix("SAFERM"),
	)
	app.Run()
}
