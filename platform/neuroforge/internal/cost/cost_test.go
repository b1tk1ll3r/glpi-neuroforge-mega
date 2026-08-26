package cost

import (
	"testing"

	"neuroforge/internal/core"
)

func TestChatRatesLongContext(t *testing.T) {
	p := core.ModelPrice{
		InputPerM: 0.20, CachedInputPerM: 0.02, OutputPerM: 1.20,
		LongContextThresholdTokens: 272000,
		LongInputPerM:              0.40, LongCachedInputPerM: 0.04, LongOutputPerM: 1.80,
	}
	in, cached, out := chatRates(p, 272000)
	if in != 0.20 || cached != 0.02 || out != 1.20 {
		t.Fatalf("short-context rates changed at threshold: %v %v %v", in, cached, out)
	}
	in, cached, out = chatRates(p, 272001)
	if in != 0.40 || cached != 0.04 || out != 1.80 {
		t.Fatalf("long-context rates not selected: %v %v %v", in, cached, out)
	}
}
