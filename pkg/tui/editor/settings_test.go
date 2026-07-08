package editor

import "testing"

func TestSettingDefault(t *testing.T) {
	m := &Model{}
	if got := m.setting("image.preview"); got != "compact" {
		t.Errorf("default image.preview = %q, want compact", got)
	}
	if got := m.setting("link.color"); got != "gray" {
		t.Errorf("default link.color = %q, want gray", got)
	}
	m.settings = map[string]string{"image.preview": "true"}
	if got := m.setting("image.preview"); got != "true" {
		t.Errorf("stored image.preview = %q, want true", got)
	}
	if got := m.setting("unknown.key"); got != "" {
		t.Errorf("unknown key = %q, want empty", got)
	}
}

func TestCycleSetting(t *testing.T) {
	d, ok := settingByKey("link.color")
	if !ok {
		t.Fatal("link.color setting missing")
	}
	if got := cycleSetting(d, "gray", 1); got != "blue" {
		t.Errorf("cycle gray +1 = %q, want blue", got)
	}
	if got := cycleSetting(d, "blue", 1); got != "gray" {
		t.Errorf("cycle blue +1 wraps to %q, want gray", got)
	}
	if got := cycleSetting(d, "gray", -1); got != "blue" {
		t.Errorf("cycle gray -1 wraps to %q, want blue", got)
	}
}

// TestThemeSettingOptions ensures every theme is offered by the theme setting, so
// moving /theme into /settings didn't drop a palette.
func TestThemeSettingOptions(t *testing.T) {
	d, _ := settingByKey("theme")
	if len(d.options) != len(themes) {
		t.Fatalf("theme options = %d, want %d", len(d.options), len(themes))
	}
	for i, o := range d.options {
		if o.value != themes[i].name {
			t.Errorf("option %d = %q, want %q", i, o.value, themes[i].name)
		}
	}
}

func TestSettingValueColor(t *testing.T) {
	for _, neg := range []string{"off", "false", "disabled", "none", "no"} {
		if settingValueColor(neg) != cRed {
			t.Errorf("%q must render red", neg)
		}
	}
	for _, pos := range []string{"true", "gray", "dark", "compact", "on"} {
		if settingValueColor(pos) != cGreen {
			t.Errorf("%q must render green", pos)
		}
	}
}
