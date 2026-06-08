"""
module_scanner.py - Scans a loaded Python MCUB module and extracts command info.

Called by the Go bridge after loading a user module.
Returns a JSON string with module metadata and registered commands.

Supports three module styles:
  1. Class-based: class Foo(ModuleBase): @command(...) async def handler(self, event): ...
  2. Function-based: def register(kernel): @kernel.register.command(...) async def h(event): ...
  3. Loader-style: import core.lib.loader.module_base as loader; @loader.command(...) (same as class-based)

Usage (from Go via _mcub_go or direct exec):
    import module_scanner
    info = module_scanner.scan_module(loaded_module_object)
    # info is a plain dict; serialise with json.dumps(info)
"""

import asyncio
import sys
import json
import inspect
import types


# ---------------------------------------------------------------------------
# Stub helpers used when calling register(kernel) safely during scanning
# ---------------------------------------------------------------------------

class _StubLogger:
    """No-op logger for scan-time kernel proxy."""
    def info(self, *a, **kw): pass
    def debug(self, *a, **kw): pass
    def warning(self, *a, **kw): pass
    warn = warning
    def error(self, *a, **kw): pass
    def critical(self, *a, **kw): pass


class _StubClient:
    """No-op Telegram client for scan-time kernel proxy."""
    async def send_message(self, *a, **kw): pass
    async def edit_message(self, *a, **kw): pass
    async def delete_messages(self, *a, **kw): pass
    async def get_entity(self, *a, **kw): return None
    async def get_messages(self, *a, **kw): return None
    async def get_me(self): return None
    async def download_media(self, *a, **kw): return None
    def is_connected(self): return True


class _RegisterCapture:
    """Captures commands registered via kernel.register.command() during scanning."""

    def __init__(self):
        self._commands = []

    def command(self, name, doc_en="", doc_ru="", **kwargs):
        cmds = self._commands
        def decorator(func):
            cmds.append({
                "name": name,
                "doc_en": doc_en,
                "doc_ru": doc_ru,
                "method": getattr(func, "__name__", ""),
                "class": None,
                "style": "function",
            })
            return func
        return decorator

    # All other register methods are no-ops that return identity decorators.
    def on_load(self, **kwargs):
        return lambda f: f

    def uninstall(self, **kwargs):
        return lambda f: f

    def watcher(self, **kwargs):
        return lambda f: f

    def loop(self, **kwargs):
        return lambda f: f

    def callback(self, **kwargs):
        return lambda f: f

    def bot_command(self, **kwargs):
        return lambda f: f

    def inline(self, **kwargs):
        return lambda f: f

    # Positional-arg variants (some modules call without keyword names)
    def __call__(self, *a, **kw):
        return lambda f: f


class _ScanKernel:
    """Minimal kernel proxy used exclusively during module scanning."""

    def __init__(self):
        self.register = _RegisterCapture()
        self.client = _StubClient()
        self.config = {}
        self.loaded_modules = {}
        self.system_modules = {}
        self.VERSION = "1.0.0-scan"
        self.start_time_ts = 0.0
        self.custom_prefix = "."
        self.logger = _StubLogger()
        self._live_module_configs = {}
        self.bot_client = None

    async def get_module_config(self, *a, **kw):
        return {}

    async def save_module_config(self, *a, **kw):
        return True

    def store_module_config_schema(self, *a, **kw):
        pass

    async def db_get(self, *a, **kw):
        return None

    async def db_set(self, *a, **kw):
        pass

    async def db_delete(self, *a, **kw):
        pass

    def log_error(self, msg):
        pass

    def log_debug(self, msg):
        pass

    async def handle_error(self, *a, **kw):
        pass

    async def send_log_message(self, *a, **kw):
        pass

    async def restart(self, *a, **kw):
        pass

    def get_module(self, *a, **kw):
        return None

    def __getattr__(self, name):
        # Catch-all for any other kernel attributes modules may access.
        return None


def _call_register_safe(register_fn):
    """
    Call register(kernel) with a _ScanKernel while suppressing errors.

    asyncio.create_task is temporarily patched to a no-op so that modules
    that schedule startup coroutines don't fail outside an event loop.
    """
    kernel = _ScanKernel()

    original_create_task = asyncio.create_task

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
        register_fn(kernel)
    except Exception:
        pass
    finally:
        asyncio.create_task = original_create_task

    return kernel


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------

def scan_module(module_obj):
    """Scan a Python module/namespace for MCUB commands.

    Accepts either a module object or a raw dict namespace (from ``exec``).

    Supports:
    1. Class-based modules: a ``ModuleBase`` subclass with ``@command``-decorated
       instance methods (including loader-style ``@loader.command``).
    2. Function-based modules: a top-level ``register(kernel)`` function where
       commands are registered via ``@kernel.register.command(...)``.

    Returns a dict with keys:
        name        str
        version     str
        author      str
        description dict  (e.g. {"en": "...", "ru": "..."})
        style       str   - "class", "function", or "loader"
        commands    list of dicts, each with:
                        name    str   - command trigger word
                        doc_en  str
                        doc_ru  str
                        method  str   - attribute name on the class or module
                        class   str|None - class name, or None for module-level
                        style   str   - "class", "function", or "module"
    """
    result = {
        "name": "unknown",
        "version": "1.0.0",
        "author": "unknown",
        "description": {},
        "commands": [],
        "style": "unknown",
    }

    # Support both module objects and raw exec namespaces (dicts).
    if isinstance(module_obj, dict):
        module_dict = module_obj
    else:
        module_dict = getattr(module_obj, "__dict__", {})

    # ---- 1. Class-based module (ModuleBase subclass) --------------------
    module_class = _find_module_base_subclass(module_obj)
    if module_class is not None:
        # Detect "loader" / "hikka" style: module imports `loader` as module alias
        loader_mod = module_dict.get("loader")
        if (isinstance(loader_mod, types.ModuleType) and
                getattr(loader_mod, "ModuleBase", None) is not None):
            result["style"] = "loader"
        else:
            result["style"] = "class"

        # Also check if the class inherits from a Hikka-style Module base
        hikka_cls, hikka_cmds = _find_hikka_module(module_obj)
        if hikka_cls is module_class and hikka_cmds:
            result["style"] = "hikka"

        result["name"] = _str_attr(module_class, "name", module_class.__name__)
        result["version"] = _str_attr(module_class, "version", "1.0.0")
        result["author"] = _str_attr(module_class, "author", "unknown")
        # For Hikka modules, the name may be in strings["name"] or strings["en"]["name"].
        # Only use strings["name"] as a fallback when the class does NOT define an explicit
        # ``name`` attribute – i.e. the class body has no ``name = "..."`` line.
        # MCUB-style modules always set ``name`` explicitly and strings["name"] is only the
        # Hikka-compat display alias (e.g. "mcub_info" for "MCUB_info").
        _has_explicit_name = "name" in module_class.__dict__
        raw_strings = getattr(module_class, "strings", {}) or {}
        if isinstance(raw_strings, dict) and not _has_explicit_name:
            hikka_name = raw_strings.get("name")
            if hikka_name is None and isinstance(raw_strings.get("en"), dict):
                hikka_name = raw_strings["en"].get("name")
            if hikka_name:
                result["name"] = str(hikka_name)
        desc = getattr(module_class, "description", {})
        result["description"] = desc if isinstance(desc, dict) else {"en": str(desc)}

        for method_name in _safe_dir(module_class):
            try:
                method = getattr(module_class, method_name)
                if callable(method) and (
                    getattr(method, "_mcub_command", False) or
                    getattr(method, "__hikka_command__", False) or
                    getattr(method, "is_command", False)
                ):
                    entry = _cmd_entry(method, method_name, module_class.__name__)
                    entry["style"] = result["style"]
                    if not _already_registered(result["commands"], entry["name"], module_class.__name__):
                        result["commands"].append(entry)
            except Exception:
                pass

    # ---- 1b. Hikka-only detection (no ModuleBase in MRO, pure loader.Module) ----
    if not result["commands"] and module_class is None:
        hikka_cls, hikka_cmds = _find_hikka_module(module_obj)
        if hikka_cls is not None:
            result["style"] = "hikka"
            result["commands"] = hikka_cmds
            # Prefer explicit ``name`` class attribute over strings["name"].
            _hcls_name = _str_attr(hikka_cls, "name", "unknown")
            _hcls_has_name = "name" in hikka_cls.__dict__
            if _hcls_has_name and _hcls_name not in ("unknown", "unnamed"):
                result["name"] = _hcls_name
            else:
                raw_strings = getattr(hikka_cls, "strings", {}) or {}
                if isinstance(raw_strings, dict):
                    hikka_name = raw_strings.get("name")
                    if hikka_name is None and isinstance(raw_strings.get("en"), dict):
                        hikka_name = raw_strings["en"].get("name")
                    if hikka_name:
                        result["name"] = str(hikka_name)
                elif _hcls_has_name:
                    result["name"] = _hcls_name
            result["version"] = _str_attr(hikka_cls, "version", "1.0.0")
            result["author"] = _str_attr(hikka_cls, "author", "unknown")

    # ---- 2. Module-level functions with @command ------------------------
    for attr_name, attr in module_dict.items():
        if callable(attr) and getattr(attr, "_mcub_command", False):
            entry = _cmd_entry(attr, attr_name, None)
            entry["style"] = "module"
            if not _already_registered(result["commands"], entry["name"], None):
                result["commands"].append(entry)

    # ---- 3. Function-based (register(kernel)) style --------------------
    register_fn = module_dict.get("register")
    if callable(register_fn) and not isinstance(register_fn, type):
        kernel = _call_register_safe(register_fn)
        if kernel.register._commands:
            result["style"] = "function"
            for cmd in kernel.register._commands:
                if not _already_registered(result["commands"], cmd["name"], cmd.get("class")):
                    result["commands"].append(cmd)

    # ---- 4. Fall back to module-level name/version/author ---------------
    if result["name"] == "unknown":
        result["name"] = _dict_or_attr(module_dict, module_obj, "name", result["name"])
    if result["version"] == "1.0.0":
        result["version"] = _dict_or_attr(module_dict, module_obj, "version", result["version"])
    if result["author"] == "unknown":
        result["author"] = _dict_or_attr(module_dict, module_obj, "author", result["author"])
    if not result["description"]:
        desc = module_dict.get("description", getattr(module_obj, "description", {})) \
            if isinstance(module_obj, dict) else getattr(module_obj, "description", {})
        result["description"] = desc if isinstance(desc, dict) else {"en": str(desc)}

    return result


def scan_module_json(module_obj):
    """Convenience wrapper: returns scan_module result as a JSON string."""
    return json.dumps(scan_module(module_obj), ensure_ascii=False)


# ---------------------------------------------------------------------------
# Internal helpers
# ---------------------------------------------------------------------------

def _find_hikka_module(namespace):
    """Find a Hikka-style Module class in a module namespace.

    Recognises classes that:
    - Inherit from ``loader.Module`` / ``_HikkaModule`` (MRO contains "Module"
      or "_HikkaModule"), AND
    - Have at least one method decorated with ``@loader.command`` (i.e.
      ``is_command=True`` or ``__hikka_command__=True``).

    Returns:
        (class_obj, list_of_cmd_dicts) or (None, []) if nothing found.
    """
    if isinstance(namespace, dict):
        items = list(namespace.items())
    else:
        items = []
        for attr_name in _safe_dir(namespace):
            try:
                items.append((attr_name, getattr(namespace, attr_name)))
            except Exception:
                pass

    for class_name, obj in items:
        if not isinstance(obj, type):
            continue
        mro_names = [c.__name__ for c in getattr(obj, "__mro__", [])]
        # Accept classes that inherit from loader.Module or _HikkaModule
        if not any(n in mro_names for n in ("Module", "_HikkaModule")):
            continue
        # Skip the base classes themselves
        if obj.__name__ in ("Module", "_HikkaModule", "ModuleBase"):
            continue

        cmds = []
        for attr_name in _safe_dir(obj):
            try:
                attr = getattr(obj, attr_name, None)
                if not callable(attr):
                    continue
                if not (
                    getattr(attr, "_hikka_command", False) or
                    getattr(attr, "__hikka_command__", False) or
                    getattr(attr, "is_command", False) or
                    getattr(attr, "_mcub_command", False)
                ):
                    continue
                # Derive the command trigger name
                cmd_name = getattr(attr, "_mcub_cmd_name", None)
                if cmd_name is None:
                    if attr_name.endswith("cmd"):
                        cmd_name = attr_name[:-3]
                    elif attr_name.endswith("Cmd"):
                        cmd_name = attr_name[:-3].lower()
                    else:
                        cmd_name = attr_name.lower()
                doc_en = (
                    getattr(attr, "_mcub_doc_en", None) or
                    getattr(attr, "en_doc", None) or
                    getattr(attr, "__doc__", None) or ""
                )
                doc_ru = (
                    getattr(attr, "_mcub_doc_ru", None) or
                    getattr(attr, "ru_doc", None) or
                    getattr(attr, "__doc_ru__", None) or ""
                )
                cmds.append({
                    "name": cmd_name,
                    "doc_en": str(doc_en),
                    "doc_ru": str(doc_ru),
                    "method": attr_name,
                    "class": class_name,
                    "style": "hikka",
                })
            except Exception:
                pass

        if cmds:
            return obj, cmds

    return None, []


def _find_module_base_subclass(module_obj):
    """Return the first ModuleBase subclass found at module level, or None.

    Works with both module objects and raw dict namespaces from ``exec``.
    """
    # Build an iterable of (name, value) pairs covering the namespace.
    if isinstance(module_obj, dict):
        items = list(module_obj.items())
    else:
        items = []
        for attr_name in _safe_dir(module_obj):
            try:
                items.append((attr_name, getattr(module_obj, attr_name)))
            except Exception:
                pass

    for attr_name, attr in items:
        try:
            if not isinstance(attr, type):
                continue
            mro = getattr(attr, "__mro__", [])
            # Use name comparison so we work even when ModuleBase was imported
            # from a different exec'd copy of mcub_compat.
            if any(b.__name__ == "ModuleBase" for b in mro[1:]):
                return attr
        except Exception:
            pass
    return None


def _cmd_entry(func, method_name, class_name):
    return {
        "name": getattr(func, "_mcub_cmd_name", method_name),
        "doc_en": getattr(func, "_mcub_doc_en", ""),
        "doc_ru": getattr(func, "_mcub_doc_ru", ""),
        "method": method_name,
        "class": class_name,
        "style": "class" if class_name else "module",
    }


def _already_registered(commands, cmd_name, class_name):
    return any(
        c["name"] == cmd_name and c["class"] == class_name
        for c in commands
    )


def _dict_or_attr(d, obj, key, default):
    """Get key from dict namespace or attribute from object, returning default if missing."""
    if isinstance(obj, dict):
        val = d.get(key, default)
    else:
        val = getattr(obj, key, default)
    return str(val) if val is not None else default


def _str_attr(obj, attr, default):
    val = getattr(obj, attr, default)
    return str(val) if val is not None else default


def _safe_dir(obj):
    try:
        return dir(obj)
    except Exception:
        return []


# ---------------------------------------------------------------------------
# CLI entry point (for debugging outside Go)
# ---------------------------------------------------------------------------
if __name__ == "__main__":
    import importlib
    import importlib.util

    if len(sys.argv) < 2:
        print("Usage: python module_scanner.py <path_to_module.py>", file=sys.stderr)
        sys.exit(1)

    module_path = sys.argv[1]
    spec = importlib.util.spec_from_file_location("_scanned_module", module_path)
    mod = importlib.util.module_from_spec(spec)
    try:
        spec.loader.exec_module(mod)
    except Exception as exc:
        print(json.dumps({"error": str(exc)}))
        sys.exit(1)

    print(scan_module_json(mod))
