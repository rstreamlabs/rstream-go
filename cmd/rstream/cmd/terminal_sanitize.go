// See LICENSE file in the project root for license information.

package cmd

import "strings"

func terminalSafe(value string) string {
	value = stripANSIEscapes(value)
	value = strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			return -1
		default:
			return r
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}

func terminalSafeDefault(value string) string {
	value = terminalSafe(value)
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func stripANSIEscapes(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != 0x1b {
			b.WriteByte(value[i])
			continue
		}
		i++
		if i >= len(value) {
			break
		}
		switch value[i] {
		case '[':
			for i+1 < len(value) {
				i++
				if value[i] >= 0x40 && value[i] <= 0x7e {
					break
				}
			}
		case ']':
			for i+1 < len(value) {
				i++
				if value[i] == 0x07 {
					break
				}
				if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '\\' {
					i++
					break
				}
			}
		default:
		}
	}
	return b.String()
}
