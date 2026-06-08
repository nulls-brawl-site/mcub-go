#!/usr/bin/env python3
"""
test_dispatch.py - Verify command handlers and module instances are stored
correctly after instantiating modules through the mcub_compat bridge.
"""

import sys
import types
import os
import asyncio

# ---- Stub _mcub_go --------------------------------------------------------
_mcub_go = types.ModuleType("_mcub_go")
_mcub_go.get_prefix = lambda: "."
_mcub_go.get_version = lambda: "1.0.0"
_mcub_go.get_start_time = lambda: 0.0
_mcub_go.get_me = lambda *a: {"id": 123, "first_name": "Test", "username": "test", "is_premium": False}
for _fn in (
    "log_info", "log_debug", "log_warn", "log_error", "restart_kernel",
    "db_get", "db_set", "db_delete", "save_module_config",
    "edit_message", "reply_message", "delete_message", "send_message",
    "get_entity", "get_reply_message", "download_media", "get_message",
):
    setattr(_mcub_go, _fn, lambda *a: None)
sys.modules["_mcub_go"] = _mcub_go

# ---- Load mcub_compat compat layer ----------------------------------------
compat_path = os.path.join(os.path.dirname(__file__), "internal", "pybridge", "mcub_compat.py")
compat_ns = {"__name__": "__compat__"}
with open(compat_path) as f:
    exec(f.read(), compat_ns)

ModuleBase = compat_ns["ModuleBase"]
KernelProxy = compat_ns["KernelProxy"]
_RegisterProxy = compat_ns["_RegisterProxy"]
_command_handlers = compat_ns["_command_handlers"]
_module_instances = compat_ns["_module_instances"]
Event = compat_ns["Event"]

modules_dir = os.path.join(os.path.dirname(__file__), "modules")

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

def load_module_class(fname):
    """Load a .py module and return (display_name, class_or_None, mod_ns)."""
    mod_path = os.path.join(modules_dir, fname)
    mod_name = fname[:-3]
    mod_ns = {"__name__": mod_name, "__file__": mod_path}
    with open(mod_path) as f:
        exec(f.read(), mod_ns)

    found_class = None
    for v in mod_ns.values():
        if isinstance(v, type):
            try:
                if issubclass(v, ModuleBase) and v is not ModuleBase:
                    found_class = v
                    break
            except TypeError:
                pass
    return mod_name, found_class, mod_ns


def instantiate(found_class, display_name):
    """Instantiate a ModuleBase subclass with a KernelProxy."""
    kp = KernelProxy(session_id=0, module_name=display_name)
    instance = found_class(kp, None, None)
    _module_instances[display_name] = instance
    _module_instances[display_name.replace("-", "_")] = instance
    return instance


# ---------------------------------------------------------------------------
# Test: class-based modules (tester)
# ---------------------------------------------------------------------------

print("=== Dispatch Test ===\n")
results = {"ok": [], "failed": []}


def check(name, cond, msg=""):
    if cond:
        print(f"  PASS  {name}")
        results["ok"].append(name)
    else:
        print(f"  FAIL  {name}" + (f": {msg}" if msg else ""))
        results["failed"].append((name, msg))


# --- tester (class-based) --------------------------------------------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("tester.py")
assert cls is not None, "tester.py: no ModuleBase subclass found"
display_name = getattr(cls, "name", mod_name)
instance = instantiate(cls, display_name)

expected_cmds = {"ping", "logs", "freezing", "teaser"}
for cmd in expected_cmds:
    check(
        f"tester: handler for '{cmd}' stored",
        (display_name, cmd) in _command_handlers,
        f"missing key ({display_name!r}, {cmd!r}) in _command_handlers",
    )

check(
    "tester: instance stored by display name",
    display_name in _module_instances,
)
check(
    "tester: strings is _SimpleStrings",
    type(instance.strings).__name__ == "_SimpleStrings",
    f"got {type(instance.strings).__name__}",
)
check(
    "tester: strings call returns string",
    isinstance(instance.strings("ping"), str),
)
check(
    "tester: strings subscript returns string",
    isinstance(instance.strings["ping"], str),
)
check(
    "tester: strings.get with nonexistent key returns default",
    instance.strings.get("nonexistent_key_xyz_12345", "default") == "default",
)
check(
    "tester: log is _Logger",
    type(instance.log).__name__ == "_Logger",
    f"got {type(instance.log).__name__}",
)

# --- settings (class-based) ------------------------------------------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("settings.py")
assert cls is not None, "settings.py: no ModuleBase subclass found"
display_name = getattr(cls, "name", mod_name)
instance = instantiate(cls, display_name)

expected_cmds = {"lang", "mcub", "setprefix"}
for cmd in expected_cmds:
    check(
        f"settings: handler for '{cmd}' stored",
        (display_name, cmd) in _command_handlers,
    )

# --- loader (class-based with self.strings["key"] usage) -------------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("loader.py")
assert cls is not None, "loader.py: no ModuleBase subclass found"
display_name = getattr(cls, "name", mod_name)
instance = instantiate(cls, display_name)

expected_cmds = {"dlm", "reload", "iload"}
for cmd in expected_cmds:
    check(
        f"loader: handler for '{cmd}' stored",
        (display_name, cmd) in _command_handlers,
    )
check(
    "loader: strings subscript returns string",
    isinstance(instance.strings["no_modules_catalog"], str) and len(instance.strings["no_modules_catalog"]) > 0,
)

# --- command.py (class-based with self.strings["key"] usage) ---------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("command.py")
display_name = getattr(cls, "name", mod_name) if cls else mod_name
if cls:
    instance = instantiate(cls, display_name)
    check("command: instantiation OK", True)
    check(
        "command: strings subscript returns string",
        isinstance(instance.strings["start_init_error"], str) and len(instance.strings["start_init_error"]) > 0,
    )
else:
    check("command: no class found (skip)", True)

# --- terminal (function-based) ---------------------------------------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("terminal.py")
register_fn = ns.get("register")
assert callable(register_fn), "terminal.py: no register() function found"
display_name = ns.get("name", mod_name)

kp = KernelProxy(session_id=0, module_name=display_name)

# Patch asyncio.create_task to a no-op so modules that schedule startup
# coroutines (asyncio.create_task) don't crash outside an event loop.
_orig_create_task = asyncio.create_task
class _FakeTask:
    def cancel(self): pass
    def done(self): return True
def _noop_create_task(coro, *args, **kwargs):
    try:
        if hasattr(coro, "close"):
            coro.close()
    except Exception:
        pass
    return _FakeTask()
asyncio.create_task = _noop_create_task
try:
    register_fn(kp)
except Exception:
    pass
finally:
    asyncio.create_task = _orig_create_task

_module_instances[display_name] = kp

expected_cmds = {"t", "tkill", "ti"}
for cmd in expected_cmds:
    check(
        f"terminal: handler for '{cmd}' stored",
        (display_name, cmd) in _command_handlers,
    )
check("terminal: instance stored", display_name in _module_instances)

# --- async dispatch (call a handler with a synthetic Event) ----------------
_command_handlers.clear()
_module_instances.clear()

mod_name, cls, ns = load_module_class("tester.py")
display_name = getattr(cls, "name", mod_name)
instance = instantiate(cls, display_name)

# Verify the handler is callable and is a coroutine function
handler = _command_handlers.get((display_name, "ping"))
check(
    "dispatch: ping handler is awaitable",
    handler is not None and asyncio.iscoroutinefunction(handler),
    f"handler={handler}",
)

if handler is not None:
    event = Event(
        session_id=0,
        chat_id=123456,
        msg_id=1,
        text=".ping",
        sender_id=999,
    )
    try:
        asyncio.run(handler(event))
        check("dispatch: ping handler ran without exception", True)
    except Exception as e:
        check("dispatch: ping handler ran without exception", False, str(e))

# ---------------------------------------------------------------------------
print(f"\n=== Results: {len(results['ok'])} PASS, {len(results['failed'])} FAIL ===")
if results["failed"]:
    print("\nFailed:")
    for name, msg in results["failed"]:
        print(f"  {name}: {msg}")
