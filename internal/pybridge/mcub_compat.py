# ============================================================
# MCUB Python Compatibility Layer for Go port
# Loaded by the Go CGo Python bridge before user modules
# ============================================================

import sys
import types
import importlib
import importlib.abc
import importlib.machinery

# _mcub_go is the C extension provided by the Go bridge.
# It is already installed in the interpreter before this runs.
# When running outside the Go binary (e.g. tests), we install a
# no-op stub so the rest of this file can be imported safely.
try:
    import _mcub_go
except ImportError:
    _stub = types.ModuleType("_mcub_go")

    def _noop(*args, **kwargs):
        return None

    for _fn in (
        "get_prefix", "get_version", "get_start_time",
        "log_info", "log_debug", "log_warn", "log_error",
        "send_message", "edit_message", "reply_message",
        "delete_message", "get_reply_message", "download_media",
        "get_me", "get_entity", "get_message",
        "db_get", "db_set", "db_delete",
        "save_module_config", "restart_kernel",
    ):
        setattr(_stub, _fn, _noop)

    _stub.get_prefix = lambda: "."
    _stub.get_version = lambda: "0.0.0-stub"
    _stub.get_start_time = lambda: 0

    sys.modules["_mcub_go"] = _stub
    _mcub_go = _stub


# ============================================================
# Stub class factory
# Returns a class that can stand in for any Telethon/core type.
# Works as a type annotation, base class, decorator, or callable.
# ============================================================
_stub_classes = {}

def _make_stub_class(name):
    """Return a stub class suitable for any missing type or function."""
    if name in _stub_classes:
        return _stub_classes[name]
    cls = type(name, (), {
        "__init__": lambda self, *a, **kw: None,
        "__call__": lambda self, *a, **kw: None,
        "__bool__": lambda self: False,
        "__str__": lambda self: f"<Stub:{name}>",
        "__repr__": lambda self: f"<Stub:{name}>",
        "__class_getitem__": classmethod(lambda cls, item: cls),
        "__await__": None,  # prevents accidental await
    })
    _stub_classes[name] = cls
    return cls


# ============================================================
# Magic stub module: returns a stub class for any unknown attr
# ============================================================
class _MagicStubModule(types.ModuleType):
    """A module stub that auto-generates stubs for any undefined attribute."""

    def __getattr__(self, name):
        if name.startswith("__") and name.endswith("__"):
            raise AttributeError(name)
        val = _make_stub_class(name)
        # Cache on the module object to avoid repeated creation.
        object.__setattr__(self, name, val)
        return val


# ============================================================
# Helper: create bound coroutine wrapper
# ============================================================
def _make_bound(func, instance):
    """Return a coroutine function bound to instance."""
    import asyncio
    import functools
    @functools.wraps(func)
    async def _bound(event):
        return await func(instance, event)
    if not asyncio.iscoroutinefunction(func):
        @functools.wraps(func)
        async def _bound_sync(event):
            return func(instance, event)
        return _bound_sync
    return _bound

# ============================================================
# ModuleBase class - all MCUB modules inherit from this
# ============================================================
# Global registry: (module_name, cmd_name) -> bound async callable
_command_handlers = {}
# Global registry: module_name -> instance
_module_instances = {}

class ModuleBase:
    name = "unnamed"
    version = "1.0.0"
    author = "unknown"
    description = {}  # {"en": "...", "ru": "..."}

    # Per-subclass command registries, populated by __init_subclass__
    _cmd_registry: list = []

    def __init_subclass__(cls, **kwargs):
        super().__init_subclass__(**kwargs)
        cls._cmd_registry = []
        for attr_name in list(cls.__dict__):
            attr = cls.__dict__[attr_name]
            if callable(attr) and hasattr(attr, '_mcub_commands'):
                for (pattern, meta) in attr._mcub_commands:
                    cls._cmd_registry.append((pattern, attr, meta))
            elif callable(attr) and hasattr(attr, '_mcub_command') and attr._mcub_command:
                cls._cmd_registry.append((attr._mcub_cmd_name, attr, {
                    'doc_en': getattr(attr, '_mcub_doc_en', ''),
                    'doc_ru': getattr(attr, '_mcub_doc_ru', ''),
                }))

    def __init__(self, kernel=None, client=None, register=None):
        # Support both new-style (kernel, client, register) and legacy ()
        self._kernel_obj = kernel
        self._client_obj = client
        self._register_obj = register
        self._session_id = None
        self.cache = _SimpleCache()
        if kernel is not None:
            self._kernel_obj = kernel
        if client is not None:
            self._client_obj = client

        # Wire up self._strings stub (do NOT assign self.log - conflicts with @property)
        self._strings = None

        # Replace class-level strings dict/method with a _SimpleStrings instance
        # that supports both self.strings("key") and self.strings["key"].
        # Assigned to instance __dict__ directly to bypass any descriptor.
        object.__setattr__(self, 'strings', _SimpleStrings())

        # Register commands from _cmd_registry into global _command_handlers
        mod_name = getattr(type(self), 'name', type(self).__name__)
        for (pattern, func, meta) in type(self)._cmd_registry:
            _bound = _make_bound(func, self)
            _command_handlers[(mod_name, pattern)] = _bound
            # Also register aliases
            alias = meta.get('alias')
            if alias:
                for a in ([alias] if isinstance(alias, str) else alias):
                    _command_handlers[(mod_name, a)] = _bound

    @property
    def kernel(self):
        return self._kernel_obj

    def set_kernel(self, kernel_proxy):
        self._kernel = kernel_proxy

    def set_session(self, session_id):
        self._session_id = session_id

    async def on_load(self):
        pass

    async def on_unload(self):
        pass

    def get_lang(self):
        return "en"

    def get_prefix(self):
        return _mcub_go.get_prefix()

    def args_raw(self, event):
        """Return command arguments as a raw string (everything after the first word)."""
        text = event.text or ""
        parts = text.split(None, 1)
        return parts[1] if len(parts) > 1 else ""

    def args(self, event):
        """Return command arguments as a list of tokens."""
        return self.args_raw(event).split()

    def _get_strings(self):
        return lambda key, **kw: key

    async def log_error(self, msg):
        _mcub_go.log_error(str(msg))

    @property
    def log(self):
        return _Logger()


# ============================================================
# @command and related decorators
# ============================================================
def command(name, doc_en="", doc_ru="", **kwargs):
    """Decorator that marks a method as a MCUB command handler."""
    def decorator(func):
        func._mcub_command = True
        func._mcub_cmd_name = name
        func._mcub_doc_en = doc_en
        func._mcub_doc_ru = doc_ru
        return func
    return decorator


def inline(name="", doc_en="", doc_ru="", **kwargs):
    """Decorator for inline query handlers (no-op for Go bridge scanning)."""
    def decorator(func):
        func._mcub_inline = True
        func._mcub_cmd_name = name
        func._mcub_doc_en = doc_en
        func._mcub_doc_ru = doc_ru
        return func
    return decorator


def callback(name="", doc_en="", doc_ru="", **kwargs):
    """Decorator for callback query handlers (no-op for Go bridge scanning)."""
    def decorator(func):
        func._mcub_callback = True
        return func
    return decorator


def bot_command(name="", doc_en="", doc_ru="", **kwargs):
    """Decorator for bot command handlers (no-op for Go bridge scanning)."""
    def decorator(func):
        func._mcub_bot_command = True
        return func
    return decorator


def loop(interval=60, autostart=False, **kwargs):
    """Decorator for periodic loop handlers (no-op for Go bridge scanning)."""
    def decorator(func):
        func._mcub_loop = True
        return func
    return decorator


# ============================================================
# ModuleConfig classes
# ============================================================
class ConfigValue:
    def __init__(self, key, default, description="", validator=None):
        self.key = key
        self.default = default
        self.description = description
        self.validator = validator


class ModuleConfig:
    def __init__(self, *config_values):
        self._values = {cv.key: cv.default for cv in config_values}

    def get(self, key, default=None):
        return self._values.get(key, default)

    def __getitem__(self, key):
        return self._values[key]

    def __setitem__(self, key, value):
        self._values[key] = value

    def from_dict(self, d):
        self._values.update(d)

    def to_dict(self):
        return dict(self._values)


class ValidationError(Exception):
    pass


class Boolean:
    def __init__(self, default=False, **kwargs):
        self.default = default


class String:
    def __init__(self, default="", **kwargs):
        self.default = default


class Integer:
    def __init__(self, default=0, **kwargs):
        self.default = default


class Float:
    def __init__(self, default=0.0, **kwargs):
        self.default = default


class Choice:
    def __init__(self, choices=None, default="", **kwargs):
        self.choices = choices or []
        self.default = default


class List:
    """Validator for list-type config values."""
    def __init__(self, default=None, **kwargs):
        self.default = default if default is not None else []


class Placeholders:
    def __init__(self, default="", placeholder_scope="any", **kwargs):
        self.default = default


# ============================================================
# Telethon Button stub
# ============================================================
class _TelethonButton:
    """Stub for telethon.Button - provides nested builder classes."""

    class inline:
        def __init__(self, *a, **kw): pass

    class url:
        def __init__(self, *a, **kw): pass

    class text:
        def __init__(self, *a, **kw): pass

    class switch_inline:
        def __init__(self, *a, **kw): pass

    class request_phone:
        def __init__(self, *a, **kw): pass

    class request_location:
        def __init__(self, *a, **kw): pass

    def __init__(self, *a, **kw): pass
    def __call__(self, *a, **kw): return None


# ============================================================
# Fake telethon events
# ============================================================
class _FakeTelethonEvents:
    class NewMessage:
        def __init__(self, pattern=None, outgoing=None, incoming=None, **kwargs):
            pass

    class MessageEdited:
        def __init__(self, **kwargs):
            pass

    class CallbackQuery:
        def __init__(self, **kwargs):
            pass

    class InlineQuery:
        def __init__(self, **kwargs):
            pass

    class Raw:
        def __init__(self, **kwargs):
            pass

    class ChatAction:
        def __init__(self, **kwargs):
            pass

    class UserUpdate:
        def __init__(self, **kwargs):
            pass


# ============================================================
# Message proxy
# ============================================================
class _MessageProxy:
    """Thin wrapper around a bare message dict returned by _mcub_go."""

    def __init__(self, msg_id, text, sender_id):
        self.id = msg_id
        self.text = text
        self.message = text
        self.sender_id = sender_id
        self.out = True
        self.via_bot = None
        self.reply_to = None
        self.reply_to_msg_id = None
        self.reply_to_top_id = None
        self.media = None
        self.file = None
        self.document = None

    @property
    def raw_text(self):
        return self.text


# ============================================================
# Event proxy - wraps Go BridgeEvent for Python
# ============================================================
class Event:
    """Python event object.  All mutating methods call back into Go via _mcub_go."""

    def __init__(self, session_id, chat_id, msg_id, text, sender_id,
                 reply_msg=None, message_obj=None):
        self._session_id = session_id
        self.chat_id = chat_id
        self.id = msg_id
        self.text = text
        self.sender_id = sender_id
        self._reply_msg = reply_msg
        self.message = message_obj or _MessageProxy(msg_id, text, sender_id)
        self.out = True  # MCUB always processes its own messages

    async def edit(self, text, parse_mode="html", **kwargs):
        return _mcub_go.edit_message(
            self._session_id, self.chat_id, self.id,
            str(text), parse_mode or "html",
        )

    async def reply(self, text, parse_mode="html", **kwargs):
        return _mcub_go.reply_message(
            self._session_id, self.chat_id, self.id,
            str(text), parse_mode or "html",
        )

    async def delete(self):
        return _mcub_go.delete_message(self._session_id, self.chat_id, self.id)

    async def respond(self, text, parse_mode="html", **kwargs):
        return _mcub_go.send_message(
            self._session_id, self.chat_id,
            str(text), parse_mode or "html",
        )

    async def get_reply_message(self):
        if self._reply_msg:
            return self._reply_msg
        result = _mcub_go.get_reply_message(self._session_id, self.chat_id, self.id)
        if result:
            return _MessageProxy(
                result["id"],
                result.get("text", ""),
                result.get("sender_id", 0),
            )
        return None

    async def download_media(self, file_path):
        return _mcub_go.download_media(
            self._session_id, self.chat_id, self.id, file_path
        )

    @property
    def r_text(self):
        """Raw text alias used by some MCUB modules."""
        return self.text


# ============================================================
# User proxy
# ============================================================
class _UserProxy:
    def __init__(self, info_dict):
        if isinstance(info_dict, dict):
            self.id = info_dict.get("id", 0)
            self.first_name = info_dict.get("first_name", "")
            self.last_name = info_dict.get("last_name", "")
            self.username = info_dict.get("username", "")
            self.premium = info_dict.get("is_premium", False)
            self.phone = info_dict.get("phone", "")
        else:
            self.id = 0
            self.first_name = str(info_dict)
            self.last_name = ""
            self.username = ""
            self.premium = False
            self.phone = ""

    def stringify(self):
        return f"User(id={self.id}, first_name={self.first_name!r})"


# ============================================================
# Client proxy - fakes a Telethon TelegramClient
# ============================================================
class ClientProxy:
    def __init__(self, session_id):
        self._session_id = session_id

    async def get_me(self):
        info = _mcub_go.get_me(self._session_id)
        return _UserProxy(info) if info else None

    async def send_message(self, entity, message="", parse_mode="html", **kwargs):
        chat_id = _resolve_entity_id(entity)
        return _mcub_go.send_message(
            self._session_id, chat_id, str(message), parse_mode or "html"
        )

    async def edit_message(self, entity, msg_id, text, parse_mode="html", **kwargs):
        chat_id = _resolve_entity_id(entity)
        return _mcub_go.edit_message(
            self._session_id, chat_id, msg_id, str(text), parse_mode or "html"
        )

    async def delete_messages(self, entity, msg_ids, **kwargs):
        chat_id = _resolve_entity_id(entity)
        if isinstance(msg_ids, int):
            msg_ids = [msg_ids]
        for mid in msg_ids:
            _mcub_go.delete_message(self._session_id, chat_id, mid)

    async def get_entity(self, entity):
        entity_id = _resolve_entity_id(entity)
        info = _mcub_go.get_entity(self._session_id, entity_id)
        return _UserProxy(info) if info else None

    async def get_messages(self, entity, ids=None, limit=None, **kwargs):
        chat_id = _resolve_entity_id(entity)
        if ids is not None:
            result = _mcub_go.get_message(self._session_id, chat_id, ids)
            if result:
                return _MessageProxy(
                    result["id"],
                    result.get("text", ""),
                    result.get("sender_id", 0),
                )
        return None

    def is_connected(self):
        return True

    async def download_media(self, message, file=None, **kwargs):
        if hasattr(message, "_session_id"):
            return _mcub_go.download_media(
                message._session_id, message.chat_id, message.id, file or ""
            )
        return None

    async def __call__(self, request, *a, **kw):
        """Allow client(SomeRequest(...)) pattern."""
        return None


# ============================================================
# Register proxy - provides kernel.register for function-style modules
# ============================================================
class _RegisterProxy:
    """Proxy for kernel.register used by function-based MCUB modules."""

    def __init__(self, module_name="unknown"):
        self._module_name = module_name
        self.registered_commands = []  # list of (name, doc_en, doc_ru)

    def command(self, name, doc_en="", doc_ru="", alias=None, **kwargs):
        mod_name = self._module_name
        reg = self
        def decorator(func):
            import asyncio, functools
            # Make async if needed
            if asyncio.iscoroutinefunction(func):
                handler = func
            else:
                @functools.wraps(func)
                async def handler(event):
                    return func(event)
            _command_handlers[(mod_name, name)] = handler
            reg.registered_commands.append((name, doc_en, doc_ru))
            # Aliases
            if alias:
                for a in ([alias] if isinstance(alias, str) else alias):
                    _command_handlers[(mod_name, a)] = handler
                    reg.registered_commands.append((a, doc_en, doc_ru))
            return func
        return decorator

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


# ============================================================
# Kernel proxy - exposes Go kernel API to Python modules
# ============================================================
class KernelProxy:
    """Proxy object that Python modules receive as ``self.kernel``."""

    def __init__(self, session_id=0, module_name="unknown"):
        self._session_id = session_id
        self._module_name = module_name
        self.client = ClientProxy(session_id)
        self.custom_prefix = _mcub_go.get_prefix()
        self.config = {}
        self.loaded_modules = {}
        self.system_modules = {}
        self.VERSION = _mcub_go.get_version()
        self.start_time_ts = _mcub_go.get_start_time()
        self.register = _RegisterProxy(module_name)
        self.logger = _Logger()
        self.bot_client = None
        self._live_module_configs = {}

    async def get_module_config(self, module_name, default=None):
        return default or {}

    async def save_module_config(self, module_name, config_data):
        _mcub_go.save_module_config(module_name, str(config_data))
        return True

    def store_module_config_schema(self, module_name, config):
        # Schema storage is handled on the Go side; no-op here.
        pass

    async def db_get(self, module, key):
        return _mcub_go.db_get(module, key)

    async def db_set(self, module, key, value):
        _mcub_go.db_set(module, key, str(value))

    async def db_delete(self, module, key):
        _mcub_go.db_delete(module, key)

    def log_error(self, msg):
        _mcub_go.log_error(str(msg))

    def log_debug(self, msg):
        _mcub_go.log_debug(str(msg))

    async def handle_error(self, error, source="unknown", message=None, event=None):
        _mcub_go.log_error(f"[{source}] {error}: {message}")

    async def send_log_message(self, text, file=None):
        _mcub_go.log_info(str(text))
        return True

    async def restart(self, chat_id=None, message_id=None):
        _mcub_go.restart_kernel()

    def get_module(self, name):
        return self.loaded_modules.get(name)


# ============================================================
# Simple TTL-less cache
# ============================================================
class _SimpleCache:
    def __init__(self):
        self._store = {}

    def get(self, key, default=None):
        return self._store.get(key, default)

    def set(self, key, value, ttl=None):
        self._store[key] = value

    def delete(self, key):
        self._store.pop(key, None)

    def clear(self):
        self._store.clear()


# ============================================================
# Logger
# ============================================================
class _Logger:
    def info(self, msg, *args):
        _mcub_go.log_info(str(msg) % args if args else str(msg))

    def debug(self, msg, *args):
        _mcub_go.log_debug(str(msg) % args if args else str(msg))

    def warning(self, msg, *args):
        _mcub_go.log_warn(str(msg) % args if args else str(msg))

    warn = warning  # alias

    def error(self, msg, *args):
        _mcub_go.log_error(str(msg) % args if args else str(msg))

    def critical(self, msg, *args):
        _mcub_go.log_error(str(msg) % args if args else str(msg))


# ============================================================
# Simple localised strings helper
# ============================================================
class _SimpleStrings:
    """Minimal Strings shim - returns the key when called or subscripted.

    Also exposes ``._active`` so modules that do ``lang = strings._active``
    get a usable dict-like object (self).
    """

    @property
    def _active(self):
        return self

    def __call__(self, key, **kwargs):
        return str(key)

    def __getitem__(self, key):
        return str(key)

    def __contains__(self, key):
        # Always report the key as present so callers skip default-value branches.
        return True

    def get(self, key, default=None):
        return default or str(key)

    def format(self, **kwargs):
        return ""

    def __getattr__(self, name):
        # Catch any other attribute access gracefully.
        return ""


# ============================================================
# Helper utilities
# ============================================================
def _resolve_entity_id(entity):
    """Coerce a Telethon-style entity reference to an integer chat ID."""
    if isinstance(entity, int):
        return entity
    if hasattr(entity, "id"):
        return entity.id
    try:
        return int(entity)
    except (TypeError, ValueError):
        return 0


# ============================================================
# sys.meta_path hook
# Intercepts imports of core.*, telethon.*, utils.*, core_inline.*
# and provides stubs.
# ============================================================
class MCUBImportHook(importlib.abc.MetaPathFinder, importlib.abc.Loader):
    """Intercepts imports for MCUB-specific and Telethon packages."""

    # Top-level names whose entire subtree we handle.
    _ROOTS = frozenset(("core", "telethon", "utils", "core_inline"))

    def _is_handled(self, fullname):
        root = fullname.split(".")[0]
        return root in self._ROOTS

    # --- MetaPathFinder -------------------------------------------------

    def find_spec(self, fullname, path, target=None):
        if self._is_handled(fullname):
            return importlib.machinery.ModuleSpec(fullname, self)
        return None

    # Deprecated but kept for older Python 3.3 compat just in case.
    def find_module(self, fullname, path=None):
        if self._is_handled(fullname):
            return self
        return None

    # --- Loader ---------------------------------------------------------

    def create_module(self, spec):
        """Return a _MagicStubModule so unknown attribute access always works."""
        mod = _MagicStubModule(spec.name)
        return mod

    def exec_module(self, module):
        name = module.__name__
        self._populate(name, module)

    def load_module(self, fullname):
        """Fallback loader used by the deprecated find_module path."""
        if fullname in sys.modules:
            return sys.modules[fullname]
        mod = _MagicStubModule(fullname)
        sys.modules[fullname] = mod
        self._populate(fullname, mod)
        return mod

    # --- Population helpers --------------------------------------------

    def _populate(self, name, mod):
        """Set well-known attributes on stub modules; magic getattr covers the rest."""

        if name in ("core.lib.loader.module_base", "core.lib.loader"):
            mod.ModuleBase = ModuleBase
            mod.command = command
            mod.inline = inline
            mod.callback = callback
            mod.bot_command = bot_command
            mod.loop = loop

        elif name == "core.lib.loader.module_config":
            mod.ModuleConfig = ModuleConfig
            mod.ConfigValue = ConfigValue
            mod.Boolean = Boolean
            mod.String = String
            mod.Integer = Integer
            mod.Float = Float
            mod.Choice = Choice
            mod.List = List
            mod.Placeholders = Placeholders
            mod.ValidationError = ValidationError

        elif name == "telethon":
            # Expose child modules eagerly so `telethon.events.X` works.
            events_mod = sys.modules.get("telethon.events")
            if events_mod is not None:
                mod.events = events_mod
            mod.TelegramClient = ClientProxy
            mod.Button = _TelethonButton
            mod.__version__ = "1.0.0-stub"
            tl_mod = sys.modules.get("telethon.tl")
            if tl_mod is not None:
                mod.tl = tl_mod

        elif name == "telethon.events":
            mod.NewMessage = _FakeTelethonEvents.NewMessage
            mod.MessageEdited = _FakeTelethonEvents.MessageEdited
            mod.CallbackQuery = _FakeTelethonEvents.CallbackQuery
            mod.InlineQuery = _FakeTelethonEvents.InlineQuery
            mod.Raw = _FakeTelethonEvents.Raw
            mod.ChatAction = _FakeTelethonEvents.ChatAction
            mod.UserUpdate = _FakeTelethonEvents.UserUpdate

        elif name == "telethon.tl":
            # Sub-modules (types, functions) populated separately; TLRequest via magic.
            pass

        elif name in ("telethon.tl.types", "telethon.types"):
            # Specific commonly-used types; magic getattr covers any others.
            for _tname in (
                "InputMediaWebPage", "InputWebDocument", "InputUserSelf",
                "MessageEntityTextUrl", "DocumentAttributeImageSize",
                "DocumentAttributeVideo", "DocumentAttributeAudio",
                "InputPeerUser", "InputPeerChat", "InputPeerChannel",
                "PeerUser", "PeerChat", "PeerChannel",
                "User", "Chat", "Channel",
                "Message", "MessageService",
                "UpdateNewMessage", "UpdateEditMessage",
            ):
                setattr(mod, _tname, _make_stub_class(_tname))

        elif name == "telethon.errors":
            for _ename in (
                "ChannelsTooMuchError", "FloodWaitError", "UserPrivacyRestrictedError",
                "ChatAdminRequiredError", "UserNotParticipantError",
                "MessageNotModifiedError", "MessageIdInvalidError",
                "RPCError", "BadRequestError",
            ):
                setattr(mod, _ename, _make_stub_class(_ename))

        elif name == "telethon.errors.rpcerrorlist":
            for _ename in (
                "MessageNotModifiedError", "FloodWaitError", "ChannelsTooMuchError",
                "UserPrivacyRestrictedError", "ChatAdminRequiredError",
                "UserNotParticipantError", "MessageIdInvalidError",
            ):
                setattr(mod, _ename, _make_stub_class(_ename))

        elif name in ("telethon.tl.functions",
                      "telethon.tl.functions.channels",
                      "telethon.tl.functions.messages",
                      "telethon.tl.functions.users",
                      "telethon.tl.functions.account",
                      "telethon.tl.functions.photos"):
            # All function requests are stubs; magic getattr covers any name.
            pass

        elif name == "utils.strings":
            mod.Strings = lambda *a, **kw: _SimpleStrings()
            mod.get_available_locales = lambda: []

        elif name == "utils":
            mod.register_decorated_placeholders = lambda *a, **kw: None
            mod.format_placeholders = lambda *a, **kw: ""
            mod.unregister_scope = lambda *a, **kw: None
            mod.resolve_placeholders = lambda *a, **kw: ""
            mod.answer = lambda *a, **kw: None

        elif name == "utils.restart":
            mod.restart_kernel = lambda *a, **kw: None

        elif name == "core.lib.loader.repository":
            mod.validate_remote_url = lambda *a, **kw: True

        elif name == "core.lib.utils.exceptions":
            mod.CommandConflictError = type(
                "CommandConflictError", (Exception,), {}
            )

        elif name == "core.lib.utils.logger":
            mod.ErrorFormatter = _make_stub_class("ErrorFormatter")

        elif name == "core.langpacks":
            mod.get_all_module_strings = lambda *a, **kw: {}

        # core_inline.*, core.lib.loader.hikka_compat.*, and all other
        # sub-packages under the handled roots: the _MagicStubModule base
        # class already covers any attribute access via __getattr__.


# Install the import hook at the front of sys.meta_path so it takes
# priority over the filesystem finders.
_hook = MCUBImportHook()
# Avoid duplicate registration if this file is exec'd more than once.
if not any(isinstance(h, MCUBImportHook) for h in sys.meta_path):
    sys.meta_path.insert(0, _hook)

# ============================================================
# Pre-populate sys.modules with stub packages so that
# ``from core.lib.loader.module_base import ModuleBase`` works even
# before the hook's exec_module fires for the parent packages.
# ============================================================
_PKG_TREE = [
    "core",
    "core.lib",
    "core.lib.loader",
    "core.lib.loader.module_base",
    "core.lib.loader.module_config",
    "core.lib.loader.repository",
    "core.lib.loader.hikka_compat",
    "core.lib.loader.hikka_compat.fake_package",
    "core.lib.utils",
    "core.lib.utils.exceptions",
    "core.lib.utils.logger",
    "core.langpacks",
    "core_inline",
    "core_inline.api",
    "core_inline.api.inline",
    "core_inline.lib",
    "core_inline.lib.manager",
    "telethon",
    "telethon.events",
    "telethon.tl",
    "telethon.tl.types",
    "telethon.tl.functions",
    "telethon.tl.functions.channels",
    "telethon.tl.functions.messages",
    "telethon.tl.functions.users",
    "telethon.tl.functions.account",
    "telethon.tl.functions.photos",
    "telethon.errors",
    "telethon.errors.rpcerrorlist",
    "telethon.types",
    "utils",
    "utils.strings",
    "utils.restart",
]

for _pkg in _PKG_TREE:
    if _pkg not in sys.modules:
        _mod = _MagicStubModule(_pkg)
        sys.modules[_pkg] = _mod
        _hook._populate(_pkg, _mod)

# Ensure parent packages expose their children as attributes so that
# ``import telethon; telethon.events.NewMessage`` works.
sys.modules["core"].lib = sys.modules["core.lib"]
sys.modules["core.lib"].loader = sys.modules["core.lib.loader"]
sys.modules["core.lib.loader"].module_base = sys.modules["core.lib.loader.module_base"]
sys.modules["core.lib.loader"].module_config = sys.modules["core.lib.loader.module_config"]
sys.modules["core.lib.loader"].repository = sys.modules["core.lib.loader.repository"]
sys.modules["core.lib"].utils = sys.modules["core.lib.utils"]
sys.modules["core.lib.utils"].exceptions = sys.modules["core.lib.utils.exceptions"]
sys.modules["core.lib.utils"].logger = sys.modules["core.lib.utils.logger"]
sys.modules["telethon"].events = sys.modules["telethon.events"]
sys.modules["telethon"].tl = sys.modules["telethon.tl"]
sys.modules["telethon"].errors = sys.modules["telethon.errors"]
sys.modules["telethon.tl"].types = sys.modules["telethon.tl.types"]
sys.modules["telethon.tl"].functions = sys.modules["telethon.tl.functions"]
sys.modules["telethon.tl.functions"].channels = sys.modules["telethon.tl.functions.channels"]
sys.modules["telethon.tl.functions"].messages = sys.modules["telethon.tl.functions.messages"]
sys.modules["telethon.errors"].rpcerrorlist = sys.modules["telethon.errors.rpcerrorlist"]
sys.modules["utils"].strings = sys.modules["utils.strings"]
sys.modules["utils"].restart = sys.modules["utils.restart"]
sys.modules["core_inline"].api = sys.modules["core_inline.api"]
sys.modules["core_inline.api"].inline = sys.modules["core_inline.api.inline"]
sys.modules["core_inline"].lib = sys.modules["core_inline.lib"]
sys.modules["core_inline.lib"].manager = sys.modules["core_inline.lib.manager"]

# Also expose telethon.types as alias for telethon.tl.types
sys.modules["telethon"].types = sys.modules["telethon.types"]

print("[mcub_compat] Python compatibility layer loaded", flush=True)
