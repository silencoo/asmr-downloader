package engine

import (
	"asmroner/internal/consts"
	"asmroner/internal/database"
	"asmroner/internal/logger"
	"asmroner/internal/model"
	"asmroner/internal/utils"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alitto/pond/v2"
	"github.com/go-resty/resty/v2"
	"golang.org/x/net/proxy"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// EngineManager 下载器管理结构
type EngineManager struct {
	DB                    *gorm.DB
	DownLimiter           *SmartLimiter
	Config                *model.Config
	WorkerPool            *pond.Pool
	DownloadPool          *pond.Pool
	Client                *resty.Client
	JWTToken              string
	ApiUrl                string
	MetadataWorkBatchChan chan []model.MetadataWork
	SyncWorkerPool        *pond.Pool
	OnDownloadPlan        func([]DownloadProgress)
	OnDownloadProgress    func(DownloadProgress)
	OnDownloadRetry       func(DownloadRetry)
}

type DownloadProgress struct {
	FileKey         string
	FileName        string
	DownloadedBytes int64
	TotalBytes      int64
	Completed       bool
}

type DownloadRetry struct {
	FileKey  string
	FileName string
	Attempt  int
	Wait     time.Duration
	Reason   string
}

type progressWriter struct {
	writer  io.Writer
	onWrite func(int64)
}

func (w *progressWriter) Write(data []byte) (int, error) {
	written, err := w.writer.Write(data)
	if written > 0 && w.onWrite != nil {
		w.onWrite(int64(written))
	}
	return written, err
}

var defaultHeaders = map[string]string{
	"accept":          "application/json, text/plain, */*",
	"accept-encoding": "gzip",
	"accept-language": "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7",
	"cache-control":   "no-cache",
	"content-type":    "application/json",
	"origin":          "https://asmr.one",
	"pragma":          "no-cache",
	"referer":         "https://asmr.one/",
	"user-agent":      "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/142.0.0.0 Safari/537.36",
}

// NewEngineManager 构造函数，增加 error 返回以符合 Go 惯例
func NewEngineManager() (*EngineManager, error) {
	config := model.AppConfig
	if config == nil {
		return nil, errors.New("application config is not initialized")
	}

	workers := config.Downloader.MaxWorkers
	pool := pond.NewPool(workers)
	downloadPool := pond.NewPool(mediaDownloadWorkers(workers))
	syncPool := pond.NewPool(2)

	client, err := buildRestyClient(config)
	if err != nil {
		return nil, fmt.Errorf("failed to build resty client: %w", err)
	}

	apiUrl := GetRespFastestSiteUrl()

	engine := &EngineManager{
		DB: database.Database,
		DownLimiter: NewSmartLimiter(
			config.Limit.DownloadQPS,
			1,
			config.Limit.DownloadJitterMin,
			config.Limit.DownloadJitterMax,
		),
		Config:                config,
		WorkerPool:            &pool,
		DownloadPool:          &downloadPool,
		Client:                client,
		ApiUrl:                apiUrl,
		MetadataWorkBatchChan: make(chan []model.MetadataWork, 100), // 适当增大缓冲
		SyncWorkerPool:        &syncPool,
	}

	// 默认初始化登录（使用默认背景 Context）
	if err := engine.AuthLogin(context.Background()); err != nil {
		logger.Warn("Initial login failed", "err", err)
	}

	return engine, nil
}

func mediaDownloadWorkers(configured int) int {
	return min(max(configured, 1), 3)
}

func buildRestyClient(config *model.Config) (*resty.Client, error) {
	proxyStr := config.Downloader.ProxyUrl
	retries := config.Downloader.MaxRetries
	r := resty.New()
	//http://112.123.45.67:8080
	if strings.Contains(proxyStr, "http") || strings.Contains(proxyStr, "https") {
		r.SetProxy(proxyStr)
	}
	//socks5://user123:pass456@112.123.45.67:8080
	if strings.Contains(proxyStr, "socks5") {
		if strings.Contains(proxyStr, "@") {
			//use auth
			//user123:pass456@112.123.45.67:8080
			authStr := strings.Split(proxyStr, "@")[0]
			proxyAddr := strings.Split(proxyStr, "@")[1]
			username := strings.Split(authStr, ":")[0]
			password := strings.Split(authStr, ":")[1]
			auth := &proxy.Auth{
				User:     username,
				Password: password,
			}

			dialer, err := proxy.SOCKS5("tcp", proxyAddr, auth, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("create socks5 dialer failed: %w", err)
			}
			r.SetTransport(&http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			})

		} else {
			//no auth
			proxyAddr := strings.Split(proxyStr, "://")[1]
			dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				return nil, fmt.Errorf("create socks5 dialer failed: %w", err)
			}
			r.SetTransport(&http.Transport{
				DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
					return dialer.Dial(network, addr)
				},
			})
		}

	}
	client := r.
		SetHeader("User-Agent", utils.RandomUserAgent(consts.UserAgents)).
		SetRetryCount(retries).
		SetRetryWaitTime(2 * time.Second)
	return client, nil
}

//func (m *EngineManager) CheckIfMetadataWorkBatchMode() {
//	var data model.MetadataWork
//	has := m.DB.Model(&model.MetadataWork{}).Limit(1).Find(&data).RowsAffected > 0
//	if has {
//		m.MetadataWorkBatchMode = false
//	}
//}

// AuthLogin 登录获取JWT Token
func (m *EngineManager) AuthLogin(ctx context.Context) error {
	headers := defaultHeaders
	user := struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}{
		Name:     m.Config.User.Account,
		Password: m.Config.User.Password,
	}
	result := make(map[string]interface{})

	response, err2 := m.Client.R().
		SetContext(ctx).
		SetHeaders(headers).
		SetResult(&result).
		SetBody(&user).
		Post(m.ApiUrl + consts.AsmrApiPath.LoginPath)
	//fmt.Println(string(response.Body()))
	if !response.IsSuccess() {
		return errors.New("auth login error: " + response.Status())
	}

	if err2 != nil {
		return errors.New("auth login error: " + err2.Error())
	}
	// 检查响应是否包含 token
	token, ok := result["token"].(string)
	if !ok || token == "" {
		return errors.New("auth login error: token not found in response")
	}
	m.JWTToken = "Bearer " + token
	return nil
}

// SimpleDownload 简单下载 可传入RJId 或者RJID列表
func (m *EngineManager) SimpleDownload(ctx context.Context, ids []string, storeBaseDir string) error {
	pool := *m.WorkerPool
	group := pool.NewGroup()
	for _, id := range ids {
		// 提交任务到 Worker Pool
		group.SubmitErr(func() error {
			return m.DownloadOne(ctx, id, storeBaseDir)
		})
	}
	err := group.Wait()
	return err
}

func (m *EngineManager) DownloadOne(ctx context.Context, id string, storeBaseDir string) error {
	//检查是否是合格的id
	valid, prefix, number, err := utils.IsValidDlsiteID(id)
	if err != nil || !valid {
		return err
	}
	//获取作品信息
	workInfo, err := m.GetWorkInfo(ctx, number)
	if err != nil {
		return err
	}
	logger.Info("Get WorkInfo", "title", workInfo.Title)
	//获取所有的tracks
	tracks, err := m.GetVoiceTracks(number)
	if err != nil {
		return err
	}
	logger.Info("Get TracksInfo list", "size", len(tracks))
	hasSubtitle := ""
	if workInfo.HasSubtitle {
		hasSubtitle = "sub"
	} else {
		hasSubtitle = "nosub"
	}

	//新建下载目录名 - 根据配置选择命名风格
	var folderName string
	rjCode := strings.ToUpper(prefix) + number

	// 根据配置决定是否规范化标题
	normalizedTitle := strings.ReplaceAll(workInfo.Title, "/", "")
	if m.Config.Downloader.SanitizeFilename {
		normalizedTitle = utils.NormalDirPathStr(normalizedTitle)
	}

	switch strings.ToLower(m.Config.Downloader.FolderNameStyle) {
	case "simple":
		// 仅 RJ 号: RJ01037721
		folderName = rjCode
	case "rj_title":
		// RJ 号 + 标题: RJ01037721-【标题】
		folderName = fmt.Sprintf("%s-%s", rjCode, normalizedTitle)
	default:
		// full (默认): RJ01037721-20230320-nosub-【标题】
		folderName = fmt.Sprintf(
			"%s-%s-%s-%s",
			rjCode,
			strings.ReplaceAll(workInfo.Release, "-", ""),
			hasSubtitle,
			normalizedTitle,
		)
	}
	defer func() {
		//递归的移除空目录
		utils.RemoveEmptyDirs(folderName)
	}()
	//正式多协程下载 到目录RJID-date-title
	logger.Info("Download folderName", "name", folderName)
	//根据配置需求下载tracks  比如只要mp3格式的
	storeFileDir := filepath.Join(storeBaseDir, folderName)
	needDownloadUrls, err := m.ensureDirExists(tracks, storeFileDir)
	if err != nil {
		return err
	}
	//过滤掉不需要的格式
	needDownloadUrls = m.filterTargetAudioFormate(needDownloadUrls)
	downloadPlan := m.buildDownloadPlan(ctx, needDownloadUrls)
	if m.OnDownloadPlan != nil {
		m.OnDownloadPlan(append([]DownloadProgress(nil), downloadPlan...))
	}
	//并行下载
	pool := *m.DownloadPool
	group := pool.NewGroup()
	for index, url := range needDownloadUrls {
		progress := downloadPlan[index]
		if progress.Completed {
			continue
		}
		//log.Println("Download file:", url[2])
		group.SubmitErr(func() error {
			return m.downloadFileWithExpectedSize(ctx, url[0], url[1], url[2], progress.TotalBytes)
			//return nil
		})
	}
	err = group.Wait()

	return err
}

func (m *EngineManager) filterTargetAudioFormate(urls [][]string) [][]string {
	// 1. 如果配置是 all，直接返回原文件列表
	config := m.Config.Downloader.PreferMedia
	if strings.ToLower(config) == "all" {
		return urls
	}
	// 2. 解析优先规则（例如 "mp3>wav>flac"）
	rules := strings.Split(strings.ToLower(config), ">")

	// 定义格式与后缀映射
	extMap := map[string][]string{
		"mp3":  {".mp3", ".mp3.vtt"},
		"wav":  {".wav", ".wav.vtt"},
		"flac": {".flac", ".flac.vtt"},
	}
	// 分成 groupA（支持的音频格式） 和 groupB（其它文件）
	groupA := make([][]string, 0)
	groupB := make([][]string, 0)

	allExtList := []string{
		".mp3", ".mp3.vtt",
		".wav", ".wav.vtt",
		".flac", ".flac.vtt",
	}

	for _, f := range urls {
		lf := strings.ToLower(f[2])

		found := false
		for _, ext := range allExtList {
			if strings.HasSuffix(lf, ext) {
				groupA = append(groupA, f)
				found = true
				break
			}
		}
		if !found {
			groupB = append(groupB, f)
		}
	}

	// 3. 按优先顺序过滤 groupA
	for _, rule := range rules {
		targetExts, ok := extMap[rule]
		if !ok {
			continue // 未知格式直接跳过
		}

		// 抽取符合该格式的文件
		selected := make([][]string, 0)
		for _, f := range groupA {
			lf := strings.ToLower(f[2])
			for _, ext := range targetExts {
				if strings.HasSuffix(lf, ext) {
					selected = append(selected, f)
					break
				}
			}
		}
		// 如果选到文件，则直接返回：选中文件 + groupB
		if len(selected) > 0 {
			return append(selected, groupB...)
		}
	}
	// 如果一个也没选到，则返回 groupB
	return groupB

}

func (m *EngineManager) ensureDirExists(tracks []model.Track, storeBaseDir string) ([][]string, error) {
	path := storeBaseDir
	// 注意：不对完整路径应用 NormalDirPathStr，只对单独的文件/文件夹名称应用
	_ = os.MkdirAll(path, os.ModePerm)
	//url,path,title
	var needDownloadUrls [][]string

	for _, t := range tracks {
		if t.Type != "folder" {
			// 根据配置决定是否规范化文件名
			fileName := t.Title
			if m.Config.Downloader.SanitizeFilename {
				fileName = utils.NormalDirPathStr(fileName)
			}
			needDownloadUrls = append(needDownloadUrls, []string{t.MediaDownloadURL, path, fileName})
		} else {
			// 根据配置决定是否规范化子目录名
			subDirName := t.Title
			if m.Config.Downloader.SanitizeFilename {
				subDirName = utils.NormalDirPathStr(subDirName)
			}
			needDownUrl, _ := m.ensureDirExists(t.Children, fmt.Sprintf("%s/%s", path, subDirName))
			needDownloadUrls = append(needDownloadUrls, needDownUrl...)
		}
	}
	return needDownloadUrls, nil
}

func (m *EngineManager) buildDownloadPlan(ctx context.Context, urls [][]string) []DownloadProgress {
	plan := make([]DownloadProgress, len(urls))
	if len(urls) == 0 {
		return plan
	}

	workers := mediaDownloadWorkers(m.Config.Downloader.MaxWorkers)
	sem := make(chan struct{}, workers)
	var wait sync.WaitGroup
	for index, target := range urls {
		fileKey := filepath.Clean(filepath.Join(target[1], target[2]))
		plan[index] = DownloadProgress{FileKey: fileKey, FileName: target[2]}
		wait.Add(1)
		go func() {
			defer wait.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			totalBytes := m.probeDownloadSize(ctx, target[0])
			progress := DownloadProgress{
				FileKey:    fileKey,
				FileName:   target[2],
				TotalBytes: totalBytes,
			}
			storePath := filepath.Join(target[1], target[2])
			if totalBytes > 0 {
				if info, err := os.Stat(storePath); err == nil && info.Size() == totalBytes {
					progress.DownloadedBytes = totalBytes
					progress.Completed = true
				} else if info, err := os.Stat(storePath + ".part"); err == nil {
					progress.DownloadedBytes = min(info.Size(), totalBytes)
				}
			} else if info, err := os.Stat(storePath + ".part"); err == nil {
				progress.DownloadedBytes = info.Size()
			}
			plan[index] = progress
		}()
	}
	wait.Wait()
	return plan
}

func (m *EngineManager) probeDownloadSize(ctx context.Context, url string) int64 {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := m.Client.R().
		SetContext(probeCtx).
		SetDoNotParseResponse(true).
		Head(url)
	if err != nil || resp == nil || resp.RawResponse == nil {
		return 0
	}
	defer resp.RawResponse.Body.Close()
	if !resp.IsSuccess() || resp.RawResponse.ContentLength <= 0 {
		return 0
	}
	return resp.RawResponse.ContentLength
}

func (m *EngineManager) GetVoiceTracks(id string) ([]model.Track, error) {
	url := m.ApiUrl + consts.AsmrApiPath.TracksPath + id
	headers := defaultHeaders

	var result []model.Track

	resp, err := m.Client.R().
		SetHeader("Authorization", m.JWTToken).
		SetHeaders(headers).
		SetResult(&result).
		Get(url)

	if err != nil {
		logger.Error("获取音轨信息失败", "err", err.Error())
		return nil, err
	}
	if !resp.IsSuccess() {
		return nil, errors.New("Request error,status code: " + strconv.Itoa(resp.StatusCode()))
	}
	return result, nil
}

func (m *EngineManager) GetWorkInfo(ctx context.Context, id string) (model.WorkInfo, error) {
	url := m.ApiUrl + consts.AsmrApiPath.WorkinfoPath + id
	headers := defaultHeaders

	var result = model.WorkInfo{}

	resp, err := m.Client.R().
		SetContext(ctx).
		SetHeader("Authorization", m.JWTToken).
		SetHeaders(headers).
		SetResult(&result).
		Get(url)

	if err != nil {
		logger.Error("获取作品信息失败", "err", err.Error())
		return result, err
	}
	if !resp.IsSuccess() {
		return result, errors.New("Request error,status code: " + strconv.Itoa(resp.StatusCode()))
	}
	return result, nil
}

func (m *EngineManager) SyncMetadata(ctx context.Context) error {
	url := m.ApiUrl + consts.AsmrApiPath.SyncMetaPath

	allPageFuture := m.fetchMetaDataRespFuture(url)
	allPageResult := <-allPageFuture
	if allPageResult == nil {
		return errors.New("获取所有元数据首页信息失败")
	}

	allSubPageFuture := m.fetchMetaDataRespFuture(url + "&subtitle=1")
	allSubPageResult := <-allSubPageFuture
	if allSubPageResult == nil {
		return errors.New("获取带字幕元数据首页信息失败")
	}
	//打印一些统计信息
	siteAll, localAll := m.printSyncMetadataStatics(allPageResult, allSubPageResult)
	if siteAll == localAll {
		logger.Info("网页数据与本地数据一致,无需同步")
		return nil
	}
	if siteAll < localAll {
		logger.Warn("本地数据存在逻辑错误,请检查数据库是否存在重复数据")
	}
	if siteAll > localAll {
		//提示网页数据有更新,是否进行同步操作
		confirm := utils.PromptConfirm("网页数据有更新,是否进行同步操作?")
		if !confirm {
			return nil
		}
	}

	//构建url列表 通过限流器 先请求数据  然后发送到 syncDataChan
	urls := m.buildMetaDataWorkUrls(allPageResult.Pagination.TotalCount, 100)
	//从syncDataChan 取出来 处理之后  发送到storeChan
	//errorgroup 可以实现携程级联退出
	// 请求阶段
	pool := *m.SyncWorkerPool
	//先启动存储 防止存储没有被goroutine来执行
	var wg sync.WaitGroup
	// ...
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.storeSyncMetadata(len(urls))
	}()

	retryMetadataWorkChan := make(chan string, 30)

	go func() {
		m.handleSyncMetadataRetry(retryMetadataWorkChan)
	}()
	group := pool.NewGroup()
	for _, u := range urls {
		url := u
		group.Submit(func() {
			// 限流
			resp, err := m.fetchMetaDataResp(url)
			if err != nil {
				logger.Warn("请求作品元数据分页失败,已做重试处理", "err", err.Error())
				retryMetadataWorkChan <- url
				return
			}
			logger.Debug("正在处理元数据分页", "url", url)
			metadataWork := resp.BuildMetadataWork()
			//metadataWork := []model.MetadataWork{}
			m.MetadataWorkBatchChan <- metadataWork

		})
	}
	group.Wait()

	//for _, url := range urls {
	//	// 限流
	//	resp, err := m.fetchMetaDataResp(url)
	//	if err != nil {
	//		return err
	//	}
	//	fmt.Println(url)
	//	metadataWork := resp.BuildMetadataWork()
	//	//metadataWork := []model.MetadataWork{}
	//	m.MetadataWorkBatchChan <- metadataWork
	//	time.Sleep(1 * time.Second)
	//}
	close(m.MetadataWorkBatchChan)
	wg.Wait()
	close(retryMetadataWorkChan)
	return nil
}

// 重试获取分页元数据
func (m *EngineManager) handleSyncMetadataRetry(retryChan chan string) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case url, ok := <-retryChan:
			if !ok {
				// retryChan 关闭 → 正常退出
				logger.Debug("Retry channel closed")
				return
			}
			logger.Info("重试获取分页元数据", "url", url)
			resp, err := m.fetchMetaDataResp(url)
			if err != nil {
				logger.Error("重试获取分页元数据失败", "err", err.Error())
				continue
			}
			metadataWork := resp.BuildMetadataWork()
			//metadataWork := []model.MetadataWork{}
			m.MetadataWorkBatchChan <- metadataWork
		case <-tick.C:
			// 定时器唤醒但没有任务，不做任何事
			continue
		}
	}

}

func (m *EngineManager) storeSyncMetadata(batchSize int) error {
	counter := 0
	for works := range m.MetadataWorkBatchChan {
		//log.Println("批量保存元数据: ", len(works))
		counter += 1
		logger.Info("元数据保存进度", "batch", counter, "total", batchSize, "pct", fmt.Sprintf("%.2f%%", float64(counter)/float64(batchSize)*100))
		tx := m.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&works)
		if tx.Error != nil {
			return tx.Error
		}
	}
	return nil
}

func (m *EngineManager) fetchMetaDataResp(url string) (*model.MetadataWorkResponse, error) {
	headers := defaultHeaders

	var result = model.MetadataWorkResponse{}

	resp, err := m.Client.R().
		SetHeader("Authorization", m.JWTToken).
		SetHeaders(headers).
		SetResult(&result).
		Get(url)
	if err != nil {
		logger.Error("获取元数据首页信息失败", "err", err.Error())
		return nil, err
	}
	if !resp.IsSuccess() {
		//log.Println("获取元数据首页信息失败: ", resp.String())
		logger.Warn("Cloudflare 429 响应状态", "status", resp.StatusCode())
		return nil, errors.New("cloudflare 429 Too Many Requests")
	}
	return &result, nil
}

func (m *EngineManager) fetchMetaDataRespFuture(url string) chan *model.MetadataWorkResponse {
	responses := make(chan *model.MetadataWorkResponse, 1)
	go func() {
		resp, err := m.fetchMetaDataResp(url)
		if err != nil {
			logger.Error("获取元数据分页失败", "err", err.Error())
			responses <- nil
			return
		}
		responses <- resp
	}()
	return responses
}

func (m *EngineManager) buildMetaDataWorkUrls(totalCount int, pageSize int) []string {
	urls := make([]string, 0)
	//page := totalCount / pageSize
	//if totalCount%pageSize != 0 {
	//	page++
	//}

	for i := 1; i <= (totalCount/pageSize)+1; i++ {
		pageStr := strings.ReplaceAll(consts.AsmrApiPath.SyncMetaPath, "page=1",
			fmt.Sprintf("page=%s", strconv.Itoa(i)))

		pageSizeStr := strings.ReplaceAll(pageStr, "pageSize=1",
			fmt.Sprintf("pageSize=%d", 100))
		url := m.ApiUrl + pageSizeStr
		urls = append(urls, url)
	}
	return urls
}

func (m *EngineManager) SyncAndDownload() error {
	return nil
}

func (m *EngineManager) SyncRetryFaild() error {
	return nil
}

func automaticRetryDelay(consecutiveNoProgress int, fastRetries int, progressMade bool) time.Duration {
	if progressMade {
		return 250 * time.Millisecond
	}
	if fastRetries < 1 {
		fastRetries = 1
	}
	var seconds int
	if consecutiveNoProgress <= fastRetries {
		seconds = 1 << min(max(consecutiveNoProgress-1, 0), 3)
	} else {
		slowAttempt := consecutiveNoProgress - fastRetries
		seconds = 5 * (1 << min(max(slowAttempt-1, 0), 3))
		if seconds > 30 {
			seconds = 30
		}
	}
	jitter := time.Duration((consecutiveNoProgress*137)%500) * time.Millisecond
	return time.Duration(seconds)*time.Second + jitter
}

func (m *EngineManager) downloadFile(ctx context.Context, url string, path string, fileName string) error {
	return m.downloadFileWithExpectedSize(ctx, url, path, fileName, 0)
}

func (m *EngineManager) downloadFileWithExpectedSize(ctx context.Context, url string, path string, fileName string, expectedTotal int64) error {
	storePath := filepath.Join(path, fileName)
	partPath := storePath + ".part"
	fileKey := filepath.Clean(storePath)
	report := func(downloadedBytes int64, totalBytes int64, completed bool) {
		if m.OnDownloadProgress != nil {
			m.OnDownloadProgress(DownloadProgress{
				FileKey:         fileKey,
				FileName:        fileName,
				DownloadedBytes: downloadedBytes,
				TotalBytes:      totalBytes,
				Completed:       completed,
			})
		}
	}
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		return fmt.Errorf("创建下载目录失败: %w", err)
	}
	if expectedTotal > 0 {
		if info, err := os.Stat(storePath); err == nil && info.Size() == expectedTotal {
			report(expectedTotal, expectedTotal, true)
			return nil
		}
	}

	fastRetries := m.Config.Downloader.MaxRetries
	if fastRetries < 1 {
		fastRetries = 1
	}
	var lastErr error
	attempt := 0
	consecutiveNoProgress := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		offset := int64(0)
		if info, err := os.Stat(partPath); err == nil {
			offset = info.Size()
		}
		progressMade := false
		request := m.Client.R().
			SetContext(ctx).
			SetDoNotParseResponse(true)
		if offset > 0 {
			request.SetHeader("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := request.Get(url)
		if err != nil {
			lastErr = err
		} else if resp == nil || resp.RawResponse == nil {
			lastErr = errors.New("empty download response")
		} else {
			statusCode := resp.StatusCode()
			if statusCode == http.StatusRequestedRangeNotSatisfiable && offset > 0 {
				_ = resp.RawResponse.Body.Close()
				if expectedTotal > 0 && offset == expectedTotal {
					if err := os.Remove(storePath); err != nil && !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("替换旧文件失败: %w", err)
					}
					if err := os.Rename(partPath, storePath); err != nil {
						return fmt.Errorf("完成下载文件失败: %w", err)
					}
					report(expectedTotal, expectedTotal, true)
					return nil
				}
				if err := os.Remove(partPath); err != nil && !errors.Is(err, os.ErrNotExist) {
					return fmt.Errorf("重置临时下载文件失败: %w", err)
				}
				lastErr = fmt.Errorf("request error,status code: %d", statusCode)
			} else if !resp.IsSuccess() {
				_ = resp.RawResponse.Body.Close()
				lastErr = fmt.Errorf("request error,status code: %d", statusCode)
				if statusCode < 500 &&
					statusCode != http.StatusTooManyRequests &&
					statusCode != http.StatusRequestTimeout &&
					statusCode != http.StatusTooEarly {
					return lastErr
				}
			} else {
				if statusCode == http.StatusPartialContent && offset > 0 {
					contentRange := resp.Header().Get("Content-Range")
					expectedPrefix := fmt.Sprintf("bytes %d-", offset)
					if contentRange != "" && !strings.HasPrefix(contentRange, expectedPrefix) {
						_ = resp.RawResponse.Body.Close()
						if err := os.Remove(partPath); err != nil && !errors.Is(err, os.ErrNotExist) {
							return fmt.Errorf("重置无效断点文件失败: %w", err)
						}
						lastErr = fmt.Errorf("invalid content range: %s", contentRange)
						continue
					}
				}
				flags := os.O_CREATE | os.O_WRONLY
				if statusCode == http.StatusPartialContent && offset > 0 {
					flags |= os.O_APPEND
				} else {
					flags |= os.O_TRUNC
					offset = 0
				}
				file, openErr := os.OpenFile(partPath, flags, 0644)
				if openErr != nil {
					_ = resp.RawResponse.Body.Close()
					return fmt.Errorf("创建临时下载文件失败: %w", openErr)
				}
				expected := resp.RawResponse.ContentLength
				totalBytes := expectedTotal
				if expected >= 0 {
					if statusCode == http.StatusPartialContent && offset > 0 {
						totalBytes = offset + expected
					} else {
						totalBytes = expected
					}
				}
				downloadedBytes := offset
				report(downloadedBytes, totalBytes, false)
				lastReport := time.Now()
				writer := &progressWriter{
					writer: file,
					onWrite: func(written int64) {
						progressMade = true
						downloadedBytes += written
						if time.Since(lastReport) >= 200*time.Millisecond {
							report(downloadedBytes, totalBytes, false)
							lastReport = time.Now()
						}
					},
				}
				written, copyErr := io.Copy(writer, resp.RawResponse.Body)
				_ = resp.RawResponse.Body.Close()
				closeFileErr := file.Close()
				report(downloadedBytes, totalBytes, false)
				switch {
				case copyErr != nil:
					if ctx.Err() != nil {
						return ctx.Err()
					}
					var pathErr *os.PathError
					if errors.As(copyErr, &pathErr) {
						return fmt.Errorf("写入下载文件失败: %w", copyErr)
					}
					lastErr = copyErr
				case closeFileErr != nil:
					return fmt.Errorf("关闭下载文件失败: %w", closeFileErr)
				case expected >= 0 && written != expected:
					lastErr = io.ErrUnexpectedEOF
				default:
					if err := os.Remove(storePath); err != nil && !errors.Is(err, os.ErrNotExist) {
						return fmt.Errorf("替换旧文件失败: %w", err)
					}
					if err := os.Rename(partPath, storePath); err != nil {
						return fmt.Errorf("完成下载文件失败: %w", err)
					}
					if totalBytes <= 0 {
						totalBytes = offset + written
					}
					report(totalBytes, totalBytes, true)
					return nil
				}
			}
		}

		attempt++
		if progressMade {
			consecutiveNoProgress = 0
		} else {
			consecutiveNoProgress++
		}
		wait := automaticRetryDelay(consecutiveNoProgress, fastRetries, progressMade)
		logger.Warn("下载连接中断，后台自动续传", "file", fileName, "attempt", attempt, "wait", wait, "err", lastErr)
		if m.OnDownloadRetry != nil {
			m.OnDownloadRetry(DownloadRetry{
				FileKey:  fileKey,
				FileName: fileName,
				Attempt:  attempt,
				Wait:     wait,
				Reason:   lastErr.Error(),
			})
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *EngineManager) SearchForCountResult(ctx context.Context, asmrOneQueryStr string, count int) (model.SearchResult, error) {
	url := m.ApiUrl + consts.AsmrApiPath.SearchPath + asmrOneQueryStr
	headers := defaultHeaders

	var result = model.SearchResult{}

	resp, err := m.Client.R().
		SetContext(ctx).
		SetHeader("Authorization", m.JWTToken).
		SetHeaders(headers).
		SetResult(&result).
		Get(url)

	if err != nil {
		logger.Error("查询关键字信息失败", "err", err.Error())
		return result, err
	}
	if !resp.IsSuccess() {
		return result, errors.New("Request error,status code: " + strconv.Itoa(resp.StatusCode()))
	}
	// 如果结果比较少
	if result.Pagination.TotalCount > count && count < result.Pagination.PageSize {
		result.Works = result.Works[:count]
		return result, nil
	}
	if result.Pagination.TotalCount < count && count > result.Pagination.PageSize {
		count = result.Pagination.TotalCount
	}
	//如果结果比count大 但是比pageSize小 则直接返回
	if result.Pagination.TotalCount >= count {
		//计算分页
		page := count / result.Pagination.PageSize
		if count%result.Pagination.PageSize != 0 {
			page++
		}
		for i := 2; i <= page; i++ {
			// 构建分页URL
			var newResult model.SearchResult
			pageURL := strings.ReplaceAll(url, "?page=1", fmt.Sprintf("?page=%d", i))
			// 发送GET请求
			resp, err := m.Client.R().
				SetHeader("Authorization", m.JWTToken).
				SetHeaders(headers).
				SetResult(&newResult).
				Get(pageURL)
			if err != nil {
				logger.Error("查询分页信息失败", "err", err.Error())
				return newResult, err
			}
			if !resp.IsSuccess() {
				return newResult, errors.New("Request error,status code: " + strconv.Itoa(resp.StatusCode()))
			}
			// 合并结果
			result.Works = append(result.Works, newResult.Works...)
			time.Sleep(500 * time.Millisecond)
		}
	}
	return result, nil
}

func (m *EngineManager) DownloadBatchMedias(ctx context.Context, works []model.SearchResultView, storePathDir string) error {
	var ids []string
	for _, work := range works {
		id := work.SourceID
		ids = append(ids, id)
	}
	pool := *m.WorkerPool
	group := pool.NewGroup()
	for _, id := range ids {
		// 提交任务到 Worker Pool
		group.SubmitErr(func() error {
			return m.DownloadOne(ctx, id, storePathDir)
		})
	}
	err := group.Wait()
	if err != nil {
		logger.Error("下载作品失败", "err", err.Error())
		return err
	}
	return nil
}

func (m *EngineManager) DownloadMediaByBatchIds(ctx context.Context, worksId []string, storePathDir string) error {
	if len(worksId) <= 0 {
		return nil
	}
	for _, id := range worksId {
		// 等待令牌
		if err := m.DownLimiter.Wait(ctx); err != nil {
			logger.Error("等待下载限流器令牌失败", "err", err.Error())
			return err
		}
		err := m.DownloadOne(ctx, id, storePathDir)
		//err := func() error {
		//	log.Println("正在下载作品: ", id)
		//	time.Sleep(5 * time.Second)
		//	return nil
		//}()
		if err != nil {
			logger.Error("下载作品失败", "id", id, "err", err.Error())
			return err
		}
	}
	return nil
}

// 打印同步元数据统计信息
func (m *EngineManager) printSyncMetadataStatics(result *model.MetadataWorkResponse, result2 *model.MetadataWorkResponse) (int, int) {
	//输出当前数据库中存在的记录数
	//var allCount int64
	//tx := m.DB.Model(&model.MetadataWork{}).Count(&allCount)
	//if tx.Error != nil {
	//	log.Println("查询数据库出现错误" + tx.Error.Error())
	//}
	type StatResult struct {
		TotalCount        int
		SubtitleTrueCount int
	}
	var localResult StatResult
	m.DB.Raw(`
    SELECT 
        COUNT(*) AS total_count,
        SUM(CASE WHEN has_subtitle = 1 THEN 1 ELSE 0 END) AS subtitle_true_count
    FROM metadata_works`).Scan(&localResult)
	//打印一些统计信息
	logger.Info("网站作品元数据数量",
		"total", result.Pagination.TotalCount,
		"with_subtitle", result2.Pagination.TotalCount,
	)
	var syncRateTotal, syncRateSubtitle float64

	if localResult.TotalCount > 0 {
		// 总体同步率 = 已同步条目 / 总条目
		syncRateTotal = float64(localResult.TotalCount) / float64(result.Pagination.TotalCount)

		// 字幕同步率 = 带字幕条目 / 总条目
		syncRateSubtitle = float64(localResult.SubtitleTrueCount) / float64(result2.Pagination.TotalCount)
	} else {
		syncRateTotal = 0.0
		syncRateSubtitle = 0.0
	}

	logger.Info(
		"本地数据库元数据统计",
		"total", localResult.TotalCount,
		"with_subtitle", localResult.SubtitleTrueCount,
		"sync_rate_total", fmt.Sprintf("%.2f%%", syncRateTotal*100),
		"sync_rate_subtitle", fmt.Sprintf("%.2f%%", syncRateSubtitle*100),
	)

	return result.Pagination.TotalCount, localResult.TotalCount
}

// 按照指定数量下载热门100作品
func (m *EngineManager) DownloadHot100(ctx context.Context, count int, dir string) error {
	url := m.ApiUrl + consts.AsmrApiPath.HotPath
	headers := defaultHeaders

	var result = model.MetadataWorkResponse{}
	body := map[string]interface{}{
		"keyword":             "",
		"page":                1,
		"pageSize":            100,
		"subtitle":            0,
		"localSubtitledWorks": []interface{}{},
		"withPlaylistStatus":  []interface{}{},
	}

	resp, err := m.Client.R().
		SetHeader("Authorization", m.JWTToken).
		SetContext(ctx).
		SetBody(body).
		SetHeaders(headers).
		SetResult(&result).
		Post(url)

	if err != nil {
		logger.Error("获取作品信息失败", "err", err.Error())
		logger.RecordFailure("DownloadHot100", url, err.Error())
		return err
	}
	if !resp.IsSuccess() {
		logger.RecordFailure("DownloadHot100", url, resp.Status())
		return errors.New("Request error,status code: " + strconv.Itoa(resp.StatusCode()))
	}
	if count <= 0 {
		return errors.New("下载数量选择必须大于0")
	}
	metadataWork := result.BuildMetadataWork()
	works := metadataWork[:count]
	var sourceIds []string
	for _, work := range works {
		sourceIds = append(sourceIds, work.SourceID)
	}
	// 下载热门100作品
	err = m.DownloadMediaByBatchIds(ctx, sourceIds, dir)
	if err != nil {
		logger.Error("下载热门100作品失败", "err", err.Error())
		return err
	}
	return nil
}
