package ui

import (
	"strings"
	"testing"
)

func TestThemes(t *testing.T) {
	defer ApplyTheme("classic", "")
	if ThemeByID("nope").ID != "classic" || NextTheme("glass") != "classic" || NextTheme("classic") != "win97" {
		t.Fatal("look order")
	}
	for _, s := range []string{"#7c3aed", "#ABCDEF"} {
		if !ValidAccent(s) {
			t.Errorf("%s should be valid", s)
		}
	}
	for _, s := range []string{"red", "#abc", "#12345g", ""} {
		if ValidAccent(s) {
			t.Errorf("%s should be refused", s)
		}
	}
	ApplyTheme("win97", "")
	if selStyle.GetBackground() == nil || headerMark != "■ " {
		t.Fatal("Windows 97 paints the selection and marks the header")
	}
	ApplyTheme("xiao", "#123456")
	if !strings.Contains(strings.ToLower(colorHex(accent)), "123456") {
		t.Fatalf("custom accent not applied: %v", accent)
	}
}

func colorHex(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()
	const hex = "0123456789abcdef"
	out := []byte{'#'}
	for _, v := range []uint32{r >> 8, g >> 8, b >> 8} {
		out = append(out, hex[v>>4], hex[v&15])
	}
	return string(out)
}
