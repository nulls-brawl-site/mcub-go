// Package loader – python_module.go
//
// PythonModule wraps a pybridge.PyModule and implements the loader.Module
// interface so that Python-backed modules integrate with the same registry and
// lifecycle machinery as native Go modules.
//
// This file was split out of pybridge/loader_integration.go so that the
// loader package can import pybridge without creating a circular dependency.
package loader

import (
	"context"
	"fmt"

	"github.com/nulls-brawl-site/mcub-go/internal/pybridge"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// pyKernelIface describes the minimal kernel surface that PythonModule needs.
// Using an interface here avoids a direct dependency on the kernel package
// (which would create an import cycle through loader).
type pyKernelIface interface {
	getPrefix() string
	getVersion() string
	getStartTimestamp() int64
}

// PythonModule wraps a PyModule and implements Module so that the standard
// loader/kernel machinery can manage it like any built-in module.
type PythonModule struct {
	bridge    *pybridge.Bridge
	pyMod     *pybridge.PyModule
	rawKernel interface{}
}

// NewPythonModule wraps an already-loaded PyModule.
func NewPythonModule(bridge *pybridge.Bridge, pyMod *pybridge.PyModule) *PythonModule {
	return &PythonModule{bridge: bridge, pyMod: pyMod}
}

// Name implements Module.
func (m *PythonModule) Name() string { return m.pyMod.Name }

// OnLoad implements Module. Stores the kernel reference and registers all
// discovered commands with the kernel.
func (m *PythonModule) OnLoad(kernel interface{}) error {
	m.rawKernel = kernel

	// Wire kernel-wide callbacks once for the whole process lifetime.
	if k, ok := kernel.(pyKernelIface); ok {
		pybridge.SetKernelCallbacks(pybridge.KernelCallbacks{
			GetPrefix:    k.getPrefix,
			GetVersion:   k.getVersion,
			GetStartTime: k.getStartTimestamp,
		})
	}

	for _, cmd := range m.Commands() {
		if k, ok := kernel.(interface {
			RegisterCommand(name, moduleName, description string, h CommandHandler)
		}); ok {
			k.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
		}
	}
	return nil
}

// OnUnload implements Module.
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

// Commands implements Module. Returns Command values whose handlers bridge into
// the Python interpreter.
func (m *PythonModule) Commands() []Command {
	cmds := make([]Command, 0, len(m.pyMod.Commands))
	for _, pyCmd := range m.pyMod.Commands {
		pyCmd := pyCmd // capture loop variable
		cmd := Command{
			Name:        pyCmd.Name,
			Description: pyCmd.Description,
			Handler:     m.makeHandler(pyCmd),
		}
		cmds = append(cmds, cmd)
	}
	return cmds
}

// makeHandler builds a CommandHandler that translates a Telegram event into a
// BridgeEvent and dispatches it to the Python handler.
func (m *PythonModule) makeHandler(pyCmd pybridge.PyCommand) CommandHandler {
	return func(ctx context.Context, ev *events.NewMessage) error {
		if ev == nil || ev.Raw == nil {
			return nil
		}

		client := m.getClient()

		msgID := int64(ev.Raw.ID)
		chatID := ev.PeerID
		text := ev.Text()

		bridgeEv := pybridge.BridgeEvent{
			ChatID:    chatID,
			MessageID: msgID,
			Text:      text,
			SenderID:  ev.SenderID,
		}

		// Propagate pipeline state from context into the bridge event.
		if pipeState := pybridge.PipelineCaptureFromContext(ctx); pipeState != nil {
			bridgeEv.PipeInput = pipeState.Input
			bridgeEv.IsPiped = pipeState.IsPiped
		}

		if client != nil {
			bridgeEv.EditFn = func(newText string) error {
				// If a pipeline capture is active, record the edit text as output.
				if pipeState := pybridge.PipelineCaptureFromContext(ctx); pipeState != nil {
					pipeState.Capture(newText)
				}
				_, err := client.EditMessage(ctx, mcubclient.EditMessageParams{
					PeerID:    chatID,
					MessageID: int(msgID),
					Text:      newText,
				})
				return err
			}
			bridgeEv.ReplyFn = func(replyText string) error {
				_, err := client.SendMessage(ctx, mcubclient.SendMessageParams{
					PeerID: chatID,
					Text:   replyText,
				})
				return err
			}
			bridgeEv.DeleteFn = func() error {
				return client.DeleteMessage(ctx, mcubclient.DeleteMessageParams{
					PeerID:     chatID,
					MessageIDs: []int{int(msgID)},
					Revoke:     true,
				})
			}
			bridgeEv.SendMessageFn = func(targetChatID int64, sendText string) error {
				_, err := client.SendMessage(ctx, mcubclient.SendMessageParams{
					PeerID: targetChatID,
					Text:   sendText,
				})
				return err
			}
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
			bridgeEv.GetEntityFn = func(entityID int64) (map[string]interface{}, error) {
				if entityID == 0 {
					return nil, nil
				}
				entity, err := client.GetEntity(ctx, fmt.Sprintf("%d", entityID))
				if err != nil || entity == nil {
					return nil, err
				}
				return map[string]interface{}{"id": entityID}, nil
			}
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

		if client != nil && ev.ReplyToMsgID != 0 {
			replyToID := ev.ReplyToMsgID
			bridgeEv.GetReplyFn = func() (*pybridge.ReplyMessage, error) {
				msgs, err := client.GetMessages(ctx, chatID, []int{replyToID})
				if err != nil || len(msgs) == 0 {
					return nil, err
				}
				raw := msgs[0]
				return &pybridge.ReplyMessage{
					MessageID: int64(raw.ID),
					ChatID:    chatID,
					Text:      raw.Message,
				}, nil
			}
		}

		bridgeEv.DownloadMediaFn = func(filePath string) error {
			return fmt.Errorf("download_media not yet implemented")
		}

		return m.bridge.CallPyCommand(m.pyMod.ModName, pyCmd.Name, bridgeEv)
	}
}

// getClient extracts the MCUBClient from the stored kernel reference via a
// duck-typed interface, avoiding an import of the kernel package.
func (m *PythonModule) getClient() *mcubclient.MCUBClient {
	if m.rawKernel == nil {
		return nil
	}
	if cg, ok := m.rawKernel.(interface {
		GetClient() *mcubclient.MCUBClient
	}); ok {
		return cg.GetClient()
	}
	return nil
}
