package main

import (
	"asmroner/internal/consts"
	"asmroner/internal/database"
	"asmroner/internal/engine"
	"asmroner/internal/logger"
	"asmroner/internal/model"
	"asmroner/internal/utils"
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v2/pkg/options"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx        context.Context
	mu         sync.RWMutex
	tasks      map[string]*DesktopTask
	taskCancel map[string]context.CancelFunc
	taskStats  map[string]*taskTransferStats
	configured bool
}

type DesktopState struct {
	Configured bool            `json:"configured"`
	Settings   DesktopSettings `json:"settings"`
	Version    string          `json:"version"`
}

type DesktopSettings struct {
	Account           string  `json:"account"`
	Password          string  `json:"password"`
	APIURL            string  `json:"apiUrl"`
	ProxyURL          string  `json:"proxyUrl"`
	DownloadDirectory string  `json:"downloadDirectory"`
	MaxWorkers        int     `json:"maxWorkers"`
	MaxRetries        int     `json:"maxRetries"`
	PreferMedia       string  `json:"preferMedia"`
	FolderNameStyle   string  `json:"folderNameStyle"`
	SanitizeFilename  bool    `json:"sanitizeFilename"`
	BrowsePageSize    int     `json:"browsePageSize"`
	DownloadQPS       float64 `json:"downloadQps"`
}

type DesktopLibrary struct {
	Works []DesktopWork `json:"works"`
	Total int           `json:"total"`
}

type DesktopWork struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	Folder       string         `json:"folder"`
	HasSubtitles bool           `json:"hasSubtitles"`
	CoverURL     string         `json:"coverUrl"`
	Tracks       []DesktopTrack `json:"tracks"`
	ModifiedAt   time.Time      `json:"modifiedAt"`
}

type DesktopTrack struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Type string `json:"type"`
}

type DesktopSearchWork struct {
	SourceID      string   `json:"sourceId"`
	Title         string   `json:"title"`
	Release       string   `json:"release"`
	Rating        float64  `json:"rating"`
	DownloadCount int      `json:"downloadCount"`
	HasSubtitle   bool     `json:"hasSubtitle"`
	CoverURL      string   `json:"coverUrl"`
	Circle        string   `json:"circle"`
	Duration      int      `json:"duration"`
	Tags          []string `json:"tags"`
}

type DesktopSearchPage struct {
	Works      []DesktopSearchWork `json:"works"`
	Page       int                 `json:"page"`
	PageSize   int                 `json:"pageSize"`
	Total      int                 `json:"total"`
	TotalPages int                 `json:"totalPages"`
}

type DesktopTask struct {
	ID                  string            `json:"id"`
	Label               string            `json:"label"`
	Status              string            `json:"status"`
	Completed           int               `json:"completed"`
	Total               int               `json:"total"`
	Current             int               `json:"current"`
	Items               []DesktopTaskItem `json:"items"`
	DownloadedBytes     int64             `json:"downloadedBytes"`
	TotalBytes          int64             `json:"totalBytes"`
	TotalBytesKnown     bool              `json:"totalBytesKnown"`
	SpeedBytesPerSecond float64           `json:"speedBytesPerSecond"`
	CurrentFile         string            `json:"currentFile,omitempty"`
	FilesCompleted      int               `json:"filesCompleted"`
	FilesTotal          int               `json:"filesTotal"`
	AutoRetryCount      int               `json:"autoRetryCount"`
	RetryWaitSeconds    int               `json:"retryWaitSeconds"`
	RetryMessage        string            `json:"retryMessage,omitempty"`
	Error               string            `json:"error,omitempty"`
	CreatedAt           time.Time         `json:"createdAt"`
	UpdatedAt           time.Time         `json:"updatedAt"`
}

type DesktopDownloadRequest struct {
	SourceID string `json:"sourceId"`
	Title    string `json:"title"`
	CoverURL string `json:"coverUrl"`
	Circle   string `json:"circle"`
}

type DesktopTaskItem struct {
	SourceID string `json:"sourceId"`
	Title    string `json:"title"`
	CoverURL string `json:"coverUrl"`
	Circle   string `json:"circle"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

type desktopTaskRunItem struct {
	Index   int
	Request DesktopDownloadRequest
}

type taskTransferStats struct {
	Files       map[string]engine.DownloadProgress
	SampleBytes int64
	SampleAt    time.Time
	Speed       float64
	LastEmit    time.Time
	Retrying    map[string]engine.DownloadRetry
}

func NewApp() *App {
	return &App{
		tasks:      make(map[string]*DesktopTask),
		taskCancel: make(map[string]context.CancelFunc),
		taskStats:  make(map[string]*taskTransferStats),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := useDesktopDataDirectory(); err != nil {
		logger.Error("桌面端数据目录初始化失败", "err", err)
	}
	_ = os.MkdirAll(consts.MetaDataDir, 0755)
	logger.Init(consts.FailedLogName)
	configPath := filepath.Join(consts.MetaDataDir, consts.ConfigFileName)
	if _, err := os.Stat(configPath); err == nil {
		if _, err := model.LoadConfig(consts.MetaDataDir); err == nil {
			a.configured = true
		}
	}
	if model.AppConfig == nil {
		model.AppConfig = defaultDesktopConfig()
	}
	normaliseConfig(model.AppConfig)
	if _, err := database.InitDB(); err != nil {
		logger.Error("桌面端数据库初始化失败", "err", err)
	}
}

func (a *App) shutdown(context.Context) {
	a.mu.Lock()
	for _, cancel := range a.taskCancel {
		cancel()
	}
	a.mu.Unlock()
	logger.Close()
}

func (a *App) secondInstance(_ options.SecondInstanceData) {
	if a.ctx != nil {
		wailsruntime.WindowUnminimise(a.ctx)
		wailsruntime.WindowShow(a.ctx)
	}
}

func defaultDesktopConfig() *model.Config {
	return &model.Config{
		User: model.User{Account: "guest", Password: "guest"},
		Downloader: model.Downloader{
			MaxWorkers:       3,
			MaxRetries:       3,
			SyncDataFolder:   defaultDownloadDirectory(),
			SyncWantedSize:   "200MB",
			PreferMedia:      "all",
			FolderNameStyle:  "full",
			SanitizeFilename: true,
			BrowsePageSize:   20,
		},
		Limit: model.Limit{
			SyncQPS:           2,
			SyncJitterMin:     100,
			SyncJitterMax:     500,
			DownloadQPS:       0.2,
			DownloadJitterMin: 2000,
			DownloadJitterMax: 5000,
		},
	}
}

func useDesktopDataDirectory() error {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	dataRoot := filepath.Join(configRoot, "ASMRoner")
	if err := os.MkdirAll(dataRoot, 0755); err != nil {
		return err
	}
	return os.Chdir(dataRoot)
}

func defaultDownloadDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", "downloads")
	}
	return filepath.Join(home, "Downloads", "ASMRoner")
}

func normaliseConfig(config *model.Config) {
	defaults := defaultDesktopConfig()
	if config.User.Account == "" {
		config.User.Account = defaults.User.Account
	}
	if config.User.Password == "" {
		config.User.Password = defaults.User.Password
	}
	if config.Downloader.SyncDataFolder == "" {
		config.Downloader.SyncDataFolder = defaults.Downloader.SyncDataFolder
	}
	if config.Downloader.MaxWorkers <= 0 {
		config.Downloader.MaxWorkers = defaults.Downloader.MaxWorkers
	}
	if config.Downloader.MaxWorkers > 3 {
		config.Downloader.MaxWorkers = 3
	}
	if config.Downloader.MaxRetries < 0 {
		config.Downloader.MaxRetries = defaults.Downloader.MaxRetries
	}
	if config.Downloader.PreferMedia == "" {
		config.Downloader.PreferMedia = defaults.Downloader.PreferMedia
	}
	if config.Downloader.FolderNameStyle == "" {
		config.Downloader.FolderNameStyle = defaults.Downloader.FolderNameStyle
	}
	if config.Downloader.BrowsePageSize < 10 || config.Downloader.BrowsePageSize > 50 {
		config.Downloader.BrowsePageSize = defaults.Downloader.BrowsePageSize
	}
	if config.Limit.DownloadQPS <= 0 {
		config.Limit.DownloadQPS = defaults.Limit.DownloadQPS
	}
}

func (a *App) GetState() DesktopState {
	config := model.AppConfig
	normaliseConfig(config)
	return DesktopState{
		Configured: a.configured,
		Version:    "v2.0.0",
		Settings: DesktopSettings{
			Account:           config.User.Account,
			Password:          config.User.Password,
			APIURL:            config.Downloader.ApiUrl,
			ProxyURL:          config.Downloader.ProxyUrl,
			DownloadDirectory: config.Downloader.SyncDataFolder,
			MaxWorkers:        config.Downloader.MaxWorkers,
			MaxRetries:        config.Downloader.MaxRetries,
			PreferMedia:       config.Downloader.PreferMedia,
			FolderNameStyle:   config.Downloader.FolderNameStyle,
			SanitizeFilename:  config.Downloader.SanitizeFilename,
			BrowsePageSize:    config.Downloader.BrowsePageSize,
			DownloadQPS:       config.Limit.DownloadQPS,
		},
	}
}

func (a *App) SaveSettings(settings DesktopSettings) error {
	settings.Account = strings.TrimSpace(settings.Account)
	settings.DownloadDirectory = strings.TrimSpace(settings.DownloadDirectory)
	if settings.Account == "" || settings.Password == "" {
		return errors.New("账号和密码不能为空")
	}
	if settings.DownloadDirectory == "" {
		return errors.New("请选择下载目录")
	}
	if settings.MaxWorkers < 1 || settings.MaxWorkers > 3 {
		return errors.New("为保证大文件下载稳定，并发数必须在 1 到 3 之间")
	}
	if settings.MaxRetries < 0 || settings.MaxRetries > 20 {
		return errors.New("重试次数必须在 0 到 20 之间")
	}
	if settings.DownloadQPS <= 0 {
		return errors.New("下载 QPS 必须大于 0")
	}
	if settings.BrowsePageSize < 10 || settings.BrowsePageSize > 50 {
		return errors.New("站点浏览每页数量必须在 10 到 50 之间")
	}
	if err := os.MkdirAll(settings.DownloadDirectory, 0755); err != nil {
		return fmt.Errorf("无法创建下载目录: %w", err)
	}
	configText := fmt.Sprintf(`[user]
account = %s
password = %s

[downloader]
api_url = %s
proxy_url = %s
max_workers = %d
max_retries = %d
sync_data_folder = %s
sync_wanted_size = "200MB"
prefer_media = %s
folder_name_style = %s
sanitize_filename = %t
browse_page_size = %d

[limit]
sync_qps = 2
sync_jitter_min = 100
sync_jitter_max = 500
download_qps = %s
download_jitter_min = 2000
download_jitter_max = 5000
`,
		strconv.Quote(settings.Account),
		strconv.Quote(settings.Password),
		strconv.Quote(settings.APIURL),
		strconv.Quote(settings.ProxyURL),
		settings.MaxWorkers,
		settings.MaxRetries,
		strconv.Quote(settings.DownloadDirectory),
		strconv.Quote(settings.PreferMedia),
		strconv.Quote(settings.FolderNameStyle),
		settings.SanitizeFilename,
		settings.BrowsePageSize,
		strconv.FormatFloat(settings.DownloadQPS, 'f', -1, 64),
	)
	if err := os.MkdirAll(consts.MetaDataDir, 0755); err != nil {
		return err
	}
	configPath := filepath.Join(consts.MetaDataDir, consts.ConfigFileName)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		return fmt.Errorf("保存配置失败: %w", err)
	}
	if _, err := model.LoadConfig(consts.MetaDataDir); err != nil {
		return fmt.Errorf("重新加载配置失败: %w", err)
	}
	normaliseConfig(model.AppConfig)
	a.configured = true
	return nil
}

func (a *App) ChooseDownloadDirectory() (string, error) {
	if a.ctx == nil {
		return "", errors.New("桌面窗口尚未初始化")
	}
	return wailsruntime.OpenDirectoryDialog(a.ctx, wailsruntime.OpenDialogOptions{
		Title:            "选择 ASMR 下载目录",
		DefaultDirectory: model.AppConfig.Downloader.SyncDataFolder,
	})
}

func (a *App) GetLibrary(filter string) (DesktopLibrary, error) {
	root, err := filepath.Abs(model.AppConfig.Downloader.SyncDataFolder)
	if err != nil {
		return DesktopLibrary{}, err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return DesktopLibrary{}, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return DesktopLibrary{}, err
	}
	filter = strings.ToLower(strings.TrimSpace(filter))
	works := make([]DesktopWork, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		work, err := scanDesktopWork(root, entry)
		if err != nil {
			continue
		}
		if filter != "" && !strings.Contains(strings.ToLower(work.ID+" "+work.Title+" "+work.Folder), filter) {
			continue
		}
		works = append(works, work)
	}
	sort.Slice(works, func(i, j int) bool { return works[i].ModifiedAt.After(works[j].ModifiedAt) })
	return DesktopLibrary{Works: works, Total: len(works)}, nil
}

func scanDesktopWork(root string, entry os.DirEntry) (DesktopWork, error) {
	name := entry.Name()
	work := DesktopWork{Folder: name, Title: name}
	parts := strings.SplitN(name, "-", 4)
	if len(parts) >= 1 && consts.AsmrOneIDRegex.MatchString(parts[0]) {
		work.ID = strings.ToUpper(parts[0])
	}
	if len(parts) == 4 && len(parts[1]) == 8 && (parts[2] == "sub" || parts[2] == "nosub") {
		work.Date = parts[1]
		work.HasSubtitles = parts[2] == "sub"
		work.Title = parts[3]
	} else if len(parts) >= 2 {
		work.Title = strings.Join(parts[1:], "-")
	}
	if work.ID == "" {
		work.ID = name
	}
	info, err := entry.Info()
	if err != nil {
		return work, err
	}
	work.ModifiedAt = info.ModTime()
	workDir := filepath.Join(root, name)
	err = filepath.WalkDir(workDir, func(path string, item os.DirEntry, walkErr error) error {
		if walkErr != nil || item.IsDir() {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(item.Name()))
		mediaURL := "/media/" + url.PathEscape(filepath.ToSlash(relative))
		switch ext {
		case ".mp3", ".wav", ".flac", ".m4a", ".ogg":
			work.Tracks = append(work.Tracks, DesktopTrack{
				Name: item.Name(),
				URL:  mediaURL,
				Type: strings.TrimPrefix(ext, "."),
			})
		case ".jpg", ".jpeg", ".png", ".webp":
			if work.CoverURL == "" {
				work.CoverURL = mediaURL
			}
		}
		return nil
	})
	return work, err
}

func (a *App) SearchPage(keyword string, page int, pageSize int) (DesktopSearchPage, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return DesktopSearchPage{}, errors.New("请输入搜索关键词或作品编号")
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 8 || pageSize > 50 {
		pageSize = 20
	}
	params := model.NewQueryParams(keyword)
	if err := params.ParseQueryStr(); err != nil {
		return DesktopSearchPage{}, err
	}
	params.PageInfo.Page = page
	params.PageInfo.PageSize = pageSize
	params.PageInfo.Count = pageSize
	query, err := params.BuildAsmrOneQueryStr()
	if err != nil {
		return DesktopSearchPage{}, err
	}
	manager, err := engine.NewEngineManager()
	if err != nil {
		return DesktopSearchPage{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	result, err := manager.SearchForCountResult(ctx, query, pageSize)
	if err != nil {
		return DesktopSearchPage{}, err
	}
	return desktopSearchPageFromResult(result, page, pageSize), nil
}

func (a *App) OpenASMRWebsite() {
	if a.ctx != nil {
		wailsruntime.BrowserOpenURL(a.ctx, "https://asmr.one")
	}
}

func (a *App) OpenASMRMirror() {
	if a.ctx != nil {
		wailsruntime.BrowserOpenURL(a.ctx, "https://as.mr")
	}
}

func (a *App) QueueDownloads(ids []string) (DesktopTask, error) {
	requests := make([]DesktopDownloadRequest, 0, len(ids))
	for _, id := range ids {
		requests = append(requests, DesktopDownloadRequest{SourceID: id})
	}
	return a.QueueDownloadItems(requests)
}

func (a *App) QueueDownloadItems(requests []DesktopDownloadRequest) (DesktopTask, error) {
	if len(requests) == 0 || len(requests) > 50 {
		return DesktopTask{}, errors.New("每次请选择 1 到 50 部作品")
	}
	clean := make([]DesktopDownloadRequest, 0, len(requests))
	seen := make(map[string]bool)
	for _, request := range requests {
		id := strings.ToUpper(strings.TrimSpace(request.SourceID))
		if !consts.AsmrOneIDRegex.MatchString(id) {
			return DesktopTask{}, fmt.Errorf("作品编号格式不正确: %s", id)
		}
		if !seen[id] {
			seen[id] = true
			clean = append(clean, DesktopDownloadRequest{
				SourceID: id,
				Title:    strings.TrimSpace(request.Title),
				CoverURL: strings.TrimSpace(request.CoverURL),
				Circle:   strings.TrimSpace(request.Circle),
			})
		}
	}
	items := make([]DesktopTaskItem, 0, len(clean))
	for _, request := range clean {
		items = append(items, DesktopTaskItem{
			SourceID: request.SourceID,
			Title:    request.Title,
			CoverURL: request.CoverURL,
			Circle:   request.Circle,
			Status:   "queued",
		})
	}
	label := clean[0].SourceID
	if clean[0].Title != "" {
		label = clean[0].Title
	}
	if len(clean) > 1 {
		label = fmt.Sprintf("%s 等 %d 部作品", label, len(clean))
	}
	now := time.Now()
	task := &DesktopTask{
		ID:        now.Format("20060102150405.000000000"),
		Label:     label,
		Status:    "queued",
		Total:     len(clean),
		Items:     items,
		CreatedAt: now,
		UpdatedAt: now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.tasks[task.ID] = task
	a.taskCancel[task.ID] = cancel
	a.mu.Unlock()
	result := cloneDesktopTask(task)
	runItems := make([]desktopTaskRunItem, 0, len(clean))
	for index, request := range clean {
		runItems = append(runItems, desktopTaskRunItem{Index: index, Request: request})
	}
	go a.runDownloadTask(ctx, task.ID, runItems)
	return result, nil
}

func (a *App) runDownloadTask(ctx context.Context, taskID string, runItems []desktopTaskRunItem) {
	defer func() {
		a.mu.Lock()
		delete(a.taskCancel, taskID)
		a.mu.Unlock()
	}()
	a.updateTask(taskID, func(task *DesktopTask) { task.Status = "running" })
	manager, err := engine.NewEngineManager()
	if err != nil {
		a.failTask(taskID, err)
		return
	}
	manager.OnDownloadPlan = func(plan []engine.DownloadProgress) {
		a.beginTaskTransfer(taskID, plan)
	}
	manager.OnDownloadProgress = func(progress engine.DownloadProgress) {
		a.recordTaskTransfer(taskID, progress)
	}
	manager.OnDownloadRetry = func(retry engine.DownloadRetry) {
		a.recordTaskRetry(taskID, retry)
	}
	directory := model.AppConfig.Downloader.SyncDataFolder
	for _, runItem := range runItems {
		index := runItem.Index
		request := runItem.Request
		a.updateTask(taskID, func(task *DesktopTask) {
			task.Current = index
			task.Items[index].Status = "running"
		})
		a.enrichTaskItem(ctx, manager, taskID, index, request.SourceID)
		if err := a.downloadWorkWithAutoRetry(ctx, manager, taskID, request, directory); err != nil {
			if errors.Is(err, context.Canceled) {
				a.updateTask(taskID, func(task *DesktopTask) {
					task.Status = "cancelled"
					task.SpeedBytesPerSecond = 0
					task.RetryWaitSeconds = 0
					task.RetryMessage = ""
					task.Items[index].Status = "cancelled"
				})
			} else {
				message := friendlyDownloadError(err)
				a.updateTask(taskID, func(task *DesktopTask) {
					task.Status = "failed"
					task.SpeedBytesPerSecond = 0
					task.RetryWaitSeconds = 0
					task.RetryMessage = ""
					task.Error = message
					task.Items[index].Status = "failed"
					task.Items[index].Error = message
				})
			}
			return
		}
		a.updateTask(taskID, func(task *DesktopTask) {
			task.Completed++
			task.Items[index].Status = "completed"
			task.SpeedBytesPerSecond = 0
			task.RetryWaitSeconds = 0
			task.RetryMessage = ""
			if task.Completed < task.Total {
				task.DownloadedBytes = 0
				task.TotalBytes = 0
				task.TotalBytesKnown = false
				task.CurrentFile = ""
				task.FilesCompleted = 0
				task.FilesTotal = 0
			}
		})
	}
	a.updateTask(taskID, func(task *DesktopTask) {
		task.Status = "completed"
		task.SpeedBytesPerSecond = 0
		task.RetryWaitSeconds = 0
		task.RetryMessage = ""
	})
}

func taskAutoRetryDelay(attempt int) time.Duration {
	seconds := 1 << min(max(attempt-1, 0), 4)
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds)*time.Second + time.Duration((attempt*173)%700)*time.Millisecond
}

func shouldAutoRetryTaskError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return true
	}
	message := strings.ToLower(err.Error())
	retryableFragments := []string{
		"unexpected eof",
		"connection reset",
		"connection refused",
		"broken pipe",
		"server closed",
		"tls handshake timeout",
		"i/o timeout",
		"temporary",
		"empty response",
		"status code: 403",
		"status code: 408",
		"status code: 425",
		"status code: 429",
		"status code: 5",
	}
	for _, fragment := range retryableFragments {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func (a *App) downloadWorkWithAutoRetry(
	ctx context.Context,
	manager *engine.EngineManager,
	taskID string,
	request DesktopDownloadRequest,
	directory string,
) error {
	attempt := 0
	for {
		err := manager.DownloadMediaByBatchIds(ctx, []string{request.SourceID}, directory)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !shouldAutoRetryTaskError(err) {
			return err
		}
		attempt++
		wait := taskAutoRetryDelay(attempt)
		fileName := request.Title
		if fileName == "" {
			fileName = request.SourceID
		}
		a.recordTaskRetry(taskID, engine.DownloadRetry{
			FileKey:  "work:" + request.SourceID,
			FileName: fileName,
			Attempt:  attempt,
			Wait:     wait,
			Reason:   err.Error(),
		})
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (a *App) enrichTaskItem(ctx context.Context, manager *engine.EngineManager, taskID string, index int, sourceID string) {
	valid, _, number, err := utils.IsValidDlsiteID(sourceID)
	if err != nil || !valid {
		return
	}
	metadataCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	work, err := manager.GetWorkInfo(metadataCtx, number)
	if err != nil {
		return
	}
	coverURL := strings.TrimSpace(work.ThumbnailCoverURL)
	if coverURL == "" {
		coverURL = strings.TrimSpace(work.SamCoverURL)
	}
	if coverURL == "" {
		coverURL = strings.TrimSpace(work.MainCoverURL)
	}
	a.updateTask(taskID, func(task *DesktopTask) {
		item := &task.Items[index]
		item.Title = strings.TrimSpace(work.Title)
		item.Circle = strings.TrimSpace(work.Circle.Name)
		item.CoverURL = coverURL
		label := item.Title
		if label == "" {
			label = item.SourceID
		}
		if task.Total > 1 {
			label = fmt.Sprintf("%s 等 %d 部作品", label, task.Total)
		}
		task.Label = label
	})
}

func friendlyDownloadError(err error) string {
	message := strings.TrimSpace(err.Error())
	lower := strings.ToLower(message)
	switch {
	case strings.Contains(lower, "unexpected eof"), strings.Contains(lower, "connection reset"), strings.Contains(lower, "broken pipe"):
		return "下载连接提前中断；已保留临时文件，重新加入任务时会从断点继续"
	case strings.Contains(lower, "context deadline exceeded"):
		return "下载请求超时；请检查网络或代理设置后重试"
	case strings.Contains(lower, "status code: 429"):
		return "站点请求过于频繁；请稍后重试或降低下载并发"
	case strings.Contains(lower, "status code: 5"):
		return "站点暂时无法提供文件；请稍后重新加入任务"
	default:
		return message
	}
}

func summarizeTransfers(files map[string]engine.DownloadProgress) (downloaded int64, total int64, totalKnown bool, completed int) {
	totalKnown = len(files) > 0
	for _, file := range files {
		downloaded += file.DownloadedBytes
		if file.TotalBytes > 0 {
			total += file.TotalBytes
		} else {
			totalKnown = false
		}
		if file.Completed {
			completed++
		}
	}
	return
}

func applyTransferStats(task *DesktopTask, stats *taskTransferStats, currentFile string) {
	downloaded, total, totalKnown, completed := summarizeTransfers(stats.Files)
	task.DownloadedBytes = downloaded
	task.TotalBytes = total
	task.TotalBytesKnown = totalKnown
	task.SpeedBytesPerSecond = stats.Speed
	task.FilesCompleted = completed
	task.FilesTotal = len(stats.Files)
	if currentFile != "" {
		task.CurrentFile = currentFile
	}
	task.UpdatedAt = time.Now()
}

func (a *App) beginTaskTransfer(taskID string, plan []engine.DownloadProgress) {
	now := time.Now()
	files := make(map[string]engine.DownloadProgress, len(plan))
	for _, progress := range plan {
		files[progress.FileKey] = progress
	}
	downloaded, _, _, _ := summarizeTransfers(files)
	stats := &taskTransferStats{
		Files:       files,
		SampleBytes: downloaded,
		SampleAt:    now,
		LastEmit:    now,
		Retrying:    make(map[string]engine.DownloadRetry),
	}

	a.mu.Lock()
	task, ok := a.tasks[taskID]
	if !ok {
		a.mu.Unlock()
		return
	}
	a.taskStats[taskID] = stats
	applyTransferStats(task, stats, "")
	task.Status = "running"
	task.RetryWaitSeconds = 0
	task.RetryMessage = ""
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
}

func (a *App) recordTaskTransfer(taskID string, progress engine.DownloadProgress) {
	now := time.Now()
	a.mu.Lock()
	task, ok := a.tasks[taskID]
	stats := a.taskStats[taskID]
	if !ok {
		a.mu.Unlock()
		return
	}
	if stats == nil {
		stats = &taskTransferStats{
			Files:    make(map[string]engine.DownloadProgress),
			Retrying: make(map[string]engine.DownloadRetry),
			SampleAt: now,
			LastEmit: now,
		}
		a.taskStats[taskID] = stats
	}
	delete(stats.Retrying, progress.FileKey)
	stats.Files[progress.FileKey] = progress
	downloaded, _, _, _ := summarizeTransfers(stats.Files)
	if elapsed := now.Sub(stats.SampleAt); elapsed >= 500*time.Millisecond {
		delta := downloaded - stats.SampleBytes
		if delta < 0 {
			delta = 0
		}
		currentSpeed := float64(delta) / elapsed.Seconds()
		if stats.Speed == 0 {
			stats.Speed = currentSpeed
		} else {
			stats.Speed = stats.Speed*0.65 + currentSpeed*0.35
		}
		stats.SampleBytes = downloaded
		stats.SampleAt = now
	}
	applyTransferStats(task, stats, progress.FileName)
	if len(stats.Retrying) == 0 {
		task.RetryWaitSeconds = 0
		task.RetryMessage = ""
		if task.Status == "retrying" {
			task.Status = "running"
		}
	}
	shouldEmit := progress.Completed || now.Sub(stats.LastEmit) >= 200*time.Millisecond
	if !shouldEmit {
		a.mu.Unlock()
		return
	}
	stats.LastEmit = now
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
}

func (a *App) recordTaskRetry(taskID string, retry engine.DownloadRetry) {
	a.mu.Lock()
	task, ok := a.tasks[taskID]
	stats := a.taskStats[taskID]
	if !ok {
		a.mu.Unlock()
		return
	}
	if stats == nil {
		now := time.Now()
		stats = &taskTransferStats{
			Files:    make(map[string]engine.DownloadProgress),
			Retrying: make(map[string]engine.DownloadRetry),
			SampleAt: now,
			LastEmit: now,
		}
		a.taskStats[taskID] = stats
	}
	if stats.Retrying == nil {
		stats.Retrying = make(map[string]engine.DownloadRetry)
	}
	stats.Retrying[retry.FileKey] = retry
	task.Status = "retrying"
	task.SpeedBytesPerSecond = 0
	task.CurrentFile = retry.FileName
	task.AutoRetryCount++
	task.RetryWaitSeconds = max(1, int(retry.Wait.Round(time.Second)/time.Second))
	task.RetryMessage = "下载源暂时不可用，程序将自动重新连接并从断点继续"
	task.UpdatedAt = time.Now()
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
}

func (a *App) updateTask(id string, update func(*DesktopTask)) {
	a.mu.Lock()
	if task, ok := a.tasks[id]; ok {
		update(task)
		task.UpdatedAt = time.Now()
	}
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
}

func (a *App) failTask(id string, err error) {
	a.updateTask(id, func(task *DesktopTask) {
		task.Status = "failed"
		task.SpeedBytesPerSecond = 0
		task.RetryWaitSeconds = 0
		task.RetryMessage = ""
		task.Error = err.Error()
	})
}

func (a *App) GetTasks() []DesktopTask {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.taskSnapshotLocked()
}

func (a *App) taskSnapshotLocked() []DesktopTask {
	result := make([]DesktopTask, 0, len(a.tasks))
	for _, task := range a.tasks {
		result = append(result, cloneDesktopTask(task))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].CreatedAt.After(result[j].CreatedAt) })
	return result
}

func cloneDesktopTask(task *DesktopTask) DesktopTask {
	copyTask := *task
	copyTask.Items = append([]DesktopTaskItem(nil), task.Items...)
	return copyTask
}

func prepareTaskRetry(task *DesktopTask) ([]desktopTaskRunItem, error) {
	if task.Status != "failed" && task.Status != "cancelled" {
		return nil, errors.New("只有失败或已取消的任务可以重试")
	}
	runItems := make([]desktopTaskRunItem, 0, len(task.Items))
	for index := range task.Items {
		item := &task.Items[index]
		if item.Status == "completed" {
			continue
		}
		item.Status = "queued"
		item.Error = ""
		runItems = append(runItems, desktopTaskRunItem{
			Index: index,
			Request: DesktopDownloadRequest{
				SourceID: item.SourceID,
				Title:    item.Title,
				CoverURL: item.CoverURL,
				Circle:   item.Circle,
			},
		})
	}
	if len(runItems) == 0 {
		return nil, errors.New("任务中没有需要重试的作品")
	}
	task.Status = "queued"
	task.Error = ""
	task.Current = runItems[0].Index
	return runItems, nil
}

func (a *App) RetryTask(id string) (DesktopTask, error) {
	id = strings.TrimSpace(id)
	a.mu.Lock()
	task, ok := a.tasks[id]
	if !ok {
		a.mu.Unlock()
		return DesktopTask{}, errors.New("下载任务不存在")
	}
	if _, active := a.taskCancel[id]; active {
		a.mu.Unlock()
		return DesktopTask{}, errors.New("任务仍在运行或正在结束，请稍后重试")
	}
	runItems, err := prepareTaskRetry(task)
	if err != nil {
		a.mu.Unlock()
		return DesktopTask{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.taskCancel[id] = cancel
	delete(a.taskStats, id)
	task.DownloadedBytes = 0
	task.TotalBytes = 0
	task.TotalBytesKnown = false
	task.SpeedBytesPerSecond = 0
	task.CurrentFile = ""
	task.FilesCompleted = 0
	task.FilesTotal = 0
	task.AutoRetryCount = 0
	task.RetryWaitSeconds = 0
	task.RetryMessage = ""
	task.UpdatedAt = time.Now()
	result := cloneDesktopTask(task)
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
	go a.runDownloadTask(ctx, id, runItems)
	return result, nil
}

func (a *App) RetryTasks(ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return 0, errors.New("请选择 1 到 500 个下载任务")
	}
	seen := make(map[string]bool)
	retried := 0
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, err := a.RetryTask(id); err == nil {
			retried++
		}
	}
	if retried == 0 {
		return 0, errors.New("所选任务中没有可重试项")
	}
	return retried, nil
}

func (a *App) CancelTask(id string) {
	a.CancelTasks([]string{id})
}

func (a *App) CancelTasks(ids []string) int {
	seen := make(map[string]bool)
	cancels := make([]context.CancelFunc, 0, len(ids))
	a.mu.RLock()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if cancel := a.taskCancel[id]; cancel != nil {
			cancels = append(cancels, cancel)
		}
	}
	a.mu.RUnlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

func (a *App) DeleteTask(id string) error {
	deleted, err := a.DeleteTasks([]string{id})
	if err != nil {
		return err
	}
	if deleted == 0 {
		return errors.New("下载任务不存在")
	}
	return nil
}

func (a *App) DeleteTasks(ids []string) (int, error) {
	if len(ids) == 0 || len(ids) > 500 {
		return 0, errors.New("请选择 1 到 500 个下载任务")
	}
	seen := make(map[string]bool)
	cancels := make([]context.CancelFunc, 0, len(ids))
	deleted := 0
	a.mu.Lock()
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if _, ok := a.tasks[id]; !ok {
			continue
		}
		if cancel := a.taskCancel[id]; cancel != nil {
			cancels = append(cancels, cancel)
		}
		delete(a.taskCancel, id)
		delete(a.taskStats, id)
		delete(a.tasks, id)
		deleted++
	}
	snapshot := a.taskSnapshotLocked()
	a.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if a.ctx != nil {
		wailsruntime.EventsEmit(a.ctx, "tasks:updated", snapshot)
	}
	return deleted, nil
}

func (a *App) RevealDownloadDirectory() error {
	path, err := filepath.Abs(model.AppConfig.Downloader.SyncDataFolder)
	if err != nil {
		return err
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		command = exec.Command("explorer.exe", path)
	case "darwin":
		command = exec.Command("open", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	return command.Start()
}
