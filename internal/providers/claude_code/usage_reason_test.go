package claude_code

import (
	"errors"
	"testing"
)

func TestUsageErrorReason(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{errors.New(`all usage API sources failed: oauth: API returned 429: {"error":{"type":"rate_limit_error"}}`), "rate_limited"},
		{errors.New("all usage API sources failed: oauth: API returned 401: unauthorized"), "unavailable"},
		{errors.New("oauth auth source (previously successful) failed and no cached usage available"), "unavailable"},
		{nil, "unavailable"},
	}
	for _, tt := range tests {
		if got := usageErrorReason(tt.err); got != tt.want {
			t.Errorf("usageErrorReason(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}
