package main

import (
	"asmroner/internal/model"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (a *App) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !strings.HasPrefix(request.URL.Path, "/media/") {
		http.NotFound(response, request)
		return
	}
	encoded := strings.TrimPrefix(request.URL.Path, "/media/")
	relative, err := url.PathUnescape(encoded)
	if err != nil {
		http.Error(response, "invalid media path", http.StatusBadRequest)
		return
	}
	root, err := filepath.Abs(model.AppConfig.Downloader.SyncDataFolder)
	if err != nil {
		http.Error(response, "invalid media root", http.StatusInternalServerError)
		return
	}
	target, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil || (target != root && !strings.HasPrefix(target, root+string(os.PathSeparator))) {
		http.Error(response, "media path is outside the library", http.StatusForbidden)
		return
	}
	file, err := os.Open(target)
	if err != nil {
		http.NotFound(response, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(response, request)
		return
	}
	http.ServeContent(response, request, info.Name(), info.ModTime(), file)
}
