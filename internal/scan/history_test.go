package scan

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDiffRawRename(t *testing.T) {
	raw := []byte(strings.Join([]string{
		":100644 100644 " + strings.Repeat("a", 40) + " " + strings.Repeat("b", 40) + " R100",
		"old/path.txt",
		"new/path.txt",
		":100644 100644 " + strings.Repeat("c", 40) + " " + strings.Repeat("d", 40) + " A",
		"added.txt",
		"",
	}, "\x00"))
	got := parseDiffRaw(raw)
	want := []diffEntry{
		{sha: strings.Repeat("b", 40), path: "new/path.txt"},
		{sha: strings.Repeat("d", 40), path: "added.txt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}
