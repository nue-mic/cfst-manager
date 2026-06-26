// 命令 cfstmgrd 是 cfst-manager 的守护进程：提供 CDN 优选 IP 测速的 Web 图形界面、
// 管理 REST API、SSE 实时进度，以及与 wetest.vip/hostmonit 兼容的对外优选 IP API。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nue-mic/cfst-manager/internal/api"
	"github.com/nue-mic/cfst-manager/internal/appcfg"
	"github.com/nue-mic/cfst-manager/internal/eventbus"
	"github.com/nue-mic/cfst-manager/internal/runner"
	"github.com/nue-mic/cfst-manager/internal/scheduler"
	"github.com/nue-mic/cfst-manager/internal/store"
	"github.com/nue-mic/cfst-manager/internal/version"
)

func main() {
	cmd := "serve"
	if len(os.Args) >= 2 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		os.Exit(runServe())
	case "health":
		os.Exit(runHealth(os.Args[2:]))
	case "version", "-v", "--version":
		fmt.Printf("cfstmgrd %s (built %s)\n", version.Number, version.BuildDate)
	case "help", "-h", "--help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `cfstmgrd — CDN 优选 IP 测速 Web 管理守护进程

用法:
  cfstmgrd <命令>

命令:
  serve     启动 HTTP 服务（默认）
  health    探测 /api/v1/health，失败则非零退出
  version   打印版本
  help      显示帮助

环境变量:
  CFST_HTTP_ADDR      监听地址 (默认 :18123)
  CFST_API_TOKEN      管理端 Bearer 令牌 (留空则自动生成并打印)
  CFST_DATA_DIR       数据目录 (默认 data)
  CFST_CORS_ORIGINS   允许跨域来源, 逗号分隔或 * (默认 *)
  CFST_LOG_LEVEL      trace|debug|info|warn|error (默认 info)
  CFST_DOCS_ENABLED   是否暴露 /api/docs (默认 true)
  CFST_PUBLIC_OPEN    公开模式:对外API接受任意key (默认 false)`)
}

func runServe() int {
	cfg := appcfg.Load()
	if err := cfg.EnsureDirs(); err != nil {
		fmt.Fprintf(os.Stderr, "创建数据目录失败: %v\n", err)
		return 1
	}

	levelVar := new(slog.LevelVar)
	levelVar.Set(appcfg.ParseLevel(cfg.LogLevel))
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: levelVar}))

	if cfg.HTTPAddrWarn != "" {
		logger.Warn("监听地址告警", slog.String("detail", cfg.HTTPAddrWarn))
	}

	token, generated, err := cfg.EnsureAPIToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化管理令牌失败: %v\n", err)
		return 1
	}
	if generated {
		logger.Warn("已自动生成管理端令牌（请妥善保存，可用于登录 Web 控制台）",
			slog.String("token", token))
	}

	st := store.New(cfg.MetaFile, cfg.RunsDir)
	if err := st.Load(); err != nil {
		fmt.Fprintf(os.Stderr, "加载数据失败: %v\n", err)
		return 1
	}
	sources, err := store.NewSourceManager(cfg.IPSourceDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化 IP 源失败: %v\n", err)
		return 1
	}

	bus := eventbus.New()
	defer bus.Stop()
	run := runner.New(st, bus, logger)

	sched := scheduler.New(st, sources, run, logger)
	sched.Start()
	defer sched.Stop()

	handler := api.NewRouter(api.Deps{
		Cfg: cfg, Logger: logger, Store: st, Sources: sources,
		Runner: run, Scheduler: sched, Bus: bus, Version: version.Number,
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	logger.Info("启动 cfstmgrd",
		slog.String("addr", cfg.HTTPAddr),
		slog.String("data_dir", cfg.DataDir),
		slog.String("version", version.Number),
		slog.Bool("public_open", cfg.PublicOpen),
	)

	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		logger.Info("收到退出信号", slog.String("signal", sig.String()))
	case err := <-errCh:
		logger.Error("HTTP 服务异常退出", slog.Any("err", err))
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownWait)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("优雅关闭失败", slog.Any("err", err))
		return 1
	}
	logger.Info("已退出")
	return 0
}

func runHealth(args []string) int {
	addr := "http://127.0.0.1:18123"
	if len(args) >= 1 {
		addr = args[0]
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(addr + "/api/v1/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "健康检查失败: %v\n", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "不健康: status=%d\n", resp.StatusCode)
		return 1
	}
	return 0
}
