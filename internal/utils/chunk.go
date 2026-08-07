package utils

import "strings"

// RecursiveSplit splits text into chunks of at most maxWords words. It
// tries paragraph boundaries first, falling back to sentence boundaries,
// then a hard word-count cut. No overlap between chunks (see
// constants.ChunkSizeWords doc comment for why overlap was deliberately
// skipped, and for why the caller's cap is sized well below the
// embedding model's real token limit despite this function counting
// words, not tokens).
func RecursiveSplit(text string, maxWords int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if maxWords <= 0 {
		// hardSplit's `for i := 0; i < len(words); i += maxWords` never
		// advances i if maxWords <= 0 — guard here rather than there so
		// every caller of the exported entrypoint is protected.
		return nil
	}

	words := strings.Fields(text)
	if len(words) <= maxWords {
		return []string{text}
	}

	paragraphs := strings.Split(text, "\n\n")
	if len(paragraphs) > 1 {
		return splitUnits(paragraphs, maxWords, "\n\n")
	}

	sentences := strings.Split(text, ". ")
	if len(sentences) > 1 {
		return splitUnits(sentences, maxWords, ". ")
	}

	return hardSplit(words, maxWords)
}

// splitUnits greedily packs units (paragraphs or sentences) into chunks
// no larger than maxWords words, falling back to hardSplit for any
// single unit that alone exceeds maxWords.
func splitUnits(units []string, maxWords int, joiner string) []string {
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

		if wordCount > maxWords {
			flush()
			chunks = append(chunks, hardSplit(strings.Fields(unit), maxWords)...)
			continue
		}

		if currentWords+wordCount > maxWords {
			flush()
		}
		current = append(current, unit)
		currentWords += wordCount
	}
	flush()
	return chunks
}

// hardSplit is the fallback: cut a word slice into fixed-size windows.
func hardSplit(words []string, maxWords int) []string {
	var chunks []string
	for i := 0; i < len(words); i += maxWords {
		end := min(i+maxWords, len(words))
		chunks = append(chunks, strings.Join(words[i:end], " "))
	}
	return chunks
}
