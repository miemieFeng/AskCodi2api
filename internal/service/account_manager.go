package service

import (
	"github.com/jmoiron/sqlx"
	"github.com/jonasen/askcodi-go/internal/database"
)

type AccountManager struct {
	db *sqlx.DB
}

func NewAccountManager(db *sqlx.DB) *AccountManager {
	return &AccountManager{db: db}
}

// GetActiveAccount selects the active account with the most remaining tokens (priority strategy).
func (am *AccountManager) GetActiveAccount(excludeIDs []int64) (*database.Account, error) {
	query := "SELECT * FROM accounts WHERE status = 'Active'"
	args := []interface{}{}

	if len(excludeIDs) > 0 {
		placeholders := ""
		for i, id := range excludeIDs {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, id)
		}
		query += " AND id NOT IN (" + placeholders + ")"
	}

	query += " ORDER BY tokens_remaining DESC LIMIT 1"

	var account database.Account
	if err := am.db.Get(&account, query, args...); err != nil {
		return nil, err
	}
	return &account, nil
}

func (am *AccountManager) DisableAccount(accountID int64, reason string) error {
	_, err := am.db.Exec(
		"UPDATE accounts SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		reason, accountID,
	)
	return err
}

func (am *AccountManager) UpdateAccountQuota(accountID int64, tokensRemaining int64) error {
	_, err := am.db.Exec(
		"UPDATE accounts SET tokens_remaining = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?",
		tokensRemaining, accountID,
	)
	return err
}

func (am *AccountManager) CountActiveAccounts() (int, error) {
	var count int
	err := am.db.Get(&count, "SELECT COUNT(*) FROM accounts WHERE status = 'Active'")
	return count, err
}
