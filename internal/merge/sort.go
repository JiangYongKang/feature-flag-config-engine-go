package merge

import (
	"sort"

	"github.com/highcumontoa/feature-flag-config-engine-go/internal/domain"
)

func sortedFlagKeys(m map[string]domain.Flag) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedPropertyKeys(m map[string]domain.Property) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
