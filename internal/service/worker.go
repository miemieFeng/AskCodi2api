package service

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/database"
)

func StartBackgroundWorker(ctx context.Context, db *sqlx.DB, regSvc *RegistrationService, proxyMgr *ProxyManager, acctMgr *AccountManager, logger *Logger) {
	for {
		intervalMinutes := 60

		func() {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("[Worker Error] panic: %v\n", r)
				}
			}()

			// Count active accounts
			var activeCount int
			if err := db.Get(&activeCount, "SELECT COUNT(*) FROM accounts WHERE status = 'Active'"); err != nil {
				fmt.Printf("[Worker Error] count accounts: %v\n", err)
				return
			}

			// Fetch system config
			var cfg database.SystemConfig
			if err := db.Get(&cfg, "SELECT * FROM system_config LIMIT 1"); err != nil {
				fmt.Printf("[Worker Error] fetch config: %v\n", err)
				return
			}

			// Auto registration
			if activeCount < cfg.MinAccountThreshold {
				fmt.Printf("[Worker] Active accounts (%d) below threshold (%d). Running batch registration of %d accounts with concurrency %d...\n",
					activeCount, cfg.MinAccountThreshold, cfg.BatchSize, cfg.Concurrency)
				regSvc.RunBatchRegistration(cfg.BatchSize, cfg.Concurrency)
			}
		}()

		fmt.Printf("[Worker] Sleeping for %d minutes...\n", intervalMinutes)
		select {
		case <-ctx.Done():
			fmt.Println("[Worker] Shutting down...")
			return
		case <-time.After(time.Duration(intervalMinutes) * time.Minute):
		}
	}
}
