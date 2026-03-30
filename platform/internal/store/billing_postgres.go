package store

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// UpsertTenant creates or updates a tenant record.
func (s *PostgresStore) UpsertTenant(ctx context.Context, t *TenantRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tenants (id, name, tier, stripe_customer_id, billing_email, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name,
			tier = EXCLUDED.tier,
			stripe_customer_id = EXCLUDED.stripe_customer_id,
			billing_email = EXCLUDED.billing_email,
			updated_at = NOW()
	`, t.ID, t.Name, t.Tier, t.StripeCustomerID, t.BillingEmail, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return fmt.Errorf("upsert tenant %s: %w", t.ID, err)
	}
	return nil
}

// GetTenant retrieves a tenant by ID.
func (s *PostgresStore) GetTenant(ctx context.Context, id string) (*TenantRecord, error) {
	var t TenantRecord
	var stripeID, email *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, tier, stripe_customer_id, billing_email, created_at, updated_at
		FROM tenants WHERE id = $1
	`, id).Scan(&t.ID, &t.Name, &t.Tier, &stripeID, &email, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("get tenant %s: %w", id, err)
	}
	if stripeID != nil {
		t.StripeCustomerID = *stripeID
	}
	if email != nil {
		t.BillingEmail = *email
	}
	return &t, nil
}

// ListTenants returns all tenants.
func (s *PostgresStore) ListTenants(ctx context.Context) ([]*TenantRecord, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, name, tier, stripe_customer_id, billing_email, created_at, updated_at FROM tenants ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	defer rows.Close()

	var tenants []*TenantRecord
	for rows.Next() {
		var t TenantRecord
		var stripeID, email *string
		if err := rows.Scan(&t.ID, &t.Name, &t.Tier, &stripeID, &email, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan tenant: %w", err)
		}
		if stripeID != nil {
			t.StripeCustomerID = *stripeID
		}
		if email != nil {
			t.BillingEmail = *email
		}
		tenants = append(tenants, &t)
	}
	return tenants, nil
}

// UpdateTenantTier changes a tenant's subscription tier.
func (s *PostgresStore) UpdateTenantTier(ctx context.Context, id, tier string) error {
	_, err := s.pool.Exec(ctx, `UPDATE tenants SET tier = $2, updated_at = NOW() WHERE id = $1`, id, tier)
	if err != nil {
		return fmt.Errorf("update tenant tier %s: %w", id, err)
	}
	return nil
}

// UpsertDailyUsage inserts or updates a daily usage counter.
func (s *PostgresStore) UpsertDailyUsage(ctx context.Context, tenantID string, date time.Time, metric string, count int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO usage_daily (tenant_id, date, metric, count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, date, metric) DO UPDATE SET
			count = EXCLUDED.count,
			updated_at = NOW()
	`, tenantID, date, metric, count)
	if err != nil {
		return fmt.Errorf("upsert daily usage %s/%s: %w", tenantID, metric, err)
	}
	return nil
}

// GetDailyUsage returns daily usage records for a tenant within a date range.
func (s *PostgresStore) GetDailyUsage(ctx context.Context, tenantID string, start, end time.Time) ([]*UsageDailyRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, date, metric, count, created_at, updated_at
		FROM usage_daily
		WHERE tenant_id = $1 AND date >= $2 AND date < $3
		ORDER BY date, metric
	`, tenantID, start, end)
	if err != nil {
		return nil, fmt.Errorf("get daily usage: %w", err)
	}
	defer rows.Close()

	var records []*UsageDailyRecord
	for rows.Next() {
		var r UsageDailyRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Date, &r.Metric, &r.Count, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan daily usage: %w", err)
		}
		records = append(records, &r)
	}
	return records, nil
}

// CreateInvoice persists a new invoice.
func (s *PostgresStore) CreateInvoice(ctx context.Context, inv *InvoiceRecord) error {
	lineItemsJSON, err := json.Marshal(inv.LineItems)
	if err != nil {
		return fmt.Errorf("marshal line items: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO invoices (id, tenant_id, period_start, period_end, tier, line_items, subtotal, total, currency, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, inv.ID, inv.TenantID, inv.PeriodStart, inv.PeriodEnd, inv.Tier, lineItemsJSON, inv.Subtotal, inv.Total, inv.Currency, inv.Status, inv.CreatedAt)
	if err != nil {
		return fmt.Errorf("create invoice %s: %w", inv.ID, err)
	}
	return nil
}

// GetInvoice retrieves an invoice by ID.
func (s *PostgresStore) GetInvoice(ctx context.Context, id string) (*InvoiceRecord, error) {
	var inv InvoiceRecord
	var lineItemsJSON []byte
	var paymentIntentID *string
	err := s.pool.QueryRow(ctx, `
		SELECT id, tenant_id, period_start, period_end, tier, line_items, subtotal, total, currency, status, payment_intent_id, created_at, paid_at
		FROM invoices WHERE id = $1
	`, id).Scan(&inv.ID, &inv.TenantID, &inv.PeriodStart, &inv.PeriodEnd, &inv.Tier, &lineItemsJSON, &inv.Subtotal, &inv.Total, &inv.Currency, &inv.Status, &paymentIntentID, &inv.CreatedAt, &inv.PaidAt)
	if err != nil {
		return nil, fmt.Errorf("get invoice %s: %w", id, err)
	}
	if paymentIntentID != nil {
		inv.PaymentIntentID = *paymentIntentID
	}
	if err := json.Unmarshal(lineItemsJSON, &inv.LineItems); err != nil {
		return nil, fmt.Errorf("unmarshal line items: %w", err)
	}
	return &inv, nil
}

// ListInvoices returns invoices for a tenant, ordered by period descending.
func (s *PostgresStore) ListInvoices(ctx context.Context, tenantID string, limit int) ([]*InvoiceRecord, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, tenant_id, period_start, period_end, tier, line_items, subtotal, total, currency, status, payment_intent_id, created_at, paid_at
		FROM invoices WHERE tenant_id = $1
		ORDER BY period_start DESC LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	defer rows.Close()

	var invoices []*InvoiceRecord
	for rows.Next() {
		var inv InvoiceRecord
		var lineItemsJSON []byte
		var paymentIntentID *string
		if err := rows.Scan(&inv.ID, &inv.TenantID, &inv.PeriodStart, &inv.PeriodEnd, &inv.Tier, &lineItemsJSON, &inv.Subtotal, &inv.Total, &inv.Currency, &inv.Status, &paymentIntentID, &inv.CreatedAt, &inv.PaidAt); err != nil {
			return nil, fmt.Errorf("scan invoice: %w", err)
		}
		if paymentIntentID != nil {
			inv.PaymentIntentID = *paymentIntentID
		}
		if err := json.Unmarshal(lineItemsJSON, &inv.LineItems); err != nil {
			return nil, fmt.Errorf("unmarshal line items: %w", err)
		}
		invoices = append(invoices, &inv)
	}
	return invoices, nil
}

// UpdateInvoiceStatus changes an invoice's status.
func (s *PostgresStore) UpdateInvoiceStatus(ctx context.Context, id, status string) error {
	query := `UPDATE invoices SET status = $2 WHERE id = $1`
	if status == "paid" {
		query = `UPDATE invoices SET status = $2, paid_at = NOW() WHERE id = $1`
	}
	_, err := s.pool.Exec(ctx, query, id, status)
	if err != nil {
		return fmt.Errorf("update invoice status %s: %w", id, err)
	}
	return nil
}

// CreateTierChangeEvent records a subscription tier change.
func (s *PostgresStore) CreateTierChangeEvent(ctx context.Context, e *TierChangeEventRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO tier_change_events (tenant_id, from_tier, to_tier, effective_at, proration_amount)
		VALUES ($1, $2, $3, $4, $5)
	`, e.TenantID, e.FromTier, e.ToTier, e.EffectiveAt, e.ProrationAmount)
	if err != nil {
		return fmt.Errorf("create tier change event: %w", err)
	}
	return nil
}
