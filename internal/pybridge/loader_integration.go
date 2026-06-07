package pybridge

import (
	"context"
	"fmt"

	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// kernelIface describes the minimal kernel surface that PythonModule needs.
// Using an interface here avoids a direct dependency on the kernel package
// (which would create an import cycle through loader).
type kernelIface interface {
	// Implemented by *kernel.Kernel
	getClient() *mcubclient.MCUBClient
	getPrefix() string
	getVersion() string
	getStartTimestamp() int64
	registerCommand(name, moduleName, description string, h loader.CommandHandler)
	unregisterCommand(name string)
}

// PythonModule wraps a PyModule and implements loader.Module so that the
// standard loader/kernel machinery can manage it like any built-in module.
type PythonModule struct {
	bridge *Bridge
	pyMod  *PyModule

	// k is set in OnLoad and used by command handler closures.
	// Stored as interface{} to match loader.Module's OnLoad signature.
	rawKernel interface{}
}

// NewPythonModule wraps an already-loaded PyModule.
func NewPythonModule(bridge *Bridge, pyMod *PyModule) *PythonModule {
	return &PythonModule{bridge: bridge, pyMod: pyMod}
}

// Name implements loader.Module.
func (m *PythonModule) Name() string {
	return m.pyMod.Name
}

// OnLoad implements loader.Module.  Stores the kernel reference and registers
// all discovered commands.
func (m *PythonModule) OnLoad(kernel interface{}) error {
	m.rawKernel = kernel

	// Register kernel-wide callbacks once for the whole process lifetime.
	// Calling SetKernelCallbacks more than once is fine – it just overwrites.
	if k, ok := m.resolveKernel(); ok {
		SetKernelCallbacks(KernelCallbacks{
			GetPrefix:    k.getPrefix,
			GetVersion:   k.getVersion,
			GetStartTime: k.getStartTimestamp,
			// Logging and DB are no-ops if the kernel doesn't provide them;
			// the loader_integration layer maps them to the logger when
			// available.  For now we leave them nil (mcub_compat handles nil).
		})
	}

	for _, cmd := range m.Commands() {
		if k, ok := kernel.(interface {
			RegisterCommand(name, moduleName, description string, h loader.CommandHandler)
		}); ok {
			k.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
		}
	}
	return nil
}

// OnUnload implements loader.Module.
func (m *PythonModule) OnUnload(kernel interface{}) error {
	for _, cmd := range m.Commands() {
		if k, ok := kernel.(interface {
			UnregisterCommand(name string)
		}); ok {
			k.UnregisterCommand(cmd.Name)
		}
	}
	m.rawKernel = nil
	return nil
}

// Commands implements loader.Module.  Returns loader.Command values whose
// handlers bridge into the Python interpreter.
func (m *PythonModule) Commands() []loader.Command {
	cmds := make([]loader.Command, 0, len(m.pyMod.Commands))
	for _, pyCmd := range m.pyMod.Commands {
		pyCmd := pyCmd // capture loop variable
		cmd := loader.Command{
			Name:        pyCmd.Name,
			Description: pyCmd.Description,
			Handler:     m.makeHandler(pyCmd),
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// makeHandler builds a loader.CommandHandler that translates a Telegram event
// into a BridgeEvent and calls the Python handler.
func (m *PythonModule) makeHandler(pyCmd PyCommand) loader.CommandHandler {
	return func(ctx context.Context, ev *events.NewMessage) error {
		if ev == nil || ev.Raw == nil {
			return nil
		}

		client := m.getClient()

		msgID := int64(ev.Raw.ID)
		chatID := ev.PeerID
		text := ev.Text()

		bridgeEv := BridgeEvent{
			ChatID:    chatID,
			MessageID: msgID,
			Text:      text,
			SenderID:  ev.SenderID,
		}

		// EditFn
		if client != nil {
			bridgeEv.EditFn = func(newText string) error {
				_, err := client.EditMessage(ctx, mcubclient.EditMessageParams{
					PeerID:    chatID,
					MessageID: int(msgID),
					Text:      newText,
				})
				return err
			}
		}

		// ReplyFn – sends a message that replies to the triggering message.
		if client != nil {
			bridgeEv.ReplyFn = func(replyText string) error {
				_, err := client.SendMessage(ctx, mcubclient.SendMessageParams{
					PeerID: chatID,
					Text:   replyText,
				})
				return err
			}
		}

		// DeleteFn
		if client != nil {
			bridgeEv.DeleteFn = func() error {
				return client.DeleteMessage(ctx, mcubclient.DeleteMessageParams{
					PeerID:     chatID,
					MessageIDs: []int{int(msgID)},
					Revoke:     true,
				})
			}
		}

		// GetReplyFn
		if client != nil && ev.ReplyToMsgID != 0 {
			replyToID := ev.ReplyToMsgID
			bridgeEv.GetReplyFn = func() (*ReplyMessage, error) {
				msgs, err := client.GetMessages(ctx, chatID, []int{replyToID})
				if err != nil || len(msgs) == 0 {
					return nil, err
				}
				raw := msgs[0]
				rm := &ReplyMessage{
					MessageID: int64(raw.ID),
					ChatID:    chatID,
					Text:      raw.Message,
				}
				return rm, nil
			}
		}

		// SendMessageFn
		if client != nil {
			bridgeEv.SendMessageFn = func(targetChatID int64, sendText string) error {
				_, err := client.SendMessage(ctx, mcubclient.SendMessageParams{
					PeerID: targetChatID,
					Text:   sendText,
				})
				return err
			}
		}

		// GetMeFn
		if client != nil {
			bridgeEv.GetMeFn = func() (map[string]interface{}, error) {
				user, err := client.GetMe(ctx)
				if err != nil || user == nil {
					return nil, err
				}
				username, _ := user.GetUsername()
				firstName, _ := user.GetFirstName()
				lastName, _ := user.GetLastName()
				phone, _ := user.GetPhone()
				return map[string]interface{}{
					"id":         user.ID,
					"first_name": firstName,
					"last_name":  lastName,
					"username":   username,
					"phone":      phone,
					"is_premium": user.Premium,
				}, nil
			}
		}

		// GetEntityFn – fetches a user by numeric ID (best-effort).
		if client != nil {
			bridgeEv.GetEntityFn = func(entityID int64) (map[string]interface{}, error) {
				// MCUBClient.GetEntity accepts a username string; for numeric
				// IDs we build the string representation.
				if entityID == 0 {
					return nil, nil
				}
				entity, err := client.GetEntity(ctx, fmt.Sprintf("%d", entityID))
				if err != nil || entity == nil {
					return nil, err
				}
				return map[string]interface{}{
					"id": entityID,
				}, nil
			}
		}

		// GetMessageFn
		if client != nil {
			bridgeEv.GetMessageFn = func(targetChatID, targetMsgID int64) (map[string]interface{}, error) {
				msgs, err := client.GetMessages(ctx, targetChatID, []int{int(targetMsgID)})
				if err != nil || len(msgs) == 0 {
					return nil, err
				}
				raw := msgs[0]
				return map[string]interface{}{
					"id":        raw.ID,
					"text":      raw.Message,
					"sender_id": 0,
				}, nil
			}
		}

		// DownloadMediaFn – not yet implemented.
		bridgeEv.DownloadMediaFn = func(filePath string) error {
			return fmt.Errorf("download_media not yet implemented")
		}

		return m.bridge.CallPyCommand(m.pyMod.ModName, pyCmd.Name, bridgeEv)
	}
}

// getClient extracts the MCUBClient from the stored kernel reference.
func (m *PythonModule) getClient() *mcubclient.MCUBClient {
	if m.rawKernel == nil {
		return nil
	}
	type clientGetter interface {
		GetClient() *mcubclient.MCUBClient
	}
	// Try the concrete kernel type via a type assertion to its public field
	// through a minimal interface.
	type withClient struct {
		Client *mcubclient.MCUBClient
	}
	// Use reflection-free duck typing: check if kernel has a Client field by
	// accessing it through the embedded struct approach is not possible without
	// importing the kernel package.  We use a direct type assertion to the
	// anonymous struct that the kernel satisfies at the interface level.
	//
	// In practice the caller is always *kernel.Kernel which has a public
	// Client field.  We access it via a thin local interface.
	if cg, ok := m.rawKernel.(interface{ GetClient() *mcubclient.MCUBClient }); ok {
		return cg.GetClient()
	}
	return nil
}

// resolveKernel attempts to obtain a kernelIface from rawKernel.
func (m *PythonModule) resolveKernel() (kernelIface, bool) {
	k, ok := m.rawKernel.(kernelIface)
	return k, ok
}
