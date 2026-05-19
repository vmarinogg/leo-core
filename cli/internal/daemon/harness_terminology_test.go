package daemon_test

import (
	"reflect"
	"testing"

	"github.com/momhq/mom/cli/internal/daemon"
)

func TestProjectRegistrationUsesHarnessesField(t *testing.T) {
	typ := reflect.TypeOf(daemon.RegistryEntry{})
	if _, ok := typ.FieldByName("Harnesses"); !ok {
		t.Fatal("ProjectRegistration should expose Harnesses, not Runtimes")
	}
	if _, ok := typ.FieldByName("Runtimes"); ok {
		t.Fatal("ProjectRegistration still exposes legacy Runtimes field")
	}
}
