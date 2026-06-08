#!/usr/bin/env python3
"""Comprehensive mcub_compat test suite.

Tests:
  1. All 16 system modules load without error
  2. Class-based modules instantiate correctly
  3. Commands are registered in _command_handlers
  4. _SimpleStrings behaves correctly (property override, get() with default)
  5. Simulated command dispatch (async and sync handlers)
  6. Session ID propagation via _mcub_call_command logic
  7. _RegisterProxy command registration (function-based modules)
  8. Pipeline event fields
  9. ModuleConfig API
  10. Stub imports (telethon, core.*, utils.*, core_inline.*, hikka.*)
"""

import sys
import types
import os
import asyncio

# ──────────────────────────────────────────────────────────────
# Setup _mcub_go stub
# ──────────────────────────────────────────────────────────────

_EDIT_CALLS = []  # track edit_message calls for dispatch tests

_stub = types.ModuleType("_mcub_go")
_stub.get_prefix = lambda: "."
_stub.get_version = lambda: "1.0.0"
_stub.get_start_time = lambda: 0.0
_stub.get_me = lambda *a: {"id": 123, "first_name": "Test", "username": "test", "is_premium": False}
_stub.edit_message = lambda sid, cid, mid, txt, pm: _EDIT_CALLS.append((sid, cid, mid, txt))

for _fn in (
    "log_info", "log_debug", "log_warn", "log_error", "restart_kernel",
    "db_get", "db_set", "db_delete", "save_module_config",
    "reply_message", "delete_message", "send_message",
    "get_entity", "get_reply_message", "download_media", "get_message",
):
    setattr(_stub, _fn, lambda *a: None)

sys.modules["_mcub_go"] = _stub

# ──────────────────────────────────────────────────────────────
# Load mcub_compat
# ──────────────────────────────────────────────────────────────

_COMPAT_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "mcub_compat.py")
_MODULES_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "..", "modules")

_compat_ns = {"__name__": "__compat__"}
with open(_COMPAT_PATH) as _f:
    exec(_f.read(), _compat_ns)

ModuleBase         = _compat_ns["ModuleBase"]
KernelProxy        = _compat_ns["KernelProxy"]
ClientProxy        = _compat_ns["ClientProxy"]
_RegisterProxy     = _compat_ns["_RegisterProxy"]
Event              = _compat_ns["Event"]
_SimpleStrings     = _compat_ns["_SimpleStrings"]
ModuleConfig       = _compat_ns["ModuleConfig"]
ConfigValue        = _compat_ns["ConfigValue"]
ValidationError    = _compat_ns["ValidationError"]
_command_handlers  = _compat_ns["_command_handlers"]
_module_instances  = _compat_ns["_module_instances"]
_LANGPACK_DATA     = _compat_ns["_LANGPACK_DATA"]

# ──────────────────────────────────────────────────────────────
# Test harness
# ──────────────────────────────────────────────────────────────

_results = {"pass": [], "fail": []}


def check(name: str, condition: bool, msg: str = "") -> None:
    if condition:
        print(f"  PASS  {name}")
        _results["pass"].append(name)
    else:
        details = f": {msg}" if msg else ""
        print(f"  FAIL  {name}{details}")
        _results["fail"].append((name, msg))


def section(title: str) -> None:
    print(f"\n── {title} {'─' * max(0, 60 - len(title))}")


# ──────────────────────────────────────────────────────────────
# Test 1: Compat layer loaded successfully
# ──────────────────────────────────────────────────────────────
section("Test 1: compat layer boot")

check("ModuleBase defined", ModuleBase is not None)
check("KernelProxy defined", KernelProxy is not None)
check("Event defined", Event is not None)
check("_command_handlers is dict", isinstance(_command_handlers, dict))
check("_module_instances is dict", isinstance(_module_instances, dict))
check("_SimpleStrings defined", _SimpleStrings is not None)

# Stub imports
import telethon
import telethon.events
import core.lib.loader.module_base as _mlm
import core.lib.loader.module_config as _mlmc

check("telethon.Button accessible", hasattr(telethon, "Button"))
check("telethon.events.NewMessage accessible", hasattr(telethon.events, "NewMessage"))
check("core ModuleBase == compat ModuleBase", _mlm.ModuleBase is ModuleBase)
check("core ModuleConfig == compat ModuleConfig", _mlmc.ModuleConfig is ModuleConfig)
check("ValidationError importable", hasattr(_mlmc, "ValidationError"))

from utils import answer as _utils_answer
check("utils.answer importable", _utils_answer is not None)
from core.lib.loader.repository import validate_remote_url
check("validate_remote_url importable", callable(validate_remote_url))
from core.lib.utils.exceptions import CommandConflictError
check("CommandConflictError importable", issubclass(CommandConflictError, Exception))
from core.lib.utils.logger import ErrorFormatter
check("ErrorFormatter importable", ErrorFormatter is not None)
from core_inline.api.inline import make_cb_button
check("make_cb_button importable", make_cb_button is not None)
import loader as hikka_loader
check("hikka loader.Module importable", hasattr(hikka_loader, "Module"))

# ──────────────────────────────────────────────────────────────
# Helper: load module namespace
# ──────────────────────────────────────────────────────────────

def _load_ns(fname):
    path = os.path.join(_MODULES_DIR, fname)
    ns = {"__name__": fname[:-3], "__file__": path}
    with open(path) as f:
        exec(f.read(), ns)
    return ns


def _find_class(ns):
    for v in ns.values():
        if isinstance(v, type):
            try:
                if issubclass(v, ModuleBase) and v is not ModuleBase:
                    return v
            except TypeError:
                pass
    return None


def _instantiate(cls, display_name):
    kp = KernelProxy(session_id=0, module_name=display_name)
    inst = cls(kp, None, None)
    _module_instances[display_name] = inst
    return inst


# ──────────────────────────────────────────────────────────────
# Test 2: All 16 system modules load
# ──────────────────────────────────────────────────────────────
section("Test 2: all modules load")

EXPECTED_MODULES = [
    "MCUB_info.py", "api_protection.py", "command.py", "config.py",
    "eval.py", "loader.py", "log_bot.py", "man.py", "settings.py",
    "terminal.py", "tester.py", "tr.py", "trusted.py", "updates.py",
    "userbot-backup.py", "utils-piped.py",
]

_loaded_ns = {}
for _fname in EXPECTED_MODULES:
    try:
        _loaded_ns[_fname] = _load_ns(_fname)
        check(f"load {_fname}", True)
    except Exception as _e:
        check(f"load {_fname}", False, str(_e))

check("all 16 modules loaded", len(_loaded_ns) == 16,
      f"only {len(_loaded_ns)} loaded")

# ──────────────────────────────────────────────────────────────
# Test 3: Class-based modules instantiate
# ──────────────────────────────────────────────────────────────
section("Test 3: class-based module instantiation")

# Clear handlers/instances so we get a clean count
_command_handlers.clear()
_module_instances.clear()

CLASS_MODULES = {
    "MCUB_info.py": {"expected_cmds": {"info"}},
    "command.py":   {"expected_cmds": set()},          # no @command methods
    "eval.py":      {"expected_cmds": set()},
    "loader.py":    {"expected_cmds": {"dlm", "reload", "iload", "um", "unlm", "addrepo", "delrepo"}},
    "log_bot.py":   {"expected_cmds": {"log_setup"}},
    "man.py":       {"expected_cmds": {"help", "man", "manhide", "manunhide"}},
    "settings.py":  {"expected_cmds": {"lang", "mcub", "setprefix"}},
    "tester.py":    {"expected_cmds": {"ping", "logs", "freezing", "teaser"}},
    "tr.py":        {"expected_cmds": {"tr"}},
    "updates.py":   {"expected_cmds": set()},
    "userbot-backup.py": {"expected_cmds": {"backup", "restore", "restore_with"}},
    "utils-piped.py": {"expected_cmds": {"echo", "sed", "grep", "head", "tail", "wc", "calc"}},
}

_class_instances = {}

for _fname, _meta in CLASS_MODULES.items():
    _ns = _loaded_ns.get(_fname)
    if _ns is None:
        check(f"instantiate {_fname}", False, "module not loaded")
        continue
    _cls = _find_class(_ns)
    if _cls is None:
        check(f"instantiate {_fname}", False, "no ModuleBase subclass found")
        continue
    _dname = getattr(_cls, "name", _fname[:-3])
    try:
        _inst = _instantiate(_cls, _dname)
        _class_instances[_dname] = _inst
        check(f"instantiate {_fname}", True)
    except Exception as _e:
        check(f"instantiate {_fname}", False, str(_e))
        continue

    # Verify expected commands exist in _command_handlers
    for _cmd in _meta["expected_cmds"]:
        check(
            f"  {_fname[:-3]}: handler '{_cmd}' registered",
            (_dname, _cmd) in _command_handlers,
            f"key ({_dname!r}, {_cmd!r}) missing; known: {[k for k in _command_handlers if k[0]==_dname]}",
        )

# ──────────────────────────────────────────────────────────────
# Test 4: Function-based modules register commands
# ──────────────────────────────────────────────────────────────
section("Test 4: function-based module command registration")

FUNC_MODULES = {
    "api_protection.py": {"expected_cmds": {"api_protection", "api_reset"}},
    "config.py":         {"expected_cmds": {"cfg", "fcfg"}},
    "terminal.py":       {"expected_cmds": {"t", "tkill", "ti"}},
    "trusted.py":        {"expected_cmds": {"trust", "untrust", "trustlist"}},
}

_orig_create_task = asyncio.create_task

class _FakeTask:
    def cancel(self): pass
    def done(self): return True

def _noop_create_task(coro, *a, **kw):
    try:
        if hasattr(coro, "close"):
            coro.close()
    except Exception:
        pass
    return _FakeTask()

for _fname, _meta in FUNC_MODULES.items():
    _ns = _loaded_ns.get(_fname)
    if _ns is None:
        check(f"register {_fname}", False, "module not loaded")
        continue
    _reg_fn = _ns.get("register")
    if not callable(_reg_fn) or isinstance(_reg_fn, type):
        check(f"register {_fname}", False, "no register() function")
        continue
    _dname = _ns.get("name", _fname[:-3])
    _kp = KernelProxy(session_id=0, module_name=_dname)
    asyncio.create_task = _noop_create_task
    try:
        _result = _reg_fn(_kp)
        if asyncio.iscoroutine(_result):
            try:
                asyncio.run(_result)
            except Exception:
                pass
        _module_instances[_dname] = _kp
        check(f"register {_fname}", True)
    except Exception as _e:
        check(f"register {_fname}", False, str(_e))
    finally:
        asyncio.create_task = _orig_create_task

    for _cmd in _meta["expected_cmds"]:
        check(
            f"  {_fname[:-3]}: handler '{_cmd}' registered",
            (_dname, _cmd) in _command_handlers,
        )

# ──────────────────────────────────────────────────────────────
# Test 5: _SimpleStrings behaviour
# ──────────────────────────────────────────────────────────────
section("Test 5: _SimpleStrings API")

# Flat dict
_ss_flat = _SimpleStrings({"hello": "Hello World", "bye": "Goodbye"})
check("flat: __call__", _ss_flat("hello") == "Hello World")
check("flat: __getitem__", _ss_flat["bye"] == "Goodbye")
check("flat: get() found", _ss_flat.get("hello", "X") == "Hello World")
check("flat: get() missing returns default", _ss_flat.get("missing_key", "DEF") == "DEF")
check("flat: get() missing no default returns None", _ss_flat.get("missing_key") is None)
check("flat: fallback to key name on __call__", _ss_flat("unknown_key") == "unknown_key")
check("flat: fallback to key name on __getitem__", _ss_flat["unknown_key"] == "unknown_key")

# Locale dict
_ss_locale = _SimpleStrings({"en": {"greet": "Hello"}, "ru": {"greet": "Привет"}})
check("locale: en key", _ss_locale("greet") == "Hello")
check("locale: get() found", _ss_locale.get("greet", "X") == "Hello")
check("locale: get() missing returns default", _ss_locale.get("nope", "DEF") == "DEF")

# Langpack-ref dict ({"name": "tester"}) - rely on loaded YAML
if _LANGPACK_DATA:
    _ss_lp = _SimpleStrings({"name": "tester"})
    _ping_val = _ss_lp("ping")
    check("langpack-ref: returns string", isinstance(_ping_val, str) and len(_ping_val) > 0)
    check("langpack-ref: get() nonexistent returns default",
          _ss_lp.get("definitely_nonexistent_key_xyz_12345", "DEF") == "DEF")
else:
    check("langpack-ref: skip (no YAML data)", True)

# Empty
_ss_empty = _SimpleStrings()
check("empty: fallback to key", _ss_empty("any_key") == "any_key")
check("empty: get() returns None", _ss_empty.get("any_key") is None)

# ──────────────────────────────────────────────────────────────
# Test 6: strings property override (class dict shadows)
# ──────────────────────────────────────────────────────────────
section("Test 6: strings property override")

class _TestMod(ModuleBase):
    name = "test_strings_override"
    strings = {"en": {"hello": "Hello"}, "ru": {"hello": "Привет"}}

_tm_inst = _instantiate(_TestMod, "test_strings_override")
check("strings type is _SimpleStrings",
      type(_tm_inst.strings).__name__ == "_SimpleStrings",
      f"got {type(_tm_inst.strings).__name__}")
check("strings not raw dict", not isinstance(_tm_inst.strings, dict))
check("strings('hello') works", _tm_inst.strings("hello") == "Hello")
check("strings['hello'] works", _tm_inst.strings["hello"] == "Hello")
check("strings.get('hello') works", _tm_inst.strings.get("hello") == "Hello")
check("strings.get('missing', 'D') returns default", _tm_inst.strings.get("missing", "D") == "D")

# ──────────────────────────────────────────────────────────────
# Test 7: ModuleConfig API
# ──────────────────────────────────────────────────────────────
section("Test 7: ModuleConfig API")

_cfg = ModuleConfig(
    ConfigValue("key1", "default_val", "desc1"),
    ConfigValue("key2", 42,            "desc2"),
)
check("cfg: __getitem__", _cfg["key1"] == "default_val")
check("cfg: __getitem__ int", _cfg["key2"] == 42)
check("cfg: __setitem__", (_cfg.__setitem__("key1", "new") or True) and _cfg["key1"] == "new")
check("cfg: get()", _cfg.get("key1") == "new")
check("cfg: get() missing returns None", _cfg.get("nokey") is None)
check("cfg: __contains__", "key1" in _cfg)
check("cfg: __len__", len(_cfg) == 2)
check("cfg: __iter__", list(_cfg) == ["key1", "key2"])
check("cfg: items()", dict(_cfg.items()) == {"key1": "new", "key2": 42})
check("cfg: to_dict()", isinstance(_cfg.to_dict(), dict))
_cfg.update({"key2": 99})
check("cfg: update()", _cfg["key2"] == 99)
check("ValidationError is Exception subclass", issubclass(ValidationError, Exception))

# ──────────────────────────────────────────────────────────────
# Test 8: Simulated command dispatch via _mcub_call_command logic
# ──────────────────────────────────────────────────────────────
section("Test 8: simulated command dispatch")

# Reload tester handlers for a clean slate
_command_handlers.clear()
_module_instances.clear()

_tester_ns = _load_ns("tester.py")
_TesterCls = _find_class(_tester_ns)
assert _TesterCls is not None, "tester.py class not found"
_tester_display = getattr(_TesterCls, "name", "tester")
_tester_inst = _instantiate(_TesterCls, _tester_display)

# Simulate the _mcub_call_command session-ID update logic
_SESSION_ID = 777

def _simulate_call_command(mod_name, cmd_name, session_id, chat_id, msg_id, text, sender_id):
    """Mirror the logic in pythonBootstrap._mcub_call_command."""
    handler = _command_handlers.get((mod_name, cmd_name))
    if handler is None:
        for (mn, cn), h in _command_handlers.items():
            if cn == cmd_name and (
                mn == mod_name or mn.replace("-", "_") == mod_name.replace("-", "_")
            ):
                handler = h
                break
    if handler is None:
        raise KeyError(f"No handler for ({mod_name!r}, {cmd_name!r})")

    instance = _module_instances.get(mod_name)
    if instance is not None and hasattr(instance, "_session_id"):
        instance._session_id = session_id
        if hasattr(instance, "client") and hasattr(instance.client, "_session_id"):
            instance.client._session_id = session_id
        if hasattr(instance, "_kernel_obj") and instance._kernel_obj is not None:
            kp = instance._kernel_obj
            if hasattr(kp, "_session_id"):
                kp._session_id = session_id
            if hasattr(kp, "client") and hasattr(kp.client, "_session_id"):
                kp.client._session_id = session_id

    ev = Event(session_id, chat_id, msg_id, text, sender_id)
    if asyncio.iscoroutinefunction(handler):
        asyncio.run(handler(ev))
    else:
        handler(ev)
    return ev

_EDIT_CALLS.clear()
try:
    _ev = _simulate_call_command(_tester_display, "ping", _SESSION_ID, 111, 1, ".ping", 999)
    check("dispatch: ping ran without exception", True)
    check("dispatch: edit_message called", len(_EDIT_CALLS) > 0)
    check("dispatch: session_id passed to edit",
          any(c[0] == _SESSION_ID for c in _EDIT_CALLS),
          f"edit calls: {_EDIT_CALLS}")
    check("dispatch: instance._session_id updated",
          _tester_inst._session_id == _SESSION_ID)
except Exception as _e:
    check("dispatch: ping ran without exception", False, str(_e))
    check("dispatch: edit_message called", False)
    check("dispatch: session_id passed to edit", False)
    check("dispatch: instance._session_id updated", False)

# Verify all tester commands are dispatachable
for _cmd in ("ping", "logs", "freezing", "teaser"):
    _h = _command_handlers.get((_tester_display, _cmd))
    check(f"dispatch: '{_cmd}' is async coroutine",
          _h is not None and asyncio.iscoroutinefunction(_h))

# ──────────────────────────────────────────────────────────────
# Test 9: Pipeline event fields
# ──────────────────────────────────────────────────────────────
section("Test 9: pipeline event fields")

_pev = Event(1, 100, 200, ".echo hello", 42)
_pev.piped = True
_pev.pipe_input = "from previous"
check("event.piped field", _pev.piped is True)
check("event.pipe_input field", _pev.pipe_input == "from previous")
check("event.get_pipe_input()", _pev.get_pipe_input() == "from previous")
_pev.set_pipe_output("result text")
check("event.set_pipe_output() / pipe_output", _pev.pipe_output == "result text")
check("event.pipe_exit_code default 0", _pev.pipe_exit_code == 0)

# ──────────────────────────────────────────────────────────────
# Test 10: KernelProxy sub-objects
# ──────────────────────────────────────────────────────────────
section("Test 10: KernelProxy sub-objects")

_kp = KernelProxy(session_id=5, module_name="test")
check("kp._session_id set", _kp._session_id == 5)
check("kp.client is ClientProxy", type(_kp.client).__name__ == "ClientProxy")
check("kp.register is _RegisterProxy", type(_kp.register).__name__ == "_RegisterProxy")
check("kp.VERSION is str", isinstance(_kp.VERSION, str))
check("kp.config is dict-like", hasattr(_kp.config, "get"))
check("kp.version_manager has detect_branch", hasattr(_kp.version_manager, "detect_branch"))
check("kp.db_manager has db_get", hasattr(_kp.db_manager, "db_get"))

# ──────────────────────────────────────────────────────────────
# Test 11: _RegisterProxy command registration
# ──────────────────────────────────────────────────────────────
section("Test 11: _RegisterProxy command registration")

_command_handlers.clear()
_rp = _RegisterProxy("test_func_module")

@_rp.command("testcmd", doc_en="Test command")
async def _test_handler(event):
    await event.edit("ok")

@_rp.command("alias_cmd", alias=["acmd", "ac"])
async def _alias_handler(event):
    pass

check("register: direct command stored",
      ("test_func_module", "testcmd") in _command_handlers)
check("register: alias_cmd stored",
      ("test_func_module", "alias_cmd") in _command_handlers)
check("register: alias 'acmd' stored",
      ("test_func_module", "acmd") in _command_handlers)
check("register: alias 'ac' stored",
      ("test_func_module", "ac") in _command_handlers)
check("register: registered_commands list has testcmd",
      any(n == "testcmd" for (n, _, _) in _rp.registered_commands))

# Sync function gets wrapped as coroutine
@_rp.command("synccmd")
def _sync_handler(event):
    return "sync result"

check("register: sync handler wrapped as async",
      asyncio.iscoroutinefunction(_command_handlers.get(("test_func_module", "synccmd"))))

# ──────────────────────────────────────────────────────────────
# Final summary
# ──────────────────────────────────────────────────────────────
print(f"\n{'═'*66}")
_total = len(_results["pass"]) + len(_results["fail"])
print(f"=== Results: {len(_results['pass'])} PASS / {len(_results['fail'])} FAIL "
      f"(total {_total}) ===")

if _results["fail"]:
    print("\nFailed tests:")
    for _name, _msg in _results["fail"]:
        print(f"  FAIL  {_name}" + (f": {_msg}" if _msg else ""))
    sys.exit(1)
else:
    print("All tests passed!")
