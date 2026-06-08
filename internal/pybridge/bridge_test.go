package pybridge_test

import (
	"os"
	"testing"

	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
)

func TestNewBridge(t *testing.T) {
	b, err := pybridge.NewBridge()
	if err != nil {
		t.Skipf("Python bridge not available: %v", err)
	}
	defer b.Finalize()

	if b == nil {
		t.Fatal("bridge is nil")
	}
}

func TestLoadPyModule(t *testing.T) {
	b, err := pybridge.NewBridge()
	if err != nil {
		t.Skipf("Python bridge not available: %v", err)
	}
	defer b.Finalize()

	// Create a simple test module.
	tmpFile := "/tmp/test_bridge_module.py"
	code := `
from core.lib.loader.module_base import ModuleBase, command

class TestBridgeModule(ModuleBase):
    name = "test_bridge"
    version = "1.0.0"

    @command("testcmd", doc_en="test command")
    async def cmd_test(self, event):
        await event.edit("test ok")
`
	if err := os.WriteFile(tmpFile, []byte(code), 0644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer os.Remove(tmpFile)

	pyMod, err := b.LoadPyModule(tmpFile)
	if err != nil {
		t.Fatalf("LoadPyModule failed: %v", err)
	}
	if pyMod == nil {
		t.Fatal("nil pyMod")
	}
	if pyMod.Name == "" {
		t.Fatal("empty module name")
	}
	if len(pyMod.Commands) == 0 {
		t.Fatal("no commands registered")
	}
	if pyMod.Commands[0].Name != "testcmd" {
		t.Errorf("expected testcmd, got %s", pyMod.Commands[0].Name)
	}
}
