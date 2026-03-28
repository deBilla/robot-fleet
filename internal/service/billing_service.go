package service

import (
	"context"
	"fmt"
	"time"

	"github.com/dimuthu/robot-fleet/internal/store"
)

// PricingTier defines rate limits and pricing for a subscription tier.
type PricingTier struct {
	Name              string  `json:"name"`
	APICallLimit      int64   `json:"api_call_limit"`       // 0 = unlimited
	InferenceLimit    int64   `json:"inference_limit"`       // 0 = unlimited
	RobotsLimit       int     `json:"robots_limit"`          // 0 = unlimited
	MonthlyBase       float64 `json:"monthly_base"`          // base monthly fee
	PricePerAPICall   float64 `json:"price_per_api_call"`    // overage cost
	PricePerInference float64 `json:"price_per_inference"`   // overage cost
}

var pricingTiers = map[string]PricingTier{
	"free": {
		Name:              "free",
		APICallLimit:      1000,
		InferenceLimit:    100,
		RobotsLimit:       5,
		MonthlyBase:       0,
		PricePerAPICall:   0.001,  // $0.001 per overage call
		PricePerInference: 0.01,   // $0.01 per overage inference
	},
	"pro": {
		Name:              "pro",
		APICallLimit:      100_000,
		InferenceLimit:    10_000,
		RobotsLimit:       100,
		MonthlyBase:       99.0,
		PricePerAPICall:   0.0005,
		PricePerInference: 0.005,
	},
	"enterprise": {
		Name:              "enterprise",
		APICallLimit:      0, // unlimited
		InferenceLimit:    0, // unlimited
		RobotsLimit:       0, // unlimited
		MonthlyBase:       499.0,
		PricePerAPICall:   0,
		PricePerInference: 0,
	},
}

// GetPricingTier returns the pricing tier by name. Falls back to free.
func GetPricingTier(name string) PricingTier {
	if tier, ok := pricingTiers[name]; ok {
		return tier
	}
	return pricingTiers["free"]
}

// Invoice represents a billing invoice for a tenant.
type Invoice struct {
	TenantID        string  `json:"tenant_id"`
	Tier            string  `json:"tier"`
	Period          string  `json:"period"`
	APICalls        int64   `json:"api_calls"`
	InferenceCalls  int64   `json:"inference_calls"`
	BaseCharge      float64 `json:"base_charge"`
	APIOverage      float64 `json:"api_overage"`
	InferenceOverage float64 `json:"inference_overage"`
	Total           float64 `json:"total"`
}

// CalculateInvoice computes the bill for a given usage period.
func CalculateInvoice(tier PricingTier, apiCalls, inferenceCalls int64) Invoice {
	invoice := Invoice{
		Tier:           tier.Name,
		APICalls:       apiCalls,
		InferenceCalls: inferenceCalls,
		BaseCharge:     tier.MonthlyBase,
	}

	// API call overage
	if tier.APICallLimit > 0 && apiCalls > tier.APICallLimit {
		overage := apiCalls - tier.APICallLimit
		invoice.APIOverage = float64(overage) * tier.PricePerAPICall
	}

	// Inference overage
	if tier.InferenceLimit > 0 && inferenceCalls > tier.InferenceLimit {
		overage := inferenceCalls - tier.InferenceLimit
		invoice.InferenceOverage = float64(overage) * tier.PricePerInference
	}

	invoice.Total = invoice.BaseCharge + invoice.APIOverage + invoice.InferenceOverage
	return invoice
}

// BillingService handles billing operations.
type BillingService interface {
	GetInvoice(ctx context.Context, tenantID, tier string) (*Invoice, error)
	ListTiers() []PricingTier
}

type billingService struct {
	cache store.CacheStore
}

// NewBillingService creates a new billing service.
func NewBillingService(cache store.CacheStore) BillingService {
	return &billingService{cache: cache}
}

func (s *billingService) GetInvoice(ctx context.Context, tenantID, tierName string) (*Invoice, error) {
	tier := GetPricingTier(tierName)
	date := time.Now().Format("2006-01-02")

	apiCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "api_calls", date)           // 0 on error — safe default for billing
	inferenceCalls, _ := s.cache.GetUsageCounter(ctx, tenantID, "inference_calls", date) // 0 on error — safe default for billing

	invoice := CalculateInvoice(tier, apiCalls, inferenceCalls)
	invoice.TenantID = tenantID
	invoice.Period = fmt.Sprintf("%s (daily)", date)

	return &invoice, nil
}

func (s *billingService) ListTiers() []PricingTier {
	return []PricingTier{
		pricingTiers["free"],
		pricingTiers["pro"],
		pricingTiers["enterprise"],
	}
}
