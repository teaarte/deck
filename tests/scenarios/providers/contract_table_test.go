package providers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// providerContract is what every provider adapter does in each situation: the tests that prove it, or n/a with why.
var providerContract = []struct{ situation, claudecode, codex, acp string }{
	{"ends with its answer",
		"TestTheAnswerOfAClaudeCodeTurnIsTheLastResultOfItsSession", "TestTheFinalAnswerOfCodexIsTheTextOfItsTurn", "TestACPRecordedTurnOfGoose"},
	{"a turn that did not end well is not a success",
		"TestTurnSucceedsOnlyStrictly", "TestCodexTurnSucceedsOnlyStrictly", "TestACPTurnSucceedsOnlyOnEndTurn"},
	{"a session the program lost starts over",
		"TestResumeAfterEarlyCrashStartsTheSessionAgain", "TestCodexLostThreadStartsTheSessionOver", "TestACPSessionTheAgentLostStartsOver"},
	{"a usage limit pauses until its reset",
		"TestClaudeCodeLimitPausesUntilItsReset", "TestCodexLostLoginAndLimit", "TestACPTurnSucceedsOnlyOnEndTurn"},
	{"a warning of a limit is not a limit",
		"TestRateLimitWarningDoesNotPause", "TestCodexHighUsageWithoutAReachedLimitIsNoLimit",
		"n/a: acp learns of a limit only from a 429 of the model API (internal/provider/acp/proxy.go)"},
	{"a lost login or a refused key waits for the owner",
		"TestLostLoginWaitsForOwnerAndRetries", "TestCodexLostLoginAndLimit", "TestACPTurnThatEndsOnARefusalOfTheModelIsTheRefusal"},
	{"a model that did not answer is unavailable",
		"TestTurnSucceedsOnlyStrictly", "TestCodexFailureOfItsModelOrTaskIsTyped", "TestACPServerErrorOfTheModelAPIIsUnavailable"},
	{"an error the agent reports fails the task",
		"TestClaudeCodeErrorResultIsAFailedTask", "TestCodexFailureOfItsModelOrTaskIsTyped", "TestACPTurnSucceedsOnlyOnEndTurn"},
	{"a result handed in beats a failure after it",
		"n/a: Claude Code has structured output, so it gets no result_submit (internal/provider/claudecode/claudecode.go caps)",
		"TestCodexLimitReachedAfterItsTurnLeavesTheResult; TestCodexResultHandedInBeatsAnUnfinishedTurn",
		"TestACPTurnThatHandedInItsResultIsDoneThoughALaterCallWasRefused"},
	{"killed from outside goes on in its session once",
		"TestAnAgentKilledFromOutsideGoesOnInItsSession", "TestCodexKilledFromOutsideGoesOnInItsThread", "TestACPAgentKilledFromOutsideGoesOnInItsSession"},
	{"killed twice says who killed it",
		"TestAnAgentKilledTwiceSaysWhoKilledIt", "TestCodexKilledTwiceSaysWhoKilledIt", "TestACPAgentKilledTwiceSaysWhoKilledIt"},
	{"a program that does not exit after its turn is killed, its result kept",
		"TestCLIThatDoesNotExitAfterItsResultIsKilled", "TestCodexThatDoesNotExitAfterItsTurnIsKilled", "TestACPAgentThatStaysAfterItsTurnIsKilled"},
	{"events of sub-agents are not the process's",
		"TestSubAgentEventsAreNotProcessEvents", "TestCodexSubAgentEventsAreNotProcessEvents", "TestACPUpdatesOfAnotherSessionAreNotTheProcess"},
	{"tokens of the turn, only its own on resume",
		"TestResumedClaudeCodeTurnCountsOnlyItsOwnTokens", "TestCodexSubAgentEventsAreNotProcessEvents", "n/a: deck's proxy counts each call of an ACP agent's model as it goes, so a resume counts only its own (internal/provider/acp/proxy.go)"},
	{"deck's tools that never reached the agent fail the turn",
		"TestAnAgentThatNeverGetsDecksToolsFails",
		"n/a: not detected: deck's MCP goes into codex by its thread config and only claudecode tells fault.DeckTools (internal/provider/codex)",
		"n/a: not detected: deck's MCP goes into acp with session/new (internal/provider/acp/turn.go)"},
	{"the fake rejects what the real program lacks",
		"TestFakeCLIRejectsUnknownFlags", "TestFakeCodexRejectsWhatTheSchemaLacks", "TestFakeACPRejectsWhatTheSchemaLacks"},
	{"the owner's stop cancels the turn",
		"TestClaudeCodeStopEndsTheCLI", "TestCodexStopInterruptsTheTurn", "TestACPStopCancelsTheTurn"},
	{"a question to the owner parks, the answer resumes",
		"TestClaudeCodeQuestionParksAndTheAnswerResumes", "TestCodexResumeAfterALateAnswerAsksForTheResultAgain",
		"TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes"},
}

// TestEverySituationOfAProviderHasItsTest: each cell of the contract names tests that exist, or says why it does not
// apply.
func TestEverySituationOfAProviderHasItsTest(t *testing.T) {
	t.Parallel()
	known := map[string]bool{}
	decl := regexp.MustCompile(`(?m)^func (Test\w+)\(`)
	err := filepath.WalkDir("..", func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, err := os.ReadFile(path)
		for _, m := range decl.FindAllStringSubmatch(string(b), -1) {
			known[m[1]] = true
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range providerContract {
		for provider, cell := range map[string]string{"claudecode": row.claudecode, "codex": row.codex, "acp": row.acp} {
			if strings.HasPrefix(cell, "n/a: ") && len(cell) > len("n/a: ") {
				continue
			}
			if strings.TrimSpace(cell) == "" {
				t.Errorf("%q of %s has no test and no reason it does not apply", row.situation, provider)
				continue
			}
			for _, name := range strings.Split(cell, "; ") {
				if !known[name] {
					t.Errorf("%q of %s names %s, which is no test under tests/scenarios (name a top-level test)", row.situation, provider, name)
				}
			}
		}
	}
}
