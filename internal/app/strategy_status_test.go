package app

import "testing"

func TestStrategySessionTerminalRejectsRemovedCompletedStatus(t *testing.T) {
	if strategySessionTerminal("completed") {
		t.Fatal("removed completed status is still terminal")
	}
}
