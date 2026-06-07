package pybridge

// callbacks.go – Go functions exported to C that are invoked by the _mcub_go
// Python extension methods.
//
// CGo rule: a file with //export directives may only have declarations (not
// definitions) in its C preamble.  All C function bodies live in bridge.go.

/*
#include <Python.h>
#include <stdlib.h>
#include <string.h>
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"time"
	"unsafe"
)

// ---------------------------------------------------------------------------
// Session-scoped callbacks (Telegram operations)
// ---------------------------------------------------------------------------

//export goEditMessage
func goEditMessage(sID, cID, mID C.longlong, text, pm *C.char) C.int {
	ev := getSession(int64(sID))
	if ev == nil || ev.EditFn == nil {
		return -1
	}
	if err := ev.EditFn(C.GoString(text)); err != nil {
		return -1
	}
	return 0
}

//export goReplyMessage
func goReplyMessage(sID, cID, mID C.longlong, text, pm *C.char) C.int {
	ev := getSession(int64(sID))
	if ev == nil || ev.ReplyFn == nil {
		return -1
	}
	if err := ev.ReplyFn(C.GoString(text)); err != nil {
		return -1
	}
	return 0
}

//export goDeleteMessage
func goDeleteMessage(sID, cID, mID C.longlong) C.int {
	ev := getSession(int64(sID))
	if ev == nil || ev.DeleteFn == nil {
		return -1
	}
	if err := ev.DeleteFn(); err != nil {
		return -1
	}
	return 0
}

//export goSendMessage
func goSendMessage(sID, cID C.longlong, text, pm *C.char) C.int {
	ev := getSession(int64(sID))
	if ev == nil || ev.SendMessageFn == nil {
		return -1
	}
	if err := ev.SendMessageFn(int64(cID), C.GoString(text)); err != nil {
		return -1
	}
	return 0
}

//export goGetMe
func goGetMe(sID C.longlong) *C.char {
	ev := getSession(int64(sID))
	if ev == nil || ev.GetMeFn == nil {
		return nil
	}
	info, err := ev.GetMeFn()
	if err != nil || info == nil {
		return nil
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	return C.CString(string(b)) // caller must free
}

//export goGetEntity
func goGetEntity(sID, eID C.longlong) *C.char {
	ev := getSession(int64(sID))
	if ev == nil || ev.GetEntityFn == nil {
		return nil
	}
	info, err := ev.GetEntityFn(int64(eID))
	if err != nil || info == nil {
		return nil
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	return C.CString(string(b)) // caller must free
}

//export goGetReplyMessage
func goGetReplyMessage(sID, cID, mID C.longlong) *C.char {
	ev := getSession(int64(sID))
	if ev == nil || ev.GetReplyFn == nil {
		return nil
	}
	msg, err := ev.GetReplyFn()
	if err != nil || msg == nil {
		return nil
	}
	m := map[string]interface{}{
		"id":        msg.MessageID,
		"text":      msg.Text,
		"sender_id": msg.SenderID,
		"chat_id":   msg.ChatID,
		"file_id":   msg.FileID,
		"file_name": msg.FileName,
	}
	b, err := json.Marshal(m)
	if err != nil {
		return nil
	}
	return C.CString(string(b)) // caller must free
}

//export goDownloadMedia
func goDownloadMedia(sID, cID, mID C.longlong, fp *C.char) C.int {
	ev := getSession(int64(sID))
	if ev == nil || ev.DownloadMediaFn == nil {
		return -1
	}
	if err := ev.DownloadMediaFn(C.GoString(fp)); err != nil {
		return -1
	}
	return 0
}

//export goGetMessage
func goGetMessage(sID, cID, mID C.longlong) *C.char {
	ev := getSession(int64(sID))
	if ev == nil || ev.GetMessageFn == nil {
		return nil
	}
	info, err := ev.GetMessageFn(int64(cID), int64(mID))
	if err != nil || info == nil {
		return nil
	}
	b, err := json.Marshal(info)
	if err != nil {
		return nil
	}
	return C.CString(string(b)) // caller must free
}

// ---------------------------------------------------------------------------
// Kernel-scoped callbacks (no session dependency)
// ---------------------------------------------------------------------------

//export goGetPrefix
func goGetPrefix() *C.char {
	cbs := getKernelCBs()
	if cbs.GetPrefix == nil {
		return C.CString(".")
	}
	return C.CString(cbs.GetPrefix()) // caller must free
}

//export goGetVersion
func goGetVersion() *C.char {
	cbs := getKernelCBs()
	if cbs.GetVersion == nil {
		return C.CString("0.0.0")
	}
	return C.CString(cbs.GetVersion()) // caller must free
}

//export goGetStartTime
func goGetStartTime() C.longlong {
	cbs := getKernelCBs()
	if cbs.GetStartTime == nil {
		return C.longlong(time.Now().Unix())
	}
	return C.longlong(cbs.GetStartTime())
}

//export goLogInfo
func goLogInfo(msg *C.char) {
	cbs := getKernelCBs()
	if cbs.LogInfo != nil {
		cbs.LogInfo(C.GoString(msg))
	}
}

//export goLogDebug
func goLogDebug(msg *C.char) {
	cbs := getKernelCBs()
	if cbs.LogDebug != nil {
		cbs.LogDebug(C.GoString(msg))
	}
}

//export goLogWarn
func goLogWarn(msg *C.char) {
	cbs := getKernelCBs()
	if cbs.LogWarn != nil {
		cbs.LogWarn(C.GoString(msg))
	}
}

//export goLogError
func goLogError(msg *C.char) {
	cbs := getKernelCBs()
	if cbs.LogError != nil {
		cbs.LogError(C.GoString(msg))
	}
}

//export goDBGet
func goDBGet(module, key *C.char) *C.char {
	cbs := getKernelCBs()
	if cbs.DBGet == nil {
		return nil
	}
	val := cbs.DBGet(C.GoString(module), C.GoString(key))
	if val == "" {
		return nil
	}
	return C.CString(val) // caller must free
}

//export goDBSet
func goDBSet(module, key, value *C.char) {
	cbs := getKernelCBs()
	if cbs.DBSet != nil {
		cbs.DBSet(C.GoString(module), C.GoString(key), C.GoString(value))
	}
}

//export goDBDelete
func goDBDelete(module, key *C.char) {
	cbs := getKernelCBs()
	if cbs.DBDelete != nil {
		cbs.DBDelete(C.GoString(module), C.GoString(key))
	}
}

//export goSaveModuleConfig
func goSaveModuleConfig(mname, data *C.char) {
	cbs := getKernelCBs()
	if cbs.SaveModuleConfig != nil {
		cbs.SaveModuleConfig(C.GoString(mname), C.GoString(data))
	}
}

//export goRestartKernel
func goRestartKernel() {
	cbs := getKernelCBs()
	if cbs.RestartKernel != nil {
		cbs.RestartKernel()
	}
}

// Ensure unsafe is used (it is, via C.free calls in the C layer).
var _ = unsafe.Pointer(nil)
var _ = fmt.Sprintf
