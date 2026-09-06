// ZRE — ZCode 模型配置可视化编辑器。
//
// 启动一个仅监听 127.0.0.1 的本地服务，编辑 ~/.zcode/v2/config.json，
// 并自动打开浏览器进入编辑界面。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"zre/internal/catalog"
	"zre/internal/config"
	"zre/internal/server"
	"zre/internal/tray"
)

var version = "0.1.1"

// defaultTray 由构建注入：托盘版（-H windowsgui）构建时设为 true，
// 使 bin/zre-tray.exe 双击即进入托盘模式；控制台版保持 false。
var defaultTray = "false"

func main() {
	home, _ := os.UserHomeDir()
	defPath := "config.json"
	if home != "" {
		defPath = home + "/.zcode/v2/config.json"
	}

	cfgPath := flag.String("config", defPath, "zcode 配置文件路径")
	port := flag.Int("port", 8765, "监听端口（占用时自动换随机端口）")
	noOpen := flag.Bool("no-open", false, "不自动打开浏览器")
	trayMode := flag.Bool("tray", defaultTray == "true", "以系统托盘模式运行（托盘版构建默认开启；控制台版可用 --tray 手动开启）")
	catalogFlag := flag.String("catalog", "", "官方模型目录路径（models_catalog*.json 文件或所在目录，多个用逗号分隔；默认自动探测 ZRE_CATALOG 环境变量与常见安装位置）")
	showVer := flag.Bool("version", false, "打印版本号")
	flag.Parse()

	if *showVer {
		fmt.Println("zre", version)
		return
	}

	st, err := config.Open(*cfgPath)
	if err != nil {
		log.Fatalf("无法打开配置: %v", err)
	}

	catPaths, fromCLI := resolveCatalogPaths(*catalogFlag, st.Settings().CatalogPaths)
	loadCatalogIndex(catPaths)
	sv := server.New(st, server.Options{
		CatalogPaths:   catPaths,
		CatalogFromCLI: fromCLI,
		Quit:           func() { os.Exit(0) },
	})
	ln, addr := listen(*port)
	url := fmt.Sprintf("http://%s", addr)

	fmt.Printf("ZRE v%s — ZCode 模型配置可视化编辑器\n", version)
	fmt.Printf("配置文件: %s\n", st.ConfigPath())
	fmt.Printf("界面地址: %s\n", url)

	// 托盘模式下控制台可能不可见，日志转存到可靠缓存目录
	//（不能用 os.TempDir()：Git Bash / zcode 终端里 TMP=/tmp 会解析成不存在的 C:\tmp）
	if *trayMode {
		logPath := trayLogPath()
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Printf("托盘模式运行中（日志写入失败 %v）\n", err)
		} else {
			fmt.Fprintf(f, "\n==== %s ZRE v%s 启动 ====\n", time.Now().Format("2006-01-02 15:04:05"), version)
			log.SetOutput(f)
			fmt.Printf("托盘模式运行中；日志: %s\n", logPath)
		}
	}

	if !*noOpen {
		go openBrowser(url)
	}

	srv := &http.Server{
		Handler:           sv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.Serve(ln); err != nil {
			log.Printf("服务退出: %v", err)
		}
	}()

	if *trayMode {
		if err := tray.Run(tray.Options{
			Tooltip: "ZRE — ZCode 模型配置编辑器",
			OnOpen:  func() { openBrowser(url) },
			OnQuit:  func() { os.Exit(0) },
		}); err != nil {
			log.Fatalf("托盘初始化失败: %v", err)
		}
		os.Exit(0)
	}

	fmt.Println("按 Ctrl+C 退出（界面关闭不影响配置，所有修改需显式保存）")
	select {}
}

// trayLogPath 返回托盘模式日志文件路径（%LocalAppData%\zre\zre.log）。
func trayLogPath() string {
	if base, err := os.UserCacheDir(); err == nil {
		d := filepath.Join(base, "zre")
		if os.MkdirAll(d, 0o755) == nil {
			return filepath.Join(d, "zre.log")
		}
	}
	return filepath.Join(".", "zre.log")
}

// resolveCatalogPaths 决定官方目录来源，优先级：
// --catalog 启动参数 > ZRE 设置文件 > ZRE_CATALOG 环境变量 > 内置候选路径。
func resolveCatalogPaths(flagVal string, settingPaths []string) ([]string, bool) {
	split := func(list string) []string {
		var out []string
		for _, p := range strings.Split(list, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, expandHome(p))
			}
		}
		return out
	}
	if flagVal != "" {
		return split(flagVal), true
	}
	if len(settingPaths) > 0 {
		log.Printf("官方模型目录来源：ZRE 设置文件（%s）", strings.Join(settingPaths, " ; "))
		return settingPaths, false
	}
	if env := os.Getenv("ZRE_CATALOG"); env != "" {
		log.Printf("官方模型目录来源：ZRE_CATALOG 环境变量")
		return split(env), false
	}
	var paths []string
	if home, err := os.UserHomeDir(); err == nil {
		// 常见安装位置：不写死任何个人的父目录名，用通配探测 <home>\*\zcodeesources\model-providers
		if matches, _ := filepath.Glob(filepath.Join(home, "*", "zcode", "resources", "model-providers")); len(matches) > 0 {
			paths = append(paths, matches...)
		}
		paths = append(paths, filepath.Join(home, ".zcode", "resources", "model-providers"))
	}
	return paths, false
}

// loadCatalogIndex 加载官方模型目录，失败不致命（只影响自动匹配功能）。
func loadCatalogIndex(paths []string) *catalog.Index {
	ix, err := catalog.LoadPaths(paths)
	if err != nil {
		log.Printf("官方模型目录加载失败（自动匹配功能不可用）: %v", err)
		return nil
	}
	if ix.Size() == 0 {
		log.Println("未找到官方模型目录（models_catalog*.json），自动匹配档位功能不可用；可在设置中配置或用 --catalog 指定路径")
	} else {
		log.Printf("官方模型目录已加载：%d 个带档位的模型", ix.Size())
	}
	return ix
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}

func listen(preferred int) (net.Listener, string) {
	p := fmt.Sprintf("127.0.0.1:%d", preferred)
	if ln, err := net.Listen("tcp", p); err == nil {
		return ln, p
	}
	log.Printf("端口 %d 被占用，改用随机端口", preferred)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatalf("无法监听端口: %v", err)
	}
	return ln, ln.Addr().String()
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		fmt.Printf("请手动打开: %s\n", url)
	}
}
