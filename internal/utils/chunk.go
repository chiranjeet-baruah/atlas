package utils

import "strings"

// RecursiveSplit splits text into chunks of at most maxTokens words
// (a word-count approximation of tokens — adequate for the extraction
// scale this project runs at). It tries paragraph boundaries first,
// falling back to sentence boundaries, then a hard word-count cut.
// No overlap between chunks (see constants.ChunkSizeTokens doc comment
// for why overlap was deliberately skipped).
func RecursiveSplit(text string, maxTokens int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxTokens <= 0 {
		// hardSplit's `for i := 0; i < len(words); i += maxTokens` never
		// advances i if maxTokens <= 0 — guard here rather than there so
		// every caller of the exported entrypoint is protected.
		return nil
	}

	words := strings.Fields(text)
	if len(words) <= maxTokens {
		return []string{text}
	}

	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) > 1 {
		return splitUnits(paragraphs, maxTokens, "\n\n")
	}

	sentences := strings.Split(text, ". ")
	if len(sentences) > 1 {
		return splitUnits(sentences, maxTokens, ". ")
	}

	return hardSplit(words, maxTokens)
}

// splitUnits greedily packs units (paragraphs or sentences) into chunks
// no larger than maxTokens words, falling back to hardSplit for any
// single unit that alone exceeds maxTokens.
func splitUnits(units []string, maxTokens int, joiner string) []string {
	var chunks []string
	var current []string
	currentWords := 0

	flush := func() {
		if len(current) > 0 {
			chunks = append(chunks, strings.Join(current, joiner))
			current = nil
			currentWords = 0
		}
	}

	for _, unit := range units {
		unit = strings.TrimSpace(unit)
		if unit == "" {
			continue
		}
		wordCount := len(strings.Fields(unit))

		if wordCount > maxTokens {
			flush()
			chunks = append(chunks, hardSplit(strings.Fields(unit), maxTokens)...)
			continue
		}

		if currentWords+wordCount > maxTokens {
			flush()
		}
		current = append(current, unit)
		currentWords += wordCount
	}
	flush()
	return chunks
}

// hardSplit is the fallback: cut a word slice into fixed-size windows.
func hardSplit(words []string, maxTokens int) []string {
	var chunks []string
	for i := 0; i < len(words); i += maxTokens {
		end := min(i+maxTokens, len(words))
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}
