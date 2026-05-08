package cmd

import (
	"testing"
)

func TestMapCmdExists(t *testing.T) {
	if mapCmd == nil {
		t.Fatal("mapCmd is nil")
	}
	if mapCmd.Use != "map" {
		t.Fatalf("expected mapCmd.Use == %q, got %q", "map", mapCmd.Use)
	}
}

func TestMapCmdRegisteredInRoot(t *testing.T) {
	found := false
	for _, sub := range rootCmd.Commands() {
		if sub.Use == "map" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("mapCmd (Use=map) not registered in rootCmd")
	}
}

func TestBootstrapAliasRemovedFromRoot(t *testing.T) {
	for _, sub := range rootCmd.Commands() {
		if sub.Use == "bootstrap" {
			t.Fatal("bootstrap alias should not be registered in rootCmd")
		}
	}
}
