package db

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PremiumUntil returns when a user's premium ends; ok is false if they have
// never had premium.
func PremiumUntil(ctx context.Context, pool *pgxpool.Pool, userID int64) (until time.Time, ok bool, err error) {
	var t *time.Time
	err = pool.QueryRow(ctx, `select premium_until from users where user_id = $1`, userID).Scan(&t)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && t == nil) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return *t, true, nil
}

// IsPremium reports whether a user currently has premium.
func IsPremium(ctx context.Context, pool *pgxpool.Pool, userID int64) (bool, error) {
	until, ok, err := PremiumUntil(ctx, pool, userID)
	return ok && until.After(time.Now()), err
}

// Payment is a completed Telegram Stars payment.
type Payment struct {
	ChargeID string
	UserID   int64
	Amount   int
	Currency string
	Payload  string
	Days     int
}

// RecordPremiumPayment stores a payment and extends the user's premium by
// p.Days. It is idempotent: a charge ID seen before changes nothing and
// returns applied=false. until is the resulting premium end.
func RecordPremiumPayment(ctx context.Context, pool *pgxpool.Pool, p Payment) (until time.Time, applied bool, err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return time.Time{}, false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	cmd, err := tx.Exec(ctx, `
		insert into payments (charge_id, user_id, amount, currency, payload, days)
		values ($1, $2, $3, $4, $5, $6) on conflict (charge_id) do nothing
	`, p.ChargeID, p.UserID, p.Amount, p.Currency, p.Payload, p.Days)
	if err != nil {
		return time.Time{}, false, err
	}
	applied = cmd.RowsAffected() > 0
	if applied {
		// Ensure the user row exists (payments come from users who talked to the bot).
		if _, err := tx.Exec(ctx, `insert into users (user_id) values ($1) on conflict do nothing`, p.UserID); err != nil {
			return time.Time{}, false, err
		}
		// Extend from now, or from the current end if still active.
		if _, err := tx.Exec(ctx, `
			update users set premium_until = greatest(coalesce(premium_until, now()), now()) + make_interval(days => $2)
			where user_id = $1
		`, p.UserID, p.Days); err != nil {
			return time.Time{}, false, err
		}
	}
	if err := tx.QueryRow(ctx, `select premium_until from users where user_id = $1`, p.UserID).Scan(&until); err != nil {
		return time.Time{}, false, err
	}
	return until, applied, tx.Commit(ctx)
}

// GetPayment looks up a payment by charge ID.
func GetPayment(ctx context.Context, pool *pgxpool.Pool, chargeID string) (*Payment, bool, error) {
	var p Payment
	var refunded bool
	err := pool.QueryRow(ctx, `select charge_id, user_id, amount, currency, payload, days, refunded from payments where charge_id = $1`, chargeID).
		Scan(&p.ChargeID, &p.UserID, &p.Amount, &p.Currency, &p.Payload, &p.Days, &refunded)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &p, !refunded, nil
}

// MarkRefunded flags a payment as refunded and takes back its days.
func MarkRefunded(ctx context.Context, pool *pgxpool.Pool, chargeID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var userID int64
	var days int
	err = tx.QueryRow(ctx, `update payments set refunded = true where charge_id = $1 and not refunded returning user_id, days`, chargeID).Scan(&userID, &days)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `update users set premium_until = premium_until - make_interval(days => $2) where user_id = $1`, userID, days); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Retention summarizes user activity for /stats.
type Retention struct {
	NewUsers7d    int // first seen in the last 7 days
	ActiveUsers7d int // seen in the last 7 days
	Returning7d   int // active in the last 7 days and first seen before that
	ActivePremium int
	StarsRevenue  int // total Stars received, minus refunds
	PaymentsTotal int
}

func GetRetention(ctx context.Context, pool *pgxpool.Pool) (Retention, error) {
	var r Retention
	err := pool.QueryRow(ctx, `
		select
			count(*) filter (where first_seen >= now() - interval '7 days'),
			count(*) filter (where last_seen >= now() - interval '7 days'),
			count(*) filter (where last_seen >= now() - interval '7 days' and first_seen < now() - interval '7 days'),
			count(*) filter (where premium_until > now())
		from users
	`).Scan(&r.NewUsers7d, &r.ActiveUsers7d, &r.Returning7d, &r.ActivePremium)
	if err != nil {
		return r, err
	}
	err = pool.QueryRow(ctx, `
		select coalesce(sum(amount) filter (where not refunded and currency = 'XTR'), 0), count(*) from payments
	`).Scan(&r.StarsRevenue, &r.PaymentsTotal)
	return r, err
}
