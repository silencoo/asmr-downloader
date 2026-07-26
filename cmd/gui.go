package cmd

import (
	"asmroner/internal/logger"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
)

var guiBinary string

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "启动 ASMRoner 原生桌面应用",
	RunE: func(cmd *cobra.Command, args []string) error {
		binary := guiBinary
		if binary == "" {
			current, err := os.Executable()
			if err != nil {
				return err
			}
			name := "ASMRoner"
			if runtime.GOOS == "windows" {
				name += ".exe"
			}
			candidates := []string{
				filepath.Join(filepath.Dir(current), name),
				filepath.Join(filepath.Dir(current), "dist", name),
				filepath.Join("dist", name),
			}
			for _, candidate := range candidates {
				if _, err := os.Stat(candidate); err == nil {
					binary = candidate
					break
				}
			}
		}
		if binary == "" {
			return fmt.Errorf("未找到桌面程序，请先运行 desktop/build.ps1 构建 ASMRoner.exe")
		}
		process := exec.Command(binary)
		if err := process.Start(); err != nil {
			return fmt.Errorf("启动桌面程序失败: %w", err)
		}
		logger.Success("ASMRoner 桌面端已启动")
		return nil
	},
}

func init() {
	guiCmd.Flags().StringVar(&guiBinary, "binary", "", "桌面程序路径")
	rootCmd.AddCommand(guiCmd)
}
