package main

import (
	"fmt"
	"strings"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

func replaceOnce(s, old, new string) string {
	return strings.Replace(s, old, new, 1)
}
