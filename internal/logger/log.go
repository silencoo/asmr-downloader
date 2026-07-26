// Package logger 提供基于 slog 的结构化、彩色输出。
//
// 终端输出使用 slog.TextHandler + fatih/color，对 ERROR/WARN/INFO/DEBUG 级别
// 分别着色；SUCCESS 通过 attr key=success=true 触发绿色。
// 错误日志文件单独维护在 FailedLogName（默认 download_errors.log），
// 由 RecordFailure 写入，线程安全。
package logger

import (
	"asmroner/internal/consts"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
)

var (
	mu           sync.Mutex
	errorLogFile *os.File
	errorLogger  *slog.Logger
	defaultLog   *slog.Logger // 终端默认 logger
)

// Init 初始化终端与错误日志 logger。logFile 为空时使用 consts.FailedLogName。
func Init(logFile string) {
	if logFile == "" {
		logFile = consts.FailedLogName
	}

	// 错误日志文件（追加 + 创建 + 仅写）
	var err error
	errorLogFile, err = os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		// 文件打不开不应阻断主程序，只用 stderr 兜底
		fmt.Fprintf(os.Stderr, "warning: open %s failed: %v\n", logFile, err)
		errorLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	} else {
		errorLogger = slog.New(slog.NewTextHandler(errorLogFile, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		}))
	}

	// 终端 handler：stderr 输出 + 自定义 ReplaceAttr 实现色彩
	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       slog.LevelInfo,
		ReplaceAttr: replaceAttrColor,
	})
	defaultLog = slog.New(handler)
	slog.SetDefault(defaultLog)

	// 当 stdout/stderr 不是 tty 时关闭色彩（写入管道或文件时不输出 ANSI 转义）
	if !isTerminal(os.Stderr) {
		color.NoColor = true
	}
}

// Close 关闭错误日志文件。
func Close() {
	mu.Lock()
	defer mu.Unlock()
	if errorLogFile != nil {
		_ = errorLogFile.Close()
		errorLogFile = nil
	}
}

// Info 记录 INFO 级别日志。
func Info(msg string, args ...any) {
	defaultLog.Info(msg, args...)
}

// Success 记录 SUCCESS 级别日志（绿色）。等价于 Info 但额外打 success=true attr。
func Success(msg string, args ...any) {
	args = append(args, slog.Bool("success", true))
	defaultLog.Info(msg, args...)
}

// Warn 记录 WARN 级别日志（黄色）。
func Warn(msg string, args ...any) {
	defaultLog.Warn(msg, args...)
}

// Error 记录 ERROR 级别日志（红色）。
func Error(msg string, args ...any) {
	defaultLog.Error(msg, args...)
}

// Debug 记录 DEBUG 级别日志（灰色）。
func Debug(msg string, args ...any) {
	defaultLog.Debug(msg, args...)
}

// Fatal 记录 ERROR 后 os.Exit(1)。
func Fatal(msg string, args ...any) {
	defaultLog.Error(msg, args...)
	os.Exit(1)
}

// RecordFailure 写入错误日志文件（线程安全）。
// 与 Error 不同：RecordFailure 走的是独立文件通道，便于事后聚合分析。
func RecordFailure(id, url, reason string) {
	if errorLogger == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	errorLogger.Error("download failure",
		slog.String("id", id),
		slog.String("url", url),
		slog.String("reason", reason),
	)
}

// replaceAttrColor 为 TextHandler 的输出加颜色。
// level -> ERROR=红 / WARN=黄 / INFO=默认色 / DEBUG=灰。
// success=true attr -> 整行变绿。
func replaceAttrColor(_ []string, a slog.Attr) slog.Attr {
	if a.Key == slog.LevelKey {
		if lvl, ok := a.Value.Any().(slog.Level); ok {
			var c *color.Color
			switch {
			case lvl >= slog.LevelError:
				c = color.New(color.FgRed)
			case lvl >= slog.LevelWarn:
				c = color.New(color.FgYellow)
			case lvl <= slog.LevelDebug:
				c = color.New(color.FgHiBlack) // 灰
			default:
				return a
			}
			a.Value = slog.StringValue(c.Sprint(strings.ToUpper(lvl.String())))
		}
	}
	return a
}

// isTerminal 简易 TTY 判断（不引入 golang.org/x/term，依赖最小化）。
func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}