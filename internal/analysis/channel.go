package analysis

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

// KnownRisks holds every risk a store channel may carry, from the most to the
// least stable. The set is defined by the store and does not depend on its
// content, so risks are validated statically just like architectures are.
var KnownRisks = []string{"stable", "candidate", "beta", "edge"}

// A `channel:` field holds patterns selecting which concrete "<track>/<risk>"
// channels an entry applies to. The track is a literal and only the risk part
// accepts operators:
//
//	*             - any risk of that track
//	!<risk>       - any risk of that track but that one
//	<risk>[,...]  - only those risks of that track
//
// Patterns are validated statically so malformed values are reported while
// editing rather than when chisel reads the release.
//
// Note that the channel is intentionally NOT used to narrow any other analysis:
// chisel computes path conflicts irrespective of both `channel:` and `arch:`,
// and this package matches that behaviour.

// ValidateChannelPattern checks a single `channel:` pattern and returns its
// track. The returned error message is suitable for direct use in a diagnostic.
func ValidateChannelPattern(pattern string) (track string, err error) {
	if pattern == "" {
		return "", errors.New("channel pattern must not be empty")
	}
	if strings.ContainsFunc(pattern, unicode.IsSpace) {
		return "", errors.New("channel pattern must not contain spaces")
	}
	track, riskPart, ok := strings.Cut(pattern, "/")
	if !ok || track == "" || riskPart == "" {
		return "", errors.New("channel pattern must be <track>/<risk>")
	}
	if strings.ContainsAny(track, "*!,") {
		return "", errors.New("only the risk accepts '*', '!' and ','")
	}
	if riskPart == "*" {
		return track, nil
	}
	if strings.Contains(riskPart, "*") {
		return "", errors.New("'*' must be the whole risk")
	}
	risks := strings.Split(riskPart, ",")
	if except, isExcept := strings.CutPrefix(riskPart, "!"); isExcept {
		if len(risks) > 1 {
			return "", errors.New("'!' cannot be combined with other risks")
		}
		if except == "" {
			return "", errors.New("channel pattern must be <track>/<risk>")
		}
		if err := validateRisk(except); err != nil {
			return "", err
		}
		return track, nil
	}
	for i, risk := range risks {
		if risk == "" {
			return "", errors.New("channel pattern must be <track>/<risk>")
		}
		if strings.Contains(risk, "!") {
			return "", errors.New("'!' must prefix the whole risk")
		}
		if slices.Contains(risks[:i], risk) {
			return "", fmt.Errorf("risk %q is repeated", risk)
		}
		if err := validateRisk(risk); err != nil {
			return "", err
		}
	}
	return track, nil
}

// validateRisk reports whether risk is one of the risks a store defines.
func validateRisk(risk string) error {
	if !slices.Contains(KnownRisks, risk) {
		return fmt.Errorf("unknown risk %q, must be one of %s", risk, strings.Join(KnownRisks, ", "))
	}
	return nil
}
