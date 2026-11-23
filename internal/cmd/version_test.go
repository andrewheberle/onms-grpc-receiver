package cmd

import (
	"runtime/debug"
	"testing"

	"github.com/prometheus/common/version"
)

func TestModuleVersion(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"release", &debug.BuildInfo{Main: debug.Module{Version: "v0.15.0"}}, true, "0.15.0"},
		{"pseudo-version", &debug.BuildInfo{Main: debug.Module{Version: "v0.14.2-0.20261123010203-abcdef123456"}}, true, "0.14.2-0.20261123010203-abcdef123456"},
		{"dirty", &debug.BuildInfo{Main: debug.Module{Version: "v0.14.2-0.20261123010203-abcdef123456+dirty"}}, true, "0.14.2-0.20261123010203-abcdef123456+dirty"},
		{"devel", &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}, true, "dev"},
		{"empty", &debug.BuildInfo{}, true, "dev"},
		{"no build info", nil, false, "dev"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := moduleVersion(tt.info, tt.ok); got != tt.want {
				t.Errorf("moduleVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestVersionString checks the version command prints the version set from
// the build information and the revision, the same values as the
// onmsgrpc_build_info metric
func TestVersionString(t *testing.T) {
	if version.Version == "" {
		t.Fatal("version.Version is empty, want it set from the build information")
	}

	want := "onms-grpc-receiver version: " + version.Version + " (revision: " + version.GetRevision() + ")"
	if got := versionString("onms-grpc-receiver"); got != want {
		t.Errorf("versionString() = %q, want %q", got, want)
	}
}
