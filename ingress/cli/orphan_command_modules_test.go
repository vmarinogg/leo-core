package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCommandPackage_DoesNotKeepObsoleteUnregisteredCommandModules(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not locate cmd package directory")
	}
	cmdDir := filepath.Dir(thisFile)

	for _, name := range []string{"reindex.go", "tour.go", "kb.go", "diagnose.go"} {
		path := filepath.Join(cmdDir, name)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("obsolete unregistered command module still exists: %s", name)
		} else if !os.IsNotExist(err) {
			t.Fatalf("checking %s: %v", name, err)
		}
	}
}
