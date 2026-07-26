package engine

import (
	"asmroner/internal/logger"
	"asmroner/internal/model"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-resty/resty/v2"
)

func TestEngineManager_AuthLogin(t *testing.T) {
	// 该测试此前依赖硬编码的本地路径，已被禁用。
	// 如需启用，请配置环境变量 ASMROER_TEST_CONFIG_DIR 指向一个有效的 .asmroner-data 目录。
	if os.Getenv("ASMROER_TEST_CONFIG_DIR") == "" {
		t.Skip("skipping: requires ASMROER_TEST_CONFIG_DIR env var pointing to a valid .asmroner-data directory")
	}
	_, err := model.LoadConfig(os.Getenv("ASMROER_TEST_CONFIG_DIR"))
	if err != nil {
		t.Errorf("LoadConfig() failed, err: %v", err)
	}
	manager, err := NewEngineManager()
	if err != nil {
		t.Fatalf("NewEngineManager() failed, err: %v", err)
	}
	if err := manager.AuthLogin(context.Background()); err != nil {
		t.Errorf("AuthLogin() failed, err: %v", err)
	}
	if manager.JWTToken == "" {
		t.Errorf("AuthLogin() failed, JWTToken is empty")
	}
}

func TestMediaDownloadWorkersCapsLargeFileConcurrency(t *testing.T) {
	tests := map[int]int{-1: 1, 1: 1, 2: 2, 5: 3, 32: 3}
	for configured, want := range tests {
		if got := mediaDownloadWorkers(configured); got != want {
			t.Fatalf("mediaDownloadWorkers(%d) = %d, want %d", configured, got, want)
		}
	}
}

func TestDownloadFileResumesAfterUnexpectedEOF(t *testing.T) {
	logger.Init(filepath.Join(t.TempDir(), "download-test.log"))
	defer logger.Close()

	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Content-Length", "10")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("hello"))
			return
		}
		if got := r.Header.Get("Range"); got != "bytes=5-" {
			t.Errorf("second request Range = %q, want %q", got, "bytes=5-")
		}
		w.Header().Set("Content-Length", "5")
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("world"))
	}))
	defer server.Close()

	manager := &EngineManager{
		Client: resty.New().SetRetryCount(0),
		Config: &model.Config{Downloader: model.Downloader{
			MaxRetries: 2,
		}},
	}
	var latestProgress DownloadProgress
	manager.OnDownloadProgress = func(progress DownloadProgress) {
		latestProgress = progress
	}
	directory := t.TempDir()
	if err := manager.downloadFile(context.Background(), server.URL, directory, "sample.bin"); err != nil {
		t.Fatalf("downloadFile() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "sample.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if got, want := string(data), "helloworld"; got != want {
		t.Fatalf("downloaded data = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(directory, "sample.bin.part")); !os.IsNotExist(err) {
		t.Fatalf("partial file still exists, stat error = %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("request count = %d, want 2", got)
	}
	if !latestProgress.Completed || latestProgress.DownloadedBytes != 10 || latestProgress.TotalBytes != 10 {
		t.Fatalf("latest download progress = %#v, want completed 10/10", latestProgress)
	}
}

func TestBuildDownloadPlanUsesRemoteSizeAndPartialFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Errorf("request method = %s, want HEAD", r.Method)
		}
		w.Header().Set("Content-Length", "12")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "sample.bin.part"), []byte("hello"), 0644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	manager := &EngineManager{
		Client: resty.New().SetRetryCount(0),
		Config: &model.Config{Downloader: model.Downloader{
			MaxWorkers: 1,
		}},
	}
	plan := manager.buildDownloadPlan(context.Background(), [][]string{{server.URL, directory, "sample.bin"}})
	if len(plan) != 1 {
		t.Fatalf("buildDownloadPlan() length = %d, want 1", len(plan))
	}
	if plan[0].DownloadedBytes != 5 || plan[0].TotalBytes != 12 || plan[0].Completed {
		t.Fatalf("buildDownloadPlan() = %#v, want partial 5/12", plan[0])
	}
}

func TestDownloadFileContinuesResumingPastConfiguredRetryCount(t *testing.T) {
	logger.Init(filepath.Join(t.TempDir(), "auto-resume-test.log"))
	defer logger.Close()

	payload := []byte("abcdefghij")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		offset := 0
		if value := r.Header.Get("Range"); value != "" {
			start := strings.TrimSuffix(strings.TrimPrefix(value, "bytes="), "-")
			parsed, err := strconv.Atoi(start)
			if err != nil {
				t.Errorf("invalid range %q: %v", value, err)
			}
			offset = parsed
			w.Header().Set("Content-Range", "bytes "+strconv.Itoa(offset)+"-9/10")
		}
		remaining := len(payload) - offset
		w.Header().Set("Content-Length", strconv.Itoa(remaining))
		if offset > 0 {
			w.WriteHeader(http.StatusPartialContent)
		}
		call := calls.Add(1)
		if call <= 4 && remaining > 2 {
			_, _ = w.Write(payload[offset : offset+2])
			return
		}
		_, _ = w.Write(payload[offset:])
	}))
	defer server.Close()

	manager := &EngineManager{
		Client: resty.New().SetRetryCount(0),
		Config: &model.Config{Downloader: model.Downloader{
			MaxRetries: 1,
		}},
	}
	var retries atomic.Int32
	manager.OnDownloadRetry = func(DownloadRetry) {
		retries.Add(1)
	}
	directory := t.TempDir()
	if err := manager.downloadFile(context.Background(), server.URL, directory, "sample.bin"); err != nil {
		t.Fatalf("downloadFile() error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(directory, "sample.bin"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("downloaded data = %q, want %q", data, payload)
	}
	if calls.Load() != 5 || retries.Load() != 4 {
		t.Fatalf("calls = %d retries = %d, want 5 calls and 4 automatic retries", calls.Load(), retries.Load())
	}
}

func TestDownloadFileAutoRetryCanBeCancelled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "temporary outage", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	manager := &EngineManager{
		Client: resty.New().SetRetryCount(0),
		Config: &model.Config{Downloader: model.Downloader{
			MaxRetries: 1,
		}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	retrying := make(chan struct{}, 1)
	manager.OnDownloadRetry = func(DownloadRetry) {
		retrying <- struct{}{}
	}
	result := make(chan error, 1)
	go func() {
		result <- manager.downloadFile(ctx, server.URL, t.TempDir(), "sample.bin")
	}()

	select {
	case <-retrying:
		cancel()
	case <-time.After(2 * time.Second):
		t.Fatal("automatic retry was not reported")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("downloadFile() error = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("download did not stop after cancellation")
	}
}
