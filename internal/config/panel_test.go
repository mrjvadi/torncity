package config

import (
	"errors"
	"testing"
	"time"
)

func TestPanelValidation(t *testing.T) {
	ok := defaultPanel()
	if err := ok.validate(); err != nil {
		t.Fatalf("the defaults do not validate: %v", err)
	}
	for name, change := range map[string]func(*Panel){
		"listen":        func(p *Panel) { p.Listen = "8090" },
		"url scheme":    func(p *Panel) { p.PublicURL = "ftp://x.example" },
		"url path":      func(p *Panel) { p.PublicURL = "https://x.example/panel" },
		"proxy":         func(p *Panel) { p.TrustedProxies = []string{"not-a-cidr"} },
		"idle":          func(p *Panel) { p.SessionIdle = 13 * time.Hour },
		"lockout order": func(p *Panel) { p.LockoutBase = 2 * time.Hour },
		"password":      func(p *Panel) { p.PasswordMinLength = 6 },
	} {
		p := defaultPanel()
		change(&p)
		if err := p.validate(); !errors.Is(err, ErrPanel) {
			t.Errorf("%s: want ErrPanel, got %v", name, err)
		}
	}
}
