package modules

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/pbkdf2"

	"github.com/gotd/td/tg"
	"github.com/nulls-brawl-site/mcub-go/internal/kernel"
	"github.com/nulls-brawl-site/mcub-go/internal/loader"
	mcubclient "github.com/nulls-brawl-site/telegram-mcub-go/client"
	"github.com/nulls-brawl-site/telegram-mcub-go/events"
)

// Custom emoji constants (from userbot-backup.py _E dict)
const (
	backupEmojiHourglass = `<tg-emoji emoji-id="5426958067763804056">⏳</tg-emoji>`
	backupEmojiOK = `<tg-emoji emoji-id="5118861066981344121">✅</tg-emoji>`
	backupEmojiError = `<tg-emoji emoji-id="5388785832956016892">❌</tg-emoji>`
	backupEmojiWarning = `<tg-emoji emoji-id="5409235172979672859">⚠️</tg-emoji>`
	backupEmojiSettings = `<tg-emoji emoji-id="5332654441508119011">⚙️</tg-emoji>`
	backupEmojiClock = `<tg-emoji emoji-id="5326015457155620929">🧳</tg-emoji>`
	backupEmojiPackage = `<tg-emoji emoji-id="5399898266265475100">📦</tg-emoji>`
	backupEmojiRefresh = `<tg-emoji emoji-id="5332600281970517875">🔄</tg-emoji>`
	backupEmojiLock = `<tg-emoji emoji-id="5447644880824181073">🔐</tg-emoji>`
	backupEmojiCloud = `<tg-emoji emoji-id="5359954476607521990">☁️</tg-emoji>`
	backupEmojiTrash = `<tg-emoji emoji-id="5380186498827373381">🗑️</tg-emoji>`
	backupEmojiList = `<tg-emoji emoji-id="5411192149058289173">☁️</tg-emoji>`
)

// backupEntry describes a file/directory to pack into the archive.
type backupEntry struct {
	srcPath  string
	archName string
	isDir    bool
}

// backupModule provides .backup, .restore, .restore_with commands.
// Faithful port of userbot-backup.py (Backup class).
type backupModule struct {
	k           *kernel.Kernel
	mu          sync.Mutex
	cancelLoop  context.CancelFunc
	delayCancel []context.CancelFunc
}

func newBackupModule() loader.Module { return &backupModule{} }
func (m *backupModule) Name() string { return "userbot-backup" }

func (m *backupModule) OnLoad(k interface{}) error {
	kern, ok := k.(*kernel.Kernel)
	if !ok {
		return fmt.Errorf("backup: expected *kernel.Kernel, got %T", k)
	}
	m.k = kern
	for _, cmd := range m.Commands() {
		kern.RegisterCommand(cmd.Name, m.Name(), cmd.Description, cmd.Handler)
	}
	m.startAutoBackupLoop()
	return nil
}

func (m *backupModule) OnUnload(k interface{}) error {
	m.mu.Lock()
	if m.cancelLoop != nil {
		m.cancelLoop()
		m.cancelLoop = nil
	}
	for _, cancel := range m.delayCancel {
		cancel()
	}
	m.delayCancel = nil
	m.mu.Unlock()
	if m.k == nil {
		return nil
	}
	for _, cmd := range m.Commands() {
		m.k.UnregisterCommand(cmd.Name)
	}
	m.k = nil
	return nil
}

func (m *backupModule) Commands() []loader.Command {
	return []loader.Command{
		{Name: "backup", Description: "create backup  [config|db|modules] [in <time>] [cleanup] [cloud]", Handler: m.cmdBackup},
		{Name: "restore", Description: "<reply> or list - restore from backup file or show list", Handler: m.cmdRestore},
		{Name: "restore_with", Description: "<reply> restore encrypted backup with password", Handler: m.cmdRestoreWith},
	}
}

func (m *backupModule) dbGet(key string) (string, bool) {
	if m.k == nil || m.k.DB == nil {
		return "", false
	}
	val, ok, err := m.k.DB.ModuleGet(m.Name(), key)
	if err != nil || !ok {
		return "", false
	}
	return val, true
}

func (m *backupModule) dbSet(key, value string) {
	if m.k == nil || m.k.DB == nil {
		return
	}
	_ = m.k.DB.ModuleSet(m.Name(), key, value)
}

func (m *backupModule) getBackupChatID() (int64, bool) {
	if raw, ok := m.dbGet("backup_chat_id"); ok && raw != "" {
		var id int64
		if _, err := fmt.Sscanf(raw, "%d", &id); err == nil && id != 0 {
			return id, true
		}
	}
	return 0, false
}

func (m *backupModule) getIntervalHours() int64 {
	if raw, ok := m.dbGet("backup_interval_hours"); ok {
		var n int64
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n >= 1 && n <= 168 {
			return n
		}
	}
	return 12
}

func (m *backupModule) isAutoEnabled() bool {
	if raw, ok := m.dbGet("enable_auto_backup"); ok {
		return raw != "false" && raw != "0"
	}
	return true
}

func (m *backupModule) getArchiveFormat() string {
	if raw, ok := m.dbGet("archive_format"); ok && (raw == "zip" || raw == "tar.gz") {
		return raw
	}
	return "zip"
}

func (m *backupModule) getEncryptionPassword() string {
	raw, _ := m.dbGet("encryption_password")
	return raw
}

func (m *backupModule) getMaxBackups() int {
	if raw, ok := m.dbGet("max_backups"); ok {
		var n int
		if _, err := fmt.Sscanf(raw, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	return 0
}

func (m *backupModule) getCloudProvider() string {
	raw, _ := m.dbGet("cloud_provider")
	if raw == "" {
		return "none"
	}
	return raw
}

func (m *backupModule) getCloudToken() string {
	raw, _ := m.dbGet("cloud_token")
	return raw
}

func (m *backupModule) getCloudAlsoTelegram() bool {
	if raw, ok := m.dbGet("cloud_also_telegram"); ok {
		return raw != "false" && raw != "0"
	}
	return true
}

func (m *backupModule) getPrefix() string {
	if m.k != nil {
		return m.k.Prefix()
	}
	return "."
}

func (m *backupModule) startAutoBackupLoop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelLoop != nil {
		m.cancelLoop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelLoop = cancel
	go func() {
		for {
			hours := m.getIntervalHours()
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(hours) * time.Hour):
				if m.isAutoEnabled() {
					m.doSendBackup(ctx, "") //nolint:errcheck
				}
			}
		}
	}()
}

// backupParseDelay parses "30m"/"2h"/"90s" -> seconds; -1 on failure.
// Matches Python _parse_delay().
func backupParseDelay(text string) int {
	re := regexp.MustCompile(`(\d+)(s|m|h)`)
	ms := re.FindStringSubmatch(strings.ToLower(strings.TrimSpace(text)))
	if ms == nil {
		return -1
	}
	var val int
	fmt.Sscanf(ms[1], "%d", &val)
	return val * map[string]int{"s": 1, "m": 60, "h": 3600}[ms[2]]
}

func backupSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func backupDeriveKey(password string, salt []byte) []byte {
	return pbkdf2.Key([]byte(password), salt, 390_000, 32, sha256.New)
}

func backupEncryptFile(src, dst, password string) error {
	plaintext, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := cryptorand.Read(salt); err != nil {
		return err
	}
	block, err := aes.NewCipher(backupDeriveKey(password, salt))
	if err != nil {
		return err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := cryptorand.Read(nonce); err != nil {
		return err
	}
	var buf bytes.Buffer
	buf.Write(salt)
	buf.Write(nonce)
	buf.Write(gcm.Seal(nil, nonce, plaintext, nil))
	return os.WriteFile(dst, buf.Bytes(), 0o600)
}

func backupDecryptFile(src, password string) ([]byte, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	if len(data) < 28 {
		return nil, fmt.Errorf("wrong password or corrupted archive")
	}
	block, err := aes.NewCipher(backupDeriveKey(password, data[:16]))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < 16+ns {
		return nil, fmt.Errorf("wrong password or corrupted archive")
	}
	pt, err := gcm.Open(nil, data[16:16+ns], data[16+ns:], nil)
	if err != nil {
		return nil, fmt.Errorf("wrong password or corrupted archive")
	}
	return pt, nil
}

// createBackupArchive matches Python create_backup_archive().
// Files are placed inside "MCUB_backup/" subdir inside archive.
func (m *backupModule) createBackupArchive(components, archiveFormat, encPassword string) (archivePath, timestamp, sha256hex string, err error) {
	var entries []backupEntry
	wantConfig := components == "" || components == "config"
	wantDB := components == "" || components == "db"
	wantMods := components == "" || components == "modules"

	if wantConfig {
		if _, e := os.Stat("config.json"); e == nil {
			entries = append(entries, backupEntry{"config.json", "config.json", false})
		}
	}
	if wantDB {
		if _, e := os.Stat("userbot.db"); e == nil {
			entries = append(entries, backupEntry{"userbot.db", "userbot.db", false})
		}
	}
	if wantMods {
		if info, e := os.Stat("modules_loaded"); e == nil && info.IsDir() {
			entries = append(entries, backupEntry{"modules_loaded", "modules_loaded", true})
		}
	}

	timestamp = time.Now().Format("20060102_150405")
	compSuffix := ""
	if components != "" {
		compSuffix = "_" + components
	}
	tmpDir, e := os.MkdirTemp("", "mcub_backup_")
	if e != nil {
		err = e
		return
	}
	archivePath = filepath.Join(tmpDir, fmt.Sprintf("MCUB_backup%s_%s.%s", compSuffix, timestamp, archiveFormat))
	if archiveFormat == "tar.gz" {
		err = backupCreateTarGz(archivePath, entries)
	} else {
		err = backupCreateZip(archivePath, entries)
	}
	if err != nil {
		os.RemoveAll(tmpDir)
		return
	}
	sha256hex, err = backupSHA256(archivePath)
	if err != nil {
		os.RemoveAll(tmpDir)
		return
	}
	if encPassword != "" {
		encPath := archivePath + ".enc"
		if e := backupEncryptFile(archivePath, encPath, encPassword); e != nil {
			os.RemoveAll(tmpDir)
			err = e
			return
		}
		os.Remove(archivePath)
		archivePath = encPath
	}
	return
}

func backupCreateZip(dst string, entries []backupEntry) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	w := zip.NewWriter(f)
	for _, e := range entries {
		prefix := "MCUB_backup/" + e.archName
		if e.isDir {
			if err := backupAddDirToZip(w, e.srcPath, prefix); err != nil {
				w.Close(); f.Close(); return err
			}
		} else {
			if err := backupAddFileToZip(w, e.srcPath, prefix); err != nil {
				w.Close(); f.Close(); return err
			}
		}
	}
	if err := w.Close(); err != nil {
		f.Close(); return err
	}
	return f.Close()
}

func backupAddFileToZip(w *zip.Writer, src, archName string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := w.Create(archName)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	return err
}

func backupAddDirToZip(w *zip.Writer, srcDir, archDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		return backupAddFileToZip(w, path, archDir+"/"+filepath.ToSlash(rel))
	})
}

func backupCreateTarGz(dst string, entries []backupEntry) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)
	for _, e := range entries {
		prefix := "MCUB_backup/" + e.archName
		if e.isDir {
			if err := backupAddDirToTar(tw, e.srcPath, prefix); err != nil {
				tw.Close(); gw.Close(); f.Close(); return err
			}
		} else {
			if err := backupAddFileToTar(tw, e.srcPath, prefix); err != nil {
				tw.Close(); gw.Close(); f.Close(); return err
			}
		}
	}
	tw.Close(); gw.Close(); return f.Close()
}

func backupAddFileToTar(tw *tar.Writer, src, archName string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name: archName, Mode: int64(fi.Mode()), Size: fi.Size(), ModTime: fi.ModTime(),
	}); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	_, err = io.Copy(tw, in)
	return err
}

func backupAddDirToTar(tw *tar.Writer, srcDir, archDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(srcDir, path)
		return backupAddFileToTar(tw, path, archDir+"/"+filepath.ToSlash(rel))
	})
}

// backupUploadToCloud matches Python upload_to_cloud()
func backupUploadToCloud(filePath, provider, token string) error {
	switch provider {
	case "yadisk":
		return backupUploadYadisk(filePath, token)
	case "gdrive":
		return backupUploadGdrive(filePath, token)
	}
	return fmt.Errorf("unknown cloud provider: %s", provider)
}

// backupUploadYadisk matches Python _upload_yadisk()
func backupUploadYadisk(filePath, token string) error {
	filename := filepath.Base(filePath)
	remotePath := "/MCUB_Backups/" + filename
	client := &http.Client{Timeout: 60 * time.Second}
	auth := "OAuth " + token
	newReq := func(method, url string) *http.Request {
		r, _ := http.NewRequest(method, url, nil)
		r.Header.Set("Authorization", auth)
		return r
	}
	client.Do(newReq(http.MethodPut, "https://cloud-api.yandex.net/v1/disk/resources?path=/MCUB_Backups")) //nolint:errcheck
	resp, err := client.Do(newReq(http.MethodGet,
		"https://cloud-api.yandex.net/v1/disk/resources/upload?path="+remotePath+"&overwrite=true"))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("yadisk get upload url: %d %s", resp.StatusCode, string(b))
	}
	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	uploadURL, _ := result["href"].(string)
	if uploadURL == "" {
		return fmt.Errorf("yadisk: no upload URL")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, _ := f.Stat()
	putReq, _ := http.NewRequest(http.MethodPut, uploadURL, f)
	if fi != nil {
		putReq.ContentLength = fi.Size()
	}
	putResp, err := client.Do(putReq)
	if err != nil {
		return err
	}
	defer putResp.Body.Close()
	if putResp.StatusCode != 200 && putResp.StatusCode != 201 {
		b, _ := io.ReadAll(putResp.Body)
		return fmt.Errorf("yadisk upload: %d %s", putResp.StatusCode, string(b))
	}
	return nil
}

// backupUploadGdrive matches Python _upload_gdrive()
func backupUploadGdrive(filePath, token string) error {
	filename := filepath.Base(filePath)
	client := &http.Client{Timeout: 120 * time.Second}
	auth := "Bearer " + token

	// Find or create MCUB_Backups folder
	var folderID string
	q := "name=%27MCUB_Backups%27+and+mimeType=%27application%2Fvnd.google-apps.folder%27+and+trashed%3Dfalse"
	lr, _ := http.NewRequest(http.MethodGet,
		"https://www.googleapis.com/drive/v3/files?q="+q+"&fields=files(id)", nil)
	lr.Header.Set("Authorization", auth)
	lresp, err := client.Do(lr)
	if err != nil {
		return err
	}
	defer lresp.Body.Close()
	var ldRaw map[string]interface{}
	json.NewDecoder(lresp.Body).Decode(&ldRaw) //nolint:errcheck
	if files, ok := ldRaw["files"].([]interface{}); ok && len(files) > 0 {
		if file, ok := files[0].(map[string]interface{}); ok {
			folderID, _ = file["id"].(string)
		}
	}
	if folderID == "" {
		createBody := `{"name":"MCUB_Backups","mimeType":"application/vnd.google-apps.folder"}`
		cr, _ := http.NewRequest(http.MethodPost,
			"https://www.googleapis.com/drive/v3/files", strings.NewReader(createBody))
		cr.Header.Set("Authorization", auth)
		cr.Header.Set("Content-Type", "application/json")
		cresp, err := client.Do(cr)
		if err != nil {
			return err
		}
		defer cresp.Body.Close()
		var cdRaw map[string]interface{}
		json.NewDecoder(cresp.Body).Decode(&cdRaw) //nolint:errcheck
		folderID, _ = cdRaw["id"].(string)
	}

	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	meta := fmt.Sprintf(`{"name":%q,"parents":[%q]}`, filename, folderID)
	var buf bytes.Buffer
	buf.WriteString("--boundary\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n")
	buf.WriteString(meta)
	buf.WriteString("\r\n--boundary\r\nContent-Type: application/octet-stream\r\n\r\n")
	buf.Write(fileData)
	buf.WriteString("\r\n--boundary--")

	ur, _ := http.NewRequest(http.MethodPost,
		"https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart", &buf)
	ur.Header.Set("Authorization", auth)
	ur.Header.Set("Content-Type", "multipart/related; boundary=boundary")
	uresp, err := client.Do(ur)
	if err != nil {
		return err
	}
	defer uresp.Body.Close()
	if uresp.StatusCode != 200 && uresp.StatusCode != 201 {
		b, _ := io.ReadAll(uresp.Body)
		return fmt.Errorf("gdrive upload: %d %s", uresp.StatusCode, string(b))
	}
	return nil
}

// backupMsgInfo holds minimal backup message info.
type backupMsgInfo struct {
	ID       int
	FileName string
	Date     time.Time
	SizeKB   int64
}

func backupGetFileName(msg *tg.Message) string {
	if msg == nil {
		return ""
	}
	doc, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return ""
	}
	d, ok := doc.GetDocument()
	if !ok {
		return ""
	}
	document, ok := d.(*tg.Document)
	if !ok {
		return ""
	}
	for _, attr := range document.Attributes {
		if fn, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return fn.FileName
		}
	}
	return ""
}

func backupGetFileSize(msg *tg.Message) int64 {
	if msg == nil {
		return 0
	}
	doc, ok := msg.Media.(*tg.MessageMediaDocument)
	if !ok {
		return 0
	}
	d, ok := doc.GetDocument()
	if !ok {
		return 0
	}
	document, ok := d.(*tg.Document)
	if !ok {
		return 0
	}
	return document.Size
}

// listBackupMessages returns backup messages from chat history, newest first.
// Matches Python list_backup_messages().
func (m *backupModule) listBackupMessages(ctx context.Context, chatID int64, limit int) ([]backupMsgInfo, error) {
	if m.k == nil || m.k.Client == nil {
		return nil, fmt.Errorf("client not available")
	}
	peer := peerIDToInputPeer(chatID)
	resp, err := m.k.Client.API().MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer: peer, Limit: 500,
	})
	if err != nil {
		return nil, err
	}
	var rawMsgs []*tg.Message
	switch r := resp.(type) {
	case *tg.MessagesMessages:
		for _, msg := range r.Messages {
			if tm, ok := msg.(*tg.Message); ok {
				rawMsgs = append(rawMsgs, tm)
			}
		}
	case *tg.MessagesMessagesSlice:
		for _, msg := range r.Messages {
			if tm, ok := msg.(*tg.Message); ok {
				rawMsgs = append(rawMsgs, tm)
			}
		}
	case *tg.MessagesChannelMessages:
		for _, msg := range r.Messages {
			if tm, ok := msg.(*tg.Message); ok {
				rawMsgs = append(rawMsgs, tm)
			}
		}
	}
	var result []backupMsgInfo
	for _, msg := range rawMsgs {
		fname := backupGetFileName(msg)
		if !strings.HasPrefix(fname, "MCUB_backup") {
			continue
		}
		if !strings.HasSuffix(fname, ".zip") && !strings.HasSuffix(fname, ".tar.gz") && !strings.HasSuffix(fname, ".enc") {
			continue
		}
		result = append(result, backupMsgInfo{
			ID: msg.ID, FileName: fname,
			Date:   time.Unix(int64(msg.Date), 0),
			SizeKB: backupGetFileSize(msg) / 1024,
		})
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

// rotateOldBackups matches Python rotate_old_backups().
func (m *backupModule) rotateOldBackups(ctx context.Context, chatID int64, maxCount int) int {
	if maxCount <= 0 || m.k == nil || m.k.Client == nil {
		return 0
	}
	msgs, err := m.listBackupMessages(ctx, chatID, 500)
	if err != nil {
		return 0
	}
	deleted := 0
	for len(msgs) > maxCount {
		old := msgs[len(msgs)-1]
		msgs = msgs[:len(msgs)-1]
		peer := peerIDToInputPeer(chatID)
		if chanPeer, ok := peer.(*tg.InputPeerChannel); ok {
			m.k.Client.API().ChannelsDeleteMessages(ctx, &tg.ChannelsDeleteMessagesRequest{ //nolint:errcheck
				Channel: &tg.InputChannel{ChannelID: chanPeer.ChannelID, AccessHash: chanPeer.AccessHash},
				ID:      []int{old.ID},
			})
		} else {
			m.k.Client.API().MessagesDeleteMessages(ctx, &tg.MessagesDeleteMessagesRequest{ //nolint:errcheck
				ID: []int{old.ID},
			})
		}
		deleted++
	}
	return deleted
}

// doSendBackup is used by auto-backup loop.
func (m *backupModule) doSendBackup(ctx context.Context, components string) error {
	archivePath, _, sha256hex, err := m.createBackupArchive(components, m.getArchiveFormat(), m.getEncryptionPassword())
	if err != nil {
		return err
	}
	defer func() { os.Remove(archivePath); os.RemoveAll(filepath.Dir(archivePath)) }()

	prefix := m.getPrefix()
	encPassword := m.getEncryptionPassword()
	tip := sf(m.k, "userbot_backup", "tip_restore", map[string]interface{}{"prefix": prefix})
	captionParts := []string{tip}
	if sha256hex != "" {
		captionParts = append(captionParts, fmt.Sprintf("<blockquote><b>SHA256: </b><code>%s</code></blockquote>", sha256hex))
	}
	if encPassword != "" {
		captionParts = append(captionParts, s(m.k, "userbot_backup", "encrypted_note"))
	}
	caption := strings.Join(captionParts, "\n")

	sendToTg := true
	cloudProvider := m.getCloudProvider()
	cloudToken := m.getCloudToken()
	if cloudProvider != "none" && cloudToken != "" {
		_ = backupUploadToCloud(archivePath, cloudProvider, cloudToken)
		sendToTg = m.getCloudAlsoTelegram()
	}
	if sendToTg {
		chatID, ok := m.getBackupChatID()
		if !ok {
			return fmt.Errorf("backup chat not configured")
		}
		if m.k != nil && m.k.Client != nil {
			if _, err := m.k.Client.SendDocument(ctx, chatID, archivePath, caption); err != nil {
				return err
			}
		}
	}
	m.dbSet("last_backup_time", time.Now().Format(time.RFC3339))
	if m.getMaxBackups() > 0 {
		if chatID, ok := m.getBackupChatID(); ok {
			m.rotateOldBackups(ctx, chatID, m.getMaxBackups())
		}
	}
	return nil
}

// restoreFromMessage matches Python _restore_from_backup_message().
func (m *backupModule) restoreFromMessage(ctx context.Context, chatID int64, replyMsgID, statusMsgID int, password string) error {
	if m.k == nil || m.k.Client == nil {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "not_backup_file"))
	}
	rawMsgs, err := m.k.Client.GetMessages(ctx, chatID, []int{replyMsgID})
	if err != nil || len(rawMsgs) == 0 || rawMsgs[0] == nil {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "not_backup_file"))
	}
	rawMsg := rawMsgs[0]
	fname := backupGetFileName(rawMsg)
	if fname == "" {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "not_backup_file"))
	}
	isEncrypted := strings.HasSuffix(fname, ".enc")
	if !strings.HasPrefix(fname, "MCUB_backup") ||
		(!strings.HasSuffix(fname, ".zip") && !strings.HasSuffix(fname, ".tar.gz") && !isEncrypted) {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "not_backup_file"))
	}
	if isEncrypted && password == "" {
		password = m.getEncryptionPassword()
		if password == "" {
			return editHTML(ctx, m.k, chatID, statusMsgID,
				sf(m.k, "userbot_backup", "encrypted_restore", map[string]interface{}{"prefix": m.getPrefix()}))
		}
	}
	var sha256fromCaption string
	if rawMsg.Message != "" {
		re := regexp.MustCompile("SHA256: ([a-f0-9]{64})")
		if match := re.FindStringSubmatch(rawMsg.Message); len(match) > 1 {
			sha256fromCaption = match[1]
		}
	}
	if err := editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restoring")); err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "mcub_restore_")
	if err != nil {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+err.Error())
	}
	defer os.RemoveAll(tmpDir)
	archivePath := filepath.Join(tmpDir, fname)
	_, dlErr := m.k.Client.DownloadMedia(ctx, mcubclient.DownloadMediaParams{
		ChatID: chatID, MessageID: replyMsgID, FilePath: archivePath,
	})
	if dlErr != nil {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+dlErr.Error())
	}
	if isEncrypted {
		plaintext, decErr := backupDecryptFile(archivePath, password)
		if decErr != nil {
			return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "wrong_password"))
		}
		realName := strings.TrimSuffix(fname, ".enc")
		decPath := filepath.Join(tmpDir, realName)
		if err := os.WriteFile(decPath, plaintext, 0o600); err != nil {
			return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+err.Error())
		}
		archivePath = decPath
		fname = realName
		if sha256fromCaption != "" {
			if actual, _ := backupSHA256(archivePath); actual != sha256fromCaption {
				return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "hash_mismatch"))
			}
		}
	}
	extractDir := filepath.Join(tmpDir, "extracted")
	os.MkdirAll(extractDir, 0o755) //nolint:errcheck
	if strings.HasSuffix(fname, ".tar.gz") {
		if err := backupExtractTarGz(archivePath, extractDir); err != nil {
			return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+err.Error())
		}
	} else {
		if err := backupExtractZip(archivePath, extractDir); err != nil {
			return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+err.Error())
		}
	}
	backupDir := filepath.Join(extractDir, "MCUB_backup")
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		backupDir = extractDir
	}
	cwd, _ := os.Getwd()
	var restored []string
	dirEntries, err := os.ReadDir(backupDir)
	if err != nil {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "restore_error")+" "+err.Error())
	}
	for _, entry := range dirEntries {
		srcPath := filepath.Join(backupDir, entry.Name())
		dstPath := filepath.Join(cwd, entry.Name())
		if _, err := os.Stat(dstPath); err == nil {
			bkName := entry.Name() + "_backup_" + time.Now().Format("20060102_150405")
			os.Rename(dstPath, filepath.Join(cwd, bkName)) //nolint:errcheck
			restored = append(restored, fmt.Sprintf("%s %s -> %s", backupEmojiPackage, entry.Name(), bkName))
		}
		if entry.IsDir() {
			backupCopyDir(srcPath, dstPath) //nolint:errcheck
		} else {
			backupCopyFile(srcPath, dstPath) //nolint:errcheck
		}
		restored = append(restored, backupEmojiOK+" "+entry.Name())
	}
	if len(restored) == 0 {
		return editHTML(ctx, m.k, chatID, statusMsgID, s(m.k, "userbot_backup", "no_files"))
	}
	restoredMsg := s(m.k, "userbot_backup", "restored") + "\n" + strings.Join(restored, "\n")
	if err := editHTML(ctx, m.k, chatID, statusMsgID, restoredMsg); err != nil {
		return err
	}
	return m.k.Restart()
}

func backupExtractZip(src, dst string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		path := filepath.Join(dst, filepath.Clean(f.Name))
		if f.FileInfo().IsDir() {
			os.MkdirAll(path, 0o755) //nolint:errcheck
			continue
		}
		os.MkdirAll(filepath.Dir(path), 0o755) //nolint:errcheck
		out, err := os.Create(path)
		if err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, rc)
		rc.Close()
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func backupExtractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		path := filepath.Join(dst, filepath.Clean(hdr.Name))
		if hdr.Typeflag == tar.TypeDir {
			os.MkdirAll(path, 0o755) //nolint:errcheck
			continue
		}
		os.MkdirAll(filepath.Dir(path), 0o755) //nolint:errcheck
		out, err := os.Create(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func backupCopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func backupCopyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return backupCopyFile(path, target)
	})
}

// cmdBackup matches Python cmd_backup() exactly.
func (m *backupModule) cmdBackup(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	body := strings.TrimPrefix(ev.Text(), m.getPrefix())
	parts := strings.Fields(body)
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	// cleanup
	if len(args) > 0 && args[0] == "cleanup" {
		chatID, ok := m.getBackupChatID()
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "no_backups_found"))
		}
		if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "processing")); err != nil {
			return err
		}
		deleted := m.rotateOldBackups(ctx, chatID, m.getMaxBackups())
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "userbot_backup", "cleanup_done", map[string]interface{}{"count": deleted}))
	}

	// cloud
	if len(args) > 0 && args[0] == "cloud" {
		provider := m.getCloudProvider()
		token := m.getCloudToken()
		if provider == "none" || token == "" {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, backupEmojiError+" Cloud provider not configured")
		}
		if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "creating_backup")); err != nil {
			return err
		}
		archivePath, _, _, archErr := m.createBackupArchive("", m.getArchiveFormat(), m.getEncryptionPassword())
		if archErr != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				sf(m.k, "userbot_backup", "cloud_fail", map[string]interface{}{"provider": provider})+
					"\n<code>"+archErr.Error()+"</code>")
		}
		defer func() { os.Remove(archivePath); os.RemoveAll(filepath.Dir(archivePath)) }()
		if err := backupUploadToCloud(archivePath, provider, token); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				sf(m.k, "userbot_backup", "cloud_fail", map[string]interface{}{"provider": provider})+
					"\n<code>"+err.Error()+"</code>")
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "userbot_backup", "cloud_ok", map[string]interface{}{"provider": provider}))
	}

	// in <time>
	if len(args) >= 2 && args[0] == "in" {
		delaySecs := backupParseDelay(args[1])
		if delaySecs < 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "invalid_time"))
		}
		targetTime := time.Now().Add(time.Duration(delaySecs) * time.Second).Format("15:04")
		if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "userbot_backup", "delayed_scheduled", map[string]interface{}{"time": targetTime})); err != nil {
			return err
		}
		var components string
		if len(args) >= 3 && (args[2] == "config" || args[2] == "db" || args[2] == "modules") {
			components = args[2]
		}
		taskCtx, cancel := context.WithCancel(ctx)
		m.mu.Lock()
		m.delayCancel = append(m.delayCancel, cancel)
		m.mu.Unlock()
		go func() {
			defer cancel()
			select {
			case <-taskCtx.Done():
			case <-time.After(time.Duration(delaySecs) * time.Second):
				m.doSendBackup(taskCtx, components) //nolint:errcheck
			}
		}()
		return nil
	}

	// [config|db|modules] or bare .backup
	var components string
	if len(args) > 0 {
		switch args[0] {
		case "config", "db", "modules":
			components = args[0]
		default:
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "unknown_arg"))
		}
	}

	if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "creating_backup")); err != nil {
		return err
	}
	archivePath, _, sha256hex, archErr := m.createBackupArchive(components, m.getArchiveFormat(), m.getEncryptionPassword())
	if archErr != nil {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			s(m.k, "userbot_backup", "backup_failed")+"\n<code>"+archErr.Error()+"</code>")
	}
	defer func() { os.Remove(archivePath); os.RemoveAll(filepath.Dir(archivePath)) }()

	chatID, hasChatID := m.getBackupChatID()
	if !hasChatID {
		chatID = ev.PeerID
	}
	prefix := m.getPrefix()
	encPassword := m.getEncryptionPassword()
	tip := sf(m.k, "userbot_backup", "tip_restore", map[string]interface{}{"prefix": prefix})
	captionParts := []string{tip}
	if sha256hex != "" {
		captionParts = append(captionParts, fmt.Sprintf("<blockquote><b>SHA256: </b><code>%s</code></blockquote>", sha256hex))
	}
	if encPassword != "" {
		captionParts = append(captionParts, s(m.k, "userbot_backup", "encrypted_note"))
	}
	caption := strings.Join(captionParts, "\n")

	if m.k != nil && m.k.Client != nil {
		if _, err := m.k.Client.SendDocument(ctx, chatID, archivePath, caption); err != nil {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
				s(m.k, "userbot_backup", "backup_failed")+"\n<code>"+err.Error()+"</code>")
		}
	}
	m.dbSet("last_backup_time", time.Now().Format(time.RFC3339))
	if m.getMaxBackups() > 0 && hasChatID {
		m.rotateOldBackups(ctx, chatID, m.getMaxBackups())
	}
	return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "backup_created"))
}

// cmdRestore matches Python cmd_restore() exactly.
func (m *backupModule) cmdRestore(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	body := strings.TrimPrefix(ev.Text(), m.getPrefix())
	parts := strings.Fields(body)
	var args []string
	if len(parts) > 1 {
		args = parts[1:]
	}

	if len(args) > 0 && args[0] == "list" {
		chatID, ok := m.getBackupChatID()
		if !ok {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "no_backups_found"))
		}
		if err := editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "processing")); err != nil {
			return err
		}
		msgs, listErr := m.listBackupMessages(ctx, chatID, 10)
		if listErr != nil || len(msgs) == 0 {
			return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "no_backups_found"))
		}
		lines := []string{s(m.k, "userbot_backup", "select_backup")}
		for _, msg := range msgs {
			lines = append(lines, fmt.Sprintf("%s %s (%dKB) - ID:%d",
				backupEmojiPackage, msg.Date.Format("2006-01-02 15:04"), msg.SizeKB, msg.ID))
		}
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, strings.Join(lines, "\n"))
	}

	if !ev.IsReply || ev.ReplyToMsgID == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "reply_to_backup"))
	}
	return m.restoreFromMessage(ctx, ev.PeerID, ev.ReplyToMsgID, ev.Raw.ID, "")
}

// cmdRestoreWith matches Python cmd_restore_with() exactly.
func (m *backupModule) cmdRestoreWith(ctx context.Context, ev *events.NewMessage) error {
	if m.k == nil || ev.Raw == nil {
		return nil
	}
	body := strings.TrimPrefix(ev.Text(), m.getPrefix())
	idx := strings.IndexByte(body, ' ')
	var password string
	if idx >= 0 {
		password = strings.TrimSpace(body[idx+1:])
	}
	if password == "" {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID,
			sf(m.k, "userbot_backup", "restore_with_usage", map[string]interface{}{"prefix": m.getPrefix()}))
	}
	if !ev.IsReply || ev.ReplyToMsgID == 0 {
		return editHTML(ctx, m.k, ev.PeerID, ev.Raw.ID, s(m.k, "userbot_backup", "reply_to_backup"))
	}
	return m.restoreFromMessage(ctx, ev.PeerID, ev.ReplyToMsgID, ev.Raw.ID, password)
}
