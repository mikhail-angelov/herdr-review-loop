package loop

// PanelWidth returns the desired split width while protecting the author pane on
// small workspaces. It is intentionally pure so layout decisions are testable.
func PanelWidth(workspaceWidth int) int {
	if workspaceWidth < 64 {
		return 0
	}
	width := workspaceWidth * 30 / 100
	if width < 32 {
		width = 32
	}
	if width > 44 {
		width = 44
	}
	if width > workspaceWidth/2 {
		width = workspaceWidth / 2
	}
	return width
}

// ResizeDirection is the resize a pane needs to reach its target width, as a fraction of the
// workspace. It reports false when the pane is already within a column of the target, so a run
// does not nudge the layout on every start.
func ResizeDirection(current, target, workspace int) (direction string, amount float64, resize bool) {
	if target == 0 || current-target >= -1 && current-target <= 1 {
		return "", 0, false
	}
	difference := current - target
	if difference > 0 {
		return "right", float64(difference) / float64(workspace), true
	}
	return "left", float64(-difference) / float64(workspace), true
}
