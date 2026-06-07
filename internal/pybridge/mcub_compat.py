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

        # Wire up self._strings stub (do NOT assign self.log - conflicts with @property)
        self._strings = None

        # Find class-level strings dict from MRO and convert to _SimpleStrings.
        # Assigned via object.__setattr__ to bypass property descriptors.
        strings_raw = None
        for klass in type(self).__mro__:
            if "strings" in klass.__dict__:
                val = klass.__dict__["strings"]
                if isinstance(val, dict):
                    strings_raw = val
                    break
                elif isinstance(val, property):
                    # Already a property on a base class - let it stay
                    break

        object.__setattr__(
            self, '_strings_obj',
            _SimpleStrings(strings_raw) if strings_raw is not None else _SimpleStrings()
        )

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
    def strings(self):
        return object.__getattribute__(self, '_strings_obj')

    @property
    def kernel(self):
        return self._kernel_obj

    @property
    def client(self):
        """Shortcut to kernel.client (or a bare ClientProxy)."""
        k = self._kernel_obj
        if k is not None and hasattr(k, 'client'):
            return k.client
        return self._client_obj or ClientProxy(0)

    # Button stub so modules can do self.Button.inline(...)
    class Button:
        """Stub for self.Button used by modules (e.g. man.py)."""
        @staticmethod
        def inline(text, callback=None, args=None, ttl=None, allow_user=None, **kw):
            return _TelethonButton.inline(text)
        @staticmethod
        def url(text, url="", **kw):
            return _TelethonButton.url(text, url)
        @staticmethod
        def text(text, **kw):
            return _TelethonButton.text(text)

    def set_kernel(self, kernel_proxy):
        self._kernel_obj = kernel_proxy

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
        text = getattr(event, 'text', None) or ""
        parts = text.split(None, 1)
        return parts[1] if len(parts) > 1 else ""

    def args(self, event):
        """Return command arguments as a list of tokens."""
        return self.args_raw(event).split()

    def _get_strings(self):
        return lambda key, **kw: key

    async def log_error(self, msg):
        _mcub_go.log_error(str(msg))

    async def edit(self, event, text, parse_mode="html", as_html=False,
                   buttons=None, file=None, **kwargs):
        """Edit the triggering message (calls event.edit)."""
        if as_html:
            parse_mode = "html"
        if hasattr(event, 'edit') and callable(event.edit):
            return await event.edit(str(text), parse_mode=parse_mode)
        return None

    async def answer(self, event, text, as_html=False, kernel=None, **kwargs):
        """Reply to or edit the event message."""
        parse_mode = "html" if as_html else kwargs.get("parse_mode", "html")
        if hasattr(event, 'edit') and callable(event.edit):
            return await event.edit(str(text), parse_mode=parse_mode)
        if hasattr(event, 'reply') and callable(event.reply):
            return await event.reply(str(text), parse_mode=parse_mode)
        return None

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
        self._values = {}
        for cv in config_values:
            if hasattr(cv, 'key'):
                self._values[cv.key] = cv.default
            elif isinstance(cv, tuple) and len(cv) >= 2:
                self._values[cv[0]] = cv[1]

    def get(self, key, default=None):
        return self._values.get(key, default)

    def __getitem__(self, key):
        return self._values[key]

    def __setitem__(self, key, value):
        self._values[key] = value

    def __getattr__(self, key):
        if key.startswith('_'):
            raise AttributeError(key)
        try:
            return self._values[key]
        except KeyError:
            raise AttributeError(f"ModuleConfig has no attribute {key!r}")

    def __iter__(self):
        return iter(self._values)

    def __contains__(self, key):
        return key in self._values

    def __len__(self):
        return len(self._values)

    def from_dict(self, d):
        self._values.update(d)

    def to_dict(self):
        return dict(self._values)

    def items(self):
        return self._values.items()

    def keys(self):
        return self._values.keys()

    def values(self):
        return self._values.values()

    def update(self, mapping):
        for key, value in mapping.items():
            self._values[key] = value


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
        self.default = default if default != "" else (choices[0] if choices else "")


class List:
    """Validator for list-type config values."""
    def __init__(self, default=None, **kwargs):
        self.default = default if default is not None else []


class Placeholders:
    def __init__(self, default="", placeholder_scope="any", **kwargs):
        self.default = default


class URL:
    """URL validator stub."""
    def __init__(self, default="", **kwargs):
        self.default = default


class Regex:
    """Regex-validated string stub."""
    def __init__(self, default="", pattern=None, **kwargs):
        self.default = default
        self.pattern = pattern


class JSON:
    """JSON value validator stub."""
    def __init__(self, default=None, **kwargs):
        self.default = default


class Color:
    """Hex color validator stub."""
    def __init__(self, default="#000000", **kwargs):
        self.default = default


class Emoji:
    """Emoji validator stub."""
    def __init__(self, default="", **kwargs):
        self.default = default


class MultiChoice:
    """Multi-choice validator stub."""
    def __init__(self, choices=None, default=None, **kwargs):
        self.choices = choices or []
        self.default = default if default is not None else []


class Link(String):
    """URL/link validator stub."""
    def __init__(self, default="", schemes=None, **kwargs):
        super().__init__(default=default, **kwargs)
        self.schemes = schemes


class RegExp(String):
    """Regex-validated string stub (alias for Regex)."""
    def __init__(self, pattern="", default="", **kwargs):
        super().__init__(default=default, **kwargs)
        self.pattern = pattern


class TelegramID(Integer):
    """Telegram ID validator stub."""
    def __init__(self, default=0, **kwargs):
        super().__init__(default=default, **kwargs)


class EntityLike:
    """Telegram entity validator stub."""
    def __init__(self, default="", **kwargs):
        self.default = default


class Union:
    """Union of validators stub - first match wins."""
    def __init__(self, *validators, default=None, **kwargs):
        self.validators = validators
        self.default = default if default is not None else (validators[0].default if validators else None)


class Secret:
    """Sensitive value validator stub."""
    def __init__(self, default=None, **kwargs):
        self.default = default
        self.secret = True


class Hidden:
    """Hidden value validator stub."""
    def __init__(self, validator=None, default=None, **kwargs):
        self.validator = validator
        self.default = default if default is not None else (validator.default if validator else None)
        self.secret = True


class NoneType:
    """None-only validator stub."""
    def __init__(self, default=None, **kwargs):
        self.default = default


class DictType:
    """Dictionary validator stub."""
    def __init__(self, default=None, **kwargs):
        self.default = default if default is not None else {}


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

        # ---- Pipeline support ----
        # Set by _mcub_call_command when the command is part of a pipeline.
        self.piped = False           # True when running inside a pipeline
        self.pipe_input = None       # text output from the preceding | segment
        self.pipe_output = None      # text this command wants to pass to the next segment
        self.pipe_exit_code = 0      # 0 = success, non-zero = failure
        self.no_add_args_to_input = False  # suppress auto-arg-appending in pipe mode

    # ---- Pipeline helper methods ------------------------------------------------

    def set_pipe_output(self, text):
        """Set what this command outputs to the pipe.

        Modules can call this instead of (or in addition to) event.edit() when
        they want fine-grained control over what the next piped command receives.
        """
        self.pipe_output = str(text) if text is not None else ""

    def get_pipe_input(self):
        """Return the input received from the preceding pipe segment, or ''."""
        return self.pipe_input or ""

    # ---- Telegram operations ---------------------------------------------------

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
# InfiniteLoop - background loop wrapper used by _RegisterProxy.loop
# ============================================================
class _InfiniteLoop:
    """Background loop managed by _RegisterProxy."""

    def __init__(self, func, interval, autostart, wait_before):
        self.func = func
        self.interval = interval
        self.autostart = autostart
        self._wait_before = wait_before
        self.status = False
        self._task = None

    def start(self):
        import asyncio
        if self._task and not self._task.done():
            return
        try:
            loop = asyncio.get_event_loop()
            if loop.is_running():
                self._task = asyncio.ensure_future(self._run())
        except RuntimeError:
            pass

    def stop(self):
        self.status = False
        if self._task and not self._task.done():
            self._task.cancel()

    def restart(self):
        self.stop()
        self.start()

    async def _run(self):
        import asyncio
        self.status = True
        try:
            while self.status:
                if self._wait_before:
                    await asyncio.sleep(self.interval)
                if not self.status:
                    break
                try:
                    result = self.func()
                    if asyncio.iscoroutine(result):
                        await result
                except asyncio.CancelledError:
                    break
                except Exception as e:
                    try:
                        _mcub_go.log_error(f"InfiniteLoop error in {getattr(self.func, '__name__', '?')}: {e}")
                    except Exception:
                        pass
                if not self._wait_before:
                    await asyncio.sleep(self.interval)
        finally:
            self.status = False

    def __call__(self, *args, **kwargs):
        return self


# ============================================================
# Register proxy - provides kernel.register for function-style modules
# ============================================================
class _RegisterProxy:
    """Proxy for kernel.register used by function-based MCUB modules."""

    def __init__(self, module_name="unknown"):
        self._module_name = module_name
        self.registered_commands = []  # list of (name, doc_en, doc_ru)
        self._watchers = []
        self._loops = []
        self._event_handlers = []
        self._bot_commands = []

    def command(self, name_or_func=None, doc_en="", doc_ru="", alias=None, **kwargs):
        mod_name = self._module_name
        reg = self

        # Support both @register.command("ping") and @register.command
        if callable(name_or_func):
            # Called as bare decorator without arguments - use function name
            func = name_or_func
            name = getattr(func, '__name__', 'unknown')
            import asyncio, functools
            if asyncio.iscoroutinefunction(func):
                handler = func
            else:
                @functools.wraps(func)
                async def handler(event):
                    return func(event)
            _command_handlers[(mod_name, name)] = handler
            reg.registered_commands.append((name, doc_en, doc_ru))
            return func

        name = name_or_func if name_or_func is not None else ""

        def decorator(func):
            import asyncio, functools
            cmd_name = name or getattr(func, '__name__', 'unknown')
            # Strip leading prefix chars
            import re
            cmd_name = re.sub(r'^[\.\!\,\/\\]+', '', cmd_name).rstrip('$')
            if asyncio.iscoroutinefunction(func):
                handler = func
            else:
                @functools.wraps(func)
                async def handler(event):
                    return func(event)
            _command_handlers[(mod_name, cmd_name)] = handler
            reg.registered_commands.append((cmd_name, doc_en, doc_ru))
            if alias:
                for a in ([alias] if isinstance(alias, str) else alias):
                    _command_handlers[(mod_name, a)] = handler
                    reg.registered_commands.append((a, doc_en, doc_ru))
            return func
        return decorator

    def watcher(self, func=None, *, bot_client=False, **tags):
        """Register a message watcher with optional filter tags."""
        def decorator(f):
            self._watchers.append((f, bot_client, tags))
            return f
        if func is not None and callable(func):
            return decorator(func)
        return decorator

    def loop(self, interval=60, autostart=True, wait_before=False, **kwargs):
        """Register a background loop."""
        def decorator(f):
            loop_obj = _InfiniteLoop(f, interval, autostart, wait_before)
            self._loops.append(loop_obj)
            if autostart:
                loop_obj.start()
            return loop_obj
        return decorator

    def bot_command(self, pattern, *, doc_en="", doc_ru="", alias=None, **kwargs):
        """Register a Telegram native bot command."""
        def decorator(f):
            self._bot_commands.append((pattern, f, {"doc_en": doc_en, "doc_ru": doc_ru}))
            return f
        return decorator

    def event(self, event_type, *args, bot_client=False, **kwargs):
        """Register a Telegram event handler."""
        def decorator(f):
            self._event_handlers.append((event_type, f, bot_client, args, kwargs))
            return f
        return decorator

    def on_load(self, func=None, **kwargs):
        """Register a callback invoked after the module is loaded."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def on_install(self, func=None, **kwargs):
        """Register a first-install callback (no-op in bridge mode)."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def uninstall(self, func=None, **kwargs):
        """Register a cleanup callback on module unload."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def method(self, func=None, **kwargs):
        """Register a setup method (no-op in bridge mode)."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def callback_handler(self, pattern, handler):
        """Register an inline callback handler (no-op in bridge mode)."""
        pass

    def inline_temp(self, func, ttl=300, article=None, data=None, **kwargs):
        """Register a temporary inline handler; returns a UUID key."""
        import uuid
        return uuid.uuid4().hex[:8]

    async def invoke(self, command, args=None, chat_id=None, reply_to=None):
        """Invoke a command programmatically (no-op in bridge mode)."""
        pass

    def owner(self, func=None, only_admin=False):
        """Restrict handler to bot owner (passthrough in bridge mode)."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def inline(self, func=None, **kwargs):
        """Register an inline query handler stub."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator

    def callback(self, func=None, **kwargs):
        """Register a callback query handler stub."""
        def decorator(f):
            return f
        if func is not None and callable(func):
            return func
        return decorator


# ============================================================
# Kernel config helper (supports both dict and .get() access)
# ============================================================
class _KernelConfig(dict):
    """dict subclass so modules can do kernel.config["key"] or kernel.config.get("key")."""
    def __init__(self):
        super().__init__({
            "language": "ru",
            "inline_bot_username": "bot",
            "prefix": ".",
        })


# ============================================================
# Stub helpers for KernelProxy sub-objects
# ============================================================

class _VersionManagerStub:
    """Minimal version_manager stub."""
    async def detect_branch(self): return "main"
    async def get_commit_sha(self): return "unknown"
    async def get_github_commit_url(self): return ""
    async def get_latest_kernel_version(self): return _mcub_go.get_version()
    async def check_module_compatibility(self, code): return True, ""


class _DBManagerStub:
    """Minimal db_manager stub."""
    conn = None
    async def db_get(self, m, k): return _mcub_go.db_get(m, k)
    async def db_set(self, m, k, v): _mcub_go.db_set(m, k, str(v))
    async def db_delete(self, m, k): _mcub_go.db_delete(m, k)


class _ArchiveManagerStub:
    async def extract(self, *a, **kw): return False, "not supported"


class _LoaderStub:
    """Minimal _loader stub used by man.py, loader.py, etc."""

    def __init__(self, module_name="unknown"):
        self._module_name = module_name
        self._archive_mgr = _ArchiveManagerStub()

    def get_module_path(self, name):
        return f"modules_loaded/{name}.py"

    def get_module_commands(self, name, lang="en"):
        """Return (commands_list, aliases_dict, descriptions_dict)."""
        cmds = [cn for (mn, cn) in _command_handlers if mn == name]
        return cmds, {}, {}

    def get_module_version_from_file(self, path):
        return "1.0.0"

    def find_module_case_insensitive(self, name):
        for (mn, cn) in _command_handlers:
            if mn.lower() == name.lower():
                return mn, None
        return None, None

    def pick_localized_text(self, d, lang, fallback=""):
        if not d:
            return fallback
        if isinstance(d, dict):
            return d.get(lang, d.get("en", fallback))
        return str(d) if d else fallback

    def parse_requires(self, code):
        return []

    def remove_module_aliases(self, mod, cmds):
        pass

    def save_persistent_type_cache(self):
        pass

    def is_archive_url(self, url):
        return False

    async def install_from_archive(self, *a, **kw):
        return False, "not supported"

    async def install_dependency(self, dep, *a, **kw):
        return True, ""

    async def install_dependencies_batch(self, deps, *a, **kw):
        return []

    async def _pip_install(self, *a, **kw):
        return True, ""

    async def load_module_from_file(self, *a, **kw):
        return None


# ============================================================
# Kernel proxy - exposes Go kernel API to Python modules
# ============================================================
class KernelProxy:
    """Proxy object that Python modules receive as ``self.kernel``."""

    def __init__(self, session_id=0, module_name="unknown"):
        import datetime as _dt
        self._session_id = session_id
        self._module_name = module_name
        self.client = ClientProxy(session_id)
        self.custom_prefix = _mcub_go.get_prefix()
        self.config = _KernelConfig()
        self.loaded_modules = {}
        self.system_modules = {}
        self.VERSION = _mcub_go.get_version()
        self.start_time_ts = _mcub_go.get_start_time()
        self.start_time = _dt.datetime.fromtimestamp(self.start_time_ts or 0)
        self.register = _RegisterProxy(module_name)
        self.logger = _Logger()
        self.bot_client = None
        self._live_module_configs = {}
        self.inline_callback_map = {}
        self._inline_cb_lock = None
        self.repositories = []
        self.default_repo = (
            "https://raw.githubusercontent.com/hairpin01/repo-MCUB-fork/main/"
        )
        self._module_sources = {}
        self._class_module_instances = {}
        self.owner_prefixes = {}
        self.error_load_modules = 0
        self.error_load_modules_name = []
        self.power_save_mode = False
        self.shutdown_flag = False
        self.scheduler = None
        self.log_chat_id = None
        self.ADMIN_ID = None
        self.MODULES_DIR = "modules"
        self.MODULES_LOADED_DIR = "modules_loaded"
        self.load_kernel = "full"
        # Command routing dicts (filled by Go dispatcher)
        self.command_handlers = {}
        self.command_owners = {}
        # Sub-object stubs
        self.version_manager = _VersionManagerStub()
        self.db_manager = _DBManagerStub()
        self._loader = _LoaderStub(module_name)
        self.HTML_PARSER_AVAILABLE = False

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

    def get_module_inline_commands(self, module_name):
        """Return list of (cmd, description) for a module's inline handlers."""
        return []

    async def get_module_metadata(self, code):
        """Parse basic metadata from module source code."""
        import re as _re
        meta = {
            "commands": {},
            "description": "",
            "description_i18n": {},
            "version": "1.0.0",
            "author": "unknown",
            "banner_url": None,
        }
        if not code:
            return meta
        m = _re.search(r'#\s*version:\s*(\S+)', code)
        if m:
            meta["version"] = m.group(1)
        m = _re.search(r'#\s*author:\s*(.+)', code)
        if m:
            meta["author"] = m.group(1).strip()
        return meta

    async def inline_query_and_click(self, chat_id=None, query="", reply_to=None):
        """Stub - inline bot integration not available in Go bridge."""
        return False, None

    # ---- Repository management ----

    async def add_repository(self, url):
        """Add a module repository URL (validation is a no-op in bridge mode)."""
        return True, "Added"

    async def remove_repository(self, index):
        """Remove a repository by 1-based index."""
        return True, "Removed"

    async def get_repo_name(self, url):
        """Return a display name for a repository URL."""
        return url.rstrip("/").split("/")[-1] or url

    async def get_repo_modules_list(self, repo_url):
        """Fetch the list of modules from a repository's modules.ini."""
        try:
            import aiohttp
            async with aiohttp.ClientSession() as session:
                async with session.get(
                    repo_url.rstrip("/") + "/modules.ini", timeout=aiohttp.ClientTimeout(total=10)
                ) as resp:
                    if resp.status == 200:
                        text = await resp.text()
                        return [line.strip() for line in text.split("\n") if line.strip()]
        except Exception:
            pass
        return []

    async def download_module_from_repo(self, repo_url, module_name):
        """Download a module .py file from a repository URL."""
        try:
            import aiohttp
            url = repo_url.rstrip("/") + f"/{module_name}.py"
            async with aiohttp.ClientSession() as session:
                async with session.get(url, timeout=aiohttp.ClientTimeout(total=30)) as resp:
                    if resp.status == 200:
                        return await resp.text()
        except Exception:
            pass
        return None

    async def install_from_url(self, url, module_name=None, auto_dependencies=True):
        """Install a module from a URL (not supported in bridge mode)."""
        return False, "Not implemented in bridge mode"

    async def load_module_from_file(self, file_path, module_name, is_system=False, **kwargs):
        """Load a module from a local file path (delegation to Go side)."""
        return True, "ok", module_name

    async def unregister_module_commands(self, module_name, force=False):
        """Unregister all commands belonging to a module."""
        keys_to_remove = [k for k in _command_handlers if k[0] == module_name]
        for k in keys_to_remove:
            del _command_handlers[k]

    async def _execute_pipeline(self, event, pipeline, depth=0):
        """Pipeline execution stub – pipelines are handled by the Go kernel.

        Python modules that call this (e.g. utils-piped script engine) will get
        a no-op so that they don't crash.  The actual pipeline orchestration is
        done in internal/kernel/pipeline.go.
        """
        return False

    def get_prefix_for_sender(self, sender_id):
        """Return the command prefix active for a given sender.

        The Go kernel uses a single global prefix; sender-specific prefixes are
        not yet supported, so we always return the global prefix.
        """
        return _mcub_go.get_prefix()


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
    """Minimal Strings shim - supports locale dict, langpack ref, and flat dict.

    Supports all patterns used by MCUB modules:
      strings("key")           - call with key
      strings("key", arg=val) - call with format args
      strings["key"]           - subscript
      strings.get("key", def) - with default
      strings.keys()           - all keys
      "key" in strings         - contains check
    """

    def __init__(self, data=None, locale="en"):
        self._data = data or {}
        self._locale = locale
        # If data has only "name" key, it's a langpack ref
        self._is_langpack = (
            isinstance(data, dict) and "name" in data and len(data) == 1
        )

    @property
    def _active(self):
        """Expose active locale dict (or self) for modules that access ._active."""
        if isinstance(self._data, dict):
            locale_data = self._data.get(self._locale, self._data.get("en", {}))
            if isinstance(locale_data, dict):
                return locale_data
        return self

    def _get(self, key):
        if isinstance(self._data, dict):
            # Try locale-specific first
            locale_data = self._data.get(self._locale, self._data.get("en", {}))
            if isinstance(locale_data, dict) and key in locale_data:
                return str(locale_data[key])
            # Try flat key directly (non-dict value)
            if key in self._data and not isinstance(self._data[key], dict):
                return str(self._data[key])
        return str(key)  # fallback: return the key itself

    def __call__(self, key, **kwargs):
        val = self._get(key)
        if kwargs:
            try:
                return val.format(**kwargs)
            except (KeyError, ValueError):
                return val
        return val

    def __getitem__(self, key):
        return self._get(key)

    def __contains__(self, key):
        # Always return True - stub always has the key
        return True

    def __getattr__(self, name):
        if name.startswith("_"):
            raise AttributeError(name)
        return self._get(name)

    def get(self, key, default=None):
        if not key:
            return default
        return self._get(key)

    def keys(self):
        if isinstance(self._data, dict):
            locale_data = self._data.get(self._locale, self._data.get("en", {}))
            if isinstance(locale_data, dict):
                return locale_data.keys()
        return {}.keys()

    def format(self, **kwargs):
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
            mod.URL = URL
            mod.Regex = Regex
            mod.JSON = JSON
            mod.Color = Color
            mod.Emoji = Emoji
            mod.MultiChoice = MultiChoice
            mod.Link = Link
            mod.RegExp = RegExp
            mod.TelegramID = TelegramID
            mod.EntityLike = EntityLike
            mod.Union = Union
            mod.Secret = Secret
            mod.Hidden = Hidden
            mod.NoneType = NoneType
            mod.DictType = DictType

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
            mod.get_available_locales = lambda: ["en", "ru", "uk", "de", "es"]

        elif name == "utils":
            mod.register_decorated_placeholders = lambda *a, **kw: None
            mod.format_placeholders = lambda *a, **kw: ""
            mod.unregister_scope = lambda *a, **kw: None
            mod.resolve_placeholders = lambda *a, **kw: ""
            mod.answer = lambda *a, **kw: None
            mod.parse_arguments = lambda text, prefix=".": text.split()[1:] if " " in (text or "") else []
            mod.get_args_raw = lambda event: (
                (getattr(event, 'text', '') or "").split(None, 1)[1]
                if " " in (getattr(event, 'text', '') or "") else ""
            )
            mod.get_args_html = mod.get_args_raw
            mod.config_placeholders = lambda name: ""
            mod.HTML_PARSER_AVAILABLE = False
            mod.telegram_to_html = lambda text, entities: text

        elif name == "utils.arg_parser":
            # Full PipelineParser stub (simplified - real one is complex)
            class _PipelineParser:
                def __init__(self, text):
                    self.text = text
                    self.segments = []
                def is_simple(self):
                    return True
            mod.PipelineParser = _PipelineParser
            mod.ArgumentParser = _PipelineParser
            mod.parse_arguments = lambda text, prefix=".": []
            mod.parse_pipeline = lambda text: _PipelineParser(text)

        elif name == "utils.html_parser":
            mod.HTML_PARSER_AVAILABLE = False
            mod.telegram_to_html = lambda text, entities: text
            mod.parse_html = lambda text: (text, [])

        elif name == "utils.helpers":
            mod.get_args = lambda event: []
            mod.get_args_raw = lambda event: (
                (getattr(event, 'text', '') or "").split(None, 1)[1]
                if " " in (getattr(event, 'text', '') or "") else ""
            )
            mod.get_args_html = mod.get_args_raw
            mod.escape_html = lambda text: text
            mod.format_time = lambda s, **kw: f"{s}s"
            mod.format_date = lambda ts, **kw: str(ts)
            mod.get_prefix = lambda *a: "."
            mod.get_lang = lambda *a, **kw: "en"
            mod.answer = lambda *a, **kw: None

        elif name == "utils.restart":
            import dataclasses as _dc

            @_dc.dataclass
            class _RestartContext:
                chat_id: int = 0
                message_id: int = 0
                timestamp: float = 0.0
                thread_id: object = None

            async def _restart_kernel(kernel, chat_id=None, message_id=None, thread_id=None):
                _mcub_go.restart_kernel()

            def _read_restart_context(path):
                return _RestartContext()

            mod.RestartContext = _RestartContext
            mod.restart_kernel = _restart_kernel
            mod.read_restart_context = _read_restart_context
            mod.write_restart_file = lambda *a, **kw: None
            mod.safe_restart = lambda *a, **kw: None

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
    "utils.arg_parser",
    "utils.html_parser",
    "utils.helpers",
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
sys.modules["utils"].arg_parser = sys.modules["utils.arg_parser"]
sys.modules["utils"].html_parser = sys.modules["utils.html_parser"]
sys.modules["utils"].helpers = sys.modules["utils.helpers"]
# Hoist key symbols from sub-modules onto utils for convenience
sys.modules["utils"].PipelineParser = sys.modules["utils.arg_parser"].PipelineParser
sys.modules["utils"].HTML_PARSER_AVAILABLE = False
sys.modules["utils"].telegram_to_html = sys.modules["utils.html_parser"].telegram_to_html
sys.modules["core_inline"].api = sys.modules["core_inline.api"]
sys.modules["core_inline.api"].inline = sys.modules["core_inline.api.inline"]
sys.modules["core_inline"].lib = sys.modules["core_inline.lib"]
sys.modules["core_inline.lib"].manager = sys.modules["core_inline.lib.manager"]

# Also expose telethon.types as alias for telethon.tl.types
sys.modules["telethon"].types = sys.modules["telethon.types"]

print("[mcub_compat] Python compatibility layer loaded", flush=True)
