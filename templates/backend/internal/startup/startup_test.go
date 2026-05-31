package startup

import (
	"context"
	"testing"

	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/database"
	"github.com/nmxmxh/ovasabi_foundation/server-kit/go/hermes"
)

func TestStartupScaffoldPackageCompiles(t *testing.T) {
	// This smoke test keeps startup wiring in the unit-test set while allowing
	// projects to customize dependency initialization helpers.
}

func TestInitDependenciesBootstrapsMandatoryHermes(t *testing.T) {
	runtimeDB, err := database.Connect(context.Background(), "", database.DriverMemory)
	if err != nil {
		t.Fatalf("connect memory runtime: %v", err)
	}
	projected, err := hermes.WrapRuntimeStore(runtimeDB, hermes.RuntimeStoreOptions{
		IndexedFields:      []string{"state"},
		MaxRecordsPerScope: 16,
		MaxBytesPerScope:   1 << 20,
	})
	if err != nil {
		t.Fatalf("wrap memory runtime: %v", err)
	}
	if projected.Store() == nil {
		t.Fatalf("Hermes store was not initialized")
	}
	if err := projected.HermesHealth(context.Background()); err != nil {
		t.Fatalf("database runtime store is not Hermes-projected")
	}
}
