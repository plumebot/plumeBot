package event

import (
	"testing"
)

// TestOutcomeLevel 验证结局级别映射（架构 §17.4：正常结局 Info、异常结局 Warn）。
func TestOutcomeLevel(t *testing.T) {
	cases := []struct {
		oc  Outcome
		warn bool
	}{
		{OutcomeAgentReplied, false},
		{OutcomeCommand, false},
		{OutcomeCommandNotFound, false},
		{OutcomeNotTriggered, false},
		{OutcomeEmptyReply, false},
		{OutcomeRateLimited, true},
		{OutcomeSensitive, true},
		{OutcomeControlError, true},
		{OutcomePromptFail, true},
		{OutcomeAgentFail, true},
		{OutcomeSendFail, true},
		{OutcomeNoSender, true},
		{OutcomeCommandError, true},
	}
	for _, c := range cases {
		if got := isWarnOutcome(c.oc); got != c.warn {
			t.Errorf("isWarnOutcome(%q) = %v, want %v", c.oc, got, c.warn)
		}
	}
}