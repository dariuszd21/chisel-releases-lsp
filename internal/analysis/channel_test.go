package analysis_test

import (
	"testing"

	"github.com/dariuszd21/chisel-releases-lsp/internal/analysis"
)

func TestValidateChannelPattern(t *testing.T) {
	tests := []struct {
		pattern string
		track   string
		err     string
	}{
		{pattern: "0.3/stable", track: "0.3"},
		{pattern: "0.3/*", track: "0.3"},
		{pattern: "0.3/!stable", track: "0.3"},
		{pattern: "0.2/beta,edge", track: "0.2"},
		{pattern: "latest/stable,candidate,beta,edge", track: "latest"},
		{pattern: "", err: "channel pattern must not be empty"},
		{pattern: "0.3", err: "channel pattern must be <track>/<risk>"},
		{pattern: "0.3/", err: "channel pattern must be <track>/<risk>"},
		{pattern: "/stable", err: "channel pattern must be <track>/<risk>"},
		{pattern: "0.3/!", err: "channel pattern must be <track>/<risk>"},
		{pattern: "*/stable", err: "only the risk accepts '*', '!' and ','"},
		{pattern: "0.3-*/stable", err: "only the risk accepts '*', '!' and ','"},
		{pattern: "0.3/e*", err: "'*' must be the whole risk"},
		{pattern: "0.3/!stable,edge", err: "'!' cannot be combined with other risks"},
		{pattern: "0.3/edge,!stable", err: "'!' must prefix the whole risk"},
		{pattern: "0.3/edge,edge", err: `risk "edge" is repeated`},
		{pattern: "0.3/not stable", err: "channel pattern must not contain spaces"},
		{pattern: "0.3/whatever", err: `unknown risk "whatever", must be one of stable, candidate, beta, edge`},
		{pattern: "0.3/!whatever", err: `unknown risk "whatever", must be one of stable, candidate, beta, edge`},
		{pattern: "0.3/Stable", err: `unknown risk "Stable", must be one of stable, candidate, beta, edge`},
		// A branch is never part of a pattern, so it is read as part of the risk.
		{pattern: "0.3/stable/mybranch", err: `unknown risk "stable/mybranch", must be one of stable, candidate, beta, edge`},
	}
	for _, tc := range tests {
		track, err := analysis.ValidateChannelPattern(tc.pattern)
		if tc.err != "" {
			if err == nil {
				t.Errorf("%q: expected error %q, got none", tc.pattern, tc.err)
			} else if err.Error() != tc.err {
				t.Errorf("%q: got error %q, want %q", tc.pattern, err, tc.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error %v", tc.pattern, err)
			continue
		}
		if track != tc.track {
			t.Errorf("%q: track got %q, want %q", tc.pattern, track, tc.track)
		}
	}
}
