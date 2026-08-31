package arrquality

import "math"

// Definition mirrors the quality-definition resources published by the pinned
// Radarr 6.3.0.10514 and Sonarr 4.0.19.2979 OpenAPI schemas. All three sizes are
// nullable doubles; pointers preserve null instead of conflating it with zero.
type Definition struct {
	Quality struct {
		Name string `json:"name"`
	} `json:"quality"`
	MinSize       *float64 `json:"minSize"`
	MaxSize       *float64 `json:"maxSize"`
	PreferredSize *float64 `json:"preferredSize"`
}

// Equivalent reports whether an Arr wire response represents the checked-in
// whole-unit policy. The pinned PCD retains one decimal place (for example,
// 50.8 and 12.5) while the policy fixture preserves its rounded semantic unit.
// Only the declared maximum sentinel is normalized to Arr's null unlimited
// representation; null minima and preferred sizes remain distinct from zero.
func (definition Definition) Equivalent(minimum, maximum, preferred, unlimitedMaximum int) bool {
	return equivalentFinite(definition.MinSize, minimum) &&
		equivalentMaximum(definition.MaxSize, maximum, unlimitedMaximum) &&
		equivalentFinite(definition.PreferredSize, preferred)
}

func equivalentFinite(observed *float64, expected int) bool {
	return observed != nil && math.Round(*observed) == float64(expected)
}

func equivalentMaximum(observed *float64, expected, unlimited int) bool {
	if expected == unlimited {
		return observed == nil
	}
	return equivalentFinite(observed, expected)
}
