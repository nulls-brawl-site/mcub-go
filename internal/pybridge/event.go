package pybridge

import (
	"sync"
	"sync/atomic"
)

// BridgeEvent carries all the context Python handlers need for a single
// command invocation.  The callback functions are set up by
// loader_integration.go and close over the kernel client and event metadata.
type BridgeEvent struct {
	ChatID    int64
	MessageID int64
	Text      string
	SenderID  int64

	// Telegram operation callbacks – set by the caller before registering the
	// session.  Each closure is responsible for using the right context,
	// peer IDs, etc.
	EditFn          func(text string) error
	ReplyFn         func(text string) error
	DeleteFn        func() error
	GetReplyFn      func() (*ReplyMessage, error)
	SendMessageFn   func(chatID int64, text string) error
	GetMeFn         func() (map[string]interface{}, error)
	GetEntityFn     func(entityID int64) (map[string]interface{}, error)
	GetMessageFn    func(chatID, msgID int64) (map[string]interface{}, error)
	DownloadMediaFn func(filePath string) error
}

// ReplyMessage is the data returned when a Python handler calls
// event.get_reply_message().
type ReplyMessage struct {
	Text      string
	SenderID  int64
	MessageID int64
	ChatID    int64
	FileID    string
	FileName  string
}

// KernelCallbacks holds kernel-wide (non-session-scoped) callbacks that Python
// code can invoke at any time.  Set once during bridge initialisation via
// SetKernelCallbacks.
type KernelCallbacks struct {
	GetPrefix        func() string
	GetVersion       func() string
	GetStartTime     func() int64
	LogInfo          func(msg string)
	LogDebug         func(msg string)
	LogWarn          func(msg string)
	LogError         func(msg string)
	DBGet            func(module, key string) string
	DBSet            func(module, key, value string)
	DBDelete         func(module, key string)
	SaveModuleConfig func(moduleName, data string)
	RestartKernel    func()
}

// globalKernelCBs stores the kernel-wide callbacks.
var (
	globalKernelCBs   KernelCallbacks
	globalKernelCBsMu sync.RWMutex
)

// SetKernelCallbacks stores the kernel-wide callbacks used by every Python
// extension call that does not depend on a per-event session.
func SetKernelCallbacks(cbs KernelCallbacks) {
	globalKernelCBsMu.Lock()
	globalKernelCBs = cbs
	globalKernelCBsMu.Unlock()
}

func getKernelCBs() KernelCallbacks {
	globalKernelCBsMu.RLock()
	defer globalKernelCBsMu.RUnlock()
	return globalKernelCBs
}

// ---------------------------------------------------------------------------
// Session map – maps a numeric session ID to a live BridgeEvent so that
// Python callbacks (edit_message, etc.) can look up the right closure.
// ---------------------------------------------------------------------------

var (
	sessionMu      sync.RWMutex
	sessions       = map[int64]*BridgeEvent{}
	sessionCounter int64
)

func registerSession(ev *BridgeEvent) int64 {
	id := atomic.AddInt64(&sessionCounter, 1)
	sessionMu.Lock()
	sessions[id] = ev
	sessionMu.Unlock()
	return id
}

func getSession(id int64) *BridgeEvent {
	sessionMu.RLock()
	ev := sessions[id]
	sessionMu.RUnlock()
	return ev
}

func releaseSession(id int64) {
	sessionMu.Lock()
	delete(sessions, id)
	sessionMu.Unlock()
}
