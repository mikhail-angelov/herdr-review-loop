package loop

import "fmt"

func ReviewPrompt(reviewPath, summaryPath string, iteration, maximum int) string {
	policy := "Review broadly for correctness, security, and missing tests. Report low-severity findings only when the fix is local and clearly worthwhile."
	if iteration == 2 {
		policy = "Verify the changes from round 1 and look for remaining high or medium issues. Treat prior rejected or deferred decisions as closed unless new concrete evidence changes their impact."
	}
	if iteration >= 3 {
		policy = "Use closure mode: report only regressions introduced by the fixes or high-severity issues. Treat prior rejected or deferred decisions as closed unless new concrete evidence changes their impact."
	}
	return fmt.Sprintf("You are the reviewer in an automated review loop. This is round %d of %d. Your session was cleared beforehand.\n\nRead %s if it exists; it is a short record of prior decisions. Review only the uncommitted changes in the working tree. Do not report findings about %s or %s.\n\n%s\n\nWrite the review to %s, overwriting whatever is there.\nThe first line of that file must be exactly one of:\n\nSTATUS: CLEAN\nSTATUS: FINDINGS\n\nUse STATUS: CLEAN only when nothing is left that is worth changing. Otherwise use\nSTATUS: FINDINGS and list every finding underneath, one per bullet, as:\n\n- [high|medium|low] path/to/file.ext:LINE — what is wrong — what to do about it\n\nDo not edit any code yourself; the review file is your only output. Reply with just\nthe path when you are done.", iteration, maximum, summaryPath, reviewPath, summaryPath, policy, reviewPath)
}

func FixPrompt(reviewPath, summaryPath string, iteration int) string {
	policy := "Apply high-severity findings you agree with. Do not make broad changes for low-severity findings, or for medium-severity findings whose fix would substantially expand the change. Mark those decisions as rejected or deferred instead."
	if iteration == 2 {
		policy = "Apply remaining high-severity findings you agree with. Apply medium-severity findings only when their fix is local; reject or defer all other findings with a short reason."
	}
	if iteration >= 3 {
		policy = "Apply only high-severity findings or regressions caused by the earlier fixes. Reject or defer every other finding with a short reason."
	}
	return fmt.Sprintf("You are the author in round %d of an automated review loop and have a fresh session. Read %s, then read %s if it exists and inspect the current uncommitted diff.\n\n%s Do not edit %s.\n\nBefore finishing, rewrite %s as a compact decision record: keep at most 20 bullets; retain only current applied decisions and still-relevant rejected or deferred decisions, each with a short reason. The next reviewer will use this record to avoid repeating settled comments.", iteration, reviewPath, summaryPath, policy, reviewPath, summaryPath)
}
