package runner_evidence

import "testing"

func TestSkippedEvidence(t *testing.T) {
	t.Skip("required environment unavailable")
}

func TestPassingEvidence(t *testing.T) {}
