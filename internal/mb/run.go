package mb

import (
	"log"
	"net/http"
	"time"
)

// Run 启动 mb 应用。根目录 main.go 只调用这里,方便以后把 CLI / 服务模式拆开。
func Run() {
	loadDotEnv(".env")

	cfg := loadConfig()

	db, err := openDB(cfg.dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if err := migrate(db); err != nil {
		log.Fatal(err)
	}
	if err := ensureDefaults(db, cfg); err != nil {
		log.Fatal(err)
	}
	if err := validateWriteStatements(db); err != nil {
		log.Fatal(err)
	}

	a := &app{
		cfg:           cfg,
		db:            db,
		templates:     mustTemplates(),
		loginLimiter:  newLoginLimiter(cfg.loginMaxFailures, cfg.loginWindow, cfg.loginLockout, cfg.loginMaxEntries),
		settingsCache: settingsCacheFor(db),
	}

	go a.startReminderLoop()

	mux := http.NewServeMux()
	a.registerRoutes(mux)

	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           a.withCommon(mux),
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("mb listening on %s (db=%s)", cfg.addr, cfg.dbPath)
	log.Fatal(srv.ListenAndServe())
}
