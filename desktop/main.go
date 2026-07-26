package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var desktopAssets embed.FS

func main() {
	app := NewApp()
	err := wails.Run(&options.App{
		Title:                    "ASMRoner",
		Width:                    1280,
		Height:                   800,
		MinWidth:                 940,
		MinHeight:                640,
		WindowStartState:         options.Normal,
		BackgroundColour:         &options.RGBA{R: 255, G: 245, B: 248, A: 255},
		AssetServer:              &assetserver.Options{Assets: desktopAssets, Handler: app},
		OnStartup:                app.startup,
		OnShutdown:               app.shutdown,
		EnableDefaultContextMenu: false,
		Bind:                     []interface{}{app},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId:               "io.asmroner.desktop",
			OnSecondInstanceLaunch: app.secondInstance,
		},
		Windows: &windows.Options{
			Theme: windows.SystemDefault,
			CustomTheme: &windows.ThemeSettings{
				DarkModeTitleBar:           windows.RGB(255, 245, 248),
				DarkModeTitleBarInactive:   windows.RGB(249, 232, 239),
				DarkModeTitleText:          windows.RGB(56, 38, 47),
				DarkModeTitleTextInactive:  windows.RGB(130, 107, 118),
				DarkModeBorder:             windows.RGB(236, 212, 223),
				DarkModeBorderInactive:     windows.RGB(236, 212, 223),
				LightModeTitleBar:          windows.RGB(255, 245, 248),
				LightModeTitleBarInactive:  windows.RGB(249, 232, 239),
				LightModeTitleText:         windows.RGB(56, 38, 47),
				LightModeTitleTextInactive: windows.RGB(130, 107, 118),
				LightModeBorder:            windows.RGB(236, 212, 223),
				LightModeBorderInactive:    windows.RGB(236, 212, 223),
			},
			BackdropType:         windows.Mica,
			IsZoomControlEnabled: false,
			DisablePinchZoom:     true,
			WindowClassName:      "ASMRonerDesktop",
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
