package main

import (
	"slices"
	"testing"
)

func TestCheckFeature(t *testing.T) {
	ids := map[string]int{"AC-001": 1, "AC-002": 2, "FR-001": 1}
	tests := []struct {
		name    string
		feature string
		want    []string
	}{
		{
			name:    "one known tag",
			feature: "Feature: f\n\n  @AC-001 @FR-001\n  Scenario: s\n",
		},
		{
			name:    "pending without tag",
			feature: "Feature: f\n\n  @pending\n  Scenario Outline: s\n",
		},
		{
			name:    "unknown tag",
			feature: "Feature: f\n\n  @AC-404\n  Scenario: s\n",
			want:    []string{"f.feature:4: @AC-404 встречается в openspec/specs 0 раз"},
		},
		{
			name:    "ambiguous tag",
			feature: "Feature: f\n\n  @AC-002\n  Scenario: s\n",
			want:    []string{"f.feature:4: @AC-002 встречается в openspec/specs 2 раз"},
		},
		{
			name:    "no tag",
			feature: "Feature: f\n\n  Scenario: s\n",
			want:    []string{"f.feature:3: у сценария 0 тегов @AC-NNN, нужен один"},
		},
		{
			name:    "tags of the previous scenario do not carry over",
			feature: "Feature: f\n\n  @AC-001\n  Scenario: a\n    Given x\n\n  Scenario: b\n",
			want:    []string{"f.feature:7: у сценария 0 тегов @AC-NNN, нужен один"},
		},
		{
			name:    "two main tags",
			feature: "Feature: f\n\n  @AC-001 @AC-001\n  Scenario: s\n",
			want:    []string{"f.feature:4: у сценария 2 тегов @AC-NNN, нужен один"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkFeature("f.feature", tt.feature, ids)
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
