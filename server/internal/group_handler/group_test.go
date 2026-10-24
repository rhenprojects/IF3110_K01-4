package grouphandler

import (
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	if _, err := validateName("  engineering  "); err != nil {
		t.Fatalf("valid name: %v", err)
	}
	cases := []string{"", "   ", strings.Repeat("x", 101)}
	for _, name := range cases {
		if _, err := validateName(name); err == nil {
			t.Errorf("validateName(%q) = nil, want error", name)
		}
	}
}

func TestValidateDescription(t *testing.T) {
	if _, err := validateDescription("  analytics team  "); err != nil {
		t.Fatalf("valid description: %v", err)
	}
	if _, err := validateDescription(strings.Repeat("d", 256)); err == nil {
		t.Error("expected error for description longer than 255")
	}
}

func TestValidateLevel(t *testing.T) {
	for _, level := range []string{"view", "edit", " edit "} {
		if _, err := validateLevel(level); err != nil {
			t.Errorf("validateLevel(%q): %v", level, err)
		}
	}
	for _, level := range []string{"", "admin", "VIEW"} {
		if _, err := validateLevel(level); err == nil {
			t.Errorf("validateLevel(%q) = nil, want error", level)
		}
	}
}
