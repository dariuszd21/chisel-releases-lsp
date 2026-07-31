package lsp

import (
	"strings"

	protocol "github.com/tliron/glsp/protocol_3_16"

	"github.com/dariuszd21/chisel-releases-lsp/internal/analysis"
)

// channelCompletion offers completions inside a `channel:` value. It returns nil
// when the cursor is not in such a value, so the caller can fall through to
// essential-reference completion.
//
// Two contexts are handled:
//   - the track has not been typed yet: offer "<default-track>/<risk>" for every
//     known risk, using the file's own default-track
//   - a track followed by "/" has been typed: offer the known risks
func (s *Server) channelCompletion(text string, uri protocol.DocumentUri, line, char int) []protocol.CompletionItem {
	value, valueStart, ok := channelValueBeforeCursor(text, line, char)
	if !ok {
		return nil
	}

	// The part being typed is the last comma-separated risk of the last
	// whitespace/bracket-separated pattern.
	itemStart := valueStart + strings.LastIndexAny(value, " ,[") + 1
	item := value[strings.LastIndexAny(value, " ,[")+1:]

	var candidates []string
	if track, _, hasSlash := strings.Cut(item, "/"); hasSlash {
		// The track is already typed; complete the risk only, replacing just
		// the risk part so the track is preserved.
		itemStart += len(track) + 1
		candidates = append(candidates, analysis.KnownRisks...)
	} else {
		// Suggest full patterns based on the file's own default-track, which is
		// by far the most common track to constrain.
		track := s.defaultTrackFor(uri)
		if track == "" {
			return []protocol.CompletionItem{}
		}
		for _, risk := range analysis.KnownRisks {
			candidates = append(candidates, track+"/"+risk)
		}
		candidates = append(candidates, track+"/*")
	}

	editRange := protocol.Range{
		Start: protocol.Position{Line: uint32(line), Character: uint32(itemStart)},
		End:   protocol.Position{Line: uint32(line), Character: uint32(char)},
	}
	kind := protocol.CompletionItemKindEnumMember
	items := []protocol.CompletionItem{}
	for _, c := range candidates {
		c := c
		items = append(items, protocol.CompletionItem{
			Label:    c,
			Kind:     &kind,
			TextEdit: protocol.TextEdit{Range: editRange, NewText: c},
		})
	}
	return items
}

// defaultTrackFor returns the `default-track:` of the file behind uri, or "".
func (s *Server) defaultTrackFor(uri protocol.DocumentUri) string {
	if s.idx == nil {
		return ""
	}
	filePath, err := uriToPath(string(uri))
	if err != nil {
		return ""
	}
	sf := s.idx.FileSliceFile(filePath)
	if sf == nil {
		return ""
	}
	return sf.DefaultTrack
}

// channelValueBeforeCursor returns the text of the `channel:` value up to the
// cursor and the column at which that value starts. ok is false when the cursor
// is not inside a channel value.
func channelValueBeforeCursor(text string, line, char int) (value string, valueStart int, ok bool) {
	lines := strings.Split(text, "\n")
	if line < 0 || line >= len(lines) {
		return "", 0, false
	}
	l := lines[line]
	if char > len(l) {
		char = len(l)
	}
	head := l[:char]
	// Find the "channel:" key that governs the cursor. It may be preceded by a
	// path key and other inline attributes, as in
	//   /dir/file: {mode: 0644, channel: 2.0/ed
	idx := strings.LastIndex(head, "channel:")
	if idx < 0 {
		return "", 0, false
	}
	// "channel:" must be a key, not the tail of something else such as a
	// content path named "/etc/channel:". A key starts the line or follows the
	// indentation, an attribute-mapping brace, or an attribute separator.
	if idx > 0 {
		switch l[idx-1] {
		case ' ', '\t', '{', ',':
		default:
			return "", 0, false
		}
	}
	valueStart = idx + len("channel:")
	// Anything that closes the attribute mapping or starts another attribute
	// means the cursor left the channel value.
	rest := head[valueStart:]
	if strings.ContainsAny(rest, "}:") {
		return "", 0, false
	}
	// Skip the whitespace and the optional "[" of a flow sequence.
	for valueStart < char && (l[valueStart] == ' ' || l[valueStart] == '\t') {
		valueStart++
	}
	return l[valueStart:char], valueStart, true
}
