package billing

// PricingTier defines rate limits and pricing for a subscription tier.
type PricingTier struct {
	Name                  string  `json:"name"`
	APICallLimit          int64   `json:"api_call_limit"`
	InferenceLimit        int64   `json:"inference_limit"`
	RobotsLimit           int     `json:"robots_limit"`
	SimHoursPerMonth      float64 `json:"sim_hours_per_month"`
	TrainingHoursPerMonth float64 `json:"training_hours_per_month"`
	MaxAgents             int     `json:"max_agents"`
	MonthlyBase           float64 `json:"monthly_base"`
	PricePerAPICall       float64 `json:"price_per_api_call"`
	PricePerInference     float64 `json:"price_per_inference"`
}

var pricingTiers = map[string]PricingTier{
	"free": {
		Name:                  "free",
		APICallLimit:          1000,
		InferenceLimit:        100,
		RobotsLimit:           5,
		SimHoursPerMonth:      10,
		TrainingHoursPerMonth: 2,
		MaxAgents:             3,
		MonthlyBase:           0,
		PricePerAPICall:       0.001,
		PricePerInference:     0.01,
	},
	"pro": {
		Name:                  "pro",
		APICallLimit:          100_000,
		InferenceLimit:        10_000,
		RobotsLimit:           100,
		SimHoursPerMonth:      100,
		TrainingHoursPerMonth: 50,
		MaxAgents:             50,
		MonthlyBase:           99.0,
		PricePerAPICall:       0.0005,
		PricePerInference:     0.005,
	},
	"enterprise": {
		Name:                  "enterprise",
		APICallLimit:          0,
		InferenceLimit:        0,
		RobotsLimit:           0,
		SimHoursPerMonth:      0,
		TrainingHoursPerMonth: 0,
		MaxAgents:             0,
		MonthlyBase:           499.0,
		PricePerAPICall:       0,
		PricePerInference:     0,
	},
}

// GetPricingTier returns the pricing tier by name. Falls back to free.
func GetPricingTier(name string) PricingTier {
	if tier, ok := pricingTiers[name]; ok {
		return tier
	}
	return pricingTiers["free"]
}

// ValidTier returns true if the tier name is recognized.
func ValidTier(name string) bool {
	_, ok := pricingTiers[name]
	return ok
}

// Invoice represents a billing invoice calculation result.
type Invoice struct {
	TenantID         string  `json:"tenant_id"`
	Tier             string  `json:"tier"`
	Period           string  `json:"period"`
	APICalls         int64   `json:"api_calls"`
	InferenceCalls   int64   `json:"inference_calls"`
	BaseCharge       float64 `json:"base_charge"`
	APIOverage       float64 `json:"api_overage"`
	InferenceOverage float64 `json:"inference_overage"`
	Total            float64 `json:"total"`
}

// CalculateInvoice computes the bill for a given usage period.
func CalculateInvoice(tier PricingTier, apiCalls, inferenceCalls int64) Invoice {
	invoice := Invoice{
		Tier:           tier.Name,
		APICalls:       apiCalls,
		InferenceCalls: inferenceCalls,
		BaseCharge:     tier.MonthlyBase,
	}

	if tier.APICallLimit > 0 && apiCalls > tier.APICallLimit {
		overage := apiCalls - tier.APICallLimit
		invoice.APIOverage = float64(overage) * tier.PricePerAPICall
	}

	if tier.InferenceLimit > 0 && inferenceCalls > tier.InferenceLimit {
		overage := inferenceCalls - tier.InferenceLimit
		invoice.InferenceOverage = float64(overage) * tier.PricePerInference
	}

	invoice.Total = invoice.BaseCharge + invoice.APIOverage + invoice.InferenceOverage
	return invoice
}

// ListTiers returns all available pricing tiers.
func ListTiers() []PricingTier {
	return []PricingTier{
		pricingTiers["free"],
		pricingTiers["pro"],
		pricingTiers["enterprise"],
	}
}
