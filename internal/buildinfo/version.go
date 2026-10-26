// Package buildinfo reports the engine version linked into this binary.
package buildinfo

// Version is the engine version that server metadata reports.
// The image build sets it with -ldflags -X.
// A binary built without that flag reports dev.
var Version = "dev"
