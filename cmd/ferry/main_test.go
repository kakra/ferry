package main

import "testing"

func TestCommandFromArgs(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "default serve", args: nil, want: "serve"},
		{name: "plain command", args: []string{"init-config"}, want: "init-config"},
		{name: "global flag before command", args: []string{"--dev", "init-config"}, want: "init-config"},
		{name: "global flag after command", args: []string{"serve", "--dev"}, want: "serve"},
		{name: "help short flag", args: []string{"-h"}, want: "help"},
		{name: "help long flag before command", args: []string{"--help", "serve"}, want: "help"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandFromArgs(tt.args); got != tt.want {
				t.Fatalf("commandFromArgs(%v) = %q, want %q", tt.args, got, tt.want)
			}
		})
	}
}
