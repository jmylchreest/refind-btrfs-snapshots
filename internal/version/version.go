// Package version exposes the build-time version metadata shared by every
// binary in this repo. Values are populated via -ldflags -X at release time;
// the defaults below apply for ad-hoc `go build` invocations.
package version

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns Version, or "dev" if a release script accidentally injected
// an empty value (which the bare default wouldn't cover).
func String() string {
	if Version != "" {
		return Version
	}
	return "dev"
}
