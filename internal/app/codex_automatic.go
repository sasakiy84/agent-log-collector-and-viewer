package app

import "strings"

// These are application protocol templates, not general topic/keyword matches.
// Keep the header anchored so quoting or discussing one does not hide a chat.
func codexAutomaticPrompt(text string, model any) bool {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "This is an app-generated request for a suggested next user message. The user did not write this message.\n\n") {
		return strings.Contains(text, "\n\nYour suggestion will appear in the user's empty message composer.") &&
			strings.Contains(text, "\n\nSet suggestion to exactly one natural user message,") &&
			strings.Contains(text, "Return only JSON matching this schema:")
	}
	if str(model) != "codex-auto-review" {
		return false
	}
	review := strings.HasPrefix(text, "The following is the Codex agent history whose request action you are assessing. ")
	delta := strings.HasPrefix(text, "The following is the Codex agent history added since your last approval assessment. Continue the same review conversation. ")
	return (review || delta) && strings.Contains(text, "\n>>> APPROVAL REQUEST START\n") &&
		strings.HasSuffix(text, ">>> APPROVAL REQUEST END")
}

func codexPromptFacts(d Detail) (automatic, human bool) {
	if d.Kind != "user" {
		return false, false
	}
	for _, block := range userRequestBlocks(d) {
		if block.Label != "Request" && block.Label != "user" {
			continue
		}
		text, page := userPromptContent(block.Text, "codex")
		if text == "" || text == "Content not exported" {
			if page && text == "" {
				automatic = true
			}
			continue
		}
		if codexAutomaticPrompt(text, d.Model) {
			automatic = true
		} else {
			human = true
		}
	}
	return automatic, human
}
