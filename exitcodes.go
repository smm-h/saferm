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
// existing script's comparison. A new code therefore takes the next number
// after the highest one in use -- ExitContention took 8 while 4 stayed empty,
// so an old script comparing against 5, 6 or 7 still means what it meant.
//
// ExitContention is deliberately distinct from ExitDatabase: it says another
// process held the archive database's write lock for longer than saferm's whole
// retry budget, which is a "try again later", not a broken archive.
const (
	ExitSuccess      = 0
	ExitGeneral      = 1
	ExitUsage        = 2
	ExitFileNotFound = 3
	ExitDatabase     = 5
	ExitArchive      = 6
	ExitConflict     = 7
	ExitContention   = 8
)
