package main

import (
	"sort"
	"testing"

	"github.com/smm-h/strictcli/go/strictcli"
	"github.com/smm-h/stricttest/go/hygiene"
)

// classification pins every command's strictcli effect classification and its
// `consequential` declaration. The table is the specification: changing a row
// here is the deliberate edit that a reclassification requires, and registering
// a command without adding its row fails the test.
//
// Reasoning, per the strictcli effects contract (§1: `read_only` means no
// user-visible or consequential mutation; §8.1: `consequential` means the act
// is worth interrupting someone for):
//
//   - delete -- mutating, NOT consequential. It moves a file out of the working
//     tree, which is a real mutation, but the file lands in the archive and
//     `undelete` brings it back. Recoverable by construction, so it runs bare:
//     `saferm delete --description "why" <files>` is the complete invocation
//     from a script or an agent.
//   - undelete -- mutating, NOT consequential. It is delete's inverse. It
//     writes to the restoration path but refuses to clobber an existing file
//     without --force-overwrite, so nothing is lost by running it.
//   - list -- read_only. It queries the database and prints a table. A
//     read_only command cannot be consequential at all.
//   - purge -- mutating AND consequential. The one saferm operation with no way
//     back: it destroys the archived content, and after it nothing in the tool
//     can recover the file. That is saferm's whole purpose inverted, which is
//     precisely what the confirm protocol exists to interrupt.
//   - info -- read_only. It reads one record and prints its metadata.
//
// The `config` group is registered by the framework (WithConfig), not by
// saferm, and its five commands are pinned here for the same reason as the
// rest: they are part of the CLI surface saferm ships, and a framework upgrade
// that reclassifies one of them changes what saferm's users get.
var classification = map[string]struct {
	effect        string
	consequential bool
}{
	"delete":      {strictcli.EffectMutating, false},
	"undelete":    {strictcli.EffectMutating, false},
	"list":        {strictcli.EffectReadOnly, false},
	"purge":       {strictcli.EffectMutating, true},
	"info":        {strictcli.EffectReadOnly, false},
	"config.show": {strictcli.EffectReadOnly, false},
	"config.set":  {strictcli.EffectMutating, false},
	"config.path": {strictcli.EffectReadOnly, false},
	"config.edit": {strictcli.EffectMutating, false},
	"config.init": {strictcli.EffectMutating, false},
}

// collectCommands flattens the app's command tree into dotted paths.
func collectCommands(app *strictcli.App) map[string]*strictcli.Command {
	out := map[string]*strictcli.Command{}
	for name, cmd := range app.Commands() {
		out[name] = cmd
	}
	var walk func(prefix string, g *strictcli.Group)
	walk = func(prefix string, g *strictcli.Group) {
		for name, cmd := range g.Commands {
			out[prefix+name] = cmd
		}
		for name, sub := range g.Groups {
			walk(prefix+name+".", sub)
		}
	}
	for name, g := range app.Groups() {
		walk(name+".", g)
	}
	return out
}

func TestCommandClassificationIsPinned(t *testing.T) {
	hygiene.Isolate(t, hygiene.Preserve(hygiene.GoPath, hygiene.GoModCache, hygiene.GoCache))

	cmds := collectCommands(newApp())

	var names []string
	for name := range cmds {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		want, ok := classification[name]
		if !ok {
			t.Errorf("command %q is registered but has no pinned classification; add a row to classification with the reasoning", name)
			continue
		}
		if cmds[name].Effect != want.effect {
			t.Errorf("command %q: effect = %q, pinned %q", name, cmds[name].Effect, want.effect)
		}
		if cmds[name].Consequential != want.consequential {
			t.Errorf("command %q: consequential = %v, pinned %v", name, cmds[name].Consequential, want.consequential)
		}
	}
	for name := range classification {
		if _, ok := cmds[name]; !ok {
			t.Errorf("classification pins %q but no such command is registered", name)
		}
	}
}

// TestEveryCommandSupportsDryRun pins the other half of the effects
// declaration. saferm previews every command honestly: `delete`, `undelete` and
// `purge` mint their mutations on the effects handle so a dry run renders a
// would-do log naming each path, and `purge --dry-run` additionally renders the
// table of what it would destroy. Nothing here is unrepresentable ahead of
// time, so no command declares WithDryRunUnsupported -- and declaring it would
// be illegal on the two read_only commands anyway.
func TestEveryCommandSupportsDryRun(t *testing.T) {
	hygiene.Isolate(t, hygiene.Preserve(hygiene.GoPath, hygiene.GoModCache, hygiene.GoCache))

	for name, cmd := range collectCommands(newApp()) {
		if !cmd.DryRunSupported {
			t.Errorf("command %q declares --dry-run unsupported; saferm previews everything", name)
		}
	}
}

// TestNoReservedGlobalFlagNames guards the framework's reserved quartet at the
// app level. Command-level flags are covered implicitly: strictcli panics at
// registration for a reserved name anywhere, and newApp() registers everything.
func TestNoReservedGlobalFlagNames(t *testing.T) {
	hygiene.Isolate(t, hygiene.Preserve(hygiene.GoPath, hygiene.GoModCache, hygiene.GoCache))

	reserved := map[string]bool{
		"dry-run":               true,
		"approve-consequential": true,
		"quiet":                 true,
		"verbose":               true,
	}
	for _, f := range newApp().GlobalFlags() {
		if reserved[f.Name] {
			t.Errorf("global flag %q is reserved by the framework", f.Name)
		}
	}
}
