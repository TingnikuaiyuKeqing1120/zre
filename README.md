# ZRE — ZCode 模型配置可视化编辑器

一个单文件、零依赖的本地 GUI 工具，用于可视化编辑 ZCode 的模型配置文件 `~/.zcode/v2/config.json`：

- **管理模型提供商**：新增 / 编辑 / 删除 / 重命名 ID，修改名称、kind（`anthropic` / `openai-compatible` / `openai`）、Base URL、API Key、启用状态
- **配置模型**：添加 / 删除 / 重命名模型，编辑显示名、上下文窗口与最大输出（`limit`）、输入输出模态（`modalities`）
- **管理思考（reasoning）**：一键开关思考；编辑思考档位（`variants`，支持 low / medium / high / xhigh / max / off / enabled / none 或自定义值）；设定默认档位（`defaultVariant`）
- **从 API 拉取模型**：调用提供商的 `/models` 接口（OpenAI / Anthropic 格式）列出可用模型，勾选批量添加；显示名、上下文窗口、最大输出、模态会按官方模型目录自动预填，可逐行修改
- **思考档位模板**：把常用档位组合（如 low-high-max，默认 max）存为模板一键应用；内置 4 个常用模板，可新建 / 编辑 / 覆盖 / 删除（存于 `~/.zcode/v2/zre-templates.json`，不影响 zcode）；设置页列出常见档位便于对照
- **自动匹配官方档位**：自动探测 zcode 官方模型目录（`models_catalog*.json`），按模型 ID 精确 / 模糊匹配目录中的档位组合并一键写入（支持单个模型或整个提供商批量）
- **排序**：拖动 ⋮⋮ 手柄调整提供商显示顺序（同步 `model-provider-display-order.json`）以及提供商内模型的顺序
- **安全机制**：保存前自动备份；命名快照（保存 / 恢复 / 删除）；检测到配置被外部（如 zcode 本身）修改时提示冲突，绝不静默覆盖
- **其他**：全局搜索、原始 JSON 查看（高亮）、Ctrl+S 保存、脏状态提示

## 运行

需要 Go 1.24+（运行时零第三方依赖；构建 Windows exe 图标用到一次性工具 `akavel/rsrc`，`cmd/zre/*.syso` 已生成并随仓库携带）：

```bash
go run ./cmd/zre            # 默认编辑 ~/.zcode/v2/config.json 并自动打开浏览器
make build                  # 控制台版 bin/zre.exe
make build-tray             # 托盘版 bin/zre-tray.exe（无控制台窗口）
```

> 未安装 make 时直接执行 Makefile 中对应的 go build 命令即可。

### 托盘版

控制台窗口容易不小心被关掉。托盘版用 `-H windowsgui` 链接为 GUI 子系统（无控制台，**双击即进托盘**，无需参数），启动后常驻系统托盘：

- 左键 / 右键点击托盘图标 → 菜单：**打开页面** / **退出**；启动时会弹气泡提示
- 图标默认在任务栏溢出区（`^`）；想常显可在 设置 → 个性化 → 任务栏 → 其他系统托盘图标 中开启 ZRE
- 日志写入 `%LocalAppData%\zre\zre.log`（含托盘初始化逐步诊断，排查"图标不可见"先看这里）
- 已知坑：在 Git Bash / zcode 终端里 `TMP=/tmp`（POSIX 风格），依赖 `os.TempDir()` 会解析成不存在的 `C:	mp`——ZRE 已改用 `%LocalAppData%`，不受影响
- explorer 重启后托盘图标会自动重新注册（监听 TaskbarCreated）
- 其余参数与控制台版完全一致（`--config` / `--port` / `--catalog` 等）；控制台版也可用 `--tray` 手动进托盘模式

### 命令行参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--config` | `~/.zcode/v2/config.json` | 配置文件路径 |
| `--port` | `8765` | 监听端口（被占用时自动换随机端口，仅监听 127.0.0.1） |
| `--no-open` | 关 | 不自动打开浏览器 |
| `--catalog` | 自动探测 | 官方模型目录路径（逗号分隔）；来源优先级：此参数 > 设置文件 > `ZRE_CATALOG` 环境变量 > 常见安装位置 |
| `--tray` | 控制台版关 / 托盘版开 | 以系统托盘模式运行 |
| `--version` | | 打印版本号 |

## 自动匹配官方档位的机制

`🎯 自动匹配` 功能的数据来源是 zcode 自带的官方模型目录（`models_catalog*.json`，每个模型声明了 `reasoning.levels` 与 `defaultLevel`）：

1. **目录探测**：优先级为 `--catalog` 参数 → `ZRE_CATALOG` 环境变量 → 内置候选路径（常见 zcode 安装位置）。按钮 tooltip 与欢迎页会显示实际加载的来源路径。
2. **文件选择**：目录下存在多份带日期的目录文件时，只取**文件名最新**的一份——zcode 升级自带新目录后，重启 ZRE（或点 ⟳ 重新扫描）即自动跟随，无需改配置。
3. **三层匹配**（按可信度依次尝试）：
   - **精确**：模型 ID 归一化后直接命中（忽略大小写、去 `[按次]` 类前缀、取 `/` 后短 ID）
   - **系列**：按"系列名@版本号"匹配（如 `glm-5.3-flash` → `glm@5.3`），要求目录中同系列候选的档位组合完全一致，否则视为歧义不匹配
   - **模糊**：ID 互相包含且候选唯一（长度 ≥4），匹配结果会明确标注"模糊，请核对"
4. **运行中刷新**：zcode 更新目录文件后，点界面上的 ⟳ 按钮（或重启 ZRE）即可重新加载。

> 目录文件随 zcode 本地分发，ZRE 不做网络拉取；若官方未来提供在线目录源，可在 `internal/catalog` 扩展。

## 界面设置与生命周期

顶栏 **⚙ 设置** 集中了所有可配置项：

- **界面缩放**：80%–160% 八档 + "自动"（按窗口高度推断），存浏览器本地，即时生效——不同分辨率/系统缩放下都能调到舒适大小
- **生命周期**：*关闭所有网页后自动退出*（默认关）。开启后页面每 2 秒发送心跳，服务端 15 秒收不到任何存活标签页的心跳即自动退出；刷新页面不受影响
- **思考档位模板**：集中新建 / 删除，模型编辑页的"存为模板"与"应用"也在这里汇总
- **自动嗅探目录**：查看与修改官方目录路径（持久化到设置文件，保存即重扫）；被 `--catalog` 启动参数锁定时提示

顶栏 **⏻** 按钮：确认后立即结束 ZRE 进程（`POST /api/quit`），托盘图标一并移除。

## 安全设计

- **外科手术式编辑**：内置保序 JSON DOM，保存时不重排任何键序、不删除任何未知字段（zcode 或其他工具写入的自定义字段原样保留），缩进与末尾换行风格保持不变
- **保存 = 显式动作**：所有编辑先发生在内存中，点「保存」（或 Ctrl+S）才写入磁盘；写入前先把磁盘当前版本备份到 `~/.zcode/v2/backups/config_zre_时间戳.json`
- **冲突检测**：若 zcode 在编辑期间改写了配置文件，保存时会弹窗让你选择「覆盖磁盘」或「丢弃修改重新加载」
- **快照**：`~/.zcode/v2/snapshots/` 下的完整配置副本，恢复前同样会自动备份

> 注意：配置值中的思考档位只是 UI 声明，模型 API 实际支持哪些档位由后端决定（可参考 [zcode-reasoning-level-probe](https://github.com/Hello-Moeka/zcode-reasoning-level-probe) 的探测思路）。**保存后需重启 zcode 才会生效**（zcode 启动时读取配置）。

## 目录结构

```
zre/
├── cmd/zre/main.go              # 入口：参数解析、启动服务、打开浏览器
├── internal/
│   ├── jsondom/                 # 保序 JSON DOM（含回归测试）
│   ├── config/store.go          # 配置加载 / 视图 / 增删改 / 保存 / 备份 / 冲突检测
│   ├── snapshot/snapshot.go     # 命名快照管理
│   └── server/                  # HTTP API + 内嵌 Web UI（web/）
├── web -> internal/server/web   # 前端（原生 HTML/CSS/JS，经 go:embed 打进二进制）
├── Makefile
└── README.md
```

## 开发

```bash
make test      # 单元测试
make vet       # 静态检查
make release   # 交叉编译 windows/darwin/linux (amd64+arm64) 到 dist/
```

前端无构建步骤：直接修改 `internal/server/web/` 下的文件后重新 `go build` 即可（资产经 `go:embed` 打包）。

## 致谢

设计思路参考 [Hello-Moeka/zcode-reasoning-level-probe](https://github.com/Hello-Moeka/zcode-reasoning-level-probe)（AGPL-3.0）：其"探测各模型真实思考档位"的思路催生了本工具的自动匹配功能。ZRE 未复制其任何代码，实现（Go / 保序 JSON DOM / Web UI）完全独立。

## HTTP API（供脚本调用）

服务监听 `127.0.0.1`，全部为 JSON 接口：

- `GET /api/state` — 完整状态（提供商 / 模型 / 统计 / 快照列表）
- `GET /api/raw` — 当前配置的格式化文本
- `POST /api/save` `{force}` — 保存（自动备份；外部修改时返回 409 冲突）
- `POST /api/reload` — 丢弃内存修改，从磁盘重载
- `POST /api/provider/{add,update,delete,rename-id,reorder,autoreason-all}`
- `POST /api/model/{add,update,delete,rename-id,reorder,autoreason}`
- `POST /api/snapshot/{save,restore,delete}`
- `POST /api/template/{save,delete}` — 思考档位模板
- `POST /api/heartbeat` / `POST /api/quit` — 页面心跳与优雅退出
- `POST /api/settings/update` — 设置（自动退出开关、嗅探目录路径）
- `POST /api/catalog/rescan` — 重新加载官方目录
