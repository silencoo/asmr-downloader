package cmd

import (
	"asmroner/internal/engine"
	"asmroner/internal/model"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type guiTask struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	Status    string    `json:"status"`
	Completed int       `json:"completed"`
	Total     int       `json:"total"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type guiTaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*guiTask
}

func newGUITaskStore() *guiTaskStore {
	return &guiTaskStore{tasks: make(map[string]*guiTask)}
}

func (s *guiTaskStore) create(kind, label string, total int) guiTask {
	now := time.Now()
	task := &guiTask{
		ID:        now.Format("20060102150405.000000000"),
		Kind:      kind,
		Label:     label,
		Status:    "queued",
		Total:     total,
		CreatedAt: now,
		UpdatedAt: now,
	}
	s.mu.Lock()
	s.tasks[task.ID] = task
	s.mu.Unlock()
	return *task
}

func (s *guiTaskStore) update(id string, fn func(*guiTask)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if task, ok := s.tasks[id]; ok {
		fn(task)
		task.UpdatedAt = time.Now()
	}
}

func (s *guiTaskStore) list() []guiTask {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]guiTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		result = append(result, *task)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

var sourceIDPattern = regexp.MustCompile(`(?i)^RJ\d{6,10}$`)

func registerGUIRoutes(r *gin.Engine, libraryDB *gorm.DB, dataFolder string) {
	tasks := newGUITaskStore()

	r.GET("/api/status", func(c *gin.Context) {
		var libraryCount int64
		_ = libraryDB.Model(&FolderInfo{}).Count(&libraryCount).Error
		config := model.AppConfig
		c.JSON(http.StatusOK, wrapResponse(gin.H{
			"libraryCount": libraryCount,
			"dataFolder":   dataFolder,
			"apiUrl":       config.Downloader.ApiUrl,
			"workers":      config.Downloader.MaxWorkers,
			"preferMedia":  config.Downloader.PreferMedia,
			"configured":   strings.TrimSpace(config.Downloader.ApiUrl) != "",
		}))
	})

	r.GET("/api/tasks", func(c *gin.Context) {
		c.JSON(http.StatusOK, wrapResponse(tasks.list()))
	})

	r.GET("/api/search", func(c *gin.Context) {
		keyword := strings.TrimSpace(c.Query("q"))
		if keyword == "" {
			c.JSON(http.StatusBadRequest, wrapResponse(errors.New("请输入搜索关键词或 RJ 编号")))
			return
		}
		count := 24
		queryParams := model.NewQueryParams(keyword)
		if err := queryParams.ParseQueryStr(); err != nil {
			c.JSON(http.StatusBadRequest, wrapResponse(err))
			return
		}
		query, err := queryParams.BuildAsmrOneQueryStr()
		if err != nil {
			c.JSON(http.StatusBadRequest, wrapResponse(err))
			return
		}
		manager, err := engine.NewEngineManager()
		if err != nil {
			c.JSON(http.StatusInternalServerError, wrapResponse(err))
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 45*time.Second)
		defer cancel()
		result, err := manager.SearchForCountResult(ctx, query, count)
		if err != nil {
			c.JSON(http.StatusBadGateway, wrapResponse(err))
			return
		}
		c.JSON(http.StatusOK, wrapResponse(result))
	})

	r.POST("/api/downloads", func(c *gin.Context) {
		var request struct {
			IDs       []string `json:"ids"`
			Directory string   `json:"directory"`
		}
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, wrapResponse(errors.New("下载请求格式不正确")))
			return
		}
		if len(request.IDs) == 0 || len(request.IDs) > 50 {
			c.JSON(http.StatusBadRequest, wrapResponse(errors.New("每次请选择 1 到 50 个作品")))
			return
		}
		ids := make([]string, 0, len(request.IDs))
		seen := make(map[string]bool)
		for _, value := range request.IDs {
			id := strings.ToUpper(strings.TrimSpace(value))
			if !sourceIDPattern.MatchString(id) {
				c.JSON(http.StatusBadRequest, wrapResponse(errors.New("作品编号格式不正确: "+id)))
				return
			}
			if !seen[id] {
				ids = append(ids, id)
				seen[id] = true
			}
		}
		directory := strings.TrimSpace(request.Directory)
		if directory == "" {
			directory = dataFolder
		}
		absoluteDirectory, err := filepath.Abs(directory)
		if err != nil {
			c.JSON(http.StatusBadRequest, wrapResponse(errors.New("下载目录无效")))
			return
		}
		if err := os.MkdirAll(absoluteDirectory, 0755); err != nil {
			c.JSON(http.StatusBadRequest, wrapResponse(errors.New("无法创建下载目录: "+err.Error())))
			return
		}

		task := tasks.create("download", strings.Join(ids, "、"), len(ids))
		go runGUIDownloadTask(tasks, task.ID, ids, absoluteDirectory)
		c.JSON(http.StatusAccepted, wrapResponse(task))
	})
}

func runGUIDownloadTask(tasks *guiTaskStore, taskID string, ids []string, directory string) {
	tasks.update(taskID, func(task *guiTask) { task.Status = "running" })
	manager, err := engine.NewEngineManager()
	if err != nil {
		tasks.update(taskID, func(task *guiTask) {
			task.Status = "failed"
			task.Error = err.Error()
		})
		return
	}
	for _, id := range ids {
		if err := manager.DownloadMediaByBatchIds(context.Background(), []string{id}, directory); err != nil {
			tasks.update(taskID, func(task *guiTask) {
				task.Status = "failed"
				task.Error = err.Error()
			})
			return
		}
		tasks.update(taskID, func(task *guiTask) { task.Completed++ })
	}
	tasks.update(taskID, func(task *guiTask) { task.Status = "completed" })
}
