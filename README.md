# ASMRoner

面向 Windows 的 ASMR 音声浏览、在线播放、下载与本地管理工具。项目以原生桌面应用为主要入口，同时保留完整 CLI 和兼容 Web 播放模式。

桌面端直接调用公开 API，以本地界面呈现作品、热门、社团、标签、声优、随机、登录与 About；点击作品即可进入详情、浏览分层音轨并在线播放，不是 iframe 网页套壳。

![ASMRoner 桌面端预览](dist/desktop-preview.png)

## 快速开始

### 直接使用 Windows 桌面版

运行构建产物：

```powershell
.\dist\ASMRoner.exe
```

首次启动后在“设置”中确认账号、下载目录和网络选项。公开服务可使用默认 `guest` 账号。

### 从源码构建

需要 Go 1.25、Wails CLI 和 WebView2 Runtime。

```powershell
go mod download
powershell -ExecutionPolicy Bypass -File .\desktop\build.ps1
.\dist\ASMRoner.exe
```

构建脚本会生成 `dist/ASMRoner.exe`。桌面资源嵌入 EXE，正常使用时不会启动本地 HTTP 服务或外部浏览器。

## 桌面端一览

| 功能 | 说明 |
| --- | --- |
| 原生站点浏览 | 作品、热门、社团、标签、声优、随机、登录和 About 横向标签 |
| 作品详情 | 封面、评分、社团、声优、标签、时长、下载量和分层文件列表 |
| 在线播放 | 直接播放站点音轨，自动组成播放队列并在曲目结束后继续 |
| 下载管理 | 单部或批量加入队列，显示进度、完成、失败与取消状态 |
| 本地资料库 | 扫描已下载作品，按作品查看和播放本地音轨 |
| 响应式密度 | 可选每页 20 / 30 / 40 / 50 部；全屏时增加列数而不拉伸模糊封面 |
| 桌面体验 | 粉色原生标题栏、单实例保护、系统目录选择器和正式应用图标 |

<details>
<summary><strong>查看完整功能列表</strong></summary>

### 搜索与浏览

- 支持 RJ / BJ / VJ 编号、标题、社团、标签和声优搜索
- 支持作品、热门和随机发现
- 支持上一页、下一页、指定页码和每页数量
- 社团、标签、声优目录支持本地筛选和分页
- About 原生展示 DMCA、Bug Bounty、联系方式和镜像

### 下载与同步

- 单个编号、批量编号、搜索结果和热门作品下载
- 自定义保存目录、并发数、重试次数、QPS 和代理
- 可配置优先媒体格式、文件夹命名和非法字符清理
- 下载状态跟踪、失败任务重试、统计报告和记录导出

### 本地播放

- 自动扫描本地作品与音轨
- 桌面底部播放器和连续播放队列
- 保留 `listen` 命令提供兼容 Web 播放界面

</details>

<details>
<summary><strong>CLI 使用方法</strong></summary>

### 初始化配置

```bash
./asmroner config
```

### 搜索

```bash
# 基本搜索
./asmroner search "护士" -c 20

# 高级筛选
./asmroner search "护士,-中出@duration:1h" -c 50

# 搜索并下载
./asmroner search download "护士" -d ./downloads -s 20

# 导出 CSV / JSON
./asmroner search export "护士" -n 100 -f data.json
```

### 下载

```bash
# 单个作品
./asmroner download RJ01037721 -d ./downloads

# 批量作品
./asmroner download RJ01037721,RJ01037722,RJ01037723 -d ./downloads

# 热门作品
./asmroner download hot100 -n 20 -d ./downloads
```

### 同步与报告

```bash
./asmroner sync download --folder ./downloads
./asmroner sync retry --folder ./downloads
./asmroner sync export --status failed --file ./failed_downloads.csv
./asmroner sync report
```

### 启动桌面端或兼容 Web 播放器

```bash
./asmroner gui
./asmroner listen -p 8080 ./syncdata
```

</details>

<details>
<summary><strong>配置、目录与技术说明</strong></summary>

### 配置

配置使用 TOML 格式。桌面应用会把配置保存在当前用户的系统配置目录，默认下载位置为 `Downloads/ASMRoner`，不会依赖程序启动目录。

主要可配置项包括：

- asmr.one 账号与密码
- 自定义 API 地址和 HTTP / SOCKS5 代理
- 下载目录、并发、重试与 QPS
- 优先音频格式和文件夹命名
- 文件名清理和站点浏览每页数量

### 项目结构

```text
asmroner/
├── cmd/                  # CLI 命令
├── desktop/              # Wails 原生桌面应用
│   ├── frontend/dist/    # 嵌入式桌面 UI
│   └── build/            # Windows 图标与构建资源
├── internal/             # API、下载、数据库和模型
├── webui/                # 兼容 Web 播放界面
├── dist/                 # Windows 构建产物与桌面预览图
├── main.go
└── go.mod
```

### 技术栈

| 组件 | 用途 |
| --- | --- |
| Go 1.25 | 核心逻辑、API 客户端与下载器 |
| Wails v2 | Windows 原生窗口和 Go / UI 桥接 |
| WebView2 | 嵌入式桌面界面渲染 |
| SQLite / GORM | 本地数据与同步状态 |
| Cobra / Viper | CLI 与配置 |
| Gin | 兼容 Web 模式 |

</details>

<details>
<summary><strong>常见问题</strong></summary>

### 桌面端无法启动

确认系统已安装 Microsoft Edge WebView2 Runtime，并尝试从终端运行 `dist/ASMRoner.exe` 查看错误。

### 搜索或详情读取失败

检查网络、代理和 API 设置。也可以先在“登录”标签重新验证账号。

### 下载失败

在“下载任务”查看错误；CLI 用户可运行 `asmroner sync retry`，并检查 `download_errors.log`。

### 找不到已下载作品

确认桌面设置中的保存位置正确。资料库会从该目录扫描作品。

</details>

## 许可证

本项目采用 [MIT License](LICENSE)。

## 来源与相关项目

本仓库是在 [fireinrain/asmr-downloader](https://github.com/fireinrain/asmr-downloader) 基础上的 fork 与二次开发。另可参考相关 Web 客户端 [asmr.furina.in](https://asmr.furina.in)。

ASMRoner 与 asmr.one 没有官方隶属关系。请遵守所在地法律、站点规则和内容版权要求。
