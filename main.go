package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/crypto"
	gsession "github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	mtproto "github.com/gotd/td/tg"
	_ "modernc.org/sqlite"
)

type config struct {
	input     string
	outputDir string
	appID     int
	appHash   string
	mode      string
	workers   int
	timeout   time.Duration
}

type sessionCandidate struct {
	Name       string
	SourcePath string
}

type accountReport struct {
	FileName    string    `json:"file_name"`
	Phone       string    `json:"phone,omitempty"`
	UserID      int64     `json:"user_id,omitempty"`
	Username    string    `json:"username,omitempty"`
	DisplayName string    `json:"display_name,omitempty"`
	StatusCode  string    `json:"status_code"`
	Alive       bool      `json:"alive"`
	CanSendDM   bool      `json:"can_send_dm"`
	Summary     string    `json:"summary"`
	RawReply    string    `json:"raw_reply,omitempty"`
	Error       string    `json:"error,omitempty"`
	CheckedAt   time.Time `json:"checked_at"`
	SourcePath  string    `json:"source_path"`
}

type probeSelf struct {
	ID        int64
	Phone     string
	Username  string
	FirstName string
	LastName  string
}

type importedSessionFile struct {
	Name string
	Data []byte
}

type importOnlyAuth struct{}

type progressTracker struct {
	mu      sync.Mutex
	total   int
	done    int
	alive   int
	limited int
	banned  int
	frozen  int
	failed  int
	unknown int
	started time.Time
}

const (
	ansiReset  = "\x1b[0m"
	ansiGray   = "\x1b[90m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBlue   = "\x1b[34m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
)

func main() {
	log.SetFlags(0)

	cfg, err := parseConfig()
	if err != nil {
		log.Fatalf("参数错误: %v", err)
	}

	if err := os.MkdirAll(cfg.outputDir, 0o755); err != nil {
		log.Fatalf("创建输出目录失败: %v", err)
	}

	candidates, cleanup, err := collectCandidates(cfg.input)
	if err != nil {
		log.Fatalf("收集 session 文件失败: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if len(candidates) == 0 {
		log.Fatalf("没有找到可检查的 .session 文件")
	}

	log.Printf("开始检查，共 %d 个 session，workers=%d", len(candidates), cfg.workers)
	reports := runChecks(cfg, candidates)

	if err := writeOutputs(cfg.outputDir, reports); err != nil {
		log.Fatalf("写出结果失败: %v", err)
	}

	counts := summarize(reports)
	log.Printf("检查完成: 正常=%d 异常=%d 失败=%d 未知=%d", counts["active"], counts["abnormal"], counts["failed"], counts["unknown"])
	log.Printf("结果目录: %s", cfg.outputDir)
}

func parseConfig() (config, error) {
	var cfg config
	flag.StringVar(&cfg.input, "input", "", "输入目录、.session 文件或 .zip 包")
	flag.StringVar(&cfg.outputDir, "out", "output", "输出目录")
	flag.StringVar(&cfg.mode, "mode", "alive", "检查模式: alive | spam | both")
	flag.IntVar(&cfg.workers, "workers", 100, "并发检查数")
	flag.DurationVar(&cfg.timeout, "timeout", 45*time.Second, "单账号检查超时")
	flag.Parse()

	cfg.appID = readEnvInt("TG_APP_ID")
	cfg.appHash = strings.TrimSpace(os.Getenv("TG_APP_HASH"))

	if strings.TrimSpace(cfg.input) == "" {
		return cfg, errors.New("必须提供 -input")
	}
	if cfg.appID <= 0 {
		return cfg, errors.New("缺少 TG_APP_ID")
	}
	if cfg.appHash == "" {
		return cfg, errors.New("缺少 TG_APP_HASH")
	}
	if cfg.workers <= 0 {
		cfg.workers = 100
	}
	cfg.mode = strings.ToLower(strings.TrimSpace(cfg.mode))
	switch cfg.mode {
	case "alive", "spam", "both":
	default:
		return cfg, errors.New("-mode 仅支持 alive / spam / both")
	}
	return cfg, nil
}

func runChecks(cfg config, candidates []sessionCandidate) []accountReport {
	reports := make([]accountReport, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	tracker := newProgressTracker(len(candidates))
	stopHeartbeat := tracker.startHeartbeat(3 * time.Second)
	defer stopHeartbeat()

	workerCount := cfg.workers
	if workerCount > len(candidates) {
		workerCount = len(candidates)
	}

	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range jobs {
				ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
				report := checkSingleSession(ctx, cfg, candidates[idx])
				cancel()
				reports[idx] = report
				tracker.record(report)
			}
		}()
	}

	for i := range candidates {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	sort.Slice(reports, func(i, j int) bool {
		return reports[i].FileName < reports[j].FileName
	})
	return reports
}

func newProgressTracker(total int) *progressTracker {
	return &progressTracker{
		total:   total,
		started: time.Now(),
	}
}

func (p *progressTracker) startHeartbeat(interval time.Duration) func() {
	p.printHeartbeat(true)

	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.printHeartbeat(false)
			case <-stop:
				return
			}
		}
	}()

	return func() {
		close(stop)
	}
}

func (p *progressTracker) record(report accountReport) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.done++

	switch statusBucket(report.StatusCode) {
	case "alive":
		p.alive++
	case "limited":
		p.limited++
	case "banned":
		p.banned++
	case "frozen":
		p.frozen++
	case "failed":
		p.failed++
	default:
		p.unknown++
	}

	statusText, statusColor := statusDisplay(report)
	phone := displayPhone(report)
	pending := p.total - p.done

	fmt.Printf(
		"%s【%s】%s %s  %s%s%s  当前存活%d  待检查%d\n",
		ansiGray, formatLineTime(time.Now()), ansiReset,
		phone,
		statusColor, statusText, ansiReset,
		p.alive,
		pending,
	)
	if p.done == p.total {
		fmt.Printf("%s耗时%s %s\n", ansiGray, ansiReset, time.Since(p.started).Round(time.Second))
	}
}

func (p *progressTracker) printHeartbeat(initial bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.done >= p.total {
		return
	}

	label := "进度"
	if initial || p.done == 0 {
		label = "预热中"
	}
	pending := p.total - p.done

	fmt.Printf(
		"%s【%s】%s %s%s%s  当前存活%d  待检查%d\n",
		ansiGray, formatLineTime(time.Now()), ansiReset,
		ansiYellow, label, ansiReset,
		p.alive,
		pending,
	)
}

func formatLineTime(t time.Time) string {
	return t.Format("2006-1-2 -15:04")
}

func displayPhone(report accountReport) string {
	if phone := strings.TrimSpace(report.Phone); phone != "" {
		return phone
	}
	if username := strings.TrimSpace(report.Username); username != "" {
		return "@" + username
	}
	if name := strings.TrimSpace(report.FileName); name != "" {
		base := strings.TrimSuffix(name, filepath.Ext(name))
		if base != "" {
			return base
		}
		return name
	}
	return "unknown"
}

func (p *progressTracker) renderCounts() string {
	return fmt.Sprintf(
		"%s[%s %d]%s %s[%s %d]%s %s[%s %d]%s %s[%s %d]%s %s[%s %d]%s %s[%s %d]%s",
		ansiGreen, "活", p.alive, ansiReset,
		ansiYellow, "限", p.limited, ansiReset,
		ansiRed, "封", p.banned, ansiReset,
		ansiBlue, "冻", p.frozen, ansiReset,
		ansiRed, "失", p.failed, ansiReset,
		ansiCyan, "未", p.unknown, ansiReset,
	)
}

func displayIdentity(report accountReport) string {
	parts := make([]string, 0, 3)
	if name := strings.TrimSpace(report.FileName); name != "" {
		parts = append(parts, name)
	}
	if phone := strings.TrimSpace(report.Phone); phone != "" {
		parts = append(parts, phone)
	}
	if username := strings.TrimSpace(report.Username); username != "" {
		parts = append(parts, "@"+username)
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " | ")
}

func statusBucket(code string) string {
	switch code {
	case "alive", "active":
		return "alive"
	case "restricted", "spam":
		return "limited"
	case "banned":
		return "banned"
	case "frozen":
		return "frozen"
	case "failed", "unauthorized":
		return "failed"
	default:
		return "unknown"
	}
}

func statusDisplay(report accountReport) (string, string) {
	switch statusBucket(report.StatusCode) {
	case "alive":
		return "存活", ansiGreen
	case "limited":
		return "受限", ansiYellow
	case "banned":
		return "封禁", ansiRed
	case "frozen":
		return "冻结", ansiBlue
	case "failed":
		return failureDisplay(report), ansiRed
	default:
		return "未知", ansiCyan
	}
}

func failureDisplay(report accountReport) string {
	if report.StatusCode == "unauthorized" {
		return "失效"
	}

	text := strings.ToLower(strings.TrimSpace(report.Error + " " + report.Summary))
	switch {
	case strings.Contains(text, "timeout"), strings.Contains(text, "deadline"):
		return "超时"
	case strings.Contains(text, "flood"), strings.Contains(text, "limit"):
		return "限流"
	case strings.Contains(text, "connect"), strings.Contains(text, "network"), strings.Contains(text, "eof"):
		return "连接失败"
	default:
		return "失败"
	}
}

func formatSpeed(done int, elapsed time.Duration) string {
	if done <= 0 || elapsed <= 0 {
		return "0 个/分"
	}
	perMinute := float64(done) / elapsed.Minutes()
	return fmt.Sprintf("%.1f 个/分", perMinute)
}

func formatETA(remaining, done int, elapsed time.Duration) string {
	if remaining <= 0 {
		return "0s"
	}
	if done <= 0 || elapsed <= 0 {
		return "--"
	}

	etaSeconds := elapsed.Seconds() * float64(remaining) / float64(done)
	if etaSeconds < 1 {
		etaSeconds = 1
	}
	return (time.Duration(etaSeconds) * time.Second).Round(time.Second).String()
}

func checkSingleSession(ctx context.Context, cfg config, candidate sessionCandidate) accountReport {
	report := accountReport{
		FileName:   candidate.Name,
		SourcePath: candidate.SourcePath,
		CheckedAt:  time.Now(),
		StatusCode: "failed",
		Summary:    "检查失败",
	}

	tmpDir, err := os.MkdirTemp("", "tg-session-check-*")
	if err != nil {
		report.Error = fmt.Sprintf("创建临时目录失败: %v", err)
		report.Summary = report.Error
		return report
	}
	defer os.RemoveAll(tmpDir)

	sourceCopy := filepath.Join(tmpDir, candidate.Name)
	if err := copyFile(candidate.SourcePath, sourceCopy); err != nil {
		report.Error = fmt.Sprintf("复制 session 失败: %v", err)
		report.Summary = report.Error
		return report
	}

	gotdSession := filepath.Join(tmpDir, "session.json")
	if err := convertTelethonSQLiteSessionFile(ctx, sourceCopy, gotdSession); err != nil {
		report.StatusCode = "unauthorized"
		report.Error = fmt.Sprintf("session 转换失败: %v", err)
		report.Summary = "session 未授权、已损坏，或不是有效的 Telethon sqlite session"
		return report
	}

	self, rawReply, code, summary, alive, canSend, err := runSessionCheck(ctx, cfg.mode, cfg.appID, cfg.appHash, gotdSession)
	if self != nil {
		report.Phone = self.Phone
		report.UserID = self.ID
		report.Username = self.Username
		report.DisplayName = strings.TrimSpace(strings.Join([]string{self.FirstName, self.LastName}, " "))
	}
	report.Alive = alive
	report.RawReply = rawReply
	report.StatusCode = code
	report.CanSendDM = canSend
	report.Summary = summary
	if err != nil {
		report.Error = classifyError(err)
		if report.StatusCode == "" {
			report.StatusCode = "failed"
		}
		if strings.TrimSpace(report.Summary) == "" {
			report.Summary = report.Error
		}
	}
	if report.StatusCode == "" {
		report.StatusCode = "unknown"
	}
	if strings.TrimSpace(report.Summary) == "" {
		report.Summary = "未识别状态"
	}
	return report
}

func writeOutputs(outputDir string, reports []accountReport) error {
	reportPath := filepath.Join(outputDir, "report.csv")
	if err := writeCSV(reportPath, reports); err != nil {
		return err
	}

	jsonPath := filepath.Join(outputDir, "report.json")
	jsonData, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return err
	}

	activePath := filepath.Join(outputDir, "normal_sessions.zip")
	if err := writeZipByFilter(activePath, reports, func(r accountReport) bool { return isPassed(r) }); err != nil {
		return err
	}

	abnormalPath := filepath.Join(outputDir, "abnormal_sessions.zip")
	if err := writeZipByFilter(abnormalPath, reports, func(r accountReport) bool { return !isPassed(r) }); err != nil {
		return err
	}
	return nil
}

func writeCSV(path string, reports []accountReport) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{"file_name", "phone", "user_id", "username", "display_name", "status_code", "alive", "can_send_dm", "summary", "raw_reply", "error", "checked_at", "source_path"}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, report := range reports {
		row := []string{
			report.FileName,
			report.Phone,
			strconv.FormatInt(report.UserID, 10),
			report.Username,
			report.DisplayName,
			report.StatusCode,
			strconv.FormatBool(report.Alive),
			strconv.FormatBool(report.CanSendDM),
			report.Summary,
			report.RawReply,
			report.Error,
			report.CheckedAt.Format(time.RFC3339),
			report.SourcePath,
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	return writer.Error()
}

func writeZipByFilter(path string, reports []accountReport, keep func(accountReport) bool) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	defer writer.Close()

	usedNames := map[string]int{}
	for _, report := range reports {
		if !keep(report) || strings.TrimSpace(report.SourcePath) == "" {
			continue
		}
		data, err := os.ReadFile(report.SourcePath)
		if err != nil {
			continue
		}
		name := uniqueZipName(usedNames, report.FileName)
		entry, err := writer.Create(name)
		if err != nil {
			return err
		}
		if _, err := entry.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func uniqueZipName(used map[string]int, name string) string {
	base := strings.TrimSpace(filepath.Base(name))
	if base == "" {
		base = "unknown.session"
	}
	if used[base] == 0 {
		used[base] = 1
		return base
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	used[base]++
	return fmt.Sprintf("%s_%d%s", stem, used[base], ext)
}

func summarize(reports []accountReport) map[string]int {
	counts := map[string]int{
		"active":   0,
		"abnormal": 0,
		"failed":   0,
		"unknown":  0,
	}
	for _, report := range reports {
		if isPassed(report) {
			counts["active"]++
			continue
		}
		switch report.StatusCode {
		case "failed", "unauthorized":
			counts["failed"]++
		case "unknown":
			counts["unknown"]++
		}
		counts["abnormal"]++
	}
	return counts
}

func collectCandidates(input string) ([]sessionCandidate, func(), error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, nil, err
	}

	if info.IsDir() {
		files := make([]sessionCandidate, 0)
		err := filepath.WalkDir(input, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			lower := strings.ToLower(d.Name())
			if strings.HasSuffix(lower, ".session") && !strings.HasSuffix(lower, ".session-journal") {
				files = append(files, sessionCandidate{Name: d.Name(), SourcePath: path})
			}
			return nil
		})
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		return files, nil, err
	}

	lower := strings.ToLower(info.Name())
	switch {
	case strings.HasSuffix(lower, ".session") && !strings.HasSuffix(lower, ".session-journal"):
		return []sessionCandidate{{Name: filepath.Base(input), SourcePath: input}}, nil, nil
	case strings.HasSuffix(lower, ".zip"):
		data, err := os.ReadFile(input)
		if err != nil {
			return nil, nil, err
		}
		files, err := extractTelethonSessionFiles(filepath.Base(input), data)
		if err != nil {
			return nil, nil, err
		}

		tmpDir, err := os.MkdirTemp("", "tg-session-check-input-*")
		if err != nil {
			return nil, nil, err
		}
		candidates := make([]sessionCandidate, 0, len(files))
		for _, file := range files {
			target := filepath.Join(tmpDir, file.Name)
			if err := os.WriteFile(target, file.Data, 0o600); err != nil {
				_ = os.RemoveAll(tmpDir)
				return nil, nil, err
			}
			candidates = append(candidates, sessionCandidate{Name: file.Name, SourcePath: target})
		}
		return candidates, func() { _ = os.RemoveAll(tmpDir) }, nil
	default:
		return nil, nil, fmt.Errorf("仅支持目录、.session 或 .zip 输入")
	}
}

func runSessionCheck(ctx context.Context, mode string, appID int, appHash, sessionFile string) (*probeSelf, string, string, string, bool, bool, error) {
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		return nil, "", "failed", "创建 session 目录失败", false, false, err
	}

	sessionStorage := &telegram.FileSessionStorage{Path: sessionFile}
	client := telegram.NewClient(appID, appHash, telegram.Options{
		SessionStorage: sessionStorage,
	})
	flow := auth.NewFlow(importOnlyAuth{}, auth.SendCodeOptions{})

	var (
		self    *probeSelf
		rawText string
		code    string
		summary string
		alive   bool
		canSend bool
	)
	err := client.Run(ctx, func(ctx context.Context) error {
		if err := client.Auth().IfNecessary(ctx, flow); err != nil {
			return err
		}

		api := client.API()
		me, err := client.Self(ctx)
		if err != nil {
			return fmt.Errorf("读取账号信息失败: %w", err)
		}
		self = &probeSelf{
			ID:        me.ID,
			Phone:     me.Phone,
			Username:  me.Username,
			FirstName: me.FirstName,
			LastName:  me.LastName,
		}
		alive = true

		if mode == "alive" {
			code = "alive"
			summary = "session 有效，账号可以正常登录"
			return nil
		}

		peer, err := resolveUsernamePeer(ctx, api, "SpamBot")
		if err != nil {
			return fmt.Errorf("无法定位 @SpamBot: %w", err)
		}
		if err := sendText(ctx, api, peer, "/start"); err != nil {
			return fmt.Errorf("无法向 @SpamBot 发起检测: %w", err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}

		history, err := api.MessagesGetHistory(ctx, &mtproto.MessagesGetHistoryRequest{
			Peer:  peer,
			Limit: 5,
		})
		if err != nil {
			return fmt.Errorf("读取 @SpamBot 回复失败: %w", err)
		}

		rawText = latestIncomingText(history)
		if strings.TrimSpace(rawText) == "" {
			code, summary, canSend = "unknown", "未拿到 @SpamBot 的有效回复，请稍后再试", false
			return nil
		}
		code, summary, canSend = interpretSpamBotStatus(rawText)
		return nil
	})
	if err != nil {
		return self, rawText, classifyStatusCode(err), classifyError(err), alive, false, err
	}
	return self, rawText, code, summary, alive, canSend, nil
}

func isPassed(report accountReport) bool {
	switch report.StatusCode {
	case "alive", "active":
		return true
	default:
		return false
	}
}

func resolveUsernamePeer(ctx context.Context, api *mtproto.Client, username string) (mtproto.InputPeerClass, error) {
	username = strings.TrimSpace(strings.TrimPrefix(username, "@"))
	if username == "" {
		return nil, errors.New("username is empty")
	}

	result, err := api.ContactsResolveUsername(ctx, &mtproto.ContactsResolveUsernameRequest{
		Username: username,
	})
	if err != nil {
		return nil, err
	}

	for _, user := range result.MapUsers() {
		u, ok := user.(*mtproto.User)
		if !ok || u == nil {
			continue
		}
		accessHash, ok := u.GetAccessHash()
		if !ok {
			continue
		}
		return &mtproto.InputPeerUser{
			UserID:     u.ID,
			AccessHash: accessHash,
		}, nil
	}
	return nil, fmt.Errorf("resolved username %s but peer is unavailable", username)
}

func sendText(ctx context.Context, api *mtproto.Client, peer mtproto.InputPeerClass, text string) error {
	randomID := time.Now().UnixNano()
	_, err := api.MessagesSendMessage(ctx, &mtproto.MessagesSendMessageRequest{
		Peer:     peer,
		Message:  text,
		RandomID: randomID,
	})
	return err
}

func latestIncomingText(history mtproto.MessagesMessagesClass) string {
	withMessages, ok := any(history).(interface {
		GetMessages() []mtproto.MessageClass
	})
	if !ok {
		return ""
	}
	for _, item := range withMessages.GetMessages() {
		message, ok := item.(*mtproto.Message)
		if !ok || message == nil || message.Out {
			continue
		}
		if text := strings.TrimSpace(message.Message); text != "" {
			return text
		}
	}
	return ""
}

func interpretSpamBotStatus(raw string) (string, string, bool) {
	text := normalizeSpamBotText(raw)

	switch {
	case containsAny(text, "some phone numbers may trigger a harsh response", "phone numbers may trigger"):
		return "active", "账号目前可以正常私信，但这个号段更容易触发风控，建议控制发送节奏", true
	case containsAny(text, "good news, no limits are currently applied", "you're free as a bird", "no limits", "free as a bird", "no restrictions", "all good", "account is free", "not limited"):
		return "active", "账号状态正常，目前没有私信限制", true
	case containsAny(text, "mutual contacts", "only people in your contacts", "only send messages to mutual contacts", "双向", "互相添加"):
		return "restricted", "账号目前只能给双向联系人发消息，不能正常私信陌生人", false
	case containsAny(text, "account is now limited until", "limited until", "moderators have confirmed the report", "users found your messages annoying", "will be automatically released", "temporarily limited"):
		return "restricted", "账号被临时限制，暂时不能正常私信", false
	case containsAny(text, "actions can trigger a harsh response from our anti-spam systems", "account was limited", "you will not be able to send messages"):
		return "spam", "账号触发了垃圾消息风控，当前不适合继续私信", false
	case containsAny(text, "permanently banned", "account has been frozen permanently", "permanently restricted", "banned permanently", "blocked for violations", "terms of service", "banned", "suspended"):
		return "banned", "账号已被永久限制或封禁，不能再用于私信", false
	case containsAny(text, "wait", "pending", "verification"):
		return "frozen", "账号处于等待验证或审核状态，暂时不能稳定私信", false
	default:
		return "unknown", "未能明确识别账号状态，请人工查看 @SpamBot 最新回复", false
	}
}

func normalizeSpamBotText(text string) string {
	replacer := strings.NewReplacer(
		"正常", "all good",
		"没有限制", "no limits",
		"无限制", "no limits",
		"永久封禁", "permanently banned",
		"限制", "limited",
		"暂时", "temporarily",
		"验证", "verification",
	)
	return strings.ToLower(replacer.Replace(strings.TrimSpace(text)))
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func classifyStatusCode(err error) string {
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(lower, "session requires re-authentication"),
		strings.Contains(lower, "未授权"),
		strings.Contains(lower, "auth"),
		strings.Contains(lower, "unauthorized"):
		return "unauthorized"
	default:
		return "failed"
	}
}

func classifyError(err error) string {
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(lower, "session requires re-authentication"):
		return "session 已失效，需要重新登录"
	case strings.Contains(lower, "auth key"):
		return "session 鉴权失败，当前账号已经失效"
	case strings.Contains(lower, "flood_wait"):
		return "请求太频繁，被 Telegram 限流了"
	case strings.Contains(lower, "timeout"), strings.Contains(lower, "deadline exceeded"):
		return "检查超时了，这个号这次没拿到结果"
	default:
		return err.Error()
	}
}

func convertTelethonSQLiteSessionFile(ctx context.Context, sourcePath, targetPath string) error {
	dsn := fmt.Sprintf("file:%s?mode=ro", filepath.ToSlash(sourcePath))
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("open sqlite session: %w", err)
	}
	defer db.Close()

	var (
		dcID          int
		serverAddress string
		port          int
		authKey       []byte
	)

	row := db.QueryRowContext(ctx, `
		SELECT dc_id, server_address, port, auth_key
		FROM sessions
		WHERE auth_key IS NOT NULL AND length(auth_key) > 0
		ORDER BY dc_id
		LIMIT 1
	`)
	if err := row.Scan(&dcID, &serverAddress, &port, &authKey); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("session 未授权或缺少 auth_key")
		}
		return fmt.Errorf("query telethon session: %w", err)
	}

	serverAddress = strings.TrimSpace(serverAddress)
	if dcID <= 0 || serverAddress == "" || port <= 0 || len(authKey) == 0 {
		return fmt.Errorf("session 数据无效")
	}

	var key crypto.Key
	copy(key[:], authKey)
	keyID := key.WithID().ID

	data := &gsession.Data{
		DC:        dcID,
		Addr:      net.JoinHostPort(serverAddress, strconv.Itoa(port)),
		AuthKey:   append([]byte(nil), authKey...),
		AuthKeyID: keyID[:],
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create session dir: %w", err)
	}

	loader := gsession.Loader{
		Storage: &gsession.FileStorage{Path: targetPath},
	}
	return loader.Save(ctx, data)
}

func extractTelethonSessionFiles(filename string, data []byte) ([]importedSessionFile, error) {
	name := strings.ToLower(strings.TrimSpace(filename))
	switch {
	case strings.HasSuffix(name, ".session"):
		base := filepath.Base(filename)
		if strings.TrimSpace(base) == "" {
			base = "imported.session"
		}
		return []importedSessionFile{{
			Name: base,
			Data: append([]byte(nil), data...),
		}}, nil
	case strings.HasSuffix(name, ".zip"):
		reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("open zip: %w", err)
		}
		files := make([]importedSessionFile, 0)
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			base := filepath.Base(file.Name)
			lowerBase := strings.ToLower(base)
			if base == "" || strings.HasSuffix(lowerBase, ".session-journal") || !strings.HasSuffix(lowerBase, ".session") {
				continue
			}
			rc, err := file.Open()
			if err != nil {
				return nil, fmt.Errorf("open zip item %s: %w", file.Name, err)
			}
			buf := new(bytes.Buffer)
			if _, err := io.Copy(buf, rc); err != nil {
				_ = rc.Close()
				return nil, fmt.Errorf("read zip item %s: %w", file.Name, err)
			}
			_ = rc.Close()
			files = append(files, importedSessionFile{
				Name: base,
				Data: buf.Bytes(),
			})
		}
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		if len(files) == 0 {
			return nil, fmt.Errorf("zip 中没有 .session 文件")
		}
		return files, nil
	default:
		return nil, fmt.Errorf("仅支持 .session 或 .zip 文件")
	}
}

func copyFile(src, dst string) error {
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

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

func readEnvInt(key string) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return 0
	}
	number, _ := strconv.Atoi(value)
	return number
}

func (importOnlyAuth) Phone(context.Context) (string, error) {
	return "", errors.New("session requires re-authentication")
}

func (importOnlyAuth) Password(context.Context) (string, error) {
	return "", errors.New("session requires re-authentication")
}

func (importOnlyAuth) AcceptTermsOfService(context.Context, mtproto.HelpTermsOfService) error {
	return errors.New("sign up flow is not supported")
}

func (importOnlyAuth) SignUp(context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, errors.New("sign up flow is not supported")
}

func (importOnlyAuth) Code(context.Context, *mtproto.AuthSentCode) (string, error) {
	return "", errors.New("session requires re-authentication")
}
