// Package models resolves the per-node model tiering from configuration.
//
// The pipeline names a *tier* ("triage", "analyze", "recommend"), never a model
// ID. That indirection is what lets a cost-sensitive deployment point every
// tier at one cheap OpenAI-compatible endpoint, and a quality-sensitive one
// spend Opus on recommendations only, without either touching pipeline code.
package models

import (
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/anthropic"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
)

// Tier names a role in the pipeline rather than a specific model.
type Tier string

const (
	// TierTriage is high-volume and trivial: is this change cosmetic?
	TierTriage Tier = "triage"
	// TierAnalyze classifies meaning; the quality/cost sweet spot.
	TierAnalyze Tier = "analyze"
	// TierRecommend produces the legal-risk output the product is sold on.
	// This is the tier not to economise on.
	TierRecommend Tier = "recommend"
	// TierAdjudicate is a rarely-invoked second opinion on high-stakes findings.
	TierAdjudicate Tier = "adjudicate"
)

// Provider selects which API surface a tier talks to.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	// ProviderOpenAI covers every OpenAI-compatible endpoint: OpenAI itself,
	// DeepSeek, Qwen, GLM, or a self-hosted gateway.
	ProviderOpenAI Provider = "openai"
)

// Rates are USD per million tokens, used to attribute cost per node.
//
// These are configuration, not constants discovered at runtime: no provider
// reports a price in its response, so a wrong number here yields a confidently
// wrong cost report. Override them per deployment rather than trusting the
// defaults to stay current.
type Rates struct {
	InputPerMTok     float64
	OutputPerMTok    float64
	CacheReadPerMTok float64
	// CacheWritePerMTok is charged once when a prefix is first cached.
	CacheWritePerMTok float64
}

// Cost prices one call. Cached input is billed at the cache-read rate and is
// excluded from the ordinary input count so it is never charged twice.
func (r Rates) Cost(inputUncached, cacheRead, cacheWrite, output int) float64 {
	const m = 1_000_000.0
	return float64(inputUncached)/m*r.InputPerMTok +
		float64(cacheRead)/m*r.CacheReadPerMTok +
		float64(cacheWrite)/m*r.CacheWritePerMTok +
		float64(output)/m*r.OutputPerMTok
}

// Spec is the resolved configuration for one tier.
type Spec struct {
	Provider Provider
	Model    string
	BaseURL  string
	APIKey   string
	Rates    Rates
}

// Registry hands out built models per tier, memoising them so that a model —
// and with it any connection pool it owns — is constructed once per process.
type Registry struct {
	specs map[Tier]Spec
	built map[Tier]model.Model
}

// Entry pairs a tier's model with the rates used to price its calls.
type Entry struct {
	Model model.Model
	Rates Rates
	Name  string
}

// anthropicDefaults reflect published list prices at the time of writing.
// Cache reads are a tenth of input; cache writes carry a 25% premium.
func anthropicRates(in, out float64) Rates {
	return Rates{
		InputPerMTok:      in,
		OutputPerMTok:     out,
		CacheReadPerMTok:  in * 0.1,
		CacheWritePerMTok: in * 1.25,
	}
}

// DefaultSpecs is the tiered configuration from the plan: a cheap model for
// triage, a mid model for analysis, and the strongest model for the
// recommendations the product is actually sold on.
func DefaultSpecs() map[Tier]Spec {
	return map[Tier]Spec{
		TierTriage: {
			Provider: ProviderAnthropic,
			Model:    "claude-haiku-4-5-20251001",
			Rates:    anthropicRates(1, 5),
		},
		TierAnalyze: {
			Provider: ProviderAnthropic,
			Model:    "claude-sonnet-5",
			Rates:    anthropicRates(3, 15),
		},
		TierRecommend: {
			Provider: ProviderAnthropic,
			Model:    "claude-opus-5",
			Rates:    anthropicRates(5, 25),
		},
		TierAdjudicate: {
			Provider: ProviderAnthropic,
			Model:    "claude-opus-5",
			Rates:    anthropicRates(5, 25),
		},
	}
}

// FromEnv builds a registry from environment variables, falling back to the
// tiered defaults for anything unset.
//
// Recognised variables, per tier T in {TRIAGE, ANALYZE, RECOMMEND, ADJUDICATE}:
//
//	DIFF_MODEL_<T>            model ID
//	DIFF_PROVIDER_<T>         "anthropic" | "openai"
//	DIFF_BASE_URL_<T>         override endpoint (OpenAI-compatible gateways)
//	DIFF_RATE_IN_<T>          USD per million input tokens
//	DIFF_RATE_OUT_<T>         USD per million output tokens
//
// and globally:
//
//	ANTHROPIC_API_KEY, OPENAI_API_KEY,
//	DIFF_PROVIDER / DIFF_MODEL / DIFF_BASE_URL   applied to every tier
func FromEnv() *Registry {
	specs := DefaultSpecs()

	globalProvider := os.Getenv("DIFF_PROVIDER")
	globalModel := os.Getenv("DIFF_MODEL")
	globalBase := os.Getenv("DIFF_BASE_URL")

	for tier, spec := range specs {
		suffix := strings.ToUpper(string(tier))

		if globalProvider != "" {
			spec.Provider = Provider(globalProvider)
		}
		if v := os.Getenv("DIFF_PROVIDER_" + suffix); v != "" {
			spec.Provider = Provider(v)
		}
		if globalModel != "" {
			spec.Model = globalModel
		}
		if v := os.Getenv("DIFF_MODEL_" + suffix); v != "" {
			spec.Model = v
		}
		if globalBase != "" {
			spec.BaseURL = globalBase
		}
		if v := os.Getenv("DIFF_BASE_URL_" + suffix); v != "" {
			spec.BaseURL = v
		}
		if v, ok := parseFloat(os.Getenv("DIFF_RATE_IN_" + suffix)); ok {
			spec.Rates.InputPerMTok = v
			spec.Rates.CacheReadPerMTok = v * 0.1
			spec.Rates.CacheWritePerMTok = v * 1.25
		}
		if v, ok := parseFloat(os.Getenv("DIFF_RATE_OUT_" + suffix)); ok {
			spec.Rates.OutputPerMTok = v
		}

		switch spec.Provider {
		case ProviderAnthropic:
			spec.APIKey = os.Getenv("ANTHROPIC_API_KEY")
		case ProviderOpenAI:
			spec.APIKey = os.Getenv("OPENAI_API_KEY")
		}
		specs[tier] = spec
	}

	return &Registry{specs: specs, built: map[Tier]model.Model{}}
}

// NewRegistry builds a registry from explicit specs, for tests and for callers
// that configure the pipeline in code rather than through the environment.
func NewRegistry(specs map[Tier]Spec) *Registry {
	return &Registry{specs: specs, built: map[Tier]model.Model{}}
}

// Configured reports whether every tier has the credentials it needs. The
// server uses this to decide whether to offer the AI tier at all, rather than
// letting a job fail on its first model call.
func (r *Registry) Configured() bool {
	for _, spec := range r.specs {
		if spec.APIKey == "" {
			return false
		}
	}
	return true
}

// Missing names the tiers that cannot run, for a precise error message.
func (r *Registry) Missing() []string {
	var out []string
	for tier, spec := range r.specs {
		if spec.APIKey == "" {
			out = append(out, fmt.Sprintf("%s (%s)", tier, spec.Provider))
		}
	}
	return out
}

// Get returns the model for a tier, constructing it on first use.
func (r *Registry) Get(tier Tier) (Entry, error) {
	spec, ok := r.specs[tier]
	if !ok {
		return Entry{}, fmt.Errorf("tier %q tidak dikenal", tier)
	}
	if m, ok := r.built[tier]; ok {
		return Entry{Model: m, Rates: spec.Rates, Name: spec.Model}, nil
	}
	if spec.APIKey == "" {
		return Entry{}, fmt.Errorf("tier %q (%s) tidak punya API key", tier, spec.Provider)
	}

	var m model.Model
	switch spec.Provider {
	case ProviderAnthropic:
		opts := []anthropic.Option{
			anthropic.WithAPIKey(spec.APIKey),
			// Caching the system prompt and the tool definitions is the single
			// largest cost lever in the design. It only pays off while the
			// cached prefix is byte-identical across requests, which is why
			// prompts in this repo never interpolate a timestamp, a job ID, or
			// a filename.
			anthropic.WithCacheSystemPrompt(true),
			anthropic.WithCacheTools(true),
		}
		if spec.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(spec.BaseURL))
		}
		m = anthropic.New(spec.Model, opts...)
	case ProviderOpenAI:
		opts := []openai.Option{openai.WithAPIKey(spec.APIKey)}
		if spec.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(spec.BaseURL))
		}
		m = openai.New(spec.Model, opts...)
	default:
		return Entry{}, fmt.Errorf("provider %q tidak didukung", spec.Provider)
	}

	r.built[tier] = m
	return Entry{Model: m, Rates: spec.Rates, Name: spec.Model}, nil
}

// Spec exposes a tier's resolved configuration, for diagnostics pages.
func (r *Registry) Spec(tier Tier) (Spec, bool) {
	s, ok := r.specs[tier]
	return s, ok
}

func parseFloat(s string) (float64, bool) {
	if s == "" {
		return 0, false
	}
	var f float64
	if _, err := fmt.Sscanf(s, "%g", &f); err != nil {
		return 0, false
	}
	return f, true
}
