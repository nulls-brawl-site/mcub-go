package modules

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// DB keys for trusted module persistence.
const (
	dbKeyTrustedUsers   = "trusted:users"
	dbKeyTrustedNonick  = "trusted:nonick"
	dbKeyTrustedExpired = "trusted:expired"
	dbKeyTrustedSgroups = "trusted:sgroups"
)

// accessCategories defines the permission categories for trusted users.
var accessCategories = []string{
	"modules", "loader", "config", "eval", "terminal", "inline", "callback", "aliases",
}

// SGroup is a named access group that bundles users and category permissions.
type SGroup struct {
	Users  []int64          `json:"users"`
	Access map[string]bool  `json:"access"`
}

// trustedModule manages trusted users who can execute owner commands.
type trustedModule struct {
	k *kernel.Kernel
}

func newTrustedModule() loader.Module { return &trustedModule{} }

func (m *trustedModule) Name() string { return "trusted" }

func (m *trustedModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("trusted: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	return nil
}

func (m *trustedModule) OnUnload(k interface{}) error {
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *trustedModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "trust", Description: "add user to trusted list", Handler: m.cmdTrust},
		{Name: "untrust", Description: "remove user from trusted list", Handler: m.cmdUntrust},
		{Name: "trustlist", Description: "show list of trusted users", Handler: m.cmdTrustlist},
		{Name: "trustaccess", Description: "manage trusted user access categories", Handler: m.cmdTrustaccess},
		{Name: "ownerprefix", Description: "show owner prefix by id/@username/reply", Handler: m.cmdOwnerprefix},
		{Name: "trustcmd", Description: "manage per-command access for trusted users", Handler: m.cmdTrustcmd},
		{Name: "nonickuser", Description: "toggle NoNick mode for trusted user", Handler: m.cmdNonickuser},
		{Name: "nonickusers", Description: "show users with NoNick enabled", Handler: m.cmdNonickusers},
		{Name: "watchers", Description: "show list of active watchers", Handler: m.cmdWatchers},
		{Name: "watchersdebug", Description: "debug watcher information", Handler: m.cmdWatchersdebug},
		{Name: "watcher", Description: "enable/disable specific watcher", Handler: m.cmdWatcher},
		{Name: "timedtrusted", Description: "show temporary trusted users with expiry", Handler: m.cmdTimedtrusted},
		{Name: "sgroup", Description: "manage access groups", Handler: m.cmdSgroup},
	}
}

// ---------- DB helpers ----------

func (m *trustedModule) getTrustedList() ([]int64, error) {
	var list []int64
	err := m.k.DB.GetJSON(dbKeyTrustedUsers, &list)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return list, err
}

func (m *trustedModule) saveTrustedList(list []int64) error {
	return m.k.DB.SetJSON(dbKeyTrustedUsers, list)
}

func (m *trustedModule) getNonickList() ([]int64, error) {
	var list []int64
	err := m.k.DB.GetJSON(dbKeyTrustedNonick, &list)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return list, err
}

func (m *trustedModule) saveNonickList(list []int64) error {
	return m.k.DB.SetJSON(dbKeyTrustedNonick, list)
}

func (m *trustedModule) getExpiredMap() (map[string]int64, error) {
	result := make(map[string]int64)
	err := m.k.DB.GetJSON(dbKeyTrustedExpired, &result)
	if err == sql.ErrNoRows {
		return result, nil
	}
	return result, err
}

func (m *trustedModule) saveExpiredMap(exp map[string]int64) error {
	return m.k.DB.SetJSON(dbKeyTrustedExpired, exp)
}

func (m *trustedModule) getAccessMap(uid int64) (map[string]bool, error) {
	key := fmt.Sprintf("trusted:access:%d", uid)
	result := make(map[string]bool)
	err := m.k.DB.GetJSON(key, &result)
	if err == sql.ErrNoRows {
		// defaults
		for _, cat := range accessCategories {
			result[cat] = (cat == "modules" || cat == "inline" || cat == "callback")
		}
		return result, nil
	}
	return result, err
}

func (m *trustedModule) saveAccessMap(uid int64, access map[string]bool) error {
	key := fmt.Sprintf("trusted:access:%d", uid)
	return m.k.DB.SetJSON(key, access)
}

func (m *trustedModule) getCmdAccessMap(uid int64) (map[string]bool, error) {
	key := fmt.Sprintf("trusted:cmdaccess:%d", uid)
	result := make(map[string]bool)
	err := m.k.DB.GetJSON(key, &result)
	if err == sql.ErrNoRows {
		return result, nil
	}
	return result, err
}

func (m *trustedModule) saveCmdAccessMap(uid int64, access map[string]bool) error {
	key := fmt.Sprintf("trusted:cmdaccess:%d", uid)
	return m.k.DB.SetJSON(key, access)
}

func (m *trustedModule) getSgroups() (map[string]SGroup, error) {
	result := make(map[string]SGroup)
	err := m.k.DB.GetJSON(dbKeyTrustedSgroups, &result)
	if err == sql.ErrNoRows {
		return result, nil
	}
	return result, err
}

func (m *trustedModule) saveSgroups(groups map[string]SGroup) error {
	return m.k.DB.SetJSON(dbKeyTrustedSgroups, groups)
}

// ---------- utility ----------

// parseArgs strips prefix+command and returns remaining tokens.
func (m *trustedModule) parseArgs(ev *events.NewMessage) []string {
	body := ev.Text()
	if m.k != nil {
		body = strings.TrimPrefix(body, m.k.Prefix())
	}
	parts := strings.Fields(body)
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// resolveUserID extracts a user ID from args (numeric ID, @username) or from
// the reply-to message sender. Returns 0 if nothing could be resolved.
func (m *trustedModule) resolveUserID(ctx context.Context, ev *events.NewMessage, args []string) (int64, error) {
	// If there are args, try to parse the first one.
	if len(args) > 0 {
		arg := args[0]
		// Numeric ID (possibly negative, but user IDs are positive).
		if id, err := strconv.ParseInt(strings.TrimPrefix(arg, "@"), 10, 64); err == nil {
			return id, nil
		}
		// @username resolution.
		if strings.HasPrefix(arg, "@") || (len(arg) > 0 && arg[0] != '-') {
			username := strings.TrimPrefix(arg, "@")
			if m.k.Client != nil {
				entity, err := m.k.Client.ResolveUsername(ctx, username)
				if err != nil {
					return 0, fmt.Errorf("cannot resolve @%s: %w", username, err)
				}
				if user, ok := entity.(*tg.User); ok {
					return user.ID, nil
				}
			}
		}
	}

	// Try reply message.
	if ev.IsReply && ev.ReplyToMsgID != 0 && m.k.Client != nil {
		msgs, err := m.k.Client.GetMessages(ctx, ev.PeerID, []int{ev.ReplyToMsgID})
		if err == nil && len(msgs) > 0 {
			if fromPeer, ok := msgs[0].FromID.(*tg.PeerUser); ok {
				return fromPeer.UserID, nil
			}
		}
	}

	return 0, nil
}

// userDisplay returns a formatted name for a user ID.
func (m *trustedModule) userDisplay(ctx context.Context, uid int64) string {
	if m.k.Client != nil {
		user, err := m.k.Client.GetUser(ctx, uid)
		if err == nil && user != nil {
			name := strings.TrimSpace(user.FirstName + " " + user.LastName)
			if name == "" && user.Username != "" {
				name = "@" + user.Username
			}
			if name != "" {
				return name
			}
		}
	}
	return strconv.FormatInt(uid, 10)
}

func int64SliceContains(slice []int64, v int64) bool {
	for _, x := range slice {
		if x == v {
			return true
		}
	}
	return false
}

func removeInt64(slice []int64, v int64) []int64 {
	out := make([]int64, 0, len(slice))
	for _, x := range slice {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func formatDuration(seconds int64) string {
	if seconds >= 86400 {
		return fmt.Sprintf("%d days", seconds/86400)
	}
	if seconds >= 3600 {
		return fmt.Sprintf("%d hours", seconds/3600)
	}
	return fmt.Sprintf("%d minutes", seconds/60)
}

// ---------- command handlers ----------

// .trust @user/id/reply — add user to trusted list
func (m *trustedModule) cmdTrust(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	uid, err := m.resolveUserID(ctx, ev, args)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ %v", err))
	}
	if uid == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.trust @user/id</code> or reply to a message")
	}

	list, _ := m.getTrustedList()
	if int64SliceContains(list, uid) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ User is already in the trusted list")
	}
	list = append(list, uid)
	if err := m.saveTrustedList(list); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ DB error: %v", err))
	}
	// Set default access.
	access := make(map[string]bool)
	for _, cat := range accessCategories {
		access[cat] = (cat == "modules" || cat == "inline" || cat == "callback")
	}
	_ = m.saveAccessMap(uid, access)

	name := m.userDisplay(ctx, uid)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("✅ <b>%s</b> (<code>%d</code>) added to trusted list", name, uid))
}

// .untrust @user/id/reply — remove user from trusted list
func (m *trustedModule) cmdUntrust(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	uid, err := m.resolveUserID(ctx, ev, args)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
	}
	if uid == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.untrust @user/id</code> or reply to a message")
	}

	list, _ := m.getTrustedList()
	if !int64SliceContains(list, uid) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ User is not in the trusted list")
	}
	list = removeInt64(list, uid)
	if err := m.saveTrustedList(list); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ DB error: %v", err))
	}
	// Also remove from nonick list.
	nonick, _ := m.getNonickList()
	nonick = removeInt64(nonick, uid)
	_ = m.saveNonickList(nonick)

	name := m.userDisplay(ctx, uid)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("✅ <b>%s</b> (<code>%d</code>) removed from trusted list", name, uid))
}

// .trustlist — show all trusted users
func (m *trustedModule) cmdTrustlist(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	list, _ := m.getTrustedList()
	if len(list) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"📋 <b>Trusted list is empty</b>")
	}
	nonick, _ := m.getNonickList()
	lines := []string{"👥 <b>Trusted Users:</b>"}
	for _, uid := range list {
		name := m.userDisplay(ctx, uid)
		nn := ""
		if int64SliceContains(nonick, uid) {
			nn = " 🔑"
		}
		lines = append(lines, fmt.Sprintf("• %s (<code>%d</code>)%s", name, uid, nn))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .trustaccess @user/id [category on|off] — show or toggle access categories
func (m *trustedModule) cmdTrustaccess(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	uid, err := m.resolveUserID(ctx, ev, args)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
	}
	if uid == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.trustaccess @user/id [category on|off]</code>")
	}
	list, _ := m.getTrustedList()
	if !int64SliceContains(list, uid) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ User is not in the trusted list")
	}

	access, _ := m.getAccessMap(uid)
	name := m.userDisplay(ctx, uid)

	// If 3+ args: "@user category on|off"
	// Shift args past the user identifier
	rest := args
	if len(rest) > 0 && (rest[0][0] == '@' || rest[0][0] >= '0' && rest[0][0] <= '9') {
		rest = rest[1:]
	}

	if len(rest) >= 2 {
		cat := strings.ToLower(rest[0])
		state := strings.ToLower(rest[1])
		found := false
		for _, c := range accessCategories {
			if c == cat {
				found = true
				break
			}
		}
		if !found {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("❌ Unknown category <code>%s</code>. Valid: %s",
					cat, strings.Join(accessCategories, ", ")))
		}
		switch state {
		case "on", "true", "1", "yes":
			access[cat] = true
		case "off", "false", "0", "no":
			access[cat] = false
		default:
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ State must be <code>on</code> or <code>off</code>")
		}
		if err := m.saveAccessMap(uid, access); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("❌ DB error: %v", err))
		}
		icon := "✅"
		if !access[cat] {
			icon = "🚫"
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("%s Category <code>%s</code> for <b>%s</b> set to <b>%s</b>",
				icon, cat, name, state))
	}

	// Show current access.
	lines := []string{fmt.Sprintf("🔐 <b>Access for %s</b> (<code>%d</code>):", name, uid),
		"<blockquote>"}
	for _, cat := range accessCategories {
		icon := "🚫"
		if access[cat] {
			icon = "✅"
		}
		lines = append(lines, fmt.Sprintf("%s <code>%s</code>", icon, cat))
	}
	lines = append(lines, "</blockquote>")
	lines = append(lines, "<i>Use .trustaccess @user category on|off to change</i>")
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .ownerprefix [id/reply] — show prefix for a user or list all
func (m *trustedModule) cmdOwnerprefix(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	prefix := m.k.Prefix()
	args := m.parseArgs(ev)

	if len(args) > 0 || ev.IsReply {
		uid, err := m.resolveUserID(ctx, ev, args)
		if err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
		}
		if uid == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Could not resolve user")
		}
		name := m.userDisplay(ctx, uid)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🔧 <b>Prefix for %s</b> (<code>%d</code>): <code>%s</code>",
				name, uid, prefix))
	}

	// List all: owner + trusted users.
	list, _ := m.getTrustedList()
	lines := []string{"🔧 <b>Owner Prefixes:</b>"}
	ownerID := m.k.AdminID
	ownerName := m.userDisplay(ctx, ownerID)
	lines = append(lines, fmt.Sprintf("• <b>%s</b> (<code>%d</code>) — <code>%s</code> [owner]",
		ownerName, ownerID, prefix))
	for _, uid := range list {
		if uid == ownerID {
			continue
		}
		name := m.userDisplay(ctx, uid)
		lines = append(lines, fmt.Sprintf("• <b>%s</b> (<code>%d</code>) — <code>%s</code>",
			name, uid, prefix))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .trustcmd @user/id +cmd|-cmd|list — toggle specific command for user
func (m *trustedModule) cmdTrustcmd(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	uid, err := m.resolveUserID(ctx, ev, args)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
	}
	if uid == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.trustcmd @user/id +cmdname|-cmdname|list</code>")
	}
	list, _ := m.getTrustedList()
	if !int64SliceContains(list, uid) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ User is not in the trusted list")
	}

	name := m.userDisplay(ctx, uid)

	// Shift args past user identifier.
	rest := args
	if len(rest) > 0 && (rest[0][0] == '@' || (rest[0][0] >= '0' && rest[0][0] <= '9')) {
		rest = rest[1:]
	}

	if len(rest) == 0 || rest[0] == "list" {
		// Show per-command access list.
		cmdAccess, _ := m.getCmdAccessMap(uid)
		if len(cmdAccess) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("📋 No per-command overrides for <b>%s</b>", name))
		}
		lines := []string{fmt.Sprintf("📋 <b>Per-command access for %s:</b>", name), "<blockquote>"}
		for cmd, allowed := range cmdAccess {
			icon := "✅"
			if !allowed {
				icon = "🚫"
			}
			lines = append(lines, fmt.Sprintf("%s <code>%s</code>", icon, cmd))
		}
		lines = append(lines, "</blockquote>")
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
	}

	arg := rest[0]
	var allow bool
	var cmdName string
	if strings.HasPrefix(arg, "+") {
		allow = true
		cmdName = arg[1:]
	} else if strings.HasPrefix(arg, "-") {
		allow = false
		cmdName = arg[1:]
	} else {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.trustcmd @user/id +cmdname</code> or <code>-cmdname</code>")
	}

	cmdAccess, _ := m.getCmdAccessMap(uid)
	cmdAccess[cmdName] = allow
	if err := m.saveCmdAccessMap(uid, cmdAccess); err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("❌ DB error: %v", err))
	}
	icon := "✅"
	action := "allowed"
	if !allow {
		icon = "🚫"
		action = "blocked"
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("%s Command <code>%s</code> %s for <b>%s</b>", icon, cmdName, action, name))
}

// .nonickuser @user/id — toggle NoNick mode for trusted user
func (m *trustedModule) cmdNonickuser(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	uid, err := m.resolveUserID(ctx, ev, args)
	if err != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
	}
	if uid == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.nonickuser @user/id</code> or reply")
	}
	list, _ := m.getTrustedList()
	if !int64SliceContains(list, uid) {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⚠️ User is not in the trusted list")
	}
	nonick, _ := m.getNonickList()
	name := m.userDisplay(ctx, uid)
	if int64SliceContains(nonick, uid) {
		nonick = removeInt64(nonick, uid)
		_ = m.saveNonickList(nonick)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("🔑 NoNick <b>disabled</b> for <b>%s</b>", name))
	}
	nonick = append(nonick, uid)
	_ = m.saveNonickList(nonick)
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("🔑 NoNick <b>enabled</b> for <b>%s</b>", name))
}

// .nonickusers — list users with NoNick enabled
func (m *trustedModule) cmdNonickusers(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	nonick, _ := m.getNonickList()
	if len(nonick) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"📋 <b>No users with NoNick enabled</b>")
	}
	lines := []string{"🔑 <b>Users with NoNick:</b>"}
	for _, uid := range nonick {
		name := m.userDisplay(ctx, uid)
		lines = append(lines, fmt.Sprintf("• %s (<code>%d</code>)", name, uid))
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .watchers — list active event watchers (Go kernel note)
func (m *trustedModule) cmdWatchers(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	// The Go kernel uses a middleware chain rather than named watchers.
	// Report the middleware count as a proxy.
	mws := m.k.Middlewares()
	if len(mws) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"👁 <b>Watchers:</b> none registered\n<i>Go kernel uses middleware chain</i>")
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		fmt.Sprintf("👁 <b>Watchers:</b> %d middleware(s) active\n<i>Go kernel uses middleware chain</i>",
			len(mws)))
}

// .watchersdebug — debug watcher/middleware info
func (m *trustedModule) cmdWatchersdebug(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	mws := m.k.Middlewares()
	lines := []string{
		"🔍 <b>Watcher Debug</b>",
		"<blockquote expandable>",
		fmt.Sprintf("Middleware chain length: <code>%d</code>", len(mws)),
		fmt.Sprintf("Loaded modules: <code>%d</code>", len(m.k.LoadedModules)+len(m.k.SystemModules)),
		"<i>Go kernel uses typed middleware chain rather than named watchers</i>",
		"</blockquote>",
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .watcher <name> on|off — toggle named watcher (stub for Go kernel)
func (m *trustedModule) cmdWatcher(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
		"⚠️ Named watcher control is not available in the Go kernel.\n"+
			"<i>Use .modules to manage modules instead.</i>")
}

// .timedtrusted — show temporary trusted users with expiry
func (m *trustedModule) cmdTimedtrusted(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)

	// If args given: .timedtrusted @user <seconds> — add timed trust.
	if len(args) >= 2 {
		uid, err := m.resolveUserID(ctx, ev, args)
		if err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, fmt.Sprintf("❌ %v", err))
		}
		if uid == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.timedtrusted @user/id &lt;seconds&gt;</code>")
		}
		secArg := args[1]
		if len(args) > 1 && (args[0][0] == '@' || (args[0][0] >= '0' && args[0][0] <= '9')) {
			secArg = args[1]
		}
		secs, err := strconv.ParseInt(secArg, 10, 64)
		if err != nil || secs <= 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Invalid seconds value")
		}
		list, _ := m.getTrustedList()
		if !int64SliceContains(list, uid) {
			list = append(list, uid)
			_ = m.saveTrustedList(list)
			access := make(map[string]bool)
			for _, cat := range accessCategories {
				access[cat] = (cat == "modules" || cat == "inline" || cat == "callback")
			}
			_ = m.saveAccessMap(uid, access)
		}
		expiry := time.Now().Unix() + secs
		expMap, _ := m.getExpiredMap()
		expMap[strconv.FormatInt(uid, 10)] = expiry
		_ = m.saveExpiredMap(expMap)
		name := m.userDisplay(ctx, uid)
		dur := formatDuration(secs)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("⏰ <b>%s</b> added as timed trusted for <b>%s</b>", name, dur))
	}

	// Show list of timed trusted users.
	expMap, _ := m.getExpiredMap()
	if len(expMap) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"⏰ <b>No timed trusted users</b>")
	}
	now := time.Now().Unix()
	lines := []string{"⏰ <b>Timed Trusted Users:</b>"}
	for uidStr, expiry := range expMap {
		uid, _ := strconv.ParseInt(uidStr, 10, 64)
		name := m.userDisplay(ctx, uid)
		remaining := expiry - now
		if remaining <= 0 {
			lines = append(lines, fmt.Sprintf("• %s (<code>%d</code>) — <i>expired</i>", name, uid))
		} else {
			dur := formatDuration(remaining)
			lines = append(lines, fmt.Sprintf("• %s (<code>%d</code>) — expires in %s", name, uid, dur))
		}
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
}

// .sgroup create|delete|add|remove|access|list|info <name> [args...] — manage access groups
func (m *trustedModule) cmdSgroup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	args := m.parseArgs(ev)
	if len(args) == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.sgroup create|delete|add|remove|access|list|info [name] [args]</code>")
	}

	action := strings.ToLower(args[0])
	groups, _ := m.getSgroups()

	switch action {
	case "list":
		if len(groups) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"📋 <b>No access groups defined</b>")
		}
		lines := []string{"📋 <b>Access Groups:</b>"}
		for gname, gdata := range groups {
			accessOn := 0
			for _, v := range gdata.Access {
				if v {
					accessOn++
				}
			}
			lines = append(lines, fmt.Sprintf("• <b>%s</b> — %d users, %d categories on",
				gname, len(gdata.Users), accessOn))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))

	case "create":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup create &lt;name&gt;</code>")
		}
		name := args[1]
		if _, exists := groups[name]; exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> already exists", name))
		}
		access := make(map[string]bool)
		for _, cat := range accessCategories {
			access[cat] = false
		}
		groups[name] = SGroup{Users: []int64{}, Access: access}
		_ = m.saveSgroups(groups)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("✅ Group <b>%s</b> created", name))

	case "delete":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup delete &lt;name&gt;</code>")
		}
		name := args[1]
		if _, exists := groups[name]; !exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> not found", name))
		}
		delete(groups, name)
		_ = m.saveSgroups(groups)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("✅ Group <b>%s</b> deleted", name))

	case "add":
		if len(args) < 3 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup add &lt;name&gt; @user/id</code>")
		}
		gname := args[1]
		g, exists := groups[gname]
		if !exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> not found", gname))
		}
		uid, err := m.resolveUserID(ctx, ev, args[2:])
		if err != nil || uid == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Could not resolve user")
		}
		if int64SliceContains(g.Users, uid) {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"⚠️ User already in group")
		}
		g.Users = append(g.Users, uid)
		groups[gname] = g
		_ = m.saveSgroups(groups)
		name := m.userDisplay(ctx, uid)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("✅ <b>%s</b> added to group <b>%s</b>", name, gname))

	case "remove":
		if len(args) < 3 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup remove &lt;name&gt; @user/id</code>")
		}
		gname := args[1]
		g, exists := groups[gname]
		if !exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> not found", gname))
		}
		uid, err := m.resolveUserID(ctx, ev, args[2:])
		if err != nil || uid == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ Could not resolve user")
		}
		if !int64SliceContains(g.Users, uid) {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"⚠️ User not in group")
		}
		g.Users = removeInt64(g.Users, uid)
		groups[gname] = g
		_ = m.saveSgroups(groups)
		name := m.userDisplay(ctx, uid)
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			fmt.Sprintf("✅ <b>%s</b> removed from group <b>%s</b>", name, gname))

	case "access":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup access &lt;name&gt; [category on|off]</code>")
		}
		gname := args[1]
		g, exists := groups[gname]
		if !exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> not found", gname))
		}
		if len(args) >= 4 {
			cat := strings.ToLower(args[2])
			state := strings.ToLower(args[3])
			validCat := false
			for _, c := range accessCategories {
				if c == cat {
					validCat = true
					break
				}
			}
			if !validCat {
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					fmt.Sprintf("❌ Unknown category <code>%s</code>", cat))
			}
			if g.Access == nil {
				g.Access = make(map[string]bool)
			}
			switch state {
			case "on", "true", "1":
				g.Access[cat] = true
			case "off", "false", "0":
				g.Access[cat] = false
			default:
				return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
					"❌ State must be <code>on</code> or <code>off</code>")
			}
			groups[gname] = g
			_ = m.saveSgroups(groups)
			icon := "✅"
			if !g.Access[cat] {
				icon = "🚫"
			}
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("%s <code>%s</code> for group <b>%s</b>", icon, cat, gname))
		}
		// Show access list.
		lines := []string{fmt.Sprintf("🔐 <b>Access for group %s:</b>", gname), "<blockquote>"}
		for _, cat := range accessCategories {
			icon := "🚫"
			if g.Access[cat] {
				icon = "✅"
			}
			lines = append(lines, fmt.Sprintf("%s <code>%s</code>", icon, cat))
		}
		lines = append(lines, "</blockquote>")
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))

	case "info":
		if len(args) < 2 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				"❌ <b>Usage:</b> <code>.sgroup info &lt;name&gt;</code>")
		}
		gname := args[1]
		g, exists := groups[gname]
		if !exists {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				fmt.Sprintf("⚠️ Group <b>%s</b> not found", gname))
		}
		lines := []string{fmt.Sprintf("📋 <b>Group: %s</b>", gname)}
		if len(g.Users) > 0 {
			lines = append(lines, "\n<b>Users:</b>")
			for _, uid := range g.Users {
				name := m.userDisplay(ctx, uid)
				lines = append(lines, fmt.Sprintf("• %s (<code>%d</code>)", name, uid))
			}
		} else {
			lines = append(lines, "\n<b>Users:</b> none")
		}
		accessOn := []string{}
		for cat, on := range g.Access {
			if on {
				accessOn = append(accessOn, cat)
			}
		}
		if len(accessOn) > 0 {
			lines = append(lines, fmt.Sprintf("\n<b>Access:</b> %s", strings.Join(accessOn, ", ")))
		} else {
			lines = append(lines, "\n<b>Access:</b> none")
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))

	default:
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			"❌ <b>Usage:</b> <code>.sgroup create|delete|add|remove|access|list|info [name] [args]</code>")
	}
}


