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
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gotd/td/crypto"
	gsession "github.com/gotd/td/session"
	tdesktop "github.com/gotd/td/session/tdesktop"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/dcs"
	mtproto "github.com/gotd/td/tg"
	"golang.org/x/net/proxy"
	_ "modernc.org/sqlite"
)

type config struct {
	input     string
	inputList string
	outputDir string
	appID     int
	appHash   string
	mode      string
	workers   int
	timeout   time.Duration
	proxyFile string
	proxyPool *proxyPool
}

type sessionCandidate struct {
	Name       string
	SourcePath string
	SourceKind string
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
	Route       string    `json:"route,omitempty"`
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

type proxyPool struct {
	mu      sync.Mutex
	proxies []*proxyEntry
	next    int
}

type proxyEntry struct {
	raw           string
	dialer        proxy.ContextDialer
	timeoutStreak int
	disabled      bool
}

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

	candidates, cleanup, err := collectInputCandidates(cfg)
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

	printFinalSummary(reports, cfg.outputDir)
}

func parseConfig() (config, error) {
	var cfg config
	flag.StringVar(&cfg.inputList, "input-list", "", "input list file, one path per line")
	flag.StringVar(&cfg.input, "input", "", "输入目录、.session 文件或 .zip 包")
	flag.StringVar(&cfg.outputDir, "out", "output", "输出目录")
	flag.StringVar(&cfg.mode, "mode", "alive", "检查模式: alive | spam | both")
	flag.IntVar(&cfg.workers, "workers", 100, "并发检查数")
	flag.DurationVar(&cfg.timeout, "timeout", 45*time.Second, "单账号检查超时")
	flag.StringVar(&cfg.proxyFile, "proxy-file", "proxy.txt", "代理列表文件，一行一个 socks5 代理")
	flag.Parse()

	cfg.appID = readEnvInt("TG_APP_ID")
	cfg.appHash = strings.TrimSpace(os.Getenv("TG_APP_HASH"))
	if strings.TrimSpace(cfg.input) == "" && strings.TrimSpace(cfg.inputList) == "" {
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

	pool, err := loadProxyPool(cfg.proxyFile)
	if err != nil {
		return cfg, err
	}
	cfg.proxyPool = pool
	return cfg, nil
}

func loadProxyPool(path string) (*proxyPool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}

	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取代理文件失败: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("代理文件路径是目录: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取代理文件失败: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	entries := make([]*proxyEntry, 0, len(lines))
	for idx, rawLine := range lines {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		dialer, normalized, err := parseProxyDialer(line)
		if err != nil {
			return nil, fmt.Errorf("proxy.txt 第 %d 行无效: %w", idx+1, err)
		}
		entries = append(entries, &proxyEntry{
			raw:    normalized,
			dialer: dialer,
		})
	}

	if len(entries) == 0 {
		return nil, nil
	}
	return &proxyPool{proxies: entries}, nil
}

func parseProxyDialer(raw string) (proxy.ContextDialer, string, error) {
	normalized := strings.TrimSpace(raw)
	if !strings.Contains(normalized, "://") {
		normalized = "socks5://" + normalized
	}

	u, err := url.Parse(normalized)
	if err != nil {
		return nil, "", err
	}
	switch strings.ToLower(u.Scheme) {
	case "socks5", "socks5h":
	default:
		return nil, "", fmt.Errorf("仅支持 socks5 代理链接: %s", raw)
	}

	if strings.TrimSpace(u.Host) == "" {
		return nil, "", fmt.Errorf("代理地址为空: %s", raw)
	}

	var auth *proxy.Auth
	if u.User != nil {
		password, _ := u.User.Password()
		auth = &proxy.Auth{
			User:     u.User.Username(),
			Password: password,
		}
	}

	dialer, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
	if err != nil {
		return nil, "", err
	}
	contextDialer, ok := dialer.(proxy.ContextDialer)
	if !ok {
		return nil, "", fmt.Errorf("代理不支持上下文拨号: %s", raw)
	}
	return contextDialer, normalized, nil
}

func (p *proxyPool) nextDialer() (func(context.Context, string, string) (net.Conn, error), *proxyEntry) {
	if p == nil {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return nil, nil
	}

	for i := 0; i < len(p.proxies); i++ {
		idx := (p.next + i) % len(p.proxies)
		entry := p.proxies[idx]
		if entry.disabled {
			continue
		}
		p.next = (idx + 1) % len(p.proxies)
		return entry.dialer.DialContext, entry
	}

	return nil, nil
}

func (p *proxyPool) reportResult(entry *proxyEntry, err error) bool {
	if p == nil || entry == nil {
		return false
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err == nil || !isTimeoutLike(err) {
		entry.timeoutStreak = 0
		return false
	}

	entry.timeoutStreak++
	if entry.timeoutStreak >= 2 {
		entry.disabled = true
		return true
	}
	return false
}

func isTimeoutLike(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	text := strings.ToLower(err.Error())
	return strings.Contains(text, "timeout") || strings.Contains(text, "deadline exceeded")
}

func runChecks(cfg config, candidates []sessionCandidate) []accountReport {
	reports := make([]accountReport, len(candidates))
	jobs := make(chan int)
	var wg sync.WaitGroup
	tracker := newProgressTracker(len(candidates))

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
	routeText, routeColor := routeDisplay(report.Route)
	pending := p.total - p.done

	fmt.Printf(
		"%s【%s】%s %s  %s%s%s  %s%s%s  当前存活%d  待检查%d\n",
		ansiGray, formatLineTime(time.Now()), ansiReset,
		phone,
		routeColor, routeText, ansiReset,
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
	case "restricted", "spam", "mutual":
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
	switch report.StatusCode {
	case "alive":
		return "存活", ansiGreen
	case "active":
		return "无限制", ansiGreen
	case "restricted", "spam", "mutual":
		if strings.HasPrefix(report.Summary, "双向至 ") {
			return report.Summary, ansiYellow
		}
		return "双向", ansiYellow
	case "banned":
		return "封禁", ansiRed
	case "frozen":
		if strings.HasPrefix(report.Summary, "冻结至 ") {
			return report.Summary, ansiBlue
		}
		return "冻结", ansiBlue
	case "timeout":
		return "超时", ansiYellow
	case "failed":
		return failureDisplay(report), ansiRed
	default:
		switch statusBucket(report.StatusCode) {
		case "alive":
			return "存活", ansiGreen
		case "limited":
			return "双向", ansiYellow
		case "banned":
			return "封禁", ansiRed
		case "frozen":
			return "冻结", ansiBlue
		default:
			return "未知", ansiCyan
		}
	}
}

func routeDisplay(route string) (string, string) {
	switch strings.TrimSpace(route) {
	case "代理":
		return "代理", ansiCyan
	case "回退直连":
		return "回退直连", ansiYellow
	default:
		return "直连", ansiGray
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

	gotdSession := filepath.Join(tmpDir, "session.json")
	switch candidate.SourceKind {
	case "tdata":
		if err := convertTDataDir(ctx, candidate.SourcePath, gotdSession); err != nil {
			report.StatusCode = "unauthorized"
			report.Error = fmt.Sprintf("tdata 转换失败: %v", err)
			report.Summary = "tdata 未授权、已损坏，或不是有效的 Telegram Desktop 数据"
			return report
		}
	default:
		sourceCopy := filepath.Join(tmpDir, candidate.Name)
		if err := copyFile(candidate.SourcePath, sourceCopy); err != nil {
			report.Error = fmt.Sprintf("复制 session 失败: %v", err)
			report.Summary = report.Error
			return report
		}
		if err := convertTelethonSQLiteSessionFile(ctx, sourceCopy, gotdSession); err != nil {
			report.StatusCode = "unauthorized"
			report.Error = fmt.Sprintf("session 转换失败: %v", err)
			report.Summary = "session 未授权、已损坏，或不是有效的 Telethon sqlite session"
			return report
		}
	}

	self, rawReply, code, summary, alive, canSend, route, err := runSessionCheck(ctx, cfg.mode, cfg.appID, cfg.appHash, cfg.proxyPool, gotdSession)
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
	report.Route = route
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
	reportPath := filepath.Join(outputDir, "检查结果.csv")
	if err := writeCSV(reportPath, reports); err != nil {
		return err
	}

	jsonPath := filepath.Join(outputDir, "检查结果.json")
	jsonData, err := json.MarshalIndent(reports, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, jsonData, 0o644); err != nil {
		return err
	}

	zipTargets := []struct {
		name string
		keep func(accountReport) bool
	}{
		{name: "存活账号.zip", keep: func(r accountReport) bool { return statusBucket(r.StatusCode) == "alive" }},
		{name: "双向账号.zip", keep: func(r accountReport) bool { return statusBucket(r.StatusCode) == "limited" }},
		{name: "封禁账号.zip", keep: func(r accountReport) bool { return statusBucket(r.StatusCode) == "banned" }},
		{name: "冻结账号.zip", keep: func(r accountReport) bool { return statusBucket(r.StatusCode) == "frozen" }},
		{name: "失效账号.zip", keep: func(r accountReport) bool {
			return statusBucket(r.StatusCode) == "failed" && failureDisplay(r) == "失效"
		}},
		{name: "超时账号.zip", keep: func(r accountReport) bool {
			return statusBucket(r.StatusCode) == "failed" && failureDisplay(r) == "超时"
		}},
		{name: "连接失败账号.zip", keep: func(r accountReport) bool {
			return statusBucket(r.StatusCode) == "failed" && failureDisplay(r) == "连接失败"
		}},
		{name: "限流账号.zip", keep: func(r accountReport) bool {
			return statusBucket(r.StatusCode) == "failed" && failureDisplay(r) == "限流"
		}},
		{name: "失败账号.zip", keep: func(r accountReport) bool {
			return statusBucket(r.StatusCode) == "failed" && failureDisplay(r) == "失败"
		}},
		{name: "未知状态.zip", keep: func(r accountReport) bool { return statusBucket(r.StatusCode) == "unknown" }},
	}

	for _, target := range zipTargets {
		if !hasMatchingReport(reports, target.keep) {
			continue
		}
		if err := writeZipByFilter(filepath.Join(outputDir, target.name), reports, target.keep); err != nil {
			return err
		}
	}
	return nil
}

func hasMatchingReport(reports []accountReport, keep func(accountReport) bool) bool {
	for _, report := range reports {
		if keep(report) {
			return true
		}
	}
	return false
}

func writeCSV(path string, reports []accountReport) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	header := []string{"file_name", "phone", "user_id", "username", "display_name", "status_code", "route", "alive", "can_send_dm", "summary", "raw_reply", "error", "checked_at", "source_path"}
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
			report.Route,
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
		name := uniqueZipName(usedNames, report.FileName)
		if err := writeZipSource(writer, name, report.SourcePath); err != nil {
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

func writeZipSource(writer *zip.Writer, name, sourcePath string) error {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return writeZipDir(writer, name, sourcePath)
	}
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return err
	}
	entry, err := writer.Create(name)
	if err != nil {
		return err
	}
	_, err = entry.Write(data)
	return err
}

func writeZipDir(writer *zip.Writer, rootName, sourceDir string) error {
	return filepath.WalkDir(sourceDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		zipPath := filepath.ToSlash(filepath.Join(rootName, relPath))
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		entry, err := writer.Create(zipPath)
		if err != nil {
			return err
		}
		if _, err := entry.Write(data); err != nil {
			return err
		}
		return nil
	})
}

func printFinalSummary(reports []accountReport, outputDir string) {
	counts := map[string]int{
		"total":     len(reports),
		"alive":     0,
		"limited":   0,
		"banned":    0,
		"frozen":    0,
		"invalid":   0,
		"timeout":   0,
		"connect":   0,
		"rateLimit": 0,
		"failed":    0,
		"unknown":   0,
	}

	for _, report := range reports {
		switch statusBucket(report.StatusCode) {
		case "alive":
			counts["alive"]++
		case "limited":
			counts["limited"]++
		case "banned":
			counts["banned"]++
		case "frozen":
			counts["frozen"]++
		case "failed":
			switch failureDisplay(report) {
			case "失效":
				counts["invalid"]++
			case "超时":
				counts["timeout"]++
			case "连接失败":
				counts["connect"]++
			case "限流":
				counts["rateLimit"]++
			default:
				counts["failed"]++
			}
		default:
			counts["unknown"]++
		}
	}

	fmt.Println()
	fmt.Printf("%s检查完成汇总%s\n", ansiGray, ansiReset)
	printSummaryLine("总计", counts["total"], ansiGray)
	printSummaryLine("存活", counts["alive"], ansiGreen)
	printSummaryLine("双向", counts["limited"], ansiYellow)
	printSummaryLine("封禁", counts["banned"], ansiRed)
	printSummaryLine("冻结", counts["frozen"], ansiBlue)
	printSummaryLine("失效", counts["invalid"], ansiRed)
	printSummaryLine("超时", counts["timeout"], ansiYellow)
	printSummaryLine("连接失败", counts["connect"], ansiRed)
	printSummaryLine("限流", counts["rateLimit"], ansiYellow)
	printSummaryLine("失败", counts["failed"], ansiRed)
	printSummaryLine("未知", counts["unknown"], ansiCyan)
	fmt.Printf("%s结果目录%s %s\n", ansiGray, ansiReset, outputDir)
}

func printSummaryLine(label string, count int, color string) {
	fmt.Printf("%s%-8s%s %d\n", color, label, ansiReset, count)
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

func collectInputCandidates(cfg config) ([]sessionCandidate, func(), error) {
	inputs := make([]string, 0, 8)
	if input := strings.TrimSpace(cfg.input); input != "" {
		inputs = append(inputs, input)
	}

	if listPath := strings.TrimSpace(cfg.inputList); listPath != "" {
		data, err := os.ReadFile(listPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read input list: %w", err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			line = strings.TrimPrefix(line, "\uFEFF")
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			inputs = append(inputs, line)
		}
	}

	if len(inputs) == 0 {
		return nil, nil, errors.New("no usable input paths found")
	}

	all := make([]sessionCandidate, 0, 1024)
	cleanups := make([]func(), 0, len(inputs))
	seen := make(map[string]struct{}, len(inputs)*4)

	for _, input := range inputs {
		candidates, cleanup, err := collectCandidates(input)
		if err != nil {
			for _, fn := range cleanups {
				if fn != nil {
					fn()
				}
			}
			return nil, nil, fmt.Errorf("%s: %w", input, err)
		}
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
		for _, candidate := range candidates {
			key := strings.ToLower(filepath.Clean(candidate.SourcePath))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			all = append(all, candidate)
		}
	}

	sort.Slice(all, func(i, j int) bool {
		if all[i].Name == all[j].Name {
			return all[i].SourcePath < all[j].SourcePath
		}
		return all[i].Name < all[j].Name
	})

	return all, func() {
		for _, fn := range cleanups {
			if fn != nil {
				fn()
			}
		}
	}, nil
}

func collectCandidates(input string) ([]sessionCandidate, func(), error) {
	info, err := os.Stat(input)
	if err != nil {
		return nil, nil, err
	}

	if info.IsDir() {
		if isTDataDir(input) {
			return []sessionCandidate{{Name: filepath.Base(input), SourcePath: input, SourceKind: "tdata"}}, nil, nil
		}
		files := make([]sessionCandidate, 0)
		err := filepath.WalkDir(input, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				if path != input && isTDataDir(path) {
					files = append(files, sessionCandidate{Name: d.Name(), SourcePath: path, SourceKind: "tdata"})
					return fs.SkipDir
				}
				return nil
			}
			lower := strings.ToLower(d.Name())
			if strings.HasSuffix(lower, ".session") && !strings.HasSuffix(lower, ".session-journal") {
				files = append(files, sessionCandidate{Name: d.Name(), SourcePath: path, SourceKind: "session"})
			}
			return nil
		})
		sort.Slice(files, func(i, j int) bool { return files[i].Name < files[j].Name })
		return files, nil, err
	}

	lower := strings.ToLower(info.Name())
	switch {
	case isTDataDir(input):
		return []sessionCandidate{{Name: filepath.Base(input), SourcePath: input, SourceKind: "tdata"}}, nil, nil
	case strings.HasSuffix(lower, ".session") && !strings.HasSuffix(lower, ".session-journal"):
		return []sessionCandidate{{Name: filepath.Base(input), SourcePath: input, SourceKind: "session"}}, nil, nil
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
			candidates = append(candidates, sessionCandidate{Name: file.Name, SourcePath: target, SourceKind: "session"})
		}
		return candidates, func() { _ = os.RemoveAll(tmpDir) }, nil
	default:
		return nil, nil, fmt.Errorf("仅支持目录、.session 或 .zip 输入")
	}
}

func isTDataDir(path string) bool {
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return false
	}
	keyDataPath := filepath.Join(path, "key_data")
	keyInfo, err := os.Stat(keyDataPath)
	return err == nil && !keyInfo.IsDir()
}

func runSessionCheck(ctx context.Context, mode string, appID int, appHash string, pool *proxyPool, sessionFile string) (*probeSelf, string, string, string, bool, bool, string, error) {
	dialContext, proxyEntry := pool.nextDialer()
	self, rawText, code, summary, alive, canSend, err := runSessionCheckOnce(ctx, mode, appID, appHash, sessionFile, dialContext)
	if pool != nil && proxyEntry != nil {
		fallbackToDirect := pool.reportResult(proxyEntry, err)
		if fallbackToDirect {
			self, rawText, code, summary, alive, canSend, err = runSessionCheckOnce(ctx, mode, appID, appHash, sessionFile, nil)
			return self, rawText, code, summary, alive, canSend, "回退直连", err
		}
		return self, rawText, code, summary, alive, canSend, "代理", err
	}
	return self, rawText, code, summary, alive, canSend, "直连", err
}

func runSessionCheckOnce(ctx context.Context, mode string, appID int, appHash, sessionFile string, dialContext func(context.Context, string, string) (net.Conn, error)) (*probeSelf, string, string, string, bool, bool, error) {
	if err := os.MkdirAll(filepath.Dir(sessionFile), 0o755); err != nil {
		return nil, "", "failed", "创建 session 目录失败", false, false, err
	}

	sessionStorage := &telegram.FileSessionStorage{Path: sessionFile}
	options := telegram.Options{
		SessionStorage: sessionStorage,
	}
	if dialContext != nil {
		options.Resolver = dcs.Plain(dcs.PlainOptions{Dial: dialContext})
	}
	client := telegram.NewClient(appID, appHash, options)
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
	untilText := extractSpamBotUntil(raw)
	onText := extractSpamBotOn(raw)

	switch {
	case containsAny(text, "some phone numbers may trigger a harsh response", "phone numbers may trigger"):
		return "active", "无限制", true
	case containsAny(text, "good news, no limits are currently applied", "you're free as a bird", "no limits", "free as a bird", "no restrictions", "all good", "account is free", "not limited"):
		return "active", "无限制", true
	case containsAny(text, "mutual contacts", "only people in your contacts", "only send messages to mutual contacts", "双向", "互相添加"):
		return "mutual", "双向", false
	case containsAny(text, "account is now limited until", "limited until", "moderators have confirmed the report", "users found your messages annoying", "will be automatically released", "temporarily limited"):
		if untilText != "" {
			return "restricted", "双向至 " + untilText, false
		}
		return "restricted", "双向", false
	case containsAny(text, "actions can trigger a harsh response from our anti-spam systems", "account was limited", "you will not be able to send messages"):
		if untilText != "" {
			return "restricted", "双向至 " + untilText, false
		}
		return "restricted", "双向", false
	case containsAny(text, "permanently banned", "account has been frozen permanently", "permanently restricted", "banned permanently", "blocked for violations", "terms of service", "banned", "suspended"):
		return "banned", "封禁", false
	case containsAny(text, "wait", "pending", "verification"):
		switch {
		case untilText != "":
			return "frozen", "冻结至 " + untilText, false
		case onText != "":
			return "frozen", "冻结至 " + onText, false
		default:
			return "frozen", "冻结", false
		}
	default:
		return "unknown", "未能明确识别账号状态，请人工查看 @SpamBot 最新回复", false
	}
}

func extractSpamBotUntil(raw string) string {
	re := regexp.MustCompile(`(?is)\buntil\b[:\s]*([^\r\n\.]+)`)
	match := re.FindStringSubmatch(raw)
	if len(match) < 2 {
		return ""
	}
	return cleanSpamBotDate(match[1])
}

func extractSpamBotOn(raw string) string {
	re := regexp.MustCompile(`(?is)\b(?:released|lifted|ends?)\s+on\b[:\s]*([^\r\n\.]+)`)
	match := re.FindStringSubmatch(raw)
	if len(match) < 2 {
		return ""
	}
	return cleanSpamBotDate(match[1])
}

func cleanSpamBotDate(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, " .!,:;")
	value = strings.ReplaceAll(value, "UTC", " UTC")
	value = strings.Join(strings.Fields(value), " ")
	return value
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

func convertTDataDir(ctx context.Context, sourcePath, targetPath string) error {
	accounts, err := tdesktop.Read(sourcePath, nil)
	if err != nil {
		return fmt.Errorf("read tdata: %w", err)
	}
	if len(accounts) == 0 {
		return fmt.Errorf("tdata 中没有可用账号")
	}
	data, err := gsession.TDesktopSession(accounts[0])
	if err != nil {
		return fmt.Errorf("convert tdata: %w", err)
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
