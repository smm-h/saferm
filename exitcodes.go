package main

// Exit codes saferm returns. The generated exit-code table in the readme is
// parsed straight out of this const block, so a code declared here is a code
// saferm promises to produce -- deadcode_test.go holds that promise by failing
// on any constant no command returns.
//
// 4 is absent on purpose. It was ExitPermission, which no code path ever
// returned; a permission failure reaches the caller as ExitArchive or
// ExitDatabase, depending on which layer met it. The number is not reused and
// the codes below it are not renumbered: that would change the meaning of every
// existing script's comparison.
const (
	ExitSuccess      = 0
	ExitGeneral      = 1
	ExitUsage        = 2
	ExitFileNotFound = 3
	ExitDatabase     = 5
	ExitArchive      = 6
	ExitConflict     = 7
)
