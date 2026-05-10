package cmd

import (
	"strings"
	"testing"
)

func TestUserFacingCommandTextUsesHarnessTerminology(t *testing.T) {
	commands := map[string]string{
		"watch short":    watchCmd.Short,
		"watch long":     watchCmd.Long,
		"record session": recordCmd.Flags().Lookup("session").Usage,
		"uninstall long": uninstallCmd.Long,
	}
	for name, text := range commands {
		if strings.Contains(strings.ToLower(text), "runtime") {
			t.Fatalf("%s uses runtime terminology: %q", name, text)
		}
	}
}
