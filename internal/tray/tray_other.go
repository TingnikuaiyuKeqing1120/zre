//go:build !windows

// Package tray：非 Windows 平台的占位实现（无系统托盘，阻塞保持进程运行）。
package tray

type Options struct {
	Tooltip string
	OnOpen  func()
	OnQuit  func()
}

// Run 阻塞直到进程被外部信号终止（服务在另一 goroutine 中继续运行）。
func Run(o Options) error {
	<-make(chan struct{})
	return nil
}
