package notifier

import (
	"testing"
	"time"
)

func TestDurationToIntervalLiteral(t *testing.T) {
	if got := durationToIntervalLiteral(90 * time.Second); got != "90 seconds" {
		t.Fatalf("got %q", got)
	}
	if got := durationToIntervalLiteral(500 * time.Millisecond); got != "1 seconds" {
		t.Fatalf("got %q", got)
	}
	// Non-positive duration should fallback to defaultRunningCampaignTTL.
	if got := durationToIntervalLiteral(0); got != "600 seconds" {
		t.Fatalf("got %q", got)
	}
}
