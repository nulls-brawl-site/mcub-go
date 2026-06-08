// SPDX-License-Identifier: MIT
// setup_bot.go — inline bot setup wizard (port of core_inline/bot.py InlineBot.create_bot)
//
// On first start, when inline_bot_token is missing from config, this wizard runs:
//   1. Auto-create via BotFather (sends /newbot, waits for token)
//   2. Or enter token manually
// Then starts the inline bot client.

package kernel

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/session"
)

// reBotToken matches a Telegram bot token: digits:alphanumeric
var reBotToken = regexp.MustCompile(`\d{8,12}:[A-Za-z0-9_-]{35,}`)

// SetupInlineBotIfNeeded runs the interactive bot setup wizard when
// inline_bot_token is absent from the config.
// Must be called while the user client is already connected (inside Run callback).
func (k *Kernel) SetupInlineBotIfNeeded(ctx context.Context) error {
	if k.Config == nil {
		return nil
	}
	if k.Config.InlineBotToken != nil && *k.Config.InlineBotToken != "" {
		// Token already configured — start the bot client.
		return k.StartInlineBot(ctx, *k.Config.InlineBotToken)
	}

	// ── No token — run setup wizard ──────────────────────────────────────
	fmt.Println()
	fmt.Println("╔══════════════════════════════════════════════╗")
	fmt.Println("║        MCUB — Inline Bot Setup               ║")
	fmt.Println("╠══════════════════════════════════════════════╣")
	fmt.Println("║  1. Auto-create via @BotFather               ║")
	fmt.Println("║  2. Enter token manually                     ║")
	fmt.Println("╚══════════════════════════════════════════════╝")
	fmt.Print("Select (1/2): ")

	choice := strings.TrimSpace(readLine())

	var token string
	var err error

	switch choice {
	case "1":
		token, err = k.autoCreateBot(ctx)
		if err != nil {
			fmt.Printf("Auto-create failed: %v\nFalling back to manual entry.\n", err)
			token, err = k.manualBotToken()
		}
	default:
		token, err = k.manualBotToken()
	}

	if err != nil {
		return fmt.Errorf("inline bot setup: %w", err)
	}
	if token == "" {
		return fmt.Errorf("inline bot setup: no token provided")
	}

	// Persist token to config.
	k.Config.InlineBotToken = &token
	if k.ConfigFile != "" {
		if saveErr := k.Config.Save(k.ConfigFile); saveErr != nil {
			k.Log.Warn("Could not save inline_bot_token to config: %v", saveErr)
		}
	}

	return k.StartInlineBot(ctx, token)
}

// autoCreateBot sends /newbot to @BotFather and waits for the token reply.
// Mirrors InlineBot._create_bot_via_botfather() in core_inline/bot.py.
func (k *Kernel) autoCreateBot(ctx context.Context) (string, error) {
	if k.Client == nil {
		return "", fmt.Errorf("client not connected")
	}

	fmt.Println("\n[BotFather] Resolving @BotFather...")
	// Resolve @BotFather user ID
	bfUser, err := k.Client.GetEntity(ctx, "BotFather")
	if err != nil {
		return "", fmt.Errorf("resolve BotFather: %w", err)
	}
	bfID := int64(0)
	if u, ok := bfUser.(*tg.User); ok {
		bfID = int64(u.ID)
	}
	if bfID == 0 {
		return "", fmt.Errorf("could not get BotFather ID")
	}

	sendMsg := func(text string) error {
		_, err := k.Client.SendMessage(ctx, mcubclient.SendMessageParams{
			PeerID: bfID,
			Text:   text,
		})
		return err
	}

	fmt.Println("[BotFather] Sending /newbot ...")
	if err := sendMsg("/newbot"); err != nil {
		return "", fmt.Errorf("send /newbot: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)

	fmt.Println("[BotFather] Sending bot name: MCUB Inline Bot")
	if err := sendMsg("MCUB Inline Bot"); err != nil {
		return "", fmt.Errorf("send bot name: %w", err)
	}
	time.Sleep(1500 * time.Millisecond)

	fmt.Print("[BotFather] Enter desired bot username (must end with _bot): ")
	username := strings.TrimSpace(readLine())
	if username == "" {
		username = fmt.Sprintf("mcub_%d_bot", time.Now().Unix()%100000)
	}
	if !strings.HasSuffix(strings.ToLower(username), "_bot") {
		username += "_bot"
	}

	fmt.Printf("[BotFather] Sending username: %s\n", username)
	if err := sendMsg(username); err != nil {
		return "", fmt.Errorf("send username: %w", err)
	}

	// Wait up to 30s for token in BotFather reply
	fmt.Println("[BotFather] Waiting for token...")
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		time.Sleep(2 * time.Second)

		msgs, err := k.Client.GetHistory(ctx, bfID, mcubclient.HistoryParams{Limit: 5})
		if err != nil {
			continue
		}
		for _, msg := range msgs {
			if tok := reBotToken.FindString(msg.Message); tok != "" {
				fmt.Printf("[BotFather] Token received: %s...\n", tok[:10])
				return tok, nil
			}
		}
	}

	return "", fmt.Errorf("timeout waiting for BotFather token (30s)")
}

// manualBotToken prompts the user to paste their bot token.
func (k *Kernel) manualBotToken() (string, error) {
	fmt.Println("\nGet a token from @BotFather → /newbot or /mybots")
	fmt.Print("Enter bot token: ")
	token := strings.TrimSpace(readLine())
	if token == "" {
		return "", fmt.Errorf("empty token")
	}
	if !reBotToken.MatchString(token) {
		return "", fmt.Errorf("token format looks invalid (expected digits:alphanum)")
	}
	return token, nil
}

// StartInlineBot authenticates the inline bot client and registers handlers.
func (k *Kernel) StartInlineBot(ctx context.Context, token string) error {
	if token == "" {
		return fmt.Errorf("empty bot token")
	}

	k.Log.Info("Starting inline bot...")

	// Create a separate bot client.
	botSessStore, sessErr := session.NewFileSessionStorage("inline_bot_session")
	if sessErr != nil {
		return fmt.Errorf("create bot session storage: %w", sessErr)
	}
	opts := mcubclient.Options{
		AppID:   int(k.APIID),
		AppHash: k.APIHash,
		Session: botSessStore.Storage(),
	}
	botClient, err := mcubclient.New(opts)
	if err != nil {
		return fmt.Errorf("create bot client: %w", err)
	}

	// Authenticate as bot inside a goroutine so it doesn't block.
	go func() {
		runCtx, cancel := context.WithCancel(context.Background())
		_ = cancel
		err := botClient.Run(runCtx, func(ctx context.Context) error {
			if authErr := botClient.AuthenticateAsBot(ctx, token); authErr != nil {
				k.Log.Error("Inline bot auth failed: %v", authErr)
				return authErr
			}
			if self, err := botClient.Self(ctx); err == nil {
				k.Log.Info("Inline bot started: @%s (id=%d)", self.Username, self.ID)
				k.BotToken = token
				// Store username in config
				if k.Config != nil {
					uname := self.Username
					k.Config.InlineBotUsername = &uname
					if k.ConfigFile != "" {
						_ = k.Config.Save(k.ConfigFile)
					}
				}
			}
			// Keep bot alive
			<-runCtx.Done()
			return nil
		})
		if err != nil {
			k.Log.Warn("Inline bot client stopped: %v", err)
		}
	}()

	return nil
}

// readLine reads one line from stdin.
func readLine() string {
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text()
	}
	return ""
}
