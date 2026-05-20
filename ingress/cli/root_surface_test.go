package cli

import "testing"

func TestRootCommandSurface_RemovesObsoleteOperationalCommands(t *testing.T) {
	for _, name := range []string{"reindex", "validate", "diagnose", "tour", "sweep", "bootstrap"} {
		cmd, _, err := rootCmd.Find([]string{name})
		if err == nil && cmd != nil && cmd.Name() == name {
			t.Fatalf("%q should not be registered on the public CLI surface", name)
		}
	}
}
