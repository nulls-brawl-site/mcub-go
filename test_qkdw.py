#!/usr/bin/env python3
"""Test that QkDw.py (PollenGen) loads without errors via mcub_compat."""
import sys
import os
import types as _types

# Stub _mcub_go extension
_mcub_go_stub = _types.ModuleType("_mcub_go")
_mcub_go_stub.log_info = lambda msg: None
_mcub_go_stub.log_debug = lambda msg: None
_mcub_go_stub.log_warn = lambda msg: None
_mcub_go_stub.log_error = lambda msg: None
_mcub_go_stub.db_get = lambda m, k: None
_mcub_go_stub.db_set = lambda m, k, v: None
_mcub_go_stub.db_delete = lambda m, k: None
_mcub_go_stub.get_prefix = lambda: "."
_mcub_go_stub.get_version = lambda: "1.0.0"
_mcub_go_stub.get_start_time = lambda: 0.0
_mcub_go_stub.restart_kernel = lambda: None
_mcub_go_stub.save_module_config = lambda m, c: None
sys.modules["_mcub_go"] = _mcub_go_stub

# Load mcub_compat.py
_compat_path = os.path.join(os.path.dirname(__file__), "internal", "pybridge", "mcub_compat.py")
with open(_compat_path) as _f:
    exec(compile(_f.read(), _compat_path, "exec"), {"__file__": _compat_path, "__name__": "mcub_compat"})

_passed = []
_failed = []

def ok(name):
    _passed.append(name)
    print(f"  PASS  {name}")

def fail(name, reason):
    _failed.append((name, reason))
    print(f"  FAIL  {name}: {reason}")

# ============================================================
# Test: load QkDw.py (PollenGen) as a Hikka module
# ============================================================

MODULE_PATH = "/tmp/opencode/QkDw.py"

try:
    import importlib, importlib.util

    # Load the module as hikka.modules.PollenGen (relative imports need a parent pkg)
    pkg_name = "hikka.modules"
    mod_name = "hikka.modules.PollenGen"

    # Ensure hikka.modules package exists
    if pkg_name not in sys.modules:
        pkg = _types.ModuleType(pkg_name)
        pkg.__path__ = []
        pkg.__package__ = "hikka"
        sys.modules[pkg_name] = pkg
        sys.modules["hikka"].modules = pkg

    spec = importlib.util.spec_from_file_location(
        mod_name,
        MODULE_PATH,
        submodule_search_locations=[],
    )
    spec.submodule_search_locations = None

    mod = importlib.util.module_from_spec(spec)
    mod.__package__ = pkg_name
    sys.modules[mod_name] = mod

    spec.loader.exec_module(mod)

    ok("QkDw.py: loaded without import errors")

    # Check that PollenGenMod class is present
    if hasattr(mod, "PollenGenMod"):
        ok("QkDw.py: PollenGenMod class found")

        # Try to instantiate it
        try:
            inst = mod.PollenGenMod()
            ok("QkDw.py: PollenGenMod() instantiated successfully")
            if hasattr(inst, "strings") and inst.strings.get("name") == "PollenGen":
                ok("QkDw.py: strings['name'] == 'PollenGen'")
            else:
                fail("QkDw.py: strings name", f"got {getattr(inst, 'strings', {}).get('name')!r}")
            if hasattr(inst, "config"):
                ok("QkDw.py: .config attribute present")
            else:
                fail("QkDw.py: .config", "missing")
        except Exception as e:
            fail("QkDw.py: instantiation", str(e))
    else:
        fail("QkDw.py: PollenGenMod", "class not found in module")

except Exception as e:
    import traceback
    fail("QkDw.py: load", traceback.format_exc())

print()
print(f"=== Results: {len(_passed)} passed, {len(_failed)} failed ===")
if _failed:
    print("\nFailed:")
    for name, reason in _failed:
        print(f"  {name}:\n    {reason}")
    sys.exit(1)
else:
    print("QkDw.py loads correctly!")
