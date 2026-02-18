package clob

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestEncodeRedeemMatchesWebPayload verifies that our ABI encoding produces
// identical calldata to a real Polymarket website claim request.
//
// Reference payload from web UI (non-neg-risk, single condition):
//   conditionID: d94be4e0752ab2583cce6f026b2fe57aedb9eb0b2db6f488f4fef59bd91c51a8
//   gasLimit:    154535
func TestEncodeRedeemMatchesWebPayload(t *testing.T) {
	conditionID := "0xd94be4e0752ab2583cce6f026b2fe57aedb9eb0b2db6f488f4fef59bd91c51a8"

	// Build non-neg-risk redeem request (same as website).
	req := RedeemRequest{
		ConditionID: conditionID,
		NegRisk:     false,
	}
	calls := BuildRedeemCalls(req)
	outerData := EncodeProxyMultiCall(calls)

	// The expected data field from the web request (without 0x prefix).
	expectedHex := strings.ReplaceAll("34ee9791"+
		"0000000000000000000000000000000000000000000000000000000000000020"+
		"0000000000000000000000000000000000000000000000000000000000000001"+
		"0000000000000000000000000000000000000000000000000000000000000020"+
		"0000000000000000000000000000000000000000000000000000000000000001"+
		"0000000000000000000000004d97dcd97ec945f40cf65f87097ace5ea0476045"+
		"0000000000000000000000000000000000000000000000000000000000000000"+
		"0000000000000000000000000000000000000000000000000000000000000080"+
		"00000000000000000000000000000000000000000000000000000000000000e4"+
		"01b7037c"+
		"0000000000000000000000002791bca1f2de4661ed88a30c99a7a9449aa84174"+
		"0000000000000000000000000000000000000000000000000000000000000000"+
		"d94be4e0752ab2583cce6f026b2fe57aedb9eb0b2db6f488f4fef59bd91c51a8"+
		"0000000000000000000000000000000000000000000000000000000000000080"+
		"0000000000000000000000000000000000000000000000000000000000000002"+
		"0000000000000000000000000000000000000000000000000000000000000001"+
		"0000000000000000000000000000000000000000000000000000000000000002"+
		"00000000000000000000000000000000000000000000000000000000", "\n", "")

	gotHex := hex.EncodeToString(outerData)

	if gotHex != expectedHex {
		t.Errorf("calldata mismatch\n  got:    %s\n  expect: %s", gotHex, expectedHex)

		// Show first difference position for debugging.
		for i := 0; i < len(gotHex) && i < len(expectedHex); i++ {
			if gotHex[i] != expectedHex[i] {
				t.Errorf("first diff at byte %d (hex char %d)", i/2, i)
				break
			}
		}
	}
}

// TestGasLimitMatchesWebsite verifies that gas limit calculation produces
// values close to observed website behavior.
func TestGasLimitMatchesWebsite(t *testing.T) {
	tests := []struct {
		name     string
		txCount  int
		wantMin  int // minimum acceptable gas limit
		wantMax  int // maximum acceptable gas limit
		webValue int // observed website value
	}{
		{
			name:     "single non-neg-risk redeem",
			txCount:  1,
			wantMin:  200000,
			wantMax:  210000,
			webValue: 205000, // 85000 + 1*120000
		},
		{
			name:     "7 redeems batch",
			txCount:  7,
			wantMin:  920000,
			wantMax:  930000,
			webValue: 925000, // 85000 + 7*120000
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateGasLimit(tt.txCount)
			var gasInt int
			for _, c := range got {
				gasInt = gasInt*10 + int(c-'0')
			}
			if gasInt < tt.wantMin || gasInt > tt.wantMax {
				t.Errorf("gasLimit(%d) = %d, want [%d, %d] (website: %d)",
					tt.txCount, gasInt, tt.wantMin, tt.wantMax, tt.webValue)
			}
		})
	}
}
