package database

import "github.com/jmoiron/sqlx"

func RunMigrations(db *sqlx.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS accounts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			password TEXT NOT NULL,
			access_token TEXT DEFAULT '',
			workspace_id TEXT DEFAULT '',
			project_id TEXT DEFAULT '',
			user_id TEXT DEFAULT '',
			api_key TEXT DEFAULT '',
			referral_code TEXT DEFAULT '',
			status TEXT DEFAULT 'Active',
			tokens_remaining INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS proxies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			url TEXT UNIQUE NOT NULL,
			status TEXT DEFAULT 'Active',
			fail_count INTEGER DEFAULT 0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS system_config (
			id INTEGER PRIMARY KEY,
			batch_size INTEGER DEFAULT 5,
			concurrency INTEGER DEFAULT 2,
			auto_register_interval_minutes INTEGER DEFAULT 60,
			min_account_threshold INTEGER DEFAULT 10,
			gptmail_api_key TEXT DEFAULT 'gpt-test',
			proxy_enabled BOOLEAN DEFAULT 1,
			initial_referral_code TEXT DEFAULT '',
			updated_at DATETIME
		)`,
	}

	for _, ddl := range tables {
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
	}

	// Ensure proxy_enabled column exists (for migration from Python version DB)
	_, _ = db.Exec("ALTER TABLE system_config ADD COLUMN proxy_enabled BOOLEAN DEFAULT 1")
	// Migration: add referral_code to accounts
	_, _ = db.Exec("ALTER TABLE accounts ADD COLUMN referral_code TEXT DEFAULT ''")
	// Migration: add initial_referral_code to system_config
	_, _ = db.Exec("ALTER TABLE system_config ADD COLUMN initial_referral_code TEXT DEFAULT ''")
	// Migration: add zenproxy config to system_config
	_, _ = db.Exec("ALTER TABLE system_config ADD COLUMN zenproxy_url TEXT DEFAULT 'http://cn.azt.cc:13000'")
	_, _ = db.Exec("ALTER TABLE system_config ADD COLUMN zenproxy_api_key TEXT DEFAULT ''")

	// Fix NULL defaults for proxies migrated from Python version
	db.Exec("UPDATE proxies SET status = 'Active' WHERE status IS NULL OR status = ''")
	db.Exec("UPDATE proxies SET fail_count = 0 WHERE fail_count IS NULL")

	// Seed default config if not exists
	var count int
	if err := db.Get(&count, "SELECT COUNT(*) FROM system_config WHERE id = 1"); err != nil {
		return err
	}
	if count == 0 {
		_, err := db.Exec(`INSERT INTO system_config (id, batch_size, concurrency, auto_register_interval_minutes, min_account_threshold, gptmail_api_key, proxy_enabled)
			VALUES (1, 5, 2, 60, 10, 'gpt-test', 1)`)
		if err != nil {
			return err
		}
	}
	return nil
}
