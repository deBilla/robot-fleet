package service

import (
	"testing"
)

func TestPricingTier_Free(t *testing.T) {
	tier := GetPricingTier("free")
	if tier.APICallLimit != 1000 {
		t.Errorf("expected 1000 api call limit, got %d", tier.APICallLimit)
	}
	if tier.InferenceLimit != 100 {
		t.Errorf("expected 100 inference limit, got %d", tier.InferenceLimit)
	}
	if tier.PricePerAPICall != 0.001 {
		t.Errorf("expected 0.001 overage price, got %f", tier.PricePerAPICall)
	}
}

func TestPricingTier_Pro(t *testing.T) {
	tier := GetPricingTier("pro")
	if tier.APICallLimit != 100000 {
		t.Errorf("expected 100000 api call limit, got %d", tier.APICallLimit)
	}
	if tier.MonthlyBase != 99.0 {
		t.Errorf("expected $99 monthly base, got %f", tier.MonthlyBase)
	}
}

func TestPricingTier_Enterprise(t *testing.T) {
	tier := GetPricingTier("enterprise")
	if tier.APICallLimit != 0 {
		t.Errorf("expected unlimited (0) api call limit, got %d", tier.APICallLimit)
	}
}

func TestPricingTier_Unknown(t *testing.T) {
	tier := GetPricingTier("unknown")
	if tier.Name != "free" {
		t.Errorf("expected fallback to free tier, got %s", tier.Name)
	}
}

func TestCalculateInvoice(t *testing.T) {
	tests := []struct {
		name       string
		tier       string
		apiCalls   int64
		inference  int64
		wantMin    float64
		wantMax    float64
	}{
		{"free within limits", "free", 500, 50, 0, 0},
		{"free overage", "free", 2000, 200, 0, 50},
		{"pro base only", "pro", 1000, 100, 99, 99},
		{"pro with overage", "pro", 200000, 1000, 99, 200},
		{"enterprise flat", "enterprise", 1000000, 50000, 499, 499},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tier := GetPricingTier(tt.tier)
			invoice := CalculateInvoice(tier, tt.apiCalls, tt.inference)

			if invoice.Total < tt.wantMin {
				t.Errorf("expected total >= %.2f, got %.2f", tt.wantMin, invoice.Total)
			}
			if invoice.Total > tt.wantMax {
				t.Errorf("expected total <= %.2f, got %.2f", tt.wantMax, invoice.Total)
			}
			if invoice.Tier != tt.tier {
				t.Errorf("expected tier %s, got %s", tt.tier, invoice.Tier)
			}
		})
	}
}
