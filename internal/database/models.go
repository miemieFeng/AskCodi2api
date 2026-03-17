package database

import "time"

type Account struct {
	ID              int64      `db:"id" json:"id"`
	Email           string     `db:"email" json:"email"`
	Password        string     `db:"password" json:"-"`
	AccessToken     string     `db:"access_token" json:"-"`
	WorkspaceID     string     `db:"workspace_id" json:"-"`
	ProjectID       string     `db:"project_id" json:"-"`
	UserID          string     `db:"user_id" json:"-"`
	APIKey          string     `db:"api_key" json:"-"`
	ReferralCode    string     `db:"referral_code" json:"-"`
	Status          string     `db:"status" json:"status"`
	TokensRemaining int64      `db:"tokens_remaining" json:"tokens_remaining"`
	CreatedAt       time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt       *time.Time `db:"updated_at" json:"updated_at"`
}

type Proxy struct {
	ID        int64      `db:"id" json:"id"`
	URL       string     `db:"url" json:"url"`
	Status    string     `db:"status" json:"status"`
	FailCount int        `db:"fail_count" json:"fail_count"`
	CreatedAt time.Time  `db:"created_at" json:"created_at"`
	UpdatedAt *time.Time `db:"updated_at" json:"updated_at"`
}

type SystemConfig struct {
	ID                          int64      `db:"id" json:"id"`
	BatchSize                   int        `db:"batch_size" json:"batch_size"`
	Concurrency                 int        `db:"concurrency" json:"concurrency"`
	AutoRegisterIntervalMinutes int        `db:"auto_register_interval_minutes" json:"auto_register_interval_minutes"`
	MinAccountThreshold         int        `db:"min_account_threshold" json:"min_account_threshold"`
	GptmailAPIKey               string     `db:"gptmail_api_key" json:"gptmail_api_key"`
	ProxyEnabled                bool       `db:"proxy_enabled" json:"proxy_enabled"`
	InitialReferralCode         string     `db:"initial_referral_code" json:"initial_referral_code"`
	ZenProxyURL                 string     `db:"zenproxy_url" json:"zenproxy_url"`
	ZenProxyAPIKey              string     `db:"zenproxy_api_key" json:"zenproxy_api_key"`
	UpdatedAt                   *time.Time `db:"updated_at" json:"updated_at"`
}
