package extension

import (
	"os"
	"testing"
)

// TestAlwaysEnabledExtensionLoadsWithoutDBRow proves an ExtensionFactory
// registered with AlwaysEnabled: true loads during Init() even though no DB
// row for it ever has Enable set to true (there is no UI anywhere that sets
// it) - this is the escape hatch plan 039 relies on to wire up the TUN
// elevation extension unconditionally.
func TestAlwaysEnabledExtensionLoadsWithoutDBRow(t *testing.T) {
	// Same technique as db.chdirToFreshDataDir: a plain, uncleaned
	// os.MkdirTemp dir, not t.TempDir(), because the DB layer caches an
	// open file handle for the rest of the process and t.TempDir()'s
	// automatic RemoveAll cleanup fails on Windows while the file is open.
	dir, err := os.MkdirTemp("", "hiddify-ext-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Chdir(dir)

	const id = "test/always-enabled-extension"
	factory := ExtensionFactory{
		Id:            id,
		Title:         "Always Enabled Test Extension",
		Description:   "test-only",
		Builder:       func() Extension { return &Base[struct{}]{} },
		AlwaysEnabled: true,
	}
	if err := RegisterExtension(factory); err != nil {
		t.Fatalf("RegisterExtension: %v", err)
	}
	t.Cleanup(func() {
		delete(allExtensionsMap, id)
		delete(enabledExtensionsMap, id)
	})

	svc := &extensionService{}
	if err := svc.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if _, ok := enabledExtensionsMap[id]; !ok {
		t.Fatalf("expected AlwaysEnabled extension %s to be loaded without any DB row ever setting Enable=true", id)
	}
}
