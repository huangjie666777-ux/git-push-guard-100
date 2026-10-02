package httpserver

import "strings"

func trimCRLF(s string) string {
	return strings.TrimRight(s, "\r\n")
}

func splitColon(line string) (string, string, bool) {
	k, v, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	return strings.TrimSpace(k), strings.TrimSpace(v), true
}
