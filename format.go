package main

import (
	"fmt"
	"strconv"
	"strings"
)

// humanBytes renders a byte count in binary units, e.g. "1.4 GB".
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// groupInt renders 1234567 as "1,234,567".
func groupInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	if len(s) <= 3 {
		return s
	}
	var b strings.Builder
	pre := len(s) % 3
	if pre > 0 {
		b.WriteString(s[:pre])
	}
	for i := pre; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// bar renders a usage bar of the given width, filled proportionally to
// value/max.
func bar(value, max int64, width int) string {
	if max <= 0 || width <= 0 {
		return strings.Repeat(" ", width)
	}
	filled := int(float64(value) / float64(max) * float64(width))
	if filled > width {
		filled = width
	}
	if value > 0 && filled == 0 {
		filled = 1
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// percent renders value/total as " 12.3%", right-aligned.
func percent(value, total int64) string {
	if total <= 0 {
		return "     -"
	}
	return fmt.Sprintf("%5.1f%%", float64(value)/float64(total)*100)
}
