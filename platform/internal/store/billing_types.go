package store

import "time"

// TenantRecord represents a tenant with billing subscription state.
type TenantRecord struct {
	ID               string
	Name             string
	Tier             string
	StripeCustomerID string
	BillingEmail     string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UsageDailyRecord represents a durable daily usage snapshot.
type UsageDailyRecord struct {
	ID        int64
	TenantID  string
	Date      time.Time
	Metric    string
	Count     int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InvoiceRecord represents a persisted billing invoice.
type InvoiceRecord struct {
	ID              string
	TenantID        string
	PeriodStart     time.Time
	PeriodEnd       time.Time
	Tier            string
	LineItems       map[string]any
	Subtotal        float64
	Total           float64
	Currency        string
	Status          string
	PaymentIntentID string
	CreatedAt       time.Time
	PaidAt          *time.Time
}

// TierChangeEventRecord represents a subscription tier change.
type TierChangeEventRecord struct {
	ID              int64
	TenantID        string
	FromTier        string
	ToTier          string
	EffectiveAt     time.Time
	ProrationAmount float64
	CreatedAt       time.Time
}
