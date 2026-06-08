// Package pybridge embeds CPython inside the Go binary and exposes a thin
// interface for loading MCUB Python modules and dispatching commands to them.
//
// Build requirements:
//   - python3-dev (or python3-embed) installed
//   - CGo enabled
//   - pkg-config with python3-embed entry
package pybridge

/*
#cgo pkg-config: python3-embed
#include <Python.h>
#include <stdlib.h>
#include <string.h>

// ---------------------------------------------------------------------------
// Forward declarations of Go-exported callback functions.
// These are defined (via //export directives) in callbacks.go.
// ---------------------------------------------------------------------------

extern int    goEditMessage(long long sID, long long cID, long long mID, char* text, char* pm);
extern int    goReplyMessage(long long sID, long long cID, long long mID, char* text, char* pm);
extern int    goDeleteMessage(long long sID, long long cID, long long mID);
extern int    goSendMessage(long long sID, long long cID, char* text, char* pm);
extern char*  goGetMe(long long sID);
extern char*  goGetEntity(long long sID, long long eID);
extern char*  goGetReplyMessage(long long sID, long long cID, long long mID);
extern int    goDownloadMedia(long long sID, long long cID, long long mID, char* fp);
extern char*  goGetMessage(long long sID, long long cID, long long mID);
extern char*  goGetPrefix();
extern char*  goGetVersion();
extern long long goGetStartTime();
extern void   goLogInfo(char* msg);
extern void   goLogDebug(char* msg);
extern void   goLogWarn(char* msg);
extern void   goLogError(char* msg);
extern char*  goDBGet(char* module, char* key);
extern void   goDBSet(char* module, char* key, char* value);
extern void   goDBDelete(char* module, char* key);
extern void   goSaveModuleConfig(char* mname, char* data);
extern void   goRestartKernel();

// ---------------------------------------------------------------------------
// json_loads helper – converts a C JSON string to a Python dict/list.
// Caches the json module to avoid repeated imports.
// ---------------------------------------------------------------------------
static PyObject* mcub_json_loads(const char* json_str) {
    static PyObject* json_mod = NULL;
    if (json_mod == NULL) {
        json_mod = PyImport_ImportModule("json");
        if (json_mod == NULL) return NULL;
    }
    PyObject* s = PyUnicode_FromString(json_str);
    if (s == NULL) return NULL;
    PyObject* result = PyObject_CallMethod(json_mod, "loads", "O", s);
    Py_DECREF(s);
    return result;
}

// ---------------------------------------------------------------------------
// Python extension methods for the _mcub_go C module
// ---------------------------------------------------------------------------

static PyObject* py_edit_message(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    const char *text, *pm;
    if (!PyArg_ParseTuple(args, "LLLss", &sID, &cID, &mID, &text, &pm))
        return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    int ret = goEditMessage(sID, cID, mID, (char*)text, (char*)pm);
    PyEval_RestoreThread(ts);
    if (ret < 0) { PyErr_SetString(PyExc_RuntimeError, "edit_message failed"); return NULL; }
    Py_RETURN_NONE;
}

static PyObject* py_reply_message(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    const char *text, *pm;
    if (!PyArg_ParseTuple(args, "LLLss", &sID, &cID, &mID, &text, &pm))
        return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    int ret = goReplyMessage(sID, cID, mID, (char*)text, (char*)pm);
    PyEval_RestoreThread(ts);
    if (ret < 0) { PyErr_SetString(PyExc_RuntimeError, "reply_message failed"); return NULL; }
    Py_RETURN_NONE;
}

static PyObject* py_delete_message(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    if (!PyArg_ParseTuple(args, "LLL", &sID, &cID, &mID)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    int ret = goDeleteMessage(sID, cID, mID);
    PyEval_RestoreThread(ts);
    if (ret < 0) { PyErr_SetString(PyExc_RuntimeError, "delete_message failed"); return NULL; }
    Py_RETURN_NONE;
}

static PyObject* py_send_message(PyObject* self, PyObject* args) {
    long long sID, cID;
    const char *text, *pm;
    if (!PyArg_ParseTuple(args, "LLss", &sID, &cID, &text, &pm)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    int ret = goSendMessage(sID, cID, (char*)text, (char*)pm);
    PyEval_RestoreThread(ts);
    if (ret < 0) { PyErr_SetString(PyExc_RuntimeError, "send_message failed"); return NULL; }
    Py_RETURN_NONE;
}

static PyObject* py_get_me(PyObject* self, PyObject* args) {
    long long sID;
    if (!PyArg_ParseTuple(args, "L", &sID)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    char* json = goGetMe(sID);
    PyEval_RestoreThread(ts);
    if (json == NULL) Py_RETURN_NONE;
    PyObject* r = mcub_json_loads(json);
    free(json);
    if (r == NULL) Py_RETURN_NONE;
    return r;
}

static PyObject* py_get_entity(PyObject* self, PyObject* args) {
    long long sID, eID;
    if (!PyArg_ParseTuple(args, "LL", &sID, &eID)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    char* json = goGetEntity(sID, eID);
    PyEval_RestoreThread(ts);
    if (json == NULL) Py_RETURN_NONE;
    PyObject* r = mcub_json_loads(json);
    free(json);
    if (r == NULL) Py_RETURN_NONE;
    return r;
}

static PyObject* py_get_reply_message(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    if (!PyArg_ParseTuple(args, "LLL", &sID, &cID, &mID)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    char* json = goGetReplyMessage(sID, cID, mID);
    PyEval_RestoreThread(ts);
    if (json == NULL) Py_RETURN_NONE;
    PyObject* r = mcub_json_loads(json);
    free(json);
    if (r == NULL) Py_RETURN_NONE;
    return r;
}

static PyObject* py_download_media(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    const char* fp;
    if (!PyArg_ParseTuple(args, "LLLs", &sID, &cID, &mID, &fp)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    int ret = goDownloadMedia(sID, cID, mID, (char*)fp);
    PyEval_RestoreThread(ts);
    if (ret < 0) { PyErr_SetString(PyExc_RuntimeError, "download_media failed"); return NULL; }
    Py_RETURN_NONE;
}

static PyObject* py_get_message(PyObject* self, PyObject* args) {
    long long sID, cID, mID;
    if (!PyArg_ParseTuple(args, "LLL", &sID, &cID, &mID)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    char* json = goGetMessage(sID, cID, mID);
    PyEval_RestoreThread(ts);
    if (json == NULL) Py_RETURN_NONE;
    PyObject* r = mcub_json_loads(json);
    free(json);
    if (r == NULL) Py_RETURN_NONE;
    return r;
}

static PyObject* py_get_prefix(PyObject* self, PyObject* args) {
    PyThreadState* ts = PyEval_SaveThread();
    char* s = goGetPrefix();
    PyEval_RestoreThread(ts);
    if (s == NULL) return PyUnicode_FromString(".");
    PyObject* r = PyUnicode_FromString(s);
    free(s);
    return r;
}

static PyObject* py_get_version(PyObject* self, PyObject* args) {
    PyThreadState* ts = PyEval_SaveThread();
    char* s = goGetVersion();
    PyEval_RestoreThread(ts);
    if (s == NULL) return PyUnicode_FromString("0.0.0");
    PyObject* r = PyUnicode_FromString(s);
    free(s);
    return r;
}

static PyObject* py_get_start_time(PyObject* self, PyObject* args) {
    PyThreadState* ts = PyEval_SaveThread();
    long long t = goGetStartTime();
    PyEval_RestoreThread(ts);
    return PyLong_FromLongLong(t);
}

static PyObject* py_log_info(PyObject* self, PyObject* args) {
    const char* msg; if (!PyArg_ParseTuple(args, "s", &msg)) return NULL;
    goLogInfo((char*)msg); Py_RETURN_NONE;
}
static PyObject* py_log_debug(PyObject* self, PyObject* args) {
    const char* msg; if (!PyArg_ParseTuple(args, "s", &msg)) return NULL;
    goLogDebug((char*)msg); Py_RETURN_NONE;
}
static PyObject* py_log_warn(PyObject* self, PyObject* args) {
    const char* msg; if (!PyArg_ParseTuple(args, "s", &msg)) return NULL;
    goLogWarn((char*)msg); Py_RETURN_NONE;
}
static PyObject* py_log_error(PyObject* self, PyObject* args) {
    const char* msg; if (!PyArg_ParseTuple(args, "s", &msg)) return NULL;
    goLogError((char*)msg); Py_RETURN_NONE;
}

static PyObject* py_db_get(PyObject* self, PyObject* args) {
    const char *mod, *key;
    if (!PyArg_ParseTuple(args, "ss", &mod, &key)) return NULL;
    PyThreadState* ts = PyEval_SaveThread();
    char* val = goDBGet((char*)mod, (char*)key);
    PyEval_RestoreThread(ts);
    if (val == NULL) Py_RETURN_NONE;
    PyObject* r = PyUnicode_FromString(val);
    free(val);
    return r;
}

static PyObject* py_db_set(PyObject* self, PyObject* args) {
    const char *mod, *key, *val;
    if (!PyArg_ParseTuple(args, "sss", &mod, &key, &val)) return NULL;
    goDBSet((char*)mod, (char*)key, (char*)val); Py_RETURN_NONE;
}

static PyObject* py_db_delete(PyObject* self, PyObject* args) {
    const char *mod, *key;
    if (!PyArg_ParseTuple(args, "ss", &mod, &key)) return NULL;
    goDBDelete((char*)mod, (char*)key); Py_RETURN_NONE;
}

static PyObject* py_save_module_config(PyObject* self, PyObject* args) {
    const char *mname, *data;
    if (!PyArg_ParseTuple(args, "ss", &mname, &data)) return NULL;
    goSaveModuleConfig((char*)mname, (char*)data); Py_RETURN_NONE;
}

static PyObject* py_restart_kernel(PyObject* self, PyObject* args) {
    PyThreadState* ts = PyEval_SaveThread();
    goRestartKernel();
    PyEval_RestoreThread(ts);
    Py_RETURN_NONE;
}

// ---------------------------------------------------------------------------
// Module definition & registration
// ---------------------------------------------------------------------------

static PyMethodDef McubMethods[] = {
    {"edit_message",        py_edit_message,        METH_VARARGS, "Edit a Telegram message"},
    {"reply_message",       py_reply_message,       METH_VARARGS, "Reply to a Telegram message"},
    {"delete_message",      py_delete_message,      METH_VARARGS, "Delete a Telegram message"},
    {"send_message",        py_send_message,        METH_VARARGS, "Send a Telegram message"},
    {"get_me",              py_get_me,              METH_VARARGS, "Get self user info as dict"},
    {"get_entity",          py_get_entity,          METH_VARARGS, "Get entity info as dict"},
    {"get_reply_message",   py_get_reply_message,   METH_VARARGS, "Get replied-to message as dict"},
    {"download_media",      py_download_media,      METH_VARARGS, "Download media to disk"},
    {"get_message",         py_get_message,         METH_VARARGS, "Get message by ID as dict"},
    {"get_prefix",          py_get_prefix,          METH_NOARGS,  "Get command prefix string"},
    {"get_version",         py_get_version,         METH_NOARGS,  "Get kernel version string"},
    {"get_start_time",      py_get_start_time,      METH_NOARGS,  "Get kernel start timestamp"},
    {"log_info",            py_log_info,            METH_VARARGS, "Log at INFO level"},
    {"log_debug",           py_log_debug,           METH_VARARGS, "Log at DEBUG level"},
    {"log_warn",            py_log_warn,            METH_VARARGS, "Log at WARN level"},
    {"log_error",           py_log_error,           METH_VARARGS, "Log at ERROR level"},
    {"db_get",              py_db_get,              METH_VARARGS, "Read from persistent KV store"},
    {"db_set",              py_db_set,              METH_VARARGS, "Write to persistent KV store"},
    {"db_delete",           py_db_delete,           METH_VARARGS, "Delete from persistent KV store"},
    {"save_module_config",  py_save_module_config,  METH_VARARGS, "Persist module config"},
    {"restart_kernel",      py_restart_kernel,      METH_NOARGS,  "Restart the Go kernel"},
    {NULL, NULL, 0, NULL}
};

static struct PyModuleDef mcubmodule = {
    PyModuleDef_HEAD_INIT, "_mcub_go",
    "MCUB Go bridge C extension – exposes Go callbacks to Python modules.",
    -1, McubMethods
};

// init function registered via PyImport_AppendInittab.
// Must NOT be static so that AppendInittab can store the pointer, but since
// we pass it by address that is fine for a static function too.
static PyObject* PyInit__mcub_go_impl(void) {
    return PyModule_Create(&mcubmodule);
}

// Call this BEFORE Py_Initialize().
static int setup_mcub_extension(void) {
    return PyImport_AppendInittab("_mcub_go", PyInit__mcub_go_impl);
}

// ---------------------------------------------------------------------------
// GIL / thread-state helpers
// ---------------------------------------------------------------------------

// After Py_Initialize() the main thread owns the GIL.  Call this once to
// release it so Go goroutines can run and acquire it later via PyGILState_*.
static PyThreadState* _mcub_saved_ts = NULL;

static void mcub_release_gil(void) {
    _mcub_saved_ts = PyEval_SaveThread();
}

// Used during Finalize() to reacquire before Py_Finalize().
static void mcub_reacquire_gil(void) {
    if (_mcub_saved_ts != NULL) {
        PyEval_RestoreThread(_mcub_saved_ts);
        _mcub_saved_ts = NULL;
    }
}

// ---------------------------------------------------------------------------
// Execute Python source code inside a named module's own namespace so that
// __name__ is set to mod_name (not __main__), which prevents accidental
// triggering of `if __name__ == "__main__"` guards.
// Returns 0 on success, -1 on Python exception.
// ---------------------------------------------------------------------------
static int mcub_exec_in_module(const char* code, const char* mod_name) {
    // Get or create a module object with the given name.
    PyObject* mod = PyImport_AddModule(mod_name);
    if (mod == NULL) return -1;

    PyObject* dict = PyModule_GetDict(mod); // borrowed

    // Set __name__
    PyObject* name_obj = PyUnicode_FromString(mod_name);
    if (name_obj == NULL) return -1;
    PyDict_SetItemString(dict, "__name__", name_obj);
    Py_DECREF(name_obj);

    // Inject builtins
    PyObject* builtins = PyImport_ImportModule("builtins");
    if (builtins) {
        PyDict_SetItemString(dict, "__builtins__", builtins);
        Py_DECREF(builtins);
    }

    // Set __file__ to the module name (best-effort; aids tracebacks)
    PyObject* file_obj = PyUnicode_FromString(mod_name);
    if (file_obj) {
        PyDict_SetItemString(dict, "__file__", file_obj);
        Py_DECREF(file_obj);
    }

    PyObject* compiled = Py_CompileString(code, mod_name, Py_file_input);
    if (compiled == NULL) return -1;

    PyObject* result = PyEval_EvalCode(compiled, dict, dict);
    Py_DECREF(compiled);
    if (result == NULL) return -1;
    Py_DECREF(result);
    return 0;
}

// ---------------------------------------------------------------------------
// Extract the current Python exception as a heap-allocated C string.
// Returns a string that the caller MUST free().
// ---------------------------------------------------------------------------
static char* mcub_get_py_error(void) {
    PyObject *ptype, *pvalue, *ptb;
    PyErr_Fetch(&ptype, &pvalue, &ptb);
    if (pvalue == NULL && ptype == NULL)
        return strdup("(no Python exception)");
    PyErr_NormalizeException(&ptype, &pvalue, &ptb);

    char* msg = NULL;
    if (pvalue != NULL) {
        PyObject* s = PyObject_Str(pvalue);
        if (s != NULL) {
            const char* utf8 = PyUnicode_AsUTF8(s);
            if (utf8) msg = strdup(utf8);
            Py_DECREF(s);
        }
    }
    Py_XDECREF(ptype);
    Py_XDECREF(pvalue);
    Py_XDECREF(ptb);
    return msg ? msg : strdup("unknown Python error");
}

// ---------------------------------------------------------------------------
// Helpers for calling named functions in __main__
// ---------------------------------------------------------------------------

// Returns a new reference to __main__.<fn_name>, or NULL on error.
static PyObject* mcub_get_main_fn(const char* fn_name) {
    PyObject* main_mod = PyImport_AddModule("__main__"); // borrowed
    if (main_mod == NULL) return NULL;
    PyObject* fn = PyObject_GetAttrString(main_mod, fn_name);
    return fn; // new ref or NULL
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unsafe"

	// The two Python files are embedded as Go string constants below.
	_ "embed"
)

//go:embed mcub_compat.py
var mcubCompatPy string

//go:embed module_scanner.py
var moduleScannerPy string

// pythonBootstrap is executed in __main__ after the two support modules are
// loaded.  It defines _mcub_load_module and _mcub_call_command.
const pythonBootstrap = `
import sys
import asyncio
import json


def _is_hikka_module(code):
    """Detect whether source code is a Hikka or Heroku userbot module."""
    patterns = [
        'from hikka', 'import hikka', '@loader.command',
        'loader.Module', 'from .. import loader',
        'from Heroku', 'import Heroku', 'Heroku.register_command',
    ]
    return any(p in code for p in patterns)


def _mcub_load_module(file_path, mod_name):
    """Load a .py module, instantiate it, register commands, return JSON info."""
    import importlib.util

    mcub_compat = sys.modules.get("mcub_compat")
    if mcub_compat is None:
        raise RuntimeError("mcub_compat not loaded")

    ModuleBase   = mcub_compat.ModuleBase
    KernelProxy  = mcub_compat.KernelProxy
    ClientProxy  = mcub_compat.ClientProxy
    _RegisterProxy = mcub_compat._RegisterProxy
    _module_instances = mcub_compat._module_instances
    _command_handlers = mcub_compat._command_handlers
    # _HikkaModule is the base class for Hikka modules; skip it like ModuleBase
    _HikkaModule = getattr(mcub_compat, '_HikkaModule', None)

    # Read source for framework detection (best-effort)
    _src_code = ""
    try:
        with open(file_path, "r", encoding="utf-8") as _f:
            _src_code = _f.read()
    except Exception:
        pass
    _framework = "hikka" if _is_hikka_module(_src_code) else "mcub"

    # Execute the .py file into a fresh namespace
    spec = importlib.util.spec_from_file_location(mod_name, file_path)
    if spec is None:
        raise ImportError(f"Cannot create spec for {file_path!r}")
    mod = importlib.util.module_from_spec(spec)
    mod.__name__ = mod_name
    sys.modules[mod_name] = mod
    spec.loader.exec_module(mod)

    # --- Detect module style ---

    # Style 1: class-based (ModuleBase subclass, including _HikkaModule subclasses)
    found_class = None
    for attr_name in dir(mod):
        try:
            attr = getattr(mod, attr_name)
        except Exception:
            continue
        if (isinstance(attr, type) and issubclass(attr, ModuleBase)
                and attr is not ModuleBase
                and (_HikkaModule is None or attr is not _HikkaModule)):
            found_class = attr
            break

    if found_class is not None:
        display_name = getattr(found_class, "name", mod_name)
        kp = KernelProxy(session_id=0, module_name=display_name)
        cp = ClientProxy(session_id=0)
        reg = _RegisterProxy(display_name)
        # Instantiate: this populates _command_handlers via __init__
        instance = found_class(kp, cp, reg)
        _module_instances[display_name] = instance
        _module_instances[mod_name] = instance  # also store by file name
        # Run on_load synchronously
        try:
            asyncio.run(instance.on_load())
        except RuntimeError:
            # Already running event loop (shouldn't happen in bridge context)
            pass
        except Exception as e:
            import traceback
            print(f"[mcub_compat] on_load error for {display_name}: {e}")
            traceback.print_exc()
        cmds = []
        for (pattern, func, meta) in type(instance)._cmd_registry:
            cmds.append({
                "name": pattern,
                "doc_en": meta.get("doc_en", ""),
                "doc_ru": meta.get("doc_ru", ""),
                "method": func.__name__,
                "class": type(instance).__name__,
                "style": "class",
            })
        return json.dumps({
            "name": display_name,
            "version": getattr(found_class, "version", "1.0.0"),
            "author": getattr(found_class, "author", "unknown"),
            "description": getattr(found_class, "description", {}),
            "commands": cmds,
            "style": _framework if _framework == "hikka" else "class",
        }, ensure_ascii=False)

    # Style 2: function-based (def register(kernel))
    register_fn = getattr(mod, "register", None)
    if callable(register_fn) and not isinstance(register_fn, type):
        # Guess module name from module-level attrs
        display_name = getattr(mod, "name", mod_name)
        kp = KernelProxy(session_id=0, module_name=display_name)
        reg = kp.register  # _RegisterProxy with module_name
        kp.register = reg
        try:
            result = register_fn(kp)
            if asyncio.iscoroutine(result):
                asyncio.run(result)
        except Exception as e:
            import traceback
            print(f"[mcub_compat] register() error for {display_name}: {e}")
            traceback.print_exc()
        _module_instances[display_name] = kp
        _module_instances[mod_name] = kp
        cmds = [{"name": n, "doc_en": de, "doc_ru": dr, "method": n, "class": "", "style": "function"}
                for (n, de, dr) in reg.registered_commands]
        return json.dumps({
            "name": display_name,
            "version": getattr(mod, "version", "1.0.0"),
            "author": getattr(mod, "author", "unknown"),
            "description": {},
            "commands": cmds,
            "style": "function",
        }, ensure_ascii=False)

    # Style 3: loader-style (module-level @command decorated functions via loader.command)
    import core.lib.loader.module_base as loader_mod
    display_name = getattr(mod, "name", mod_name)
    kp = KernelProxy(session_id=0, module_name=display_name)
    reg = _RegisterProxy(display_name)
    cmds_found = []
    for attr_name in dir(mod):
        try:
            attr = getattr(mod, attr_name)
        except Exception:
            continue
        if callable(attr) and hasattr(attr, "_mcub_commands"):
            for (pattern, meta) in attr._mcub_commands:
                import functools
                _fn = attr
                async def _handler(event, f=_fn):
                    return await f(event)
                _command_handlers[(display_name, pattern)] = _handler
                cmds_found.append({"name": pattern, "doc_en": meta.get("doc_en",""),
                                    "doc_ru": meta.get("doc_ru",""), "method": attr_name,
                                    "class": "", "style": "loader"})
        elif callable(attr) and getattr(attr, "_mcub_command", False):
            pattern = attr._mcub_cmd_name
            import functools
            _fn = attr
            async def _handler(event, f=_fn):
                return await f(event)
            _command_handlers[(display_name, pattern)] = _handler
            cmds_found.append({"name": pattern, "doc_en": getattr(attr,"_mcub_doc_en",""),
                                "doc_ru": getattr(attr,"_mcub_doc_ru",""), "method": attr_name,
                                "class": "", "style": "loader"})
    _module_instances[display_name] = mod
    _module_instances[mod_name] = mod
    return json.dumps({
        "name": display_name,
        "version": getattr(mod, "version", "1.0.0"),
        "author": getattr(mod, "author", "unknown"),
        "description": {},
        "commands": cmds_found,
        "style": "loader",
    }, ensure_ascii=False)


def _mcub_call_command(mod_name, cmd_name,
                       session_id, chat_id, msg_id, text, sender_id,
                       pipe_input="", is_piped=False):
    """Invoke a stored command handler with a synthetic Event."""
    mcub_compat = sys.modules.get("mcub_compat")
    if mcub_compat is None:
        raise RuntimeError("mcub_compat not loaded")

    Event = mcub_compat.Event
    _command_handlers = mcub_compat._command_handlers
    _module_instances  = mcub_compat._module_instances

    # Look up the handler stored during module load
    handler = _command_handlers.get((mod_name, cmd_name))
    if handler is None:
        # Try alternate key (file-based mod_name vs display_name)
        for (mn, cn), h in _command_handlers.items():
            if cn == cmd_name and (mn == mod_name or mn.replace("-","_") == mod_name.replace("-","_")):
                handler = h
                break

    if handler is None:
        raise KeyError(f"No handler for ({mod_name!r}, {cmd_name!r}). "
                       f"Known: {list(_command_handlers.keys())}")

    # If the stored instance needs session wired in, update KernelProxy
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

    event = Event(session_id, chat_id, msg_id, text, sender_id)
    # Wire pipeline state into the event
    event.piped = bool(is_piped)
    event.pipe_input = pipe_input or ""

    if asyncio.iscoroutinefunction(handler):
        asyncio.run(handler(event))
    else:
        handler(event)


def _mcub_purge_module(mod_name):
    """Remove a module (and related entries) from sys.modules."""
    to_remove = [k for k in sys.modules if k == mod_name or k.startswith(mod_name + ".")]
    for k in to_remove:
        sys.modules.pop(k, None)
    mcub_compat = sys.modules.get("mcub_compat")
    if mcub_compat is not None:
        _command_handlers = mcub_compat._command_handlers
        _module_instances  = mcub_compat._module_instances
        for key in list(_command_handlers.keys()):
            if key[0] == mod_name:
                del _command_handlers[key]
        _module_instances.pop(mod_name, None)
`

// Bridge manages the embedded CPython interpreter.  Create exactly one Bridge
// per process lifetime.
type Bridge struct {
	mu          sync.Mutex
	initialized bool

	// cmdIndex maps modName → cmdName → PyCommand for fast lookup in
	// CallPyCommand.
	cmdIndex map[string]map[string]PyCommand
}

// NewBridge initialises the CPython interpreter, installs the _mcub_go C
// extension, and executes the Python support code.
func NewBridge() (*Bridge, error) {
	b := &Bridge{
		cmdIndex: make(map[string]map[string]PyCommand),
	}

	// Register the _mcub_go extension BEFORE Py_Initialize so the import
	// machinery can find it when mcub_compat.py does `import _mcub_go`.
	if C.setup_mcub_extension() != 0 {
		return nil, errors.New("pybridge: failed to register _mcub_go extension")
	}

	C.Py_Initialize()
	if C.Py_IsInitialized() == 0 {
		return nil, errors.New("pybridge: Python initialization failed")
	}

	// --- Execute mcub_compat.py in its own module namespace ---------------
	mcubCompatC := C.CString(mcubCompatPy)
	modNameC1 := C.CString("mcub_compat")
	ret1 := C.mcub_exec_in_module(mcubCompatC, modNameC1)
	C.free(unsafe.Pointer(mcubCompatC))
	C.free(unsafe.Pointer(modNameC1))
	if ret1 != 0 {
		err := b.pyError("init mcub_compat")
		C.Py_Finalize()
		return nil, err
	}

	// --- Execute module_scanner.py in its own module namespace -----------
	modScannerC := C.CString(moduleScannerPy)
	modNameC2 := C.CString("module_scanner")
	ret2 := C.mcub_exec_in_module(modScannerC, modNameC2)
	C.free(unsafe.Pointer(modScannerC))
	C.free(unsafe.Pointer(modNameC2))
	if ret2 != 0 {
		err := b.pyError("init module_scanner")
		C.Py_Finalize()
		return nil, err
	}

	// --- Execute bootstrap helper code in __main__ -----------------------
	bootstrapC := C.CString(pythonBootstrap)
	retBoot := C.PyRun_SimpleString(bootstrapC)
	C.free(unsafe.Pointer(bootstrapC))
	if retBoot != 0 {
		C.Py_Finalize()
		return nil, errors.New("pybridge: Python bootstrap execution failed")
	}

	// Release the GIL so Go goroutines can run freely.  Every later Python
	// call reacquires it via PyGILState_Ensure / PyGILState_Release.
	C.mcub_release_gil()

	b.initialized = true
	return b, nil
}

// Finalize shuts down the CPython interpreter.  Call at most once, after all
// Python modules have been unloaded.
func (b *Bridge) Finalize() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.initialized {
		return
	}
	C.mcub_reacquire_gil()
	C.Py_Finalize()
	b.initialized = false
}

// ---------------------------------------------------------------------------
// Module loading
// ---------------------------------------------------------------------------

// moduleInfo mirrors the JSON returned by module_scanner.scan_module.
type moduleInfo struct {
	Name     string `json:"name"`
	Version  string `json:"version"`
	Author   string `json:"author"`
	Commands []struct {
		Name   string `json:"name"`
		DocEN  string `json:"doc_en"`
		DocRU  string `json:"doc_ru"`
		Method string `json:"method"`
		Class  string `json:"class"`
	} `json:"commands"`
}

// LoadPyModule loads the .py file at path into the interpreter, scans it for
// @command-decorated methods, and returns a PyModule.
func (b *Bridge) LoadPyModule(path string) (*PyModule, error) {
	// Derive a Python-legal module name from the file name.
	base := filepath.Base(path)
	modName := strings.TrimSuffix(base, ".py")

	// ── Parse and install # requires: dependencies before loading ────────────
	// Matches pre_install_requirements() in dependency_manager_mixin.py.
	if src, err := os.ReadFile(path); err == nil {
		logFn := func(msg string) { fmt.Println("[pybridge/deps]", msg) }
		errs := InstallRequires(context.Background(), string(src), modName, logFn)
		for _, e := range errs {
			fmt.Printf("[pybridge/deps] WARNING: %v\n", e)
		}
	}

	// Acquire the GIL.
	state := C.PyGILState_Ensure()
	defer C.PyGILState_Release(state)

	// Get _mcub_load_module from __main__.
	loadFnNameC := C.CString("_mcub_load_module")
	loadFn := C.mcub_get_main_fn(loadFnNameC)
	C.free(unsafe.Pointer(loadFnNameC))
	if loadFn == nil {
		return nil, b.pyError("LoadPyModule: get _mcub_load_module")
	}
	defer C.Py_DecRef(loadFn)

	// Build (file_path, mod_name) args tuple.
	cPath := C.CString(path)
	cModName := C.CString(modName)
	pyPath := C.PyUnicode_FromString(cPath)
	pyModName := C.PyUnicode_FromString(cModName)
	C.free(unsafe.Pointer(cPath))
	C.free(unsafe.Pointer(cModName))

	if pyPath == nil || pyModName == nil {
		C.Py_XDECREF(pyPath)
		C.Py_XDECREF(pyModName)
		return nil, errors.New("pybridge: out of memory building args")
	}

	args := C.PyTuple_New(2)
	if args == nil {
		C.Py_DecRef(pyPath)
		C.Py_DecRef(pyModName)
		return nil, errors.New("pybridge: out of memory building tuple")
	}
	C.PyTuple_SetItem(args, 0, pyPath)    // steals ref
	C.PyTuple_SetItem(args, 1, pyModName) // steals ref

	result := C.PyObject_Call(loadFn, args, nil)
	C.Py_DecRef(args)
	if result == nil {
		return nil, b.pyError(fmt.Sprintf("LoadPyModule(%q)", path))
	}
	defer C.Py_DecRef(result)

	// result is a JSON string.
	cjson := C.PyUnicode_AsUTF8(result) // valid while result is alive
	if cjson == nil {
		return nil, errors.New("pybridge: _mcub_load_module returned non-string")
	}
	jsonStr := C.GoString(cjson) // copies to Go heap

	// Parse the scan result.
	var info moduleInfo
	if err := json.Unmarshal([]byte(jsonStr), &info); err != nil {
		return nil, fmt.Errorf("pybridge: parse scan result: %w", err)
	}

	cmds := make([]PyCommand, 0, len(info.Commands))
	for _, c := range info.Commands {
		desc := c.DocEN
		if desc == "" {
			desc = c.DocRU
		}
		cmds = append(cmds, PyCommand{
			Name:        c.Name,
			Description: desc,
			DocRU:       c.DocRU,
			DocEN:       c.DocEN,
			ClassName:   c.Class,
			MethodName:  c.Method,
		})
	}

	// Store the command index for fast lookup in CallPyCommand.
	b.mu.Lock()
	idx := make(map[string]PyCommand, len(cmds))
	for _, cmd := range cmds {
		idx[cmd.Name] = cmd
	}
	b.cmdIndex[modName] = idx
	b.mu.Unlock()

	pyMod := &PyModule{
		Name:     info.Name,
		ModName:  modName,
		Commands: cmds,
	}

	displayName := info.Name
	if displayName == "unknown" || displayName == "" {
		displayName = modName
	}
	pyMod.Name = displayName

	return pyMod, nil
}

// ---------------------------------------------------------------------------
// Command invocation
// ---------------------------------------------------------------------------

// CallPyCommand calls the Python handler registered for cmdName inside the
// module identified by modName.  ev provides the Telegram event callbacks.
func (b *Bridge) CallPyCommand(modName, cmdName string, ev BridgeEvent) error {
	// Look up the command metadata.
	b.mu.Lock()
	cmdMap, ok := b.cmdIndex[modName]
	b.mu.Unlock()
	if !ok {
		return fmt.Errorf("pybridge: module %q not loaded", modName)
	}
	if _, ok := cmdMap[cmdName]; !ok {
		return fmt.Errorf("pybridge: command %q not found in module %q", cmdName, modName)
	}

	// Register the session so Python callbacks can reach our closures.
	sessionID := registerSession(&ev)
	defer releaseSession(sessionID)

	// Acquire the GIL.
	state := C.PyGILState_Ensure()
	defer C.PyGILState_Release(state)

	// Get _mcub_call_command from __main__.
	callFnNameC := C.CString("_mcub_call_command")
	callFn := C.mcub_get_main_fn(callFnNameC)
	C.free(unsafe.Pointer(callFnNameC))
	if callFn == nil {
		return b.pyError("CallPyCommand: get _mcub_call_command")
	}
	defer C.Py_DecRef(callFn)

	// Build args: (mod_name, cmd_name, session_id, chat_id, msg_id, text, sender_id,
	//              pipe_input, is_piped)
	cModName := C.CString(modName)
	cCmdName := C.CString(cmdName)
	cText := C.CString(ev.Text)
	pipeInputStr := ev.PipeInput
	cPipeInput := C.CString(pipeInputStr)

	pyModName := C.PyUnicode_FromString(cModName)
	pyCmdName := C.PyUnicode_FromString(cCmdName)
	pySessID := C.PyLong_FromLongLong(C.longlong(sessionID))
	pyChatID := C.PyLong_FromLongLong(C.longlong(ev.ChatID))
	pyMsgID := C.PyLong_FromLongLong(C.longlong(ev.MessageID))
	pyText := C.PyUnicode_FromString(cText)
	pySenderID := C.PyLong_FromLongLong(C.longlong(ev.SenderID))
	pyPipeInput := C.PyUnicode_FromString(cPipeInput)
	isPipedLong := C.long(0)
	if ev.IsPiped {
		isPipedLong = C.long(1)
	}
	pyIsPiped := C.PyBool_FromLong(isPipedLong)

	C.free(unsafe.Pointer(cModName))
	C.free(unsafe.Pointer(cCmdName))
	C.free(unsafe.Pointer(cText))
	C.free(unsafe.Pointer(cPipeInput))

	if pyModName == nil || pyCmdName == nil ||
		pySessID == nil || pyChatID == nil || pyMsgID == nil ||
		pyText == nil || pySenderID == nil || pyPipeInput == nil || pyIsPiped == nil {
		C.Py_XDECREF(pyModName)
		C.Py_XDECREF(pyCmdName)
		C.Py_XDECREF(pySessID)
		C.Py_XDECREF(pyChatID)
		C.Py_XDECREF(pyMsgID)
		C.Py_XDECREF(pyText)
		C.Py_XDECREF(pySenderID)
		C.Py_XDECREF(pyPipeInput)
		C.Py_XDECREF(pyIsPiped)
		return errors.New("pybridge: out of memory building call args")
	}

	args := C.PyTuple_New(9)
	if args == nil {
		C.Py_DecRef(pyModName)
		C.Py_DecRef(pyCmdName)
		C.Py_DecRef(pySessID)
		C.Py_DecRef(pyChatID)
		C.Py_DecRef(pyMsgID)
		C.Py_DecRef(pyText)
		C.Py_DecRef(pySenderID)
		C.Py_DecRef(pyPipeInput)
		C.Py_DecRef(pyIsPiped)
		return errors.New("pybridge: out of memory building tuple")
	}
	C.PyTuple_SetItem(args, 0, pyModName)
	C.PyTuple_SetItem(args, 1, pyCmdName)
	C.PyTuple_SetItem(args, 2, pySessID)
	C.PyTuple_SetItem(args, 3, pyChatID)
	C.PyTuple_SetItem(args, 4, pyMsgID)
	C.PyTuple_SetItem(args, 5, pyText)
	C.PyTuple_SetItem(args, 6, pySenderID)
	C.PyTuple_SetItem(args, 7, pyPipeInput)
	C.PyTuple_SetItem(args, 8, pyIsPiped)

	result := C.PyObject_Call(callFn, args, nil)
	C.Py_DecRef(args)
	if result == nil {
		return b.pyError(fmt.Sprintf("CallPyCommand(%q.%q)", modName, cmdName))
	}
	C.Py_DecRef(result)
	return nil
}

// ---------------------------------------------------------------------------
// Module purge
// ---------------------------------------------------------------------------

// PurgePyModule calls _mcub_purge_module(modName) in __main__, removing the
// module from sys.modules and clearing its command handlers.
func (b *Bridge) PurgePyModule(modName string) error {
	state := C.PyGILState_Ensure()
	defer C.PyGILState_Release(state)

	purgeFnNameC := C.CString("_mcub_purge_module")
	purgeFn := C.mcub_get_main_fn(purgeFnNameC)
	C.free(unsafe.Pointer(purgeFnNameC))
	if purgeFn == nil {
		// Function not yet defined (older bootstrap) – not fatal.
		return nil
	}
	defer C.Py_DecRef(purgeFn)

	cName := C.CString(modName)
	pyName := C.PyUnicode_FromString(cName)
	C.free(unsafe.Pointer(cName))
	if pyName == nil {
		return errors.New("pybridge: PurgePyModule: out of memory")
	}

	args := C.PyTuple_New(1)
	if args == nil {
		C.Py_DecRef(pyName)
		return errors.New("pybridge: PurgePyModule: out of memory")
	}
	C.PyTuple_SetItem(args, 0, pyName)

	result := C.PyObject_Call(purgeFn, args, nil)
	C.Py_DecRef(args)
	if result == nil {
		return b.pyError(fmt.Sprintf("PurgePyModule(%q)", modName))
	}
	C.Py_DecRef(result)
	return nil
}

// ---------------------------------------------------------------------------
// Error helper
// ---------------------------------------------------------------------------

// pyError fetches the current Python exception (must be called with GIL held)
// and wraps it as a Go error.
func (b *Bridge) pyError(ctx string) error {
	cMsg := C.mcub_get_py_error()
	if cMsg == nil {
		return fmt.Errorf("pybridge: %s: unknown error", ctx)
	}
	msg := C.GoString(cMsg)
	C.free(unsafe.Pointer(cMsg))
	return fmt.Errorf("pybridge: %s: %s", ctx, msg)
}
