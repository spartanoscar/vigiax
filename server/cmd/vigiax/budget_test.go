package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestBudgetSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "budget.json")
	now := time.Date(2026, 10, 28, 15, 0, 0, 0, time.UTC)
	b := loadBudget(path, 1, 0.5)
	b.spend(now, "1.2.3.4", 0.5)
	b.spend(now, "5.6.7.8", 0.5)

	b = loadBudget(path, 1, 0.5)
	if err := b.allow(now, "9.9.9.9"); err == nil {
		t.Fatal("a restart reset the day's budget")
	}
	if got := b.today(now); got != 1 {
		t.Fatalf("spent %v after restart, want 1", got)
	}
	if err := b.allow(now.Add(24*time.Hour), "1.2.3.4"); err != nil {
		t.Fatalf("next day: %v", err)
	}
}
