// Package app 把各个组件装配起来:打开数据库、启动 HTTP 服务与后台任务,并处理优雅退出。
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mb/internal/buildinfo"
	"mb/internal/config"
	"mb/internal/fx"
	"mb/internal/reminder"
	"mb/internal/store"
	"mb/internal/web"
)

// jobInterval 是后台任务(汇率更新、到期提醒)的检查间隔。
const jobInterval = time.Hour

// Run 启动服务并阻塞,直到收到退出信号或服务出错。
func Run() error {
	cfg := config.Load()

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := web.Bootstrap(ctx, st, cfg.AdminPassword); err != nil {
		return err
	}
	rates := fx.New(st)
	if err := rates.Init(ctx); err != nil {
		return err
	}
	handler, err := web.New(cfg, st, rates)
	if err != nil {
		return err
	}

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 发测试邮件、WebDAV 备份等操作要等外部服务响应(上限 45 秒),写超时需要留出余量。
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  2 * time.Minute,
	}

	go runJobs(ctx, rates, reminder.New(st))

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	log.Printf("mb %s listening on %s (db=%s)", buildinfo.DisplayVersion(), cfg.Addr, cfg.DBPath)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// runJobs 在启动时以及之后每隔 jobInterval 执行一轮后台任务,直到 ctx 结束。
func runJobs(ctx context.Context, rates *fx.Service, reminders *reminder.Service) {
	ticker := time.NewTicker(jobInterval)
	defer ticker.Stop()
	for {
		runJobsOnce(ctx, rates, reminders)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runJobsOnce(ctx context.Context, rates *fx.Service, reminders *reminder.Service) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	rates.RefreshIfStale(ctx)
	if err := reminders.Run(ctx); err != nil {
		log.Printf("reminder: %v", err)
	}
}

// Healthcheck 请求本机的健康检查接口,供容器的 HEALTHCHECK 使用。
func Healthcheck() error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.HealthcheckURL(os.Getenv("MB_ADDR")), nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck failed: %s", resp.Status)
	}
	return nil
}
