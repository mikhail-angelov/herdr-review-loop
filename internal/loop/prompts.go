package loop

import "fmt"

func ReviewPrompt(scope, reviewPath string, iteration, maximum int) string {
	opening := fmt.Sprintf("You are the reviewer in an automated review loop. This is round %d of %d, and your session was cleared beforehand, so you have no memory of the earlier rounds and should not try to reconstruct them. The code has already been through %d round(s) of review; some findings were applied and others were deliberately rejected by the author. Judge the code as it stands now, on its own merits.", iteration, maximum, iteration-1)
	if iteration == 1 {
		opening = fmt.Sprintf("You are the reviewer in an automated review loop. This is round 1 of %d. Review the code as it stands now, on its own merits.", maximum)
	}
	return fmt.Sprintf("%s\n\nReview %s.\n\nWrite the review to %s, overwriting whatever is there.\nThe first line of that file must be exactly one of:\n\nSTATUS: CLEAN\nSTATUS: FINDINGS\n\nUse STATUS: CLEAN only when nothing is left that is worth changing. Otherwise use\nSTATUS: FINDINGS and list every finding underneath, one per bullet, as:\n\n- [high|medium|low] path/to/file.ext:LINE — what is wrong — what to do about it\n\nDo not edit any code yourself; the review file is your only output. Reply with just\nthe path when you are done.", opening, scope, reviewPath)
}

func FixPrompt(reviewPath string) string {
	return fmt.Sprintf("A review of your changes is in %s.\n\nRead it and apply every finding you agree with. Deliberately skip the ones you\nconsider wrong or out of scope. Do not edit %s.\n\nWhen you are done, summarize in a few lines what you applied and what you rejected\nand why — the reviewer will re-review from the code, not from your summary.", reviewPath, reviewPath)
}
