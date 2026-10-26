package buildinfo

import "testing"

func TestDefaultVersion(t *testing.T) {
	t.Parallel()
	if Version != "dev" {
		t.Fatalf("version %q", Version)
	}
}
