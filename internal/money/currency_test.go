package money_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prasham/ledger/internal/money"
)

func TestCurrency_Validate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		currency money.Currency
		wantErr  bool
	}{
		{money.INR, false},
		{money.USD, false},
		{money.EUR, false},
		{money.GBP, false},
		{money.SGD, false},
		{money.AED, false},
		{money.JPY, false},
		{"BTC", true},
		{"XYZ", true},
		{"", true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.currency), func(t *testing.T) {
			t.Parallel()
			err := tc.currency.Validate()
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCurrency_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "INR", money.INR.String())
	assert.Equal(t, "USD", money.USD.String())
}
