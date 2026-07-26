package engine

import (
	"asmroner/internal/model"
	"context"
	"os"
	"testing"
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
