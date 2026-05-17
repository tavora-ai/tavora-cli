package main

import (
	"testing"

	tavora "github.com/tavora-ai/tavora-sdk-go"
)

// Document the resolver's matching policy in one place so future
// changes (e.g. case-insensitive name match, prefix match) flag
// existing assumptions instead of silently breaking the contract.
func TestPickAgent(t *testing.T) {
	t.Parallel()
	supportLocalID := "support"
	billingLocalID := "billing"
	configs := []tavora.AgentConfig{
		{
			ID:               "ag-uuid-1",
			Name:             "Support Bot",
			CodeFirstLocalID: &supportLocalID,
		},
		{
			ID:               "ag-uuid-2",
			Name:             "Billing Bot",
			CodeFirstLocalID: &billingLocalID,
		},
		{
			ID:   "ag-uuid-3",
			Name: "Standalone Bot",
		},
	}

	cases := []struct {
		name  string
		arg   string
		want  string
		isErr bool
	}{
		{name: "UUID hit", arg: "ag-uuid-2", want: "ag-uuid-2"},
		{name: "local id hit", arg: "support", want: "ag-uuid-1"},
		{name: "name hit", arg: "Standalone Bot", want: "ag-uuid-3"},
		{name: "UUID beats name when both match",
			arg: "ag-uuid-1", want: "ag-uuid-1"},
		{name: "unknown", arg: "missing", isErr: true},
		{name: "empty arg", arg: "", isErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickAgent(tc.arg, configs)
			if tc.isErr {
				if err == nil {
					t.Fatalf("expected error for %q, got %q", tc.arg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("pickAgent(%q) = %q, want %q", tc.arg, got, tc.want)
			}
		})
	}
}
