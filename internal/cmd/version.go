package cmd

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/andrewheberle/simplecommand"
	"github.com/bep/simplecobra"
	"github.com/prometheus/common/version"
)

// The version of the receiver is held by github.com/prometheus/common/version,
// which the version command and the onmsgrpc_build_info metric both read.
//
// The version is taken from the module version Go records in the binary,
// rather than set by -X linker flags, so it cannot be broken by a package
// being moved, as happened when the flags of releases set a variable that no
// longer existed. Releases are built from the module proxy, so they record
// the version they were tagged with, as does go install of a version, and
// local builds record a pseudo-version from git.
//
// The revision is read by github.com/prometheus/common/version from the VCS
// information Go records in local builds. Releases have none, as they are
// built from the module proxy, so their revision is set with -X.
func init() {
	info, ok := debug.ReadBuildInfo()
	version.Version = moduleVersion(info, ok)
}

// moduleVersion returns the version of the main module in info, without its
// leading "v", or "dev" if info has no version, as for a local build outside
// git
func moduleVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "dev"
	}

	v := info.Main.Version
	if v == "" || v == "(devel)" {
		return "dev"
	}

	return strings.TrimPrefix(v, "v")
}

// versionString returns the version information printed for the program
// name
func versionString(name string) string {
	return fmt.Sprintf("%s version: %s (revision: %s)", name, version.Version, version.GetRevision())
}

type versionCommand struct {
	*simplecommand.Command
}

func (c *versionCommand) Run(ctx context.Context, cd *simplecobra.Commandeer, args []string) error {
	fmt.Println(versionString(cd.Root.Command.Name()))

	return nil
}
