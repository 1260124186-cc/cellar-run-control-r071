package search

import "strconv"

// formatFloat renders integral capacities without a trailing ".0" while
// keeping fractional values readable.
func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}
