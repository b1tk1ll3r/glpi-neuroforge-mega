package cost

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"neuroforge/internal/core"
	"neuroforge/internal/provider"
	"neuroforge/internal/store"
)

type Manager struct {
	mu                             sync.Mutex
	store                          *store.Store
	reservedDaily, reservedMonthly float64
}

func New(s *store.Store) *Manager { return &Manager{store: s} }

func estimateTokens(text string) int64 {
	n := int64(len([]rune(text)) / 4)
	if n < 1 {
		n = 1
	}
	return n
}

func chatRates(p core.ModelPrice, inputTokens int64) (input, cached, output float64) {
	input, cached, output = p.InputPerM, p.CachedInputPerM, p.OutputPerM
	if p.LongContextThresholdTokens > 0 && inputTokens > p.LongContextThresholdTokens {
		if p.LongInputPerM > 0 {
			input = p.LongInputPerM
		}
		if p.LongCachedInputPerM > 0 {
			cached = p.LongCachedInputPerM
		}
		if p.LongOutputPerM > 0 {
			output = p.LongOutputPerM
		}
	}
	if cached == 0 {
		cached = input
	}
	return input, cached, output
}

func (m *Manager) EstimateOpenAIChat(model, input string, maxOutput int) (float64, error) {
	cfg := m.store.Config()
	p, ok := cfg.OpenAI.Prices[model]
	if !ok || (p.InputPerM == 0 && p.OutputPerM == 0) {
		return 0, errors.New("no OpenAI chat price configured for model " + model)
	}
	inputTokens := estimateTokens(input)
	inputRate, _, outputRate := chatRates(p, inputTokens)
	return float64(inputTokens)/1e6*inputRate + float64(maxOutput)/1e6*outputRate, nil
}
func (m *Manager) EstimateOpenAIEmbed(model, input string) (float64, error) {
	cfg := m.store.Config()
	p, ok := cfg.OpenAI.Prices[model]
	if !ok {
		return 0, errors.New("no OpenAI embedding price configured for model " + model)
	}
	rate := p.EmbeddingInputPerM
	if rate == 0 {
		rate = p.InputPerM
	}
	if rate == 0 {
		return 0, errors.New("OpenAI embedding price is zero/unconfigured for model " + model)
	}
	return float64(estimateTokens(input)) / 1e6 * rate, nil
}

func (m *Manager) Reserve(estimated float64) (func(), error) {
	if estimated <= 0 {
		return func() {}, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.store.Config()
	daily, monthly := m.store.UsageTotals(time.Now())
	if cfg.OpenAI.DailyBudgetUSD > 0 && daily+m.reservedDaily+estimated > cfg.OpenAI.DailyBudgetUSD {
		return nil, fmt.Errorf("OpenAI daily budget would be exceeded: %.4f + %.4f > %.4f USD", daily, estimated, cfg.OpenAI.DailyBudgetUSD)
	}
	if cfg.OpenAI.MonthlyBudgetUSD > 0 && monthly+m.reservedMonthly+estimated > cfg.OpenAI.MonthlyBudgetUSD {
		return nil, fmt.Errorf("OpenAI monthly budget would be exceeded: %.4f + %.4f > %.4f USD", monthly, estimated, cfg.OpenAI.MonthlyBudgetUSD)
	}
	m.reservedDaily += estimated
	m.reservedMonthly += estimated
	done := false
	return func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if done {
			return
		}
		done = true
		m.reservedDaily -= estimated
		m.reservedMonthly -= estimated
		if m.reservedDaily < 0 {
			m.reservedDaily = 0
		}
		if m.reservedMonthly < 0 {
			m.reservedMonthly = 0
		}
	}, nil
}

func (m *Manager) ActualCost(model, category string, u provider.Usage) (float64, error) {
	cfg := m.store.Config()
	p, ok := cfg.OpenAI.Prices[model]
	if !ok {
		return 0, errors.New("no price configured for model " + model)
	}
	if category == "embedding" {
		rate := p.EmbeddingInputPerM
		if rate == 0 {
			rate = p.InputPerM
		}
		return float64(u.InputTokens) / 1e6 * rate, nil
	}
	uncached := u.InputTokens - u.CachedTokens
	if uncached < 0 {
		uncached = 0
	}
	inputRate, cachedRate, outputRate := chatRates(p, u.InputTokens)
	return float64(uncached)/1e6*inputRate + float64(u.CachedTokens)/1e6*cachedRate + float64(u.OutputTokens)/1e6*outputRate, nil
}

func (m *Manager) Record(providerName, model, category string, u provider.Usage) (float64, error) {
	costUSD := 0.0
	var err error
	if providerName == "openai" {
		costUSD, err = m.ActualCost(model, category, u)
		if err != nil {
			return 0, err
		}
	}
	e := core.UsageEvent{Provider: providerName, Model: model, Category: category, InputTokens: u.InputTokens, CachedTokens: u.CachedTokens, OutputTokens: u.OutputTokens, CostUSD: costUSD}
	return costUSD, m.store.AddUsage(e)
}

func (m *Manager) Totals() map[string]float64 {
	d, mo := m.store.UsageTotals(time.Now())
	return map[string]float64{"daily_usd": d, "monthly_usd": mo}
}
