package meta

// Prices from https://dev.meta.ai/docs/pricing-rate-limits (checked 2026-10-26), USD.
var tokenPrices = map[string]struct{ in, cached, out float64 }{ // per 1M tokens
	"muse-spark-1.3":             {1.25, 0.15, 4.25},
	"muse-spark-1.2":             {1.25, 0.15, 4.25},
	"muse-spark-1.3-contributor": {0.10, 0.002, 0.20},
	"muse-spark-1.2-contributor": {0.10, 0.002, 0.20},
	"muse-wonder-1.0":            {4.00, 0.20, 20.00},
}

const (
	samImageUSD = 2.50 / 1000 // per image
	samFrameUSD = 0.20 / 1000 // per video frame, billed at least 50 frames per request
)

// CostUSD estimates the price of one response from its usage.
func (r *Response) CostUSD() float64 {
	if r.Model == "sam-3.1" {
		return float64(r.Usage.ImagesProcessed) * samImageUSD
	}
	p, ok := tokenPrices[r.Model]
	if !ok {
		return 0
	}
	cached := r.Usage.InputTokensDetails.CachedTokens
	fresh := r.Usage.InputTokens - cached
	return (float64(fresh)*p.in + float64(cached)*p.cached + float64(r.Usage.OutputTokens)*p.out) / 1e6
}
