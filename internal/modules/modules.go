package modules

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// modulesModule provides module-management commands: man, iload, um, reload.
type modulesModule struct {
	k *kernel.Kernel
}

func newModulesModule() *modulesModule { return &modulesModule{} }

// Name implements loader.Module.
func (m *modulesModule) Name() string { return "modules" }

// OnLoad implements loader.Module.
func (m *modulesModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("modules: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *modulesModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

// Commands implements loader.Module.
func (m *modulesModule) Commands() []loader.Command {
	return []loader.Command{
		{
			Name:        "man",
			Description: "List all modules and their commands, or details for a specific module",
			Handler:     m.manCmd,
		},
		{
			Name:        "iload",
			Description: "Install a Go plugin module from a .so file attached to a replied message",
			Handler:     m.iloadCmd,
		},
		{
			Name:        "um",
			Description: "Unload a module by name (system modules cannot be unloaded)",
			Handler:     m.umCmd,
		},
		{
			Name:        "reload",
			Description: "Soft-reload a module (or all user modules when no name given)",
			Handler:     m.reloadCmd,
		},
	}
}

// --- helpers -----------------------------------------------------------------

// editReply edits the triggering message with the given text.
func (m *modulesModule) editReply(ctx context.Context, ev *events.NewMessage, text string) error {
	if m.k.Client == nil || ev.Raw == nil {
		return nil
	}
	_, err := m.k.Client.EditMessage(ctx, mcubclient.EditMessageParams{
		PeerID:    ev.PeerID,
		MessageID: ev.Raw.ID,
		Text:      text,
	})
	return err
}

// parseArgs strips the prefix+command word and returns the remaining tokens.
func (m *modulesModule) parseArgs(ev *events.NewMessage) []string {
	body := strings.TrimPrefix(ev.Text(), m.k.Prefix())
	parts := strings.Fields(body)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// findModule searches both SystemModules and LoadedModules for the given name.
func (m *modulesModule) findModule(name string) (loader.Module, bool) {
	if mod, ok := m.k.SystemModules[name]; ok {
		return mod, true
	}
	if mod, ok := m.k.LoadedModules[name]; ok {
		return mod, true
	}
	return nil, false
}

// allModuleNames returns a sorted list of all module names (system + user).
func (m *modulesModule) allModuleNames() []string {
	seen := make(map[string]bool)
	for name := range m.k.SystemModules {
		seen[name] = true
	}
	for name := range m.k.LoadedModules {
		seen[name] = true
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// --- command handlers --------------------------------------------------------

// manCmd lists all modules (and their commands) or details for a specific module.
//
// Usage:
//
//	.man          — list all modules
//	.man <name>   — show commands for the named module
func (m *modulesModule) manCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil {
		return nil
	}

	args := m.parseArgs(ev)

	var sb strings.Builder

	if len(args) == 0 {
		// List every module with its command names.
		names := m.allModuleNames()
		if len(names) == 0 {
			return m.editReply(ctx, ev, "No modules loaded.")
		}
		fmt.Fprintf(&sb, "Loaded modules (%d):\n", len(names))
		for _, name := range names {
			mod, ok := m.findModule(name)
			if !ok {
				continue
			}
			cmds := mod.Commands()
			cmdNames := make([]string, 0, len(cmds))
			for _, c := range cmds {
				cmdNames = append(cmdNames, m.k.Prefix()+c.Name)
			}
			source := "user"
			if _, isSys := m.k.SystemModules[name]; isSys {
				source = "system"
			}
			fmt.Fprintf(&sb, "\n[%s] (%s)\n", name, source)
			if len(cmdNames) == 0 {
				sb.WriteString("  (no commands)\n")
			} else {
				for _, cn := range cmdNames {
					sb.WriteString("  ")
					sb.WriteString(cn)
					sb.WriteByte('\n')
				}
			}
		}
	} else {
		// Detail view for a single module.
		name := args[0]
		mod, ok := m.findModule(name)
		if !ok {
			return m.editReply(ctx, ev, fmt.Sprintf("Module %q not found.", name))
		}
		cmds := mod.Commands()
		source := "user"
		if _, isSys := m.k.SystemModules[name]; isSys {
			source = "system"
		}
		fmt.Fprintf(&sb, "Module: %s (%s)\n", name, source)
		if len(cmds) == 0 {
			sb.WriteString("  (no commands)")
		} else {
			for _, c := range cmds {
				fmt.Fprintf(&sb, "  %s%s — %s\n", m.k.Prefix(), c.Name, c.Description)
			}
		}
	}

	return m.editReply(ctx, ev, strings.TrimRight(sb.String(), "\n"))
}

// iloadCmd downloads a .so plugin from the document of the replied message and
// loads it into the kernel.
//
// Usage: reply to a message containing a .so file, then send .iload
func (m *modulesModule) iloadCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || m.k.Client == nil || ev.Raw == nil {
		return nil
	}

	if ev.ReplyToMsgID == 0 {
		return m.editReply(ctx, ev, "Reply to a message containing a .so plugin file.")
	}

	// Fetch the replied message.
	msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
	if err != nil {
		return m.editReply(ctx, ev, fmt.Sprintf("Failed to fetch replied message: %v", err))
	}
	if len(msgs) == 0 {
		return m.editReply(ctx, ev, "Replied message not found.")
	}

	repliedMsg := msgs[0]
	if repliedMsg == nil {
		return m.editReply(ctx, ev, "Replied message is empty.")
	}

	// Extract document from media.
	mediaDoc, ok := repliedMsg.Media.(*tg.MessageMediaDocument)
	if !ok || mediaDoc == nil {
		return m.editReply(ctx, ev, "Replied message does not contain a document.")
	}

	doc, ok := mediaDoc.Document.(*tg.Document)
	if !ok || doc == nil {
		return m.editReply(ctx, ev, "Could not read document from replied message.")
	}

	// Determine filename.
	fileName := fmt.Sprintf("%d.so", doc.ID)
	for _, attr := range doc.Attributes {
		if fa, ok := attr.(*tg.DocumentAttributeFilename); ok && fa.FileName != "" {
			fileName = fa.FileName
			break
		}
	}

	if !strings.HasSuffix(strings.ToLower(fileName), ".so") {
		return m.editReply(ctx, ev, fmt.Sprintf("Expected a .so file, got %q.", fileName))
	}

	// Ensure destination directory exists.
	destDir := m.k.ModulesLoadedDir
	if destDir == "" {
		destDir = "modules_loaded"
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return m.editReply(ctx, ev, fmt.Sprintf("Cannot create modules directory: %v", err))
	}

	destPath := filepath.Join(destDir, fileName)

	_ = m.editReply(ctx, ev, fmt.Sprintf("Downloading %s...", fileName))

	// Download the file.
	loc := &tg.InputDocumentFileLocation{
		ID:            doc.ID,
		AccessHash:    doc.AccessHash,
		FileReference: doc.FileReference,
		ThumbSize:     "",
	}
	if err := m.k.Client.DownloadFile(ctx, mcubclient.DownloadParams{
		Location: loc,
		DestPath: destPath,
		DCID:     doc.DCID,
	}); err != nil {
		return m.editReply(ctx, ev, fmt.Sprintf("Download failed: %v", err))
	}

	// Snapshot registry before loading to detect new modules.
	beforeNames := make(map[string]bool)
	for _, n := range m.k.Loader.Registry().Names() {
		beforeNames[n] = true
	}

	// Load the plugin.
	if err := m.k.Loader.LoadPlugin(destPath); err != nil {
		return m.editReply(ctx, ev, fmt.Sprintf("Failed to load plugin: %v", err))
	}

	// Detect and register newly added modules in the kernel's LoadedModules map.
	var newNames []string
	for _, n := range m.k.Loader.Registry().Names() {
		if !beforeNames[n] {
			newNames = append(newNames, n)
			if mod, ok := m.k.Loader.Registry().Get(n); ok {
				m.k.LoadedModules[n] = mod
				m.k.ModuleSources[n] = loader.ModuleSourcePlugin
			}
		}
	}

	if len(newNames) == 0 {
		return m.editReply(ctx, ev, fmt.Sprintf("Plugin loaded from %s (no new module names detected).", fileName))
	}
	return m.editReply(ctx, ev, fmt.Sprintf("Plugin loaded: %s", strings.Join(newNames, ", ")))
}

// umCmd unloads a user module by name.
//
// Usage: .um <name>
func (m *modulesModule) umCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil {
		return nil
	}

	args := m.parseArgs(ev)
	if len(args) == 0 {
		return m.editReply(ctx, ev, "Usage: um <module-name>")
	}

	name := args[0]

	// Disallow unloading system modules.
	if _, isSys := m.k.SystemModules[name]; isSys {
		return m.editReply(ctx, ev, fmt.Sprintf("Cannot unload system module %q.", name))
	}

	mod, ok := m.k.LoadedModules[name]
	if !ok {
		return m.editReply(ctx, ev, fmt.Sprintf("Module %q is not loaded.", name))
	}

	// Unregister commands from kernel.
	for _, cmd := range mod.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}

	// Unload via loader (calls OnUnload + removes from registry).
	if err := m.k.Loader.Unload(name); err != nil {
		// Non-fatal: continue cleanup.
		if m.k.Log != nil {
			m.k.Log.Warn("um: loader unload %q: %v", name, err)
		}
	}

	// Remove from kernel maps.
	delete(m.k.LoadedModules, name)
	delete(m.k.ModuleSources, name)

	return m.editReply(ctx, ev, fmt.Sprintf("Module %q unloaded.", name))
}

// reloadCmd soft-reloads a module (or all user modules) by calling OnUnload
// then OnLoad. Go plugins cannot truly be reloaded at runtime, but this allows
// modules to refresh internal state.
//
// Usage:
//
//	.reload          — reload all user modules
//	.reload <name>   — reload a specific module (system or user)
func (m *modulesModule) reloadCmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil {
		return nil
	}

	args := m.parseArgs(ev)

	if len(args) == 0 {
		// Reload all user modules.
		names := make([]string, 0, len(m.k.LoadedModules))
		for name := range m.k.LoadedModules {
			names = append(names, name)
		}
		sort.Strings(names)

		if len(names) == 0 {
			return m.editReply(ctx, ev, "No user modules to reload.")
		}

		var failed []string
		for _, name := range names {
			if err := m.softReload(name); err != nil {
				failed = append(failed, fmt.Sprintf("%s (%v)", name, err))
			}
		}

		if len(failed) > 0 {
			return m.editReply(ctx, ev, fmt.Sprintf(
				"Reloaded %d modules. Errors:\n%s",
				len(names)-len(failed),
				strings.Join(failed, "\n"),
			))
		}
		return m.editReply(ctx, ev, fmt.Sprintf("Reloaded %d module(s).", len(names)))
	}

	// Reload a single named module.
	name := args[0]
	if _, ok := m.findModule(name); !ok {
		return m.editReply(ctx, ev, fmt.Sprintf("Module %q not found.", name))
	}

	if err := m.softReload(name); err != nil {
		return m.editReply(ctx, ev, fmt.Sprintf("Reload of %q failed: %v", name, err))
	}
	return m.editReply(ctx, ev, fmt.Sprintf("Module %q reloaded.", name))
}

// softReload calls OnUnload then OnLoad on the named module without removing it
// from the registry.
func (m *modulesModule) softReload(name string) error {
	mod, ok := m.findModule(name)
	if !ok {
		return fmt.Errorf("module %q not found", name)
	}

	// Unregister existing commands.
	for _, cmd := range mod.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}

	if err := mod.OnUnload(m.k); err != nil {
		if m.k.Log != nil {
			m.k.Log.Warn("reload: OnUnload %q: %v", name, err)
		}
	}

	if err := mod.OnLoad(m.k); err != nil {
		return fmt.Errorf("OnLoad: %w", err)
	}

	return nil
}
