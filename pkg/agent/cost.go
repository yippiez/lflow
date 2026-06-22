package agent

import "strings"

// Cost estimation for backends that report token counts but NOT a price. Today
// that is only grok over ACP — its session/prompt result carries inputTokens /
// outputTokens / cachedReadTokens but no cost. pi and opencode report cost
// directly, so they never consult this table.
//
// Prices are HARDCODED and approximate; edit modelPrices to match current vendor
// pricing. An unknown model yields no estimate (cost stays $0) rather than a
// fabricated number.

// Price is per-million-token pricing for a model, in USD.
type Price struct {
	InPer1M         float64 // non-cached input tokens
	OutPer1M        float64 // output tokens
	CachedReadPer1M float64 // cached-read input tokens; 0 → billed at InPer1M
}

// modelPrices maps a bare model id (provider/cli prefix stripped) to its pricing,
// in USD per 1M tokens. Sourced June 2026; adjust as vendors change pricing.
//
//   - grok-build (grok-build-0.1): official xAI pricing — $1.00 in / $2.00 out,
//     $0.20 cached-read (256k context). Source: docs.x.ai/developers/models.
//   - grok-composer-2.5-fast: xAI publishes NO per-token price for this id; it is
//     Cursor's "Composer 2.5 Fast" exposed via the Grok CLI. Numbers below are
//     Cursor's published Fast-tier rates ($3 in / $15 out, no cached rate), the
//     closest real figures. Source: cursor.com/blog/composer-2-5.
var modelPrices = map[string]Price{
	"grok-build":             {InPer1M: 1.00, OutPer1M: 2.00, CachedReadPer1M: 0.20},
	"grok-composer-2.5-fast": {InPer1M: 3.00, OutPer1M: 15.00},
}

// EstimateCost returns an estimated USD cost for a turn and whether a price was
// known for the model. `in` is the TOTAL input tokens (including cachedRead);
// cachedRead tokens are billed at the cached rate when one is set, else at the
// input rate. Backends call this only when the CLI does not report cost itself.
func EstimateCost(model string, in, out, cachedRead int) (float64, bool) {
	p, ok := lookupPrice(model)
	if !ok {
		return 0, false
	}
	if cachedRead < 0 {
		cachedRead = 0
	}
	if cachedRead > in {
		cachedRead = in
	}
	cachedRate := p.CachedReadPer1M
	if cachedRate == 0 {
		cachedRate = p.InPer1M
	}
	nonCached := in - cachedRead
	cost := float64(nonCached)*p.InPer1M/1e6 +
		float64(cachedRead)*cachedRate/1e6 +
		float64(out)*p.OutPer1M/1e6
	return cost, true
}

// lookupPrice resolves a model id to its Price, tolerating a "provider/model" or
// "cli:provider/model" prefix by matching on the trailing bare model id.
func lookupPrice(model string) (Price, bool) {
	if i := strings.LastIndexByte(model, '/'); i >= 0 {
		model = model[i+1:]
	}
	if i := strings.LastIndexByte(model, ':'); i >= 0 {
		model = model[i+1:]
	}
	p, ok := modelPrices[model]
	return p, ok
}
