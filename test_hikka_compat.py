#!/usr/bin/env python3
"""Comprehensive test for Hikka compatibility layer in mcub_compat.py"""
import sys
import os

# Stub out the Go extension so we can test without building
import types as _types

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

# Now load mcub_compat.py via exec
_compat_path = os.path.join(os.path.dirname(__file__), "internal", "pybridge", "mcub_compat.py")
with open(_compat_path) as _f:
    exec(compile(_f.read(), _compat_path, "exec"), {"__file__": _compat_path, "__name__": "mcub_compat"})

# ===========================================================================
# Test helpers
# ===========================================================================
_passed = []
_failed = []

def ok(name):
    _passed.append(name)
    print(f"  PASS  {name}")

def fail(name, reason):
    _failed.append((name, reason))
    print(f"  FAIL  {name}: {reason}")

def check(name, condition, reason=""):
    if condition:
        ok(name)
    else:
        fail(name, reason or "condition was False")

# ===========================================================================
# Pattern 1: from hikka.loader import loader → class Mod(loader.Module)
# ===========================================================================
try:
    from hikka.loader import loader as _ldr1

    class Mod1(_ldr1.Module):
        strings = {"name": "mod1", "_cls_doc": "Test module 1"}

        @_ldr1.command()
        async def testcmd(self, msg):
            pass

    check("P1: loader.Module subclass", issubclass(Mod1, _ldr1.Module))
    check("P1: @loader.command marks method", getattr(Mod1.testcmd, "is_command", False) or getattr(Mod1.testcmd, "__hikka_command__", False))
except Exception as e:
    fail("P1: hikka.loader.loader pattern", str(e))

# ===========================================================================
# Pattern 2: from hikka import loader as ldr → class Mod(ldr.Module)
# ===========================================================================
try:
    from hikka import loader as _ldr2

    class Mod2(_ldr2.Module):
        strings = {"name": "mod2"}

        @_ldr2.command()
        async def testcmd(self, msg):
            pass

    check("P2: hikka.loader.Module subclass", issubclass(Mod2, _ldr2.Module))
except Exception as e:
    fail("P2: from hikka import loader pattern", str(e))

# ===========================================================================
# Pattern 3: from Heroku import loader
# ===========================================================================
try:
    from Heroku import loader as _ldr3

    class Mod3(_ldr3.Module):
        strings = {"name": "mod3"}

        @_ldr3.command()
        async def testcmd(self, msg):
            pass

    check("P3: Heroku loader.Module subclass", issubclass(Mod3, _ldr3.Module))
except Exception as e:
    fail("P3: Heroku import pattern", str(e))

# ===========================================================================
# Pattern 4: with Hikka-style strings (nested locales + _cls_doc)
# ===========================================================================
try:
    from hikka.loader import loader as _ldr4

    class Mod4(_ldr4.Module):
        strings = {
            "_cls_doc": "Module description",
            "cmd_doc": "Command help",
            "en": {"name": "mod4", "greet": "Hello"},
            "ru": {"name": "мод4", "greet": "Привет"},
        }

        @_ldr4.command()
        async def testcmd(self, msg):
            pass

    inst4 = Mod4()
    cls_doc = inst4.strings["_cls_doc"]
    check("P4: strings _cls_doc", cls_doc == "Module description", f"got {cls_doc!r}")
    # en locale lookup
    from mcub_compat import _SimpleStrings
    ss = _SimpleStrings(Mod4.strings, locale="en")
    check("P4: strings en locale", ss["greet"] == "Hello", f"got {ss['greet']!r}")
    ss_ru = _SimpleStrings(Mod4.strings, locale="ru")
    check("P4: strings ru locale", ss_ru["greet"] == "Привет", f"got {ss_ru['greet']!r}")
except Exception as e:
    fail("P4: Hikka strings pattern", str(e))

# ===========================================================================
# Pattern 5: no-arg instantiation
# ===========================================================================
try:
    inst1 = Mod1()
    check("P5: no-arg instantiation works", True)
    check("P5: .db attribute present", hasattr(inst1, "db"))
    check("P5: .allmodules attribute present", hasattr(inst1, "allmodules"))
    check("P5: .allmodules.modules is list", isinstance(inst1.allmodules.modules, list))
except Exception as e:
    fail("P5: no-arg instantiation", str(e))

# ===========================================================================
# Pattern 6: MCUB-style instantiation (kernel, client, register)
# ===========================================================================
try:
    from mcub_compat import KernelProxy, ClientProxy, _RegisterProxy
    check("P6: _RegisterProxy importable from mcub_compat", True)

    kp = KernelProxy(0, "test")
    cp = ClientProxy(0)
    reg = _RegisterProxy("test")
    inst2 = Mod1(kp, cp, reg)
    check("P6: MCUB-style instantiation works", True)
    check("P6: .db attribute present", hasattr(inst2, "db"))
except Exception as e:
    fail("P6: MCUB-style instantiation", str(e))

# ===========================================================================
# Pattern 7: hikka.utils imports
# ===========================================================================
try:
    from hikka.utils import get_args, get_args_raw, get_chat_id, get_entity_id
    check("P7: hikka.utils.get_args", callable(get_args))
    check("P7: hikka.utils.get_args_raw", callable(get_args_raw))
    check("P7: hikka.utils.get_chat_id", callable(get_chat_id))
    check("P7: hikka.utils.get_entity_id", callable(get_entity_id))
except Exception as e:
    fail("P7: hikka.utils imports", str(e))

# ===========================================================================
# Pattern 8: mcub_compat exports _InfiniteLoop
# ===========================================================================
try:
    from mcub_compat import _InfiniteLoop, _AllModulesProxy, _make_bound
    check("P8: _InfiniteLoop importable", True)
    check("P8: _AllModulesProxy importable", True)
    check("P8: _make_bound importable", True)
except Exception as e:
    fail("P8: mcub_compat extra exports", str(e))

# ===========================================================================
# Pattern 9: bot_command, Boolean, String etc. exports
# ===========================================================================
try:
    from mcub_compat import bot_command, Boolean, String, Integer, Float, Choice
    check("P9: bot_command importable", callable(bot_command))
    check("P9: Boolean importable", True)
    check("P9: String importable", True)
    check("P9: Integer importable", True)
    check("P9: Float importable", True)
    check("P9: Choice importable", True)
except Exception as e:
    fail("P9: mcub_compat config type exports", str(e))

# ===========================================================================
# Pattern 10: _AllModulesProxy.lookup
# ===========================================================================
try:
    from mcub_compat import _AllModulesProxy as AMP
    amp = AMP()
    check("P10: _AllModulesProxy.lookup returns None for unknown", amp.lookup("nonexistent") is None)
    check("P10: _AllModulesProxy iterable", hasattr(amp, "__iter__"))
except Exception as e:
    fail("P10: _AllModulesProxy usage", str(e))

# ===========================================================================
# Summary
# ===========================================================================
print()
print(f"=== Results: {len(_passed)} passed, {len(_failed)} failed ===")
if _failed:
    print("\nFailed tests:")
    for name, reason in _failed:
        print(f"  {name}: {reason}")
    sys.exit(1)
else:
    print("All Hikka compat tests passed!")
