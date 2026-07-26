package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"asmroner/internal/engine"
)

func TestFriendlyDownloadErrorHidesUnexpectedEOF(t *testing.T) {
	message := friendlyDownloadError(errors.New("download failed: unexpected EOF"))
	if strings.Contains(strings.ToLower(message), "unexpected eof") {
		t.Fatalf("friendlyDownloadError() leaked low-level error: %q", message)
	}
	if !strings.Contains(message, "断点") {
		t.Fatalf("friendlyDownloadError() = %q, want resumable-download guidance", message)
	}
}

func TestPrepareTaskRetryOnlyQueuesUnfinishedItems(t *testing.T) {
	task := &DesktopTask{
		Status:    "failed",
		Completed: 1,
		Total:     3,
		Current:   1,
		Error:     "连接中断",
		Items: []DesktopTaskItem{
			{SourceID: "RJ00000001", Status: "completed"},
			{SourceID: "RJ00000002", Title: "失败作品", Status: "failed", Error: "unexpected EOF"},
			{SourceID: "RJ00000003", Status: "queued"},
		},
	}

	runItems, err := prepareTaskRetry(task)
	if err != nil {
		t.Fatalf("prepareTaskRetry() error = %v", err)
	}
	if len(runItems) != 2 || runItems[0].Index != 1 || runItems[1].Index != 2 {
		t.Fatalf("prepareTaskRetry() run items = %#v, want indexes 1 and 2", runItems)
	}
	if task.Status != "queued" || task.Error != "" || task.Current != 1 {
		t.Fatalf("prepareTaskRetry() task = %#v, want queued task without error", task)
	}
	if task.Items[0].Status != "completed" || task.Items[1].Status != "queued" || task.Items[1].Error != "" {
		t.Fatalf("prepareTaskRetry() item statuses = %#v", task.Items)
	}
}

func TestDeleteTasksCancelsActiveTasksAndKeepsFilesOutOfScope(t *testing.T) {
	app := NewApp()
	ctx, cancel := context.WithCancel(context.Background())
	app.tasks["active"] = &DesktopTask{ID: "active", Status: "running"}
	app.tasks["done"] = &DesktopTask{ID: "done", Status: "completed"}
	app.taskCancel["active"] = cancel

	deleted, err := app.DeleteTasks([]string{"active", "done", "active"})
	if err != nil {
		t.Fatalf("DeleteTasks() error = %v", err)
	}
	if deleted != 2 || len(app.tasks) != 0 || len(app.taskCancel) != 0 {
		t.Fatalf("DeleteTasks() deleted = %d, tasks = %d, cancels = %d", deleted, len(app.tasks), len(app.taskCancel))
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("DeleteTasks() did not cancel the active task")
	}
}

func TestTaskTransferStatsTrackBytesFilesAndSpeed(t *testing.T) {
	app := NewApp()
	app.tasks["task"] = &DesktopTask{ID: "task", Status: "running"}
	app.beginTaskTransfer("task", []engine.DownloadProgress{
		{FileKey: "one", FileName: "one.wav", DownloadedBytes: 5, TotalBytes: 10},
		{FileKey: "two", FileName: "two.wav", DownloadedBytes: 20, TotalBytes: 20, Completed: true},
	})

	task := app.tasks["task"]
	if task.DownloadedBytes != 25 || task.TotalBytes != 30 || !task.TotalBytesKnown {
		t.Fatalf("beginTaskTransfer() task bytes = %d/%d known=%v", task.DownloadedBytes, task.TotalBytes, task.TotalBytesKnown)
	}
	if task.FilesCompleted != 1 || task.FilesTotal != 2 {
		t.Fatalf("beginTaskTransfer() files = %d/%d", task.FilesCompleted, task.FilesTotal)
	}

	app.recordTaskRetry("task", engine.DownloadRetry{
		FileKey: "one", FileName: "one.wav", Attempt: 1, Wait: 3 * time.Second, Reason: "unexpected EOF",
	})
	if task.Status != "retrying" || task.AutoRetryCount != 1 || task.RetryWaitSeconds != 3 || task.RetryMessage == "" {
		t.Fatalf("recordTaskRetry() task = %#v", task)
	}

	app.taskStats["task"].SampleAt = time.Now().Add(-time.Second)
	app.taskStats["task"].SampleBytes = 25
	app.recordTaskTransfer("task", engine.DownloadProgress{
		FileKey: "one", FileName: "one.wav", DownloadedBytes: 10, TotalBytes: 10, Completed: true,
	})
	if task.DownloadedBytes != 30 || task.FilesCompleted != 2 || task.SpeedBytesPerSecond <= 0 {
		t.Fatalf("recordTaskTransfer() task = %#v", task)
	}
	if task.CurrentFile != "one.wav" {
		t.Fatalf("recordTaskTransfer() current file = %q", task.CurrentFile)
	}
	if task.Status != "running" || task.RetryMessage != "" {
		t.Fatalf("recordTaskTransfer() did not clear retry state: %#v", task)
	}
}

func TestTaskLevelTransientErrorsAreAutomaticallyRetried(t *testing.T) {
	retryable := []error{
		errors.New("download failed: unexpected EOF"),
		errors.New("request error,status code: 503"),
		errors.New("request error,status code: 403"),
		context.DeadlineExceeded,
	}
	for _, err := range retryable {
		if !shouldAutoRetryTaskError(err) {
			t.Fatalf("shouldAutoRetryTaskError(%v) = false, want true", err)
		}
	}
	permanent := []error{
		errors.New("request error,status code: 404"),
		errors.New("写入下载文件失败: disk full"),
		context.Canceled,
	}
	for _, err := range permanent {
		if shouldAutoRetryTaskError(err) {
			t.Fatalf("shouldAutoRetryTaskError(%v) = true, want false", err)
		}
	}
}

func TestTaskRetryStateWorksBeforeDownloadPlanIsAvailable(t *testing.T) {
	app := NewApp()
	app.tasks["task"] = &DesktopTask{ID: "task", Status: "running"}
	app.recordTaskRetry("task", engine.DownloadRetry{
		FileKey: "work:RJ00000001", FileName: "RJ00000001", Attempt: 1, Wait: 2 * time.Second,
	})
	task := app.tasks["task"]
	if task.Status != "retrying" || task.AutoRetryCount != 1 || task.RetryWaitSeconds != 2 {
		t.Fatalf("recordTaskRetry() task = %#v", task)
	}
}
