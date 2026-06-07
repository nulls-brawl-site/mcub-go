#!/usr/bin/env python3
"""Test that all Python modules can be loaded and scanned"""

import sys
import types
import os

# Add stub for _mcub_go
_mcub_go = types.ModuleType('_mcub_go')
_mcub_go.get_prefix = lambda: '.'
_mcub_go.get_version = lambda: '1.0.0'
_mcub_go.get_start_time = lambda: 0.0
_mcub_go.log_info = lambda m: print(f"[INFO] {m}")
_mcub_go.log_debug = lambda m: None  
_mcub_go.log_warn = lambda m: print(f"[WARN] {m}")
_mcub_go.log_error = lambda m: print(f"[ERROR] {m}")
_mcub_go.restart_kernel = lambda: None
_mcub_go.db_get = lambda m,k: None
_mcub_go.db_set = lambda m,k,v: None
_mcub_go.db_delete = lambda m,k: None
_mcub_go.save_module_config = lambda m,d: None
_mcub_go.edit_message = lambda *a: None
_mcub_go.reply_message = lambda *a: None
_mcub_go.delete_message = lambda *a: None
_mcub_go.send_message = lambda *a: None
_mcub_go.get_me = lambda *a: {'id': 123, 'first_name': 'Test', 'username': 'test', 'is_premium': False}
_mcub_go.get_entity = lambda *a: None
_mcub_go.get_reply_message = lambda *a: None
_mcub_go.download_media = lambda *a: None
_mcub_go.get_message = lambda *a: None
sys.modules['_mcub_go'] = _mcub_go

# Load compat layer
compat_path = os.path.join(os.path.dirname(__file__), 'internal', 'pybridge', 'mcub_compat.py')
with open(compat_path) as f:
    exec(f.read(), {'__name__': '__compat__'})

# Load scanner
scanner_path = os.path.join(os.path.dirname(__file__), 'internal', 'pybridge', 'module_scanner.py')
scanner_ns = {}
with open(scanner_path) as f:
    exec(f.read(), scanner_ns)
scan_module = scanner_ns['scan_module']

# Test each module
modules_dir = os.path.join(os.path.dirname(__file__), 'modules')
results = {'ok': [], 'failed': []}

for fname in sorted(os.listdir(modules_dir)):
    if not fname.endswith('.py') or fname.startswith('__'):
        continue
    
    mod_path = os.path.join(modules_dir, fname)
    mod_name = fname[:-3]
    
    try:
        mod_ns = {'__name__': mod_name, '__file__': mod_path}
        with open(mod_path) as f:
            exec(f.read(), mod_ns)
        
        result = scan_module(mod_ns)
        cmds = result.get('commands', [])
        print(f"  OK  {mod_name:30s} style={result.get('style','?'):8s} commands: {[c['name'] for c in cmds]}")
        results['ok'].append(mod_name)
    except Exception as e:
        print(f"FAIL  {mod_name:30s} ERROR: {e}")
        results['failed'].append((mod_name, str(e)))

print(f"\n=== Results: {len(results['ok'])} OK, {len(results['failed'])} FAILED ===")
if results['failed']:
    print("\nFailed modules:")
    for name, err in results['failed']:
        print(f"  {name}: {err}")
