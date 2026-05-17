package main

import "github.com/smm-h/strictcli/go/strictcli"

func main() {
	app := strictcli.NewApp("saferm", version, "AI-first safe rm replacement",
		strictcli.WithEnvPrefix("SAFERM"),
	)

	app.GlobalFlag(strictcli.BoolFlag("verbose", "Enable verbose output"))

	registerDeleteCmd(app)
	registerUndeleteCmd(app)
	registerListCmd(app)
	registerPurgeCmd(app)
	registerInfoCmd(app)

	app.Run()
}
